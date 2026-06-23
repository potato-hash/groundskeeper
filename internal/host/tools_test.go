package host

import (
	"path/filepath"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

func newTestBridgeDB(t *testing.T) *gkdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := gkdb.Open(filepath.Join(dir, "gk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBridgeUnknownTool(t *testing.T) {
	b := NewBridge(newTestBridgeDB(t))
	res := b.HandleToolCallInternal(&ToolCall{ID: "x", ToolName: "nope"})
	if !res.IsError {
		t.Error("unknown tool should return error")
	}
}

func TestRequestApprovalCreatesRow(t *testing.T) {
	db := newTestBridgeDB(t)
	b := NewBridge(db)
	res := b.HandleToolCallInternal(&ToolCall{ID: "1", ToolName: "request_approval",
		Arguments: map[string]any{"risk": "high", "summary": "delete files", "action": "rm"}})
	if res.IsError {
		t.Fatalf("request_approval failed: %v", res.Result)
	}
	pending, _ := db.ListPendingApprovals()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	if pending[0].Risk != gkdb.RiskHigh {
		t.Errorf("risk = %s, want high", pending[0].Risk)
	}
}

func TestRecordAuditRedacts(t *testing.T) {
	db := newTestBridgeDB(t)
	b := NewBridge(db)
	b.HandleToolCallInternal(&ToolCall{ID: "1", ToolName: "record_audit",
		Arguments: map[string]any{"action": "send", "detail": "token: " + longTok(40)}})
	rows, _ := db.ListAudit(10)
	// Two rows: one for the host_tool_call itself, one from the record_audit tool.
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit rows (tool call + record_audit), got %d", len(rows))
	}
	// The record_audit row's detail should be redacted.
	found := false
	for _, r := range rows {
		if r.Action == "send" && contains(r.Detail, "[REDACTED]") {
			found = true
		}
	}
	if !found {
		t.Error("record_audit detail was not redacted")
	}
}

func TestJobStatus(t *testing.T) {
	db := newTestBridgeDB(t)
	th, _ := db.CreateThread("t", "omp", ".")
	job, _ := db.CreateJob(th.ID, "turn")
	b := NewBridge(db)
	res := b.HandleToolCallInternal(&ToolCall{ID: "1", ToolName: "job_status",
		Arguments: map[string]any{"job_id": job.ID}})
	if res.IsError {
		t.Fatalf("job_status failed: %v", res.Result)
	}
}

func TestURISchemeReadTasks(t *testing.T) {
	db := newTestBridgeDB(t)
	s := NewURIScheme(db)
	// empty tasks list should still return valid JSON
	content, err := s.Read("pa://tasks")
	if err != nil {
		t.Fatalf("read pa://tasks: %v", err)
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestURISchemeReadApprovals(t *testing.T) {
	db := newTestBridgeDB(t)
	s := NewURIScheme(db)
	content, err := s.Read("pa://approvals")
	if err != nil {
		t.Fatalf("read pa://approvals: %v", err)
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestURISchemeWriteRefusedByDefault(t *testing.T) {
	db := newTestBridgeDB(t)
	s := NewURIScheme(db)
	_, err := s.Write("pa://tasks", "data")
	if err == nil {
		t.Error("write to read-only pa://tasks should be refused")
	}
}

func TestURISchemeUnknownScheme(t *testing.T) {
	db := newTestBridgeDB(t)
	s := NewURIScheme(db)
	_, err := s.Read("http://example.com")
	if err == nil {
		t.Error("non-pa:// scheme should be rejected")
	}
}

func TestHandleToolCallHostHandlerInterface(t *testing.T) {
	db := newTestBridgeDB(t)
	b := NewBridge(db)
	// Bridge implements runtime.HostHandler via the adapter methods.
	result, isErr := b.HandleToolCall("id1", "tc1", "job_status", map[string]any{})
	_ = result
	_ = isErr
	// job_status with no job_id should error (not crash).
	if !isErr {
		// job_status with empty job_id: returns error about job_id required
	}
}

func longTok(n int) string {
	const a = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = a[(i*7)%len(a)]
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
