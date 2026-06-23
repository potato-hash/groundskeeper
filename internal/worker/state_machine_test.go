package worker

import (
	"context"
	"testing"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/runtime"
)

// TestPromptAckDoesNotCompleteJob: after SendTurn (prompt ack), the job is
// waiting_runtime, NOT done. Only agent_end completes it.
func TestPromptAckDoesNotCompleteJob(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	adapter.TurnDelay = 200 * time.Millisecond // delay so we can observe states
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond})

	th, _ := db.CreateThread("ack", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	j, _ := db.CreateJob(th.ID, "turn")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// After ~100ms the job should be claimed and SendTurn called (prompt ack),
	// so the job should be waiting_runtime — NOT done.
	time.Sleep(100 * time.Millisecond)
	got, _ := db.GetJob(j.ID)
	if got != nil && got.Status == gkdb.JobDone {
		t.Error("job is done after prompt ack — prompt ack is NOT completion")
	}
	if got != nil && got.Status == gkdb.JobWaitingRuntime {
		// Good — the state machine set it to waiting_runtime.
	}

	// Wait for completion.
	deadline := time.After(5 * time.Second)
	for {
		got, _ := db.GetJob(j.ID)
		if got != nil && got.Status == gkdb.JobDone {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not complete within 5s")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()
}

// TestAgentEndCompletesJob: agent_end event marks the job done.
func TestAgentEndCompletesJob(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond})

	th, _ := db.CreateThread("end", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	j, _ := db.CreateJob(th.ID, "turn")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	deadline := time.After(5 * time.Second)
	for {
		got, _ := db.GetJob(j.ID)
		if got != nil && got.Status == gkdb.JobDone {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not complete (agent_end did not fire)")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()
}

// TestProcessExitRetriesOrDeadLetters: when the worker process exits while the
// job is running (stream closes before agent_end), the job is failed/retried
// or dead-lettered.
func TestProcessExitRetriesOrDeadLetters(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	adapter.TurnDelay = -1 // stuck: never emits agent_end
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond, TurnTimeout: 100 * time.Millisecond})

	th, _ := db.CreateThread("exit", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	j, _ := db.CreateJob(th.ID, "turn")
	db.DB().Exec(`UPDATE jobs SET max_attempts=1 WHERE id=?`, j.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	deadline := time.After(5 * time.Second)
	for {
		got, _ := db.GetJob(j.ID)
		if got != nil && (got.Status == gkdb.JobDeadLetter || got.Status == gkdb.JobFailed) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not fail/dead-letter after process exit/timeout")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()
}
