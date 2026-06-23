package watcher

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

func newWatcherDB(t *testing.T) *gkdb.DB {
	dir := t.TempDir()
	db, err := gkdb.Open(filepath.Join(dir, "gk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWebhookBadSignature(t *testing.T) {
	db := newWatcherDB(t)
	srv := NewWebhookServer(db, []byte("secret"))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body, _ := json.Marshal(WebhookPayload{Action: "audit_only"})
	req, _ := http.NewRequest("POST", ts.URL, bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", "badsig")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad sig, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestWebhookEnqueuesTurn(t *testing.T) {
	db := newWatcherDB(t)
	th, _ := db.CreateThread("webhook-test", "omp", ".")
	srv := NewWebhookServer(db, nil) // no signature verification
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body, _ := json.Marshal(WebhookPayload{
		Action:   "enqueue_turn",
		ThreadID: th.ID,
		Prompt:   "do the thing",
	})
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	jobs, _ := db.ListJobs(gkdb.JobQueued)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 queued job, got %d", len(jobs))
	}
	// Audit record should exist.
	audit, _ := db.ListAudit(10)
	if len(audit) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(audit))
	}
}

func TestWebhookRejectsUnknownThread(t *testing.T) {
	db := newWatcherDB(t)
	srv := NewWebhookServer(db, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body, _ := json.Marshal(WebhookPayload{
		Action:   "enqueue_turn",
		ThreadID: "nonexistent",
	})
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown thread, got %d", resp.StatusCode)
	}
}
