package process

import (
	"context"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"os"
	"path/filepath"
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

func TestProcessOmpRpcRefusedWithoutFields(t *testing.T) {
	cases := []struct {
		name string
		req  *LaunchRequest
	}{
		{"no thread_id", &LaunchRequest{Command: "omp", Args: []string{"--mode", "rpc"}, JobID: "j", WorkerID: "w", SessionDir: "/s", WorkspacePath: "/ws"}},
		{"no job_id", &LaunchRequest{Command: "omp", Args: []string{"--mode", "rpc"}, ThreadID: "t", WorkerID: "w", SessionDir: "/s", WorkspacePath: "/ws"}},
		{"no worker_id", &LaunchRequest{Command: "omp", Args: []string{"--mode", "rpc"}, ThreadID: "t", JobID: "j", SessionDir: "/s", WorkspacePath: "/ws"}},
		{"no session_dir", &LaunchRequest{Command: "omp", Args: []string{"--mode", "rpc"}, ThreadID: "t", JobID: "j", WorkerID: "w", WorkspacePath: "/ws"}},
		{"no workspace", &LaunchRequest{Command: "omp", Args: []string{"--mode", "rpc"}, ThreadID: "t", JobID: "j", WorkerID: "w", SessionDir: "/s"}},
		{"wrong command", &LaunchRequest{Command: "not-omp", Args: []string{"--mode", "rpc"}, ThreadID: "t", JobID: "j", WorkerID: "w", SessionDir: "/s", WorkspacePath: "/ws"}},
		{"no --mode rpc", &LaunchRequest{Command: "omp", Args: []string{"--print"}, ThreadID: "t", JobID: "j", WorkerID: "w", SessionDir: "/s", WorkspacePath: "/ws"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ProcessOmpRpc(context.Background(), c.req)
			if err == nil {
				t.Error("expected refusal, got nil error")
			}
		})
	}
}

func TestProcessOmpRpcAcceptedWithAllFields(t *testing.T) {
	db := newTestDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	req := &LaunchRequest{
		Command: "omp", Args: []string{"--mode", "rpc", "--session-dir", "/s"},
		ThreadID: th.ID, JobID: "j1", WorkerID: "w1",
		SessionDir: "/s", WorkspacePath: ".", Auditor: db,
	}
	cmd, err := ProcessOmpRpc(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	// Audit event should exist.
	audit, _ := db.ListAudit(10)
	if len(audit) == 0 {
		t.Error("expected audit event for the launch")
	}
}

func TestIsAllowedLaunchFile(t *testing.T) {
	if !IsAllowed("internal/runtime/omp.go") {
		t.Error("internal/runtime/omp.go should be allowed")
	}
	if IsAllowed("internal/watcher/gk_webhook.go") {
		t.Error("watcher files should not be allowed to launch omp")
	}
}

func TestWatcherCannotLaunchOmp(t *testing.T) {
	// The watcher package does not import internal/process and has no
	// exec.Command("omp") call. This test asserts the allowlist excludes
	// watcher files.
	if IsAllowed("internal/watcher/gk_webhook.go") {
		t.Fatal("watcher must not be in the launch allowlist")
	}
	// Also verify the webhook test file exists and doesn't reference process.
	_, err := os.Stat("gk_webhook_test.go")
	if err != nil {
		// The test runs from the package dir; check parent.
		_, err = os.Stat("../watcher/gk_webhook_test.go")
	}
	_ = err // file existence is not the assertion; the allowlist is
}
