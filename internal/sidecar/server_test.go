package sidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// stubHandler records deliveries for assertions without hitting a real platform.
type stubHandler struct {
	deliveries []*channel.DeliveryRequest
	err        error
}

func (s *stubHandler) Deliver(req *channel.DeliveryRequest) error {
	s.deliveries = append(s.deliveries, req)
	return s.err
}

func newTestServer(t *testing.T, key []byte, h Handler) *Server {
	t.Helper()
	return NewServer(Config{Addr: "127.0.0.1:0", HMACKey: key, Handler: h})
}

func TestServerRejectsBadSignature(t *testing.T) {
	h := &stubHandler{}
	srv := newTestServer(t, []byte("good-key"), h)
	mux := http.NewServeMux()
	mux.HandleFunc("/deliver", srv.handleDeliver)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req := &channel.DeliveryRequest{
		NotificationID: "n1",
		Channel:        channel.ChannelEmail,
		Payload:        json.RawMessage(`{"message":"hi"}`),
		Timestamp:      time.Now().Unix(),
	}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", ts.URL+"/deliver", strings.NewReader(string(body)))
	httpReq.Header.Set("X-GK-Signature", "badsig")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for bad sig", resp.StatusCode)
	}
	resp.Body.Close()
	if len(h.deliveries) != 0 {
		t.Error("handler was called despite bad signature")
	}
}

func TestServerAcceptsGoodSignature(t *testing.T) {
	h := &stubHandler{}
	key := []byte("shared-key")
	srv := newTestServer(t, key, h)
	mux := http.NewServeMux()
	mux.HandleFunc("/deliver", srv.handleDeliver)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req := &channel.DeliveryRequest{
		NotificationID: "n2",
		Channel:        channel.ChannelSlack,
		Payload:        json.RawMessage(`{"message":"hi"}`),
		Timestamp:      time.Now().Unix(),
	}
	sig, _ := channel.SignRequest(req, key)
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", ts.URL+"/deliver", strings.NewReader(string(body)))
	httpReq.Header.Set("X-GK-Signature", sig)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if len(h.deliveries) != 1 || h.deliveries[0].NotificationID != "n2" {
		t.Errorf("handler not called once: %+v", h.deliveries)
	}
}

func TestEmailHandlerNoCredential(t *testing.T) {
	h := &EmailHandler{}
	if err := h.Deliver(&channel.DeliveryRequest{Channel: channel.ChannelEmail, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Error("expected error with no credential")
	}
}

func TestCalendarHandlerNoCredential(t *testing.T) {
	h := &CalendarHandler{}
	if err := h.Deliver(&channel.DeliveryRequest{Channel: channel.ChannelCalendar, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Error("expected error with no credential")
	}
}

func TestContactHandlerNoCredential(t *testing.T) {
	h := &ContactHandler{}
	if err := h.Deliver(&channel.DeliveryRequest{Channel: channel.ChannelCalendar, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Error("expected error with no credential")
	}
}

func TestCalendarHandlerDelivers(t *testing.T) {
	h := &CalendarHandler{Token: "cred", CalendarID: "primary"}
	err := h.Deliver(&channel.DeliveryRequest{
		Channel: channel.ChannelCalendar,
		Payload: json.RawMessage(`{"message":"remind me","thread_id":"t1"}`),
	})
	if err != nil {
		t.Errorf("calendar deliver failed: %v", err)
	}
}
