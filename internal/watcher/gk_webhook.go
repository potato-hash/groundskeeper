// Package watcher ingests webhook events into Groundskeeper's durable
// substrate. A webhook event creates an audit record and can enqueue a turn
// for an existing thread. Watchers must NOT spawn orphan sessions outside
// Groundskeeper — they can only enqueue jobs for threads that already exist.
package watcher

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// WebhookServer receives webhook events and routes them to threads.
type WebhookServer struct {
	db     *gkdb.DB
	secret []byte // HMAC secret for signature verification (empty = no verification)
}

// NewWebhookServer creates a webhook server with an optional HMAC secret.
func NewWebhookServer(db *gkdb.DB, secret []byte) *WebhookServer {
	return &WebhookServer{db: db, secret: secret}
}

// Handler returns an http.Handler that processes webhook events.
func (s *WebhookServer) Handler() http.Handler {
	return http.HandlerFunc(s.handleWebhook)
}

// WebhookPayload is the expected event shape.
type WebhookPayload struct {
	ThreadID string `json:"thread_id"`
	Action   string `json:"action"` // "enqueue_turn" or "audit_only"
	Prompt   string `json:"prompt"` // for enqueue_turn
	Detail   string `json:"detail"` // for audit_only
}

func (s *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Verify HMAC signature if a secret is set.
	if len(s.secret) > 0 {
		sig := r.Header.Get("X-Webhook-Signature")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		if !s.verifySignature(body, sig) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
	}
	var payload WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Audit the webhook event.
	_ = s.db.RecordAudit(payload.ThreadID, "", "webhook_event", "watcher", payload.Action+" "+payload.Detail)

	switch payload.Action {
	case "enqueue_turn":
		// Watchers can only enqueue turns for EXISTING threads — they cannot
		// spawn orphan sessions outside Groundskeeper.
		thread, err := s.db.GetThread(payload.ThreadID)
		if err != nil || thread == nil {
			http.Error(w, "thread not found: "+payload.ThreadID, http.StatusNotFound)
			return
		}
		if thread.Status == gkdb.ThreadArchived {
			http.Error(w, "thread is archived", http.StatusForbidden)
			return
		}
		if payload.Prompt != "" {
			_ = s.db.SetThreadGoal(payload.ThreadID, payload.Prompt)
		}
		j, err := s.db.CreateJob(payload.ThreadID, "turn")
		if err != nil {
			http.Error(w, "enqueue failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"status":"enqueued","job_id":%q}`, j.ID)
	case "audit_only":
		fmt.Fprintf(w, `{"status":"audited"}`)
	default:
		http.Error(w, "unknown action: "+payload.Action, http.StatusBadRequest)
	}
}

// verifySignature checks the HMAC-SHA256 signature of the body.
func (s *WebhookServer) verifySignature(body []byte, sig string) bool {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}
