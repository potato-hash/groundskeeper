package sidecar

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// EmailHandler sends notifications via SMTP. It holds the SMTP credential; the
// daemon never sees it.
type EmailHandler struct {
	// SMTPHost:port (e.g. "smtp.gmail.com:587").
	Addr string
	// From is the sender address.
	From string
	// Auth is the SMTP auth (the credential). Created with smtp.PlainAuth.
	Auth smtp.Auth
	// Recipients is the default recipient list (per-notification overrides can
	// be added to the payload later).
	Recipients []string
}

// Deliver sends an email for the verified request.
func (h *EmailHandler) Deliver(req *channel.DeliveryRequest) error {
	if h.Auth == nil || h.Addr == "" {
		return errNoHandler
	}
	var p struct {
		Message  string   `json:"message"`
		ThreadID string   `json:"thread_id"`
		To       []string `json:"to"`
	}
	if err := decodeRaw(req.Payload, &p); err != nil {
		return fmt.Errorf("email: decode payload: %w", err)
	}
	to := p.To
	if len(to) == 0 {
		to = h.Recipients
	}
	if len(to) == 0 {
		return fmt.Errorf("email: no recipients")
	}
	body := fmt.Sprintf("Subject: Groundskeeper [%s]\n\n%s", req.Channel, p.Message)
	msg := []byte("To: " + strings.Join(to, ", ") + "\r\n" + body)
	return smtp.SendMail(h.Addr, h.Auth, h.From, to, msg)
}
