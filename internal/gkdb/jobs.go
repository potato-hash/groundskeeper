package gkdb

import (
	"database/sql"
	"fmt"
	"time"
)

// Job status enum (the job state machine).
//
// queued           → waiting on the queue
// running          → claimed and dispatched to a worker
// waiting_runtime  → prompt sent, awaiting agent response (between ack and agent_end)
// waiting_approval → a host_tool_call requiring approval parked the job
// done             → turn completed (agent_end received)
// retry            → failed but retries remain; requeued
// failed           → failed (terminal, no retries)
// dead_letter      → exhausted retries, moved to dead_letters
const (
	JobQueued          = "queued"
	JobRunning         = "running"
	JobWaitingRuntime  = "waiting_runtime"
	JobWaitingApproval = "waiting_approval"
	JobDone            = "done"
	JobRetry           = "retry"
	JobFailed          = "failed"
	JobDeadLetter      = "dead_letter"
)

// JobRow is a row in jobs.
type JobRow struct {
	ID          string
	ThreadID    string
	TaskID      string
	Status      string
	Kind        string
	Attempts    int64
	MaxAttempts int64
	NextRunAt   int64  // 0 = NULL (run immediately)
	LoopRunID   string // associated loop run (empty = standalone job)
	TurnID      string // associated thread turn (empty = no turn)
	CreatedAt   int64
	UpdatedAt   int64
}

// CreateJob inserts a new queued job for a thread and returns it.
func (g *DB) CreateJob(threadID, kind string) (*JobRow, error) {
	return g.CreateJobWithLoop(threadID, kind, "", "")
}

// CreateJobWithLoop inserts a queued job associated with a loop run and turn.
func (g *DB) CreateJobWithLoop(threadID, kind, loopRunID, turnID string) (*JobRow, error) {
	now := time.Now().Unix()
	j := &JobRow{
		ID:          newID(),
		ThreadID:    threadID,
		Status:      JobQueued,
		Kind:        kind,
		MaxAttempts: 3,
		LoopRunID:   loopRunID,
		TurnID:      turnID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := g.db.Exec(`INSERT INTO jobs
		(id, thread_id, task_id, status, kind, attempts, max_attempts,
		 next_run_at, created_at, updated_at, loop_run_id, turn_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.ThreadID, j.TaskID, j.Status, j.Kind, j.Attempts, j.MaxAttempts,
		nil, j.CreatedAt, j.UpdatedAt, j.LoopRunID, j.TurnID)
	if err != nil {
		return nil, fmt.Errorf("gkdb: create job: %w", err)
	}
	return j, nil
}

// GetJob returns one job by id, or (nil, nil) if not found.
func (g *DB) GetJob(id string) (*JobRow, error) {
	var j JobRow
	var nextRun sql.NullInt64
	err := g.db.QueryRow(`SELECT id, thread_id, task_id, status, kind, attempts,
		max_attempts, next_run_at, created_at, updated_at, loop_run_id, turn_id
		FROM jobs WHERE id=?`, id).
		Scan(&j.ID, &j.ThreadID, &j.TaskID, &j.Status, &j.Kind, &j.Attempts,
			&j.MaxAttempts, &nextRun, &j.CreatedAt, &j.UpdatedAt,
			&j.LoopRunID, &j.TurnID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: get job: %w", err)
	}
	if nextRun.Valid {
		j.NextRunAt = nextRun.Int64
	}
	return &j, nil
}
func (g *DB) ListJobs(status string) ([]JobRow, error) {
	q := `SELECT id, thread_id, task_id, status, kind, attempts, max_attempts,
		next_run_at, created_at, updated_at, loop_run_id, turn_id FROM jobs`
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		q += ` ORDER BY created_at ASC`
		rows, err = g.db.Query(q)
	} else {
		q += ` WHERE status=? ORDER BY created_at ASC`
		rows, err = g.db.Query(q, status)
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: list jobs: %w", err)
	}
	defer rows.Close()
	var out []JobRow
	for rows.Next() {
		var j JobRow
		var nextRun sql.NullInt64
		if err := rows.Scan(&j.ID, &j.ThreadID, &j.TaskID, &j.Status, &j.Kind,
			&j.Attempts, &j.MaxAttempts, &nextRun, &j.CreatedAt, &j.UpdatedAt,
			&j.LoopRunID, &j.TurnID); err != nil {
			return nil, fmt.Errorf("gkdb: list jobs scan: %w", err)
		}
		if nextRun.Valid {
			j.NextRunAt = nextRun.Int64
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// StartJob marks a job running (manual start, not the claim path).
func (g *DB) StartJob(id string) error {
	_, err := g.db.Exec(
		`UPDATE jobs SET status='running', updated_at=? WHERE id=?`,
		time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("gkdb: start job: %w", err)
	}
	return nil
}

// CompleteJob marks a job done.
func (g *DB) CompleteJob(id string) error {
	_, err := g.db.Exec(
		`UPDATE jobs SET status='done', updated_at=? WHERE id=?`,
		time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("gkdb: complete job: %w", err)
	}
	return nil
}

// FailJob records a failure. If attempts >= max_attempts it dead-letters the job
// and returns deadLettered=true; otherwise it requeues the job for retry.
func (g *DB) FailJob(id, errMsg string) (bool, error) {
	j, err := g.GetJob(id)
	if err != nil {
		return false, err
	}
	if j == nil {
		return false, fmt.Errorf("gkdb: fail job: not found: %s", id)
	}
	if j.Attempts >= j.MaxAttempts {
		if err := g.DeadLetter(id, errMsg); err != nil {
			return false, err
		}
		return true, nil
	}
	// requeue with a short backoff: next_run_at = now + 1s per attempt
	backoff := j.Attempts
	_, err = g.db.Exec(
		`UPDATE jobs SET status='queued', next_run_at=?, updated_at=? WHERE id=?`,
		time.Now().Unix()+backoff, time.Now().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("gkdb: fail job requeue: %w", err)
	}
	return false, nil
}
