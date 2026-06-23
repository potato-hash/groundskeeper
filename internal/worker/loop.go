package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// loadLoopSpec reads the enabled loop_spec for a thread via gkdb (nil if none).
func loadLoopSpec(db *gkdb.DB, threadID string) (*gkdb.LoopSpecRow, error) {
	return db.GetLoopSpec(threadID)
}

// LoopState tracks how far a loop has progressed against its caps.
type LoopState struct {
	Turns     int
	ToolCalls int
	StartedAt time.Time
	CostUSD   float64
}

// ShouldStop evaluates the loop's stop conditions against the current state and
// the last turn's output. Implements the prompt's stop conditions:
//
//	agent_says_done        — the agent's output contains a done signal
//	tests_pass             — the output indicates tests passed
//	diff_empty             — the output indicates no changes
//	approval_required      — a pending approval exists for the thread
//	same_failure_repeated  — the same error appeared N times in a row
//	max_turns              — the turn cap (checked against turns_enqueued)
//	max_wall_minutes       — the wall-clock cap
//	max_tool_calls         — the tool-call cap
//	max_cost               — the cost cap
//	secret_or_policy_event — the output indicates a secret/policy refusal
func (s LoopState) ShouldStop(spec *gkdb.LoopSpecRow, lastOutput string, failureCount int, hasPendingApproval bool) (bool, string) {
	if spec == nil {
		return false, ""
	}
	// Budget caps — s.Turns is turns_enqueued from the loop_run, NOT job.Attempts
	if spec.MaxTurns > 0 && s.Turns >= int(spec.MaxTurns) {
		return true, fmt.Sprintf("max_turns reached (%d)", spec.MaxTurns)
	}
	if spec.MaxWallMinutes > 0 && time.Since(s.StartedAt) > time.Duration(spec.MaxWallMinutes)*time.Minute {
		return true, fmt.Sprintf("max_wall_minutes reached (%d)", spec.MaxWallMinutes)
	}
	if spec.MaxToolCalls > 0 && s.ToolCalls >= int(spec.MaxToolCalls) {
		return true, fmt.Sprintf("max_tool_calls reached (%d)", spec.MaxToolCalls)
	}
	if spec.MaxCostUSD > 0 && s.CostUSD >= spec.MaxCostUSD {
		return true, fmt.Sprintf("max_cost_usd reached (%.4f)", spec.MaxCostUSD)
	}
	// Safety stops
	if hasPendingApproval {
		return true, "approval_required: pending approval blocks the loop"
	}
	if failureCount >= 3 {
		return true, "same_failure_repeated: same error repeated 3+ times"
	}
	// Text-based stop conditions (evaluated against the last turn's output)
	stop := spec.StopWhen
	if stop != "" && lastOutput != "" {
		lower := strings.ToLower(lastOutput)
		switch stop {
		case "agent_says_done":
			if strings.Contains(lower, "done") || strings.Contains(lower, "task complete") {
				return true, "agent_says_done"
			}
		case "tests_pass":
			if strings.Contains(lower, "pass") && (strings.Contains(lower, "test") || strings.Contains(lower, "ok")) {
				return true, "tests_pass"
			}
		case "diff_empty":
			if strings.Contains(lower, "no changes") || strings.Contains(lower, "nothing to commit") {
				return true, "diff_empty"
			}
		case "approval_required", "blocked":
			if hasPendingApproval {
				return true, stop
			}
		case "secret_or_policy_event":
			if strings.Contains(lower, "secret") || strings.Contains(lower, "policy") || strings.Contains(lower, "refus") {
				return true, "secret_or_policy_event"
			}
		default:
			// Custom stop_when: substring match
			if strings.Contains(lastOutput, stop) {
				return true, "stop_when matched: " + stop
			}
		}
	}
	return false, ""
}

// runLoop is called after a turn completes. It:
//  1. Finds or creates the active loop_run for the thread.
//  2. Increments turns_completed.
//  3. Evaluates stop conditions using turns_enqueued from the loop_run.
//  4. If not stopping and turns_enqueued < max_turns, increments turns_enqueued
//     and enqueues the next turn job with the loop_run_id.
//
// The turn counter (turns_enqueued) is persistent in the loop_runs table, so it
// survives daemon restarts. Retries (FailJob + requeue) do NOT increment
// turns_enqueued — only new loop turns do.
func runLoop(ctx context.Context, p *Pool, job *gkdb.JobRow, thread *gkdb.ThreadRow, spec *gkdb.LoopSpecRow) error {
	if spec == nil || spec.Mode == "manual" || spec.Mode == "single" {
		return nil // single turn done
	}

	// Find the active loop run for this thread, or create one if this is the
	// first turn of a new loop.
	run, err := p.db.GetActiveLoopRun(thread.ID)
	if err != nil {
		return fmt.Errorf("get active loop_run: %w", err)
	}
	if run == nil {
		// No active run — create one. This happens when loop start enqueued
		// the first job without a loop_run_id (the CLI loop start path).
		run, err = p.db.StartLoopRun(thread.ID, spec.ID)
		if err != nil {
			return fmt.Errorf("start loop_run: %w", err)
		}
		// The first job already ran, so turns_enqueued should be 1.
		_, _ = p.db.IncrementTurnEnqueued(run.ID)
	}

	// Mark the completed turn.
	_ = p.db.IncrementTurnCompleted(run.ID)

	// Check for pending approvals (approval_required stop).
	pending, _ := p.db.ListPendingApprovals()
	hasPending := false
	for _, a := range pending {
		if a.ThreadID == thread.ID {
			hasPending = true
			break
		}
	}

	// Evaluate stop conditions using turns_enqueued from the persistent counter.
	state := LoopState{
		StartedAt: time.Unix(run.StartedAt, 0),
		Turns:     int(run.TurnsEnqueued),
	}
	stop, reason := state.ShouldStop(spec, thread.Goal, 0, hasPending)
	if stop {
		p.logger.Info("worker: loop stopped", "thread", thread.ID, "reason", reason)
		_ = p.db.StopLoopRun(run.ID, gkdb.RunStopped, reason)
		_ = p.db.SetLoopEnabled(thread.ID, false)
		return nil
	}

	// Check max_turns before queuing the next turn.
	if spec.MaxTurns > 0 && run.TurnsEnqueued >= spec.MaxTurns {
		p.logger.Info("worker: loop max_turns reached", "thread", thread.ID,
			"enqueued", run.TurnsEnqueued, "max", spec.MaxTurns)
		_ = p.db.StopLoopRun(run.ID, gkdb.RunCompleted, "max_turns reached")
		_ = p.db.SetLoopEnabled(thread.ID, false)
		return nil
	}

	// Enqueue the next turn: increment the persistent counter first, then create
	// the job with the loop_run_id so it's associated with this run.
	if spec.Mode == "until_done" || spec.Mode == "interval" || spec.Mode == "review_retry" {
		turnID := fmt.Sprintf("turn-%d", run.TurnsEnqueued+1)
		_, err := p.db.CreateJobWithLoop(thread.ID, "turn", run.ID, turnID)
		if err != nil {
			return fmt.Errorf("enqueue next turn: %w", err)
		}
		_, _ = p.db.IncrementTurnEnqueued(run.ID)
		p.logger.Info("worker: loop enqueued next turn", "thread", thread.ID,
			"run", run.ID, "enqueued", run.TurnsEnqueued+1)
	}
	return nil
}
