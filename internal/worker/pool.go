// Package worker is Groundskeeper's worker pool — a single dispatcher with a
// bounded slot cap that pulls claimed jobs from gkdb, spawns an OMP worker per
// thread via the runtime adapter, and reconciles an in-memory inflight set
// against the DB on crash.
//
// Design (from docs/upstream-roboomp-audit.md — roboomp WorkerPool shape):
//   - The DB is the source of truth; the pool is ephemeral.
//   - A single dispatcher goroutine claims jobs (ClaimNextJob enforces per-thread
//     serialization in SQL) and hands each to a worker slot.
//   - A bounded slot cap (semaphore) limits concurrency.
//   - An in-memory inflight map tracks thread_id -> job so a daemon restart can
//     reconcile (ResetStuckRunning requeues anything the pool lost).
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/potato-hash/groundskeeper/internal/channel"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/runtime"
)

// Pool is the worker dispatch pool.
type Pool struct {
	db      *gkdb.DB
	adapter runtime.AgentRuntimeAdapter
	cfg     Config
	slots   chan struct{} // semaphore bounding concurrency
	logger  *slog.Logger
	gateway *channel.Gateway // optional notification gateway (Phase 7 wiring)

	mu       sync.Mutex
	inflight map[string]*runningJob // thread_id -> active job
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

type runningJob struct {
	jobID     string
	threadID  string
	startedAt time.Time
	cancel    context.CancelFunc
}

// Config tunes the pool.
type Config struct {
	// MaxSlots bounds concurrent workers (roboomp SlotPool equivalent).
	MaxSlots int
	// PollInterval is how often the dispatcher re-checks for claimable jobs.
	PollInterval time.Duration
	// TurnTimeout caps how long a single turn may run before it is failed.
	// Zero = no per-turn timeout (rely on the context / loop caps). A stuck
	// worker (no agent_end) otherwise hangs the slot forever.
	TurnTimeout time.Duration
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Config {
	return Config{MaxSlots: 4, PollInterval: 500 * time.Millisecond}
}

// New returns a worker pool. Call Start to begin dispatching; Stop to drain.
func New(db *gkdb.DB, adapter runtime.AgentRuntimeAdapter, cfg Config) *Pool {
	if cfg.MaxSlots <= 0 {
		cfg.MaxSlots = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	return &Pool{
		db:       db,
		adapter:  adapter,
		cfg:      cfg,
		slots:    make(chan struct{}, cfg.MaxSlots),
		logger:   slog.Default(),
		inflight: make(map[string]*runningJob),
		stopCh:   make(chan struct{}),
	}
}

// SetLogger overrides the default logger.
func (p *Pool) SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	p.logger = l
}

// SetGateway wires a notification gateway. When set, the pool triggers
// notifications on dead-lettered jobs and privileged host_tool_call events.
func (p *Pool) SetGateway(gw *channel.Gateway) { p.gateway = gw }

// Start launches the dispatcher goroutine. It first reconciles the inflight
// set against the DB: any job still marked 'running' from a prior daemon run is
// requeued (ResetStuckRunning).
func (p *Pool) Start(ctx context.Context) {
	// Crash recovery: requeue jobs left 'running' by a prior daemon.
	if n, err := p.db.ResetStuckRunning(); err != nil {
		p.logger.Error("worker: reset stuck running", "err", err)
	} else if n > 0 {
		p.logger.Info("worker: requeued stuck jobs", "count", n)
	}
	p.wg.Add(1)
	go p.dispatchLoop(ctx)
}

// Stop signals the dispatcher to exit and waits for in-flight jobs to drain.
func (p *Pool) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

// InflightCount returns the number of currently-running jobs (for status/UI).
func (p *Pool) InflightCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inflight)
}

// InflightThreads returns the thread_ids with active jobs (for reconciliation).
func (p *Pool) InflightThreads() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.inflight))
	for k := range p.inflight {
		out = append(out, k)
	}
	return out
}

// dispatchLoop is the single dispatcher. It repeatedly claims the next job,
// acquires a slot, and runs the job in its own goroutine.
func (p *Pool) dispatchLoop(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tryClaimAndRun(ctx)
		}
	}
}

// tryClaimAndRun claims one job and, if found, runs it in a goroutine.
func (p *Pool) tryClaimAndRun(ctx context.Context) {
	job, err := p.db.ClaimNextJob(time.Now())
	if err != nil {
		p.logger.Error("worker: claim", "err", err)
		return
	}
	if job == nil {
		return // nothing claimable
	}
	// Acquire a slot (blocks until one is free, respecting stop/ctx).
	select {
	case p.slots <- struct{}{}:
	case <-p.stopCh:
		// Requeue the job we claimed but cannot run.
		_ = p.requeue(job.ID)
		return
	case <-ctx.Done():
		_ = p.requeue(job.ID)
		return
	}
	p.wg.Add(1)
	go p.runJob(ctx, job)
}

// runJob executes a claimed job: start a worker thread, send the turn prompt,
// stream events, and mark the job done/failed on completion.
func (p *Pool) runJob(ctx context.Context, job *gkdb.JobRow) {
	defer p.wg.Done()
	defer func() { <-p.slots }() // release the slot

	thread, err := p.db.GetThread(job.ThreadID)
	if err != nil || thread == nil {
		p.logger.Error("worker: get thread", "thread", job.ThreadID, "err", err)
		_, _ = p.db.FailJob(job.ID, "thread not found")
		return
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	r := p.registerInflight(job, cancel)
	defer p.unregisterInflight(r.threadID)

	// Per-task worktree: run the worker in an isolated git worktree so
	// concurrent tasks never collide on a shared checkout (roboomp
	// SandboxManager.ensure_workspace). Falls back to the thread's workspace
	// in-place for non-git repos.
	workPath := thread.WorkspacePath
	wtPath, wtErr := EnsureWorktree(thread.WorkspacePath, "gk-job-"+shortID(job.ID))
	if wtErr == nil && wtPath != thread.WorkspacePath {
		workPath = wtPath
		defer RemoveWorktree(thread.WorkspacePath, wtPath)
	}

	// Start (or resume) the worker in the (worktree) workspace + session dir.
	ref, err := p.startWorker(jobCtx, &gkdb.ThreadRow{
		ID:            thread.ID,
		Title:         thread.Title,
		Runtime:       thread.Runtime,
		Status:        thread.Status,
		WorkspacePath: workPath,
		SessionDir:    thread.SessionDir,
	})

	// Drain ready before prompting.
	events := p.adapter.StreamEvents(ref)
	if !p.waitForReady(jobCtx, events) {
		_, _ = p.db.FailJob(job.ID, "worker did not become ready")
		return
	}

	// Send the turn. The prompt is the thread's goal (or a default turn prompt).
	prompt := thread.Goal
	if prompt == "" {
		prompt = "Continue the task."
	}
	if err := p.adapter.SendTurn(jobCtx, ref, prompt); err != nil {
		p.logger.Error("worker: send turn", "thread", thread.ID, "err", err)
		_, _ = p.db.FailJob(job.ID, "send turn failed: "+err.Error())
		return
	}
	// State machine: prompt ack received → job is waiting_runtime (prompt ack
	// is NOT completion; the turn is in flight until agent_end).
	p.setJobStatus(job.ID, gkdb.JobWaitingRuntime)

	// Wait for agent_end (turn completion). This is the prompt-ack-is-not-
	// completion contract: SendTurn returned immediately, but we block here
	// until agent_end.
	if ok, reason := p.waitForCompletion(jobCtx, events, job); !ok {
		if reason == "" {
			reason = "turn did not complete"
		}
		dead, _ := p.db.FailJob(job.ID, reason)
		if dead {
			p.notifyDeadLetter(job, reason)
		}
		return
	}

	if err := p.db.CompleteJob(job.ID); err != nil {
		p.logger.Error("worker: complete", "job", job.ID, "err", err)
	}

	// Loop-spec runner: if the thread has an enabled loop_spec, evaluate caps
	// and enqueue the next turn as a new job (mode "loop"). Per-thread
	// serialization in ClaimNextJob ensures the next turn runs only after this
	// one. This is the wired loop runner (Phase 5).
	if spec, _ := loadLoopSpec(p.db, thread.ID); spec != nil {
		if err := runLoop(ctx, p, job, thread, spec); err != nil {
			p.logger.Error("worker: loop enqueue", "thread", thread.ID, "err", err)
		}
	}
}

// startWorker starts a fresh worker or resumes an existing session.
func (p *Pool) startWorker(ctx context.Context, thread *gkdb.ThreadRow) (*runtime.RuntimeThreadRef, error) {
	if thread.SessionDir != "" {
		// Resume path: try to resume; fall back to fresh start.
		ref := &runtime.RuntimeThreadRef{
			Runtime:       "omp",
			SessionDir:    thread.SessionDir,
			WorkspacePath: thread.WorkspacePath,
		}
		if err := p.adapter.ResumeThread(ctx, ref); err == nil {
			return ref, nil
		}
		// resume failed — fall through to fresh start
	}
	return p.adapter.StartThread(ctx, thread.WorkspacePath, thread.SessionDir)
}

// waitForReady blocks until a ready event arrives or the context is cancelled.
func (p *Pool) waitForReady(ctx context.Context, events <-chan runtime.RuntimeEvent) bool {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return false
			}
			if ev.Kind == runtime.EventReady {
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
}

func (p *Pool) waitForCompletion(ctx context.Context, events <-chan runtime.RuntimeEvent, job *gkdb.JobRow) (bool, string) {
	// Per-turn timeout: a stuck worker (no agent_end) fails the turn rather
	// than holding the slot forever. Zero TurnTimeout disables this.
	var timeoutCh <-chan time.Time
	if p.cfg.TurnTimeout > 0 {
		timer := time.NewTimer(p.cfg.TurnTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return false, "runtime exited before completion"
			}
			switch ev.Kind {
			case runtime.EventAgentStart:
				// State: streaming (agent is producing output)
				p.setJobStatus(job.ID, gkdb.JobRunning)
			case runtime.EventAgentEnd:
				return true, ""
			case runtime.EventHostToolCall:
				_ = p.db.RecordAudit(job.ThreadID, job.ID, "host_tool_call", "agent",
					ev.ToolName+" "+ev.ToolArgs)
				risk := classifyToolRisk(ev.ToolName)
				if risk == gkdb.RiskHigh || risk == gkdb.RiskMedium {
					// Park the job: a risky host_tool_call requires approval.
					p.setJobStatus(job.ID, gkdb.JobWaitingApproval)
					if appr, err := p.db.RequestApproval(job.ID, risk,
						ev.ToolName+" "+truncateForLog(ev.ToolArgs, 80),
						ev.ToolName); err == nil && appr != nil {
						p.notifyApproval(appr)
					}
				}
			case runtime.EventError:
				p.logger.Warn("worker: error event", "job", job.ID, "payload", ev.Payload)
				return false, "runtime error: " + ev.Payload
			}
		case <-timeoutCh:
			p.logger.Warn("worker: turn timed out", "job", job.ID, "timeout", p.cfg.TurnTimeout)
			return false, "turn timed out"
		case <-ctx.Done():
			return false, "turn cancelled"
		}
	}
}

// registerInflight records a running job and returns a handle for cleanup.
func (p *Pool) registerInflight(job *gkdb.JobRow, cancel context.CancelFunc) *runningJob {
	r := &runningJob{jobID: job.ID, threadID: job.ThreadID, startedAt: time.Now(), cancel: cancel}
	p.mu.Lock()
	p.inflight[job.ThreadID] = r
	p.mu.Unlock()
	return r
}

func (p *Pool) unregisterInflight(threadID string) {
	p.mu.Lock()
	delete(p.inflight, threadID)
	p.mu.Unlock()
}

// requeue flips a job back to queued (used when we claimed but cannot run).
func (p *Pool) requeue(jobID string) error {
	// Use a targeted update rather than FailJob (which bumps attempts) — we
	// never actually started, so no attempt should be consumed.
	_, err := p.db.DB().Exec(
		`UPDATE jobs SET status='queued', next_run_at=NULL, updated_at=? WHERE id=?`,
		time.Now().Unix(), jobID)
	return err
}

// setJobStatus updates a job's status (state machine transition). Best-effort:
// logged on error, not fatal (the job's logical state follows the event stream
// regardless of the DB column).
func (p *Pool) setJobStatus(jobID, status string) {
	_, err := p.db.DB().Exec(
		`UPDATE jobs SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().Unix(), jobID)
	if err != nil {
		p.logger.Warn("worker: set job status", "job", jobID, "status", status, "err", err)
	}
}

// classifyToolRisk maps a host tool name to an approval risk level. Write/shell/
// network actions are high-risk (destructive or external); read-only tools are
// low. This is the approvals gate: an unapproved high-risk tool call would block
// the turn in a fully-wired runtime (the worker would pause on the pending
// approval).
func classifyToolRisk(toolName string) string {
	switch toolName {
	case "write", "edit", "bash", "shell", "exec", "rm", "git":
		return gkdb.RiskHigh
	case "network", "fetch", "http":
		return gkdb.RiskMedium
	default:
		return gkdb.RiskLow
	}
}

// truncateForLog caps a string at n chars for audit/summary fields.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// notifyApproval routes a pending approval through the channel gateway if one
// is wired. Best-effort: a gateway failure is logged, not fatal.
func (p *Pool) notifyApproval(a *gkdb.ApprovalRow) {
	if p.gateway == nil {
		return
	}
	if err := channel.NotifyApproval(p.gateway, a); err != nil {
		p.logger.Warn("worker: notify approval", "approval", a.ID, "err", err)
	}
}

// notifyDeadLetter fires a critical notification when a job is dead-lettered.
func (p *Pool) notifyDeadLetter(job *gkdb.JobRow, reason string) {
	if p.gateway == nil {
		return
	}
	n := &channel.Notification{
		ID:       job.ID,
		ThreadID: job.ThreadID,
		Severity: channel.SeverityCritical,
		Message:  "Job dead-lettered: " + reason,
		Channels: p.gateway.Policy.TargetsFor(channel.SeverityCritical),
	}
	if err := p.gateway.Send(n); err != nil {
		p.logger.Warn("worker: notify dead-letter", "job", job.ID, "err", err)
	}
}
