package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/runtime"
)

// TestLoopMaxTurnsEnqueuesExactly: max_turns=3 enqueues exactly 3 loop turns
// and no fourth. The loop_run counter stops the loop after 3.
func TestLoopMaxTurnsEnqueuesExactly(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond})

	th, _ := db.CreateThread("max3", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)

	// Create a loop spec with max_turns=3.
	db.CreateLoopSpec(th.ID, "until_done", "go", 3, 60, 100, 0, "agent_says_done")

	// Start the loop: create a loop_run and enqueue the first turn.
	run, _ := db.StartLoopRun(th.ID, "")
	db.IncrementTurnEnqueued(run.ID)
	db.CreateJobWithLoop(th.ID, "turn", run.ID, "turn-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for the loop to finish (all 3 turns complete, loop stopped).
	deadline := time.After(10 * time.Second)
	for {
		updatedRun, _ := db.GetLoopRun(run.ID)
		if updatedRun != nil && updatedRun.Status != gkdb.RunActive {
			break // loop stopped
		}
		select {
		case <-deadline:
			t.Fatal("loop did not stop within 10s")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	pool.Stop()

	// Assert: exactly 3 turns enqueued, no fourth.
	finalRun, _ := db.GetLoopRun(run.ID)
	if finalRun.TurnsEnqueued != 3 {
		t.Errorf("turns_enqueued = %d, want 3", finalRun.TurnsEnqueued)
	}
	if finalRun.Status != gkdb.RunCompleted && finalRun.Status != gkdb.RunStopped {
		t.Errorf("run status = %s, want completed/stopped", finalRun.Status)
	}

	// Count done jobs — should be exactly 3 (no 4th queued).
	doneJobs, _ := db.ListJobs(gkdb.JobDone)
	if len(doneJobs) != 3 {
		t.Errorf("done jobs = %d, want 3", len(doneJobs))
	}
	// No queued jobs remaining (the 4th was never enqueued).
	queuedJobs, _ := db.ListJobs(gkdb.JobQueued)
	if len(queuedJobs) != 0 {
		t.Errorf("queued jobs = %d, want 0 (no 4th turn)", len(queuedJobs))
	}
}

// TestLoopCounterPersistsAcrossReopen: the loop_run counter survives a DB
// close+reopen (daemon restart).
func TestLoopCounterPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gk.db")

	db1, err := gkdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	th, _ := db1.CreateThread("persist", "omp", ".")
	db1.CreateLoopSpec(th.ID, "until_done", "go", 5, 60, 100, 0, "")
	run, _ := db1.StartLoopRun(th.ID, "")
	db1.IncrementTurnEnqueued(run.ID)
	db1.IncrementTurnEnqueued(run.ID) // 2 turns enqueued
	db1.Close()

	// Reopen and verify the counter persisted.
	db2, err := gkdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	run2, _ := db2.GetLoopRun(run.ID)
	if run2 == nil {
		t.Fatal("loop_run did not persist")
	}
	if run2.TurnsEnqueued != 2 {
		t.Errorf("turns_enqueued after reopen = %d, want 2", run2.TurnsEnqueued)
	}
	if run2.Status != gkdb.RunActive {
		t.Errorf("run status = %s, want active", run2.Status)
	}
}

// TestTwoLoopRunsSeparateCounters: two loop runs on one thread have separate
// turn counters. The first run's counter does not affect the second.
func TestTwoLoopRunsSeparateCounters(t *testing.T) {
	db := newTestPoolDB(t)
	th, _ := db.CreateThread("two-runs", "omp", ".")

	// First run: 2 turns.
	run1, _ := db.StartLoopRun(th.ID, "")
	db.IncrementTurnEnqueued(run1.ID)
	db.IncrementTurnEnqueued(run1.ID)
	db.StopLoopRun(run1.ID, gkdb.RunCompleted, "done")

	// Second run: 1 turn.
	run2, _ := db.StartLoopRun(th.ID, "")
	db.IncrementTurnEnqueued(run2.ID)

	r1, _ := db.GetLoopRun(run1.ID)
	r2, _ := db.GetLoopRun(run2.ID)
	if r1.TurnsEnqueued != 2 {
		t.Errorf("run1 turns_enqueued = %d, want 2", r1.TurnsEnqueued)
	}
	if r2.TurnsEnqueued != 1 {
		t.Errorf("run2 turns_enqueued = %d, want 1", r2.TurnsEnqueued)
	}
	if r1.ID == r2.ID {
		t.Error("two runs should have different IDs")
	}
}

// TestRetryDoesNotCountAsNewTurn: when a job fails and is retried (requeued),
// the turns_enqueued counter does NOT increment. Only new loop turns increment it.
func TestRetryDoesNotCountAsNewTurn(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	adapter.TurnDelay = -1 // stuck: turn never completes
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond, TurnTimeout: 100 * time.Millisecond})

	th, _ := db.CreateThread("retry", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	// max_turns=10, max_attempts=1 (so it dead-letters on first failure)
	db.CreateLoopSpec(th.ID, "until_done", "go", 10, 60, 100, 0, "")
	run, _ := db.StartLoopRun(th.ID, "")
	db.IncrementTurnEnqueued(run.ID) // 1 turn enqueued
	j, _ := db.CreateJobWithLoop(th.ID, "turn", run.ID, "turn-1")
	db.DB().Exec(`UPDATE jobs SET max_attempts=1 WHERE id=?`, j.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for the job to dead-letter (turn timeout -> fail -> dead-letter).
	deadline := time.After(5 * time.Second)
	for {
		got, _ := db.GetJob(j.ID)
		if got != nil && got.Status == gkdb.JobDeadLetter {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not dead-letter within 5s")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()

	// The turn counter should still be 1 (the retry/dead-letter did not
	// increment turns_enqueued).
	finalRun, _ := db.GetLoopRun(run.ID)
	if finalRun.TurnsEnqueued != 1 {
		t.Errorf("turns_enqueued = %d, want 1 (retry should not count as new turn)", finalRun.TurnsEnqueued)
	}
}
