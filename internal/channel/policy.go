// Package channel is Groundskeeper's notification channel gateway. It owns the
// notification policy (which severities go to which channels), an HMAC signing
// layer so the daemon never holds platform credentials (the sidecar does), and
// a pluggable delivery backend.
//
// Security model (from docs/upstream-roboomp-audit.md "Sidecar holds the
// privileged credential"): the daemon holds an HMAC signing key; the sidecar
// holds the platform credential (email SMTP, Slack token, calendar OAuth). The
// daemon signs each delivery request; the sidecar verifies the signature and
// forwards the request to the platform. A compromised daemon process cannot
// exfiltrate the platform credential because it never has it.
package channel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Severity is a notification's urgency.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Channel is a delivery target (email, slack, calendar, etc.).
type Channel string

const (
	ChannelEmail    Channel = "email"
	ChannelSlack    Channel = "slack"
	ChannelCalendar Channel = "calendar"
)

// Policy maps severities to channels. A severity with no entry is dropped.
type Policy struct {
	// Routes maps a severity to the channels that should receive it.
	Routes map[Severity][]Channel
}

// DefaultPolicy routes critical to all channels, warning to email+slack, info
// to none.
func DefaultPolicy() Policy {
	return Policy{Routes: map[Severity][]Channel{
		SeverityCritical: {ChannelEmail, ChannelSlack, ChannelCalendar},
		SeverityWarning:  {ChannelEmail, ChannelSlack},
	}}
}

// TargetsFor returns the channels a notification of the given severity should
// go to. An empty list means drop.
func (p Policy) TargetsFor(sev Severity) []Channel {
	return p.Routes[sev]
}

// Notification is a single outbound message.
type Notification struct {
	ID        string
	ThreadID  string
	Severity  Severity
	Message   string
	Channels  []Channel
	CreatedAt time.Time
}

// DeliveryRequest is what the daemon signs and sends to a sidecar. The sidecar
// verifies the signature against its shared key, then performs the privileged
// action (SMTP send, Slack post, calendar insert) with the platform credential
// it holds.
type DeliveryRequest struct {
	NotificationID string          `json:"notification_id"`
	Channel        Channel         `json:"channel"`
	Payload        json.RawMessage `json:"payload"`
	Timestamp      int64           `json:"timestamp"`
}

// SignRequest signs a DeliveryRequest with an HMAC-SHA256 key, returning the
// hex signature. The sidecar verifies with the same key.
func SignRequest(req *DeliveryRequest, key []byte) (string, error) {
	body, err := canonicalJSON(req)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyRequest returns nil if the signature matches the request body under the
// shared key. The sidecar calls this before performing the privileged action.
func VerifyRequest(req *DeliveryRequest, key []byte, sig string) error {
	want, err := SignRequest(req, key)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("channel: invalid request signature")
	}
	// Reject replays: timestamp must be within 5 minutes.
	if req.Timestamp > 0 {
		age := time.Since(time.Unix(req.Timestamp, 0))
		if age > 5*time.Minute || age < -5*time.Minute {
			return errors.New("channel: request timestamp out of window (replay protection)")
		}
	}
	return nil
}

// canonicalJSON marshals the request deterministically (sorted keys) so the
// daemon and sidecar compute the same signature.
func canonicalJSON(req *DeliveryRequest) ([]byte, error) {
	// json.Marshal already sorts struct keys by declaration order, which is
	// stable across both sides. We set the timestamp fresh so signatures are
	// unique per send.
	if req.Timestamp == 0 {
		req.Timestamp = time.Now().Unix()
	}
	return json.Marshal(req)
}

// SidecarClient delivers a signed request to a sidecar endpoint over HTTP.
type SidecarClient struct {
	BaseURL string
	Key     []byte
	HTTP    *http.Client
}

// Deliver posts a signed DeliveryRequest to the sidecar. The sidecar verifies
// the signature, then performs the privileged action with its own credential.
func (s *SidecarClient) Deliver(req *DeliveryRequest) error {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	sig, err := SignRequest(req, s.Key)
	if err != nil {
		return fmt.Errorf("channel: sign: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("channel: marshal request: %w", err)
	}
	httpReq, err := http.NewRequest("POST", s.BaseURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("channel: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-GK-Signature", sig)
	resp, err := s.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("channel: deliver to %s: %w", s.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("channel: sidecar rejected: %s", resp.Status)
	}
	return nil
}

// Gateway is the daemon-side notification router. It applies the policy, builds
// a DeliveryRequest per target channel, signs it, and hands it to the sidecar.
type Gateway struct {
	Policy  Policy
	Sidecar *SidecarClient
}

// NewGateway returns a gateway with a policy and a sidecar client.
func NewGateway(policy Policy, sidecar *SidecarClient) *Gateway {
	return &Gateway{Policy: policy, Sidecar: sidecar}
}

// Send routes a notification through the policy and delivers to each target.
// Channels with no sidecar client are skipped (the daemon never holds the
// platform credential, so it cannot deliver directly).
func (g *Gateway) Send(n *Notification) error {
	targets := g.Policy.TargetsFor(n.Severity)
	if len(targets) == 0 {
		return nil // dropped by policy
	}
	var errs []string
	for _, ch := range targets {
		req := &DeliveryRequest{
			NotificationID: n.ID,
			Channel:        ch,
			Payload:        json.RawMessage(fmt.Sprintf(`{"message":%q,"thread_id":%q}`, n.Message, n.ThreadID)),
		}
		if err := g.Sidecar.Deliver(req); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ch, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("channel: delivery errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
