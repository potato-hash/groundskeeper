package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPolicyTargetsFor(t *testing.T) {
	p := DefaultPolicy()
	if got := p.TargetsFor(SeverityCritical); len(got) != 3 {
		t.Errorf("critical targets = %d, want 3", len(got))
	}
	if got := p.TargetsFor(SeverityInfo); len(got) != 0 {
		t.Errorf("info targets = %d, want 0 (dropped)", len(got))
	}
}

func TestSignVerifyRequest(t *testing.T) {
	key := []byte("test-shared-key")
	req := &DeliveryRequest{
		NotificationID: "abc",
		Channel:        ChannelEmail,
		Payload:        json.RawMessage(`{"message":"hi"}`),
		Timestamp:      time.Now().Unix(),
	}
	sig, err := SignRequest(req, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRequest(req, key, sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	// Wrong key must fail.
	if err := VerifyRequest(req, []byte("wrong-key"), sig); err == nil {
		t.Error("verify succeeded with wrong key")
	}
	// Tampered payload must fail.
	req2 := *req
	req2.NotificationID = "tampered"
	if err := VerifyRequest(&req2, key, sig); err == nil {
		t.Error("verify succeeded on tampered request")
	}
}

func TestVerifyRejectsReplay(t *testing.T) {
	key := []byte("k")
	req := &DeliveryRequest{
		NotificationID: "old",
		Channel:        ChannelSlack,
		Timestamp:      time.Now().Add(-10 * time.Minute).Unix(), // too old
	}
	sig, _ := SignRequest(req, key)
	if err := VerifyRequest(req, key, sig); err == nil {
		t.Error("verify accepted a stale (replay) request")
	}
}

func TestSidecarDeliver(t *testing.T) {
	received := make(chan *DeliveryRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		sig := r.Header.Get("X-GK-Signature")
		if err := VerifyRequest(&req, []byte("key"), sig); err != nil {
			http.Error(w, "bad sig", 401)
			return
		}
		received <- &req
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sc := &SidecarClient{BaseURL: srv.URL, Key: []byte("key")}
	req := &DeliveryRequest{
		NotificationID: "n1",
		Channel:        ChannelEmail,
		Payload:        json.RawMessage(`{"message":"hello"}`),
		Timestamp:      time.Now().Unix(),
	}
	if err := sc.Deliver(req); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	select {
	case got := <-received:
		if got.NotificationID != "n1" {
			t.Errorf("got id %s, want n1", got.NotificationID)
		}
	case <-time.After(time.Second):
		t.Fatal("sidecar did not receive the request")
	}
}

func TestGatewaySendDropsInfo(t *testing.T) {
	gw := NewGateway(DefaultPolicy(), &SidecarClient{BaseURL: "http://none", Key: []byte("k")})
	n := &Notification{ID: "n", Severity: SeverityInfo, Message: "m"}
	// Info has no routes in DefaultPolicy -> dropped silently (no error, no deliver).
	if err := gw.Send(n); err != nil {
		t.Errorf("info should be dropped, got error: %v", err)
	}
}
