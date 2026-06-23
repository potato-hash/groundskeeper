// Package sidecar implements Groundskeeper's credential-holding sidecar
// processes. Each sidecar (email, calendar, contact) holds a platform
// credential (SMTP password, OAuth token, address-book token) that the daemon
// never sees, and serves HMAC-signed delivery requests from the daemon over a
// local HTTP endpoint.
//
// The daemon signs each request with a shared HMAC key; the sidecar verifies
// the signature (channel.VerifyRequest) before performing the privileged
// action. A compromised daemon cannot exfiltrate the credential — it can only
// ask the sidecar to send a notification, which the sidecar does with its own
// credential.
package sidecar

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// Config configures one sidecar instance.
type Config struct {
	// Addr is the listen address (e.g. "127.0.0.1:7780").
	Addr string
	// HMACKey is the shared signing key (must match the daemon's SidecarClient.Key).
	HMACKey []byte
	// Handler is the channel-specific privileged action (send email, insert
	// calendar event, look up contact). It receives the verified payload.
	Handler Handler
}

// Handler performs the privileged action for a verified delivery request.
// The credential it uses lives in the handler implementation, never in the
// daemon. Returns an error if the platform action fails.
type Handler interface {
	Deliver(req *channel.DeliveryRequest) error
}

// Server is an HMAC-verified HTTP sidecar. It verifies the X-GK-Signature
// header, decodes the DeliveryRequest, and hands it to the Handler.
type Server struct {
	cfg     Config
	handler Handler
}

// NewServer returns a sidecar server.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, handler: cfg.Handler}
}

// ListenAndServe starts the sidecar on cfg.Addr.
func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/deliver", s.handleDeliver)
	srv := &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return srv.ListenAndServe()
}

// handleDeliver verifies the signature and delegates to the handler.
func (s *Server) handleDeliver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req channel.DeliveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("X-GK-Signature")
	if err := channel.VerifyRequest(&req, s.cfg.HMACKey, sig); err != nil {
		http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}
	if err := s.handler.Deliver(&req); err != nil {
		http.Error(w, "deliver failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// Ensure channel import is used in the package even if a future shrink removes
// the direct reference above.
var _ = channel.SeverityInfo

var errNoHandler = errors.New("sidecar: no handler configured")
