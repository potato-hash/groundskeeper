package fleet

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

func newTestDB(t *testing.T) *gkdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := gkdb.Open(filepath.Join(dir, "gk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLoadAndCounts(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t1", "omp", ".")
	job, _ := db.CreateJob(th.ID, "turn")
	db.RequestApproval(job.ID, gkdb.RiskMedium, "summary", "act")

	v, err := Load(db)
	if err != nil {
		t.Fatal(err)
	}
	threads, running, pending, dead := v.Counts()
	if threads != 1 {
		t.Errorf("threads = %d, want 1", threads)
	}
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}
	if running != 0 {
		t.Errorf("running = %d, want 0", running)
	}
	if dead != 0 {
		t.Errorf("dead = %d, want 0", dead)
	}
}

func TestRenderText(t *testing.T) {
	db := newTestDB(t)
	_, _ = db.CreateThread("render-test", "omp", "/tmp")
	v, err := Load(db)
	if err != nil {
		t.Fatal(err)
	}
	out := v.RenderText()
	if !strings.Contains(out, "threads: 1") {
		t.Errorf("render missing threads count: %q", out)
	}
	if !strings.Contains(out, "render-test") {
		t.Errorf("render missing thread title: %q", out)
	}
}

func TestRenderTUI(t *testing.T) {
	db := newTestDB(t)
	_, _ = db.CreateThread("tui-test", "omp", "/tmp")
	v, err := Load(db)
	if err != nil {
		t.Fatal(err)
	}
	out := v.RenderTUI(40)
	if !strings.Contains(out, "Groundskeeper Fleet") {
		t.Errorf("tui render missing title: %q", out)
	}
	if !strings.Contains(out, "tui-test") {
		t.Errorf("tui render missing thread: %q", out)
	}
}
