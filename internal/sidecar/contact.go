package sidecar

import (
	"fmt"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// ContactHandler resolves a notification's recipient via an address book. The
// credential (address-book API token) lives here.
type ContactHandler struct {
	// Token is the platform credential held by the sidecar.
	Token string
}

func (h *ContactHandler) Deliver(req *channel.DeliveryRequest) error {
	if h.Token == "" {
		return errNoHandler
	}
	var p struct {
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
		Query    string `json:"query"`
	}
	if err := decodeRaw(req.Payload, &p); err != nil {
		return fmt.Errorf("contact: decode: %w", err)
	}
	// In production: look up the contact by query with h.Token and return the
	// address for the email/calendar sidecar to use. Here: accept silently.
	_ = p.Query
	return nil
}
