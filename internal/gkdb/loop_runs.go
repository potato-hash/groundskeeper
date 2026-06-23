package gkdb

import (
	"database/sql"
	"fmt"
	"time"
)

// LoopRunStatus enum.
const (
	RunActive    = "active"
	RunStopped   = "stopped"
	RunCompleted = "completed"
	RunFailed    = "failed"
)

// LoopRunRow is a row in loop_runs.
type LoopRunRow struct {
	ID             string
	ThreadID       string
	LoopSpecID     string
	Status         string
	TurnsEnqueued  int64
	TurnsStarted   int64
	TurnsCompleted int64
	Attempts       int64
	StartedAt      int64
	EndedAt        int64 // 0 = still active
	StopReason     string
}

// StartLoopRun creates a new active loop run for a thread and returns it.
// There should be at most one active loop run per thread at a time.
func (g *DB) StartLoopRun(threadID, loopSpecID string) (*LoopRunRow, error) {
	now := time.Now().Unix()
	r := &LoopRunRow{
		ID:         newID(),
		ThreadID:   threadID,
		LoopSpecID: loopSpecID,
		Status:     RunActive,
		StartedAt:  now,
	}
	_, err := g.db.Exec(`INSERT INTO loop_runs
		(id, thread_id, loop_spec_id, status, turns_enqueued, turns_started,
		 turns_completed, attempts, started_at, ended_at, stop_reason)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.ThreadID, r.LoopSpecID, r.Status,
		0, 0, 0, 0, r.StartedAt, nil, "")
	if err != nil {
		return nil, fmt.Errorf("gkdb: start loop_run: %w", err)
	}
	return r, nil
}

// GetLoopRun returns one loop run by id.
func (g *DB) GetLoopRun(id string) (*LoopRunRow, error) {
	var r LoopRunRow
	var endedAt sql.NullInt64
	err := g.db.QueryRow(
		`SELECT id, thread_id, loop_spec_id, status, turns_enqueued,
			turns_started, turns_completed, attempts, started_at, ended_at,
			stop_reason
		 FROM loop_runs WHERE id=?`, id).
		Scan(&r.ID, &r.ThreadID, &r.LoopSpecID, &r.Status,
			&r.TurnsEnqueued, &r.TurnsStarted, &r.TurnsCompleted, &r.Attempts,
			&r.StartedAt, &endedAt, &r.StopReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: get loop_run: %w", err)
	}
	if endedAt.Valid {
		r.EndedAt = endedAt.Int64
	}
	return &r, nil
}

// GetActiveLoopRun returns the active loop run for a thread (nil if none).
func (g *DB) GetActiveLoopRun(threadID string) (*LoopRunRow, error) {
	var r LoopRunRow
	var endedAt sql.NullInt64
	err := g.db.QueryRow(
		`SELECT id, thread_id, loop_spec_id, status, turns_enqueued,
			turns_started, turns_completed, attempts, started_at, ended_at,
			stop_reason
		 FROM loop_runs WHERE thread_id=? AND status='active' LIMIT 1`, threadID).
		Scan(&r.ID, &r.ThreadID, &r.LoopSpecID, &r.Status,
			&r.TurnsEnqueued, &r.TurnsStarted, &r.TurnsCompleted, &r.Attempts,
			&r.StartedAt, &endedAt, &r.StopReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: get active loop_run: %w", err)
	}
	if endedAt.Valid {
		r.EndedAt = endedAt.Int64
	}
	return &r, nil
}

// IncrementTurnEnqueued atomically increments the turns_enqueued counter for a
// loop run and returns the new value. This is the turn counter that max_turns
// is checked against — it counts how many turns have been queued, NOT how many
// attempts have been made (retries don't increment it).
func (g *DB) IncrementTurnEnqueued(loopRunID string) (int64, error) {
	_, err := g.db.Exec(
		`UPDATE loop_runs SET turns_enqueued = turns_enqueued + 1 WHERE id=?`,
		loopRunID)
	if err != nil {
		return 0, fmt.Errorf("gkdb: increment turns_enqueued: %w", err)
	}
	var n int64
	err = g.db.QueryRow(
		`SELECT turns_enqueued FROM loop_runs WHERE id=?`, loopRunID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("gkdb: read turns_enqueued: %w", err)
	}
	return n, nil
}

// IncrementTurnCompleted increments the turns_completed counter for a loop run.
func (g *DB) IncrementTurnCompleted(loopRunID string) error {
	_, err := g.db.Exec(
		`UPDATE loop_runs SET turns_completed = turns_completed + 1 WHERE id=?`,
		loopRunID)
	if err != nil {
		return fmt.Errorf("gkdb: increment turns_completed: %w", err)
	}
	return nil
}

// IncrementAttempts increments the attempts counter for a loop run.
func (g *DB) IncrementAttempts(loopRunID string) error {
	_, err := g.db.Exec(
		`UPDATE loop_runs SET attempts = attempts + 1 WHERE id=?`, loopRunID)
	if err != nil {
		return fmt.Errorf("gkdb: increment attempts: %w", err)
	}
	return nil
}

// StopLoopRun marks a loop run as stopped/completed/failed with a reason.
func (g *DB) StopLoopRun(loopRunID, status, reason string) error {
	_, err := g.db.Exec(
		`UPDATE loop_runs SET status=?, ended_at=?, stop_reason=? WHERE id=?`,
		status, time.Now().Unix(), reason, loopRunID)
	if err != nil {
		return fmt.Errorf("gkdb: stop loop_run: %w", err)
	}
	return nil
}
