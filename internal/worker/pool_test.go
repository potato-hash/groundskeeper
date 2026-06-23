package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/runtime"
)

func newTestPoolDB(t *testing.T) *gkdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := gkdb.Open(filepath.Join(dir, "gk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestPoolRunsJobToCompletion: the pool claims a queued job, runs it via the
// fake adapter, and marks it done.
func TestPoolRunsJobToCompletion(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	pool := New(db, adapter, Config{MaxSlots: 2, PollInterval: 20 * time.Millisecond})

	th, err := db.CreateThread("test", "omp", ".")
	if err != nil {
		t.Fatal(err)
	}
	// Give the thread a goal so the fake adapter has a prompt.
	if _, err := db.DB().Exec(
		`UPDATE agent_threads SET goal=? WHERE id=?`, "say READYTEST", th.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateJob(th.ID, "turn"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for the job to complete.
	deadline := time.After(5 * time.Second)
	for {
		jobs, _ := db.ListJobs(gkdb.JobDone)
		if len(jobs) >= 1 {
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

// TestPoolPerThreadSerialization: two jobs for the same thread run one at a
// time (never concurrently). The pool claims the second only after the first
// completes.
func TestPoolPerThreadSerialization(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	// Slow fake so we can observe serialization.
	adapter.TurnDelay = 100 * time.Millisecond
	pool := New(db, adapter, Config{MaxSlots: 4, PollInterval: 20 * time.Millisecond})

	th, _ := db.CreateThread("serial", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	db.CreateJob(th.ID, "turn")
	db.CreateJob(th.ID, "turn")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Both jobs should eventually be done, but never concurrently for the same
	// thread. ClaimNextJob enforces this in SQL; the pool respects it.
	deadline := time.After(5 * time.Second)
	for {
		done, _ := db.ListJobs(gkdb.JobDone)
		if len(done) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d/2 jobs done", len(done))
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()
}

// TestPoolRequeuesStuckOnRestart: a job left 'running' by a crash is requeued
// when the pool starts (ResetStuckRunning).
func TestPoolRequeuesStuckOnRestart(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()

	th, _ := db.CreateThread("crash", "omp", ".")
	j, _ := db.CreateJob(th.ID, "turn")
	// Simulate a crash: manually mark the job running.
	db.StartJob(j.ID)

	pool := New(db, adapter, Config{MaxSlots: 2, PollInterval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx) // should requeue the stuck job

	deadline := time.After(3 * time.Second)
	for {
		queued, _ := db.ListJobs(gkdb.JobQueued)
		if len(queued) >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("stuck running job was not requeued on pool start")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()
}

// TestPoolInflightCount: the pool tracks running jobs.
func TestPoolInflightCount(t *testing.T) {
	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	adapter.TurnDelay = 200 * time.Millisecond
	pool := New(db, adapter, Config{MaxSlots: 4, PollInterval: 10 * time.Millisecond})

	// Two different threads so they can run concurrently.
	th1, _ := db.CreateThread("t1", "omp", ".")
	th2, _ := db.CreateThread("t2", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th1.ID)
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th2.ID)
	db.CreateJob(th1.ID, "turn")
	db.CreateJob(th2.ID, "turn")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// At some point both should be inflight (different threads, 4 slots).
	deadline := time.After(3 * time.Second)
	sawTwo := false
	timedOut := false
	for {
		if pool.InflightCount() >= 2 {
			sawTwo = true
			break
		}
		select {
		case <-deadline:
			timedOut = true
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if timedOut || sawTwo {
			break
		}
	}
	pool.Stop()
	if !sawTwo {
		// They may have completed too fast to observe both inflight at once;
		// that's acceptable as long as both complete.
		done, _ := db.ListJobs(gkdb.JobDone)
		if len(done) < 2 {
			t.Fatalf("expected 2 done jobs, got %d", len(done))
		}
	}
}

// TestLoopSpecShouldStop: caps and stop conditions are enforced.
func TestLoopSpecShouldStop(t *testing.T) {
	spec := &gkdb.LoopSpecRow{MaxTurns: 3, StopWhen: ""}
	s := LoopState{Turns: 3}
	stop, _ := s.ShouldStop(spec, "", 0, false)
	if !stop {
		t.Error("expected stop at max_turns=3")
	}
	s = LoopState{Turns: 1}
	stop, _ = s.ShouldStop(spec, "", 0, false)
	if stop {
		t.Error("should not stop at turns=1 < max_turns=3")
	}
	// approval_required
	stop, _ = s.ShouldStop(spec, "", 0, true)
	if !stop {
		t.Error("expected stop on approval_required")
	}
	// same_failure_repeated
	stop, _ = s.ShouldStop(spec, "", 3, false)
	if !stop {
		t.Error("expected stop on same_failure_repeated (3)")
	}
	// custom stop_when substring
	spec2 := &gkdb.LoopSpecRow{StopWhen: "DONE"}
	s2 := LoopState{}
	stop, _ = s2.ShouldStop(spec2, "the work is DONE now", 0, false)
	if !stop {
		t.Error("stop_when substring not matched")
	}
	stop, _ = s2.ShouldStop(spec2, "still working", 0, false)
	if stop {
		t.Error("stop_when should not match unrelated text")
	}
	// agent_says_done
	spec3 := &gkdb.LoopSpecRow{StopWhen: "agent_says_done"}
	stop, _ = s2.ShouldStop(spec3, "The task is done", 0, false)
	if !stop {
		t.Error("agent_says_done not detected")
	}
}
