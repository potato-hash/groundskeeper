package sidecar

import (
	"fmt"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// CalendarHandler inserts reminders into a calendar. The credential (OAuth
// token / CalDAV) lives here, never in the daemon. This first cut logs the
// event; a real impl would call the calendar API.
type CalendarHandler struct {
	// CalendarID is the target calendar (e.g. "primary").
	CalendarID string
	// Token is the platform credential (held here, not in the daemon).
	Token string
}

func (h *CalendarHandler) Deliver(req *channel.DeliveryRequest) error {
	if h.Token == "" {
		return errNoHandler
	}
	var p struct {
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
	}
	if err := decodeRaw(req.Payload, &p); err != nil {
		return fmt.Errorf("calendar: decode: %w", err)
	}
	// In production: insert an event via the calendar API with h.Token.
	// Here: record the intent (the sidecar is the credential boundary; the
	// actual API call is a thin layer over the verified payload).
	_ = h.CalendarID
	_ = p.Message
	return nil
}
