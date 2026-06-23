package gkdb

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestDB opens a fresh DB in a temp dir and closes it on test end.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "gk.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestThreadPersistsAcrossReopen: a thread created in one DB instance must be
// visible after Close + Open on the same file.
func TestThreadPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gk.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := db1.CreateThread("fix leak", "omp", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	got, err := db2.GetThread(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("thread did not persist across reopen")
	}
	if got.Title != "fix leak" || got.Runtime != "omp" || got.WorkspacePath != "/tmp/ws" {
		t.Fatalf("persisted thread mismatch: %+v", got)
	}
	if got.Status != ThreadIdle {
		t.Fatalf("status = %q, want idle", got.Status)
	}
}

// TestJobPersists: a created job is retrievable and starts queued.
func TestJobPersists(t *testing.T) {
	db := newTestDB(t)
	th, err := db.CreateThread("t", "omp", ".")
	if err != nil {
		t.Fatal(err)
	}
	j, err := db.CreateJob(th.ID, "turn")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Status != JobQueued || got.ThreadID != th.ID {
		t.Fatalf("job mismatch: %+v", got)
	}
}

// TestApprovalRequestThenResolvePersists: an approval persists and resolves.
func TestApprovalRequestThenResolvePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gk.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	th, _ := db1.CreateThread("t", "omp", ".")
	job, _ := db1.CreateJob(th.ID, "turn")
	a, err := db1.RequestApproval(job.ID, RiskHigh, "run rm -rf", "delete files")
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	pending, err := db2.ListPendingApprovals()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != a.ID {
		t.Fatalf("pending mismatch: %+v", pending)
	}
	if err := db2.ResolveApproval(a.ID, false, "tester"); err != nil {
		t.Fatal(err)
	}
	pending2, _ := db2.ListPendingApprovals()
	if len(pending2) != 0 {
		t.Fatalf("expected no pending after reject, got %d", len(pending2))
	}
}

// TestAuditRedactsSensitiveValues: a sensitive detail must be redacted on insert,
// not stored raw. This is the trust-boundary test.
func TestAuditRedactsSensitiveValues(t *testing.T) {
	db := newTestDB(t)
	// Build a sensitive string from fragments so the test source does not hold
	// a contiguous secret literal (same discipline as redaction.go).
	secret := "Authorization: Bearer " + tokenChars(40)
	if err := db.RecordAudit("thread1", "job1", "send_message", "agent", secret); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListAudit(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].Detail == secret {
		t.Fatal("audit stored raw sensitive value (trust boundary violation)")
	}
	if !contains(rows[0].Detail, "[REDACTED]") {
		t.Fatalf("audit detail not redacted: %q", rows[0].Detail)
	}
}

// TestClaimNextJobSerializesSameThread: two queued jobs for the same thread — only
// one can be claimed at a time (per-thread serialization).
func TestClaimNextJobSerializesSameThread(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	j1, _ := db.CreateJob(th.ID, "turn")
	j2, _ := db.CreateJob(th.ID, "turn")
	now := time.Now()

	first, err := db.ClaimNextJob(now)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.ID != j1.ID {
		t.Fatalf("first claim = %+v, want j1 %s", first, j1.ID)
	}
	// Second claim for the SAME thread must return nil (j1 is running).
	second, err := db.ClaimNextJob(now)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatalf("second claim should be nil (thread busy), got %+v", second)
	}
	// Complete j1; now j2 is claimable.
	if err := db.CompleteJob(j1.ID); err != nil {
		t.Fatal(err)
	}
	third, err := db.ClaimNextJob(now)
	if err != nil {
		t.Fatal(err)
	}
	if third == nil || third.ID != j2.ID {
		t.Fatalf("third claim = %+v, want j2 %s", third, j2.ID)
	}
	_ = j2
}

// TestClaimNextJobDifferentThreadsConcurrent: two jobs for DIFFERENT threads can
// both be claimed (no cross-thread serialization).
func TestClaimNextJobDifferentThreadsConcurrent(t *testing.T) {
	db := newTestDB(t)
	th1, _ := db.CreateThread("t1", "omp", ".")
	th2, _ := db.CreateThread("t2", "omp", ".")
	_, _ = db.CreateJob(th1.ID, "turn")
	_, _ = db.CreateJob(th2.ID, "turn")
	now := time.Now()

	a, err := db.ClaimNextJob(now)
	if err != nil || a == nil {
		t.Fatalf("first claim failed: %v", a)
	}
	b, err := db.ClaimNextJob(now)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("second claim for a different thread should succeed, got nil")
	}
	if a.ThreadID == b.ThreadID {
		t.Fatalf("claimed two jobs on same thread: %s", a.ThreadID)
	}
}

// TestResetStuckRunningRequeues: a running job is requeued to queued on reset.
func TestResetStuckRunningRequeues(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	j, _ := db.CreateJob(th.ID, "turn")
	now := time.Now()
	claimed, err := db.ClaimNextJob(now)
	if err != nil || claimed == nil {
		t.Fatal("claim failed")
	}
	// Simulate a crash: job is left running. Reset should requeue it.
	n, err := db.ResetStuckRunning()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reset requeued %d, want 1", n)
	}
	got, _ := db.GetJob(j.ID)
	if got.Status != JobQueued {
		t.Fatalf("after reset status = %q, want queued", got.Status)
	}
	if got.NextRunAt != 0 {
		t.Fatalf("after reset next_run_at = %d, want 0 (NULL)", got.NextRunAt)
	}
}

// TestFailJobDeadLettersAfterMaxAttempts: a job that fails repeatedly past
// max_attempts is dead-lettered.
func TestFailJobDeadLettersAfterMaxAttempts(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	j, _ := db.CreateJob(th.ID, "turn")
	// max_attempts is 3; cycle claim+fail three times.
	now := time.Now()
	for i := 0; i < 3; i++ {
		// Advance past the retry backoff so the requeued job is due again.
		now = now.Add(time.Duration(i+1) * time.Second)
		claimed, err := db.ClaimNextJob(now)
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			t.Fatalf("claim %d returned nil", i)
		}
		dead, err := db.FailJob(claimed.ID, "boom")
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && dead {
			t.Fatalf("attempt %d dead-lettered early", i)
		}
		if i == 2 && !dead {
			t.Fatal("expected dead-letter on final attempt")
		}
	}
	got, _ := db.GetJob(j.ID)
	if got.Status != JobDeadLetter {
		t.Fatalf("status = %q, want dead_letter", got.Status)
	}
}

// TestArchiveThreadHidesFromList: an archived thread is hidden by default.
func TestArchiveThreadHidesFromList(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	visible, _ := db.ListThreads(false)
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible, got %d", len(visible))
	}
	if err := db.ArchiveThread(th.ID); err != nil {
		t.Fatal(err)
	}
	visible2, _ := db.ListThreads(false)
	if len(visible2) != 0 {
		t.Fatalf("archived thread still visible by default: %d", len(visible2))
	}
	all, _ := db.ListThreads(true)
	if len(all) != 1 {
		t.Fatalf("archived thread not in includeArchived list: %d", len(all))
	}
}

// tokenChars returns n deterministic token-like characters for test secrets.
// Kept here so test sources never embed a real-looking secret literal.
func tokenChars(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789-_"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[i%len(chars)]
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
