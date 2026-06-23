package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/potato-hash/groundskeeper/internal/channel"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/runtime"
)

// recordingHandler is a stub sidecar Handler that records every delivery.
type recordingHandler struct {
	deliveries []*channel.DeliveryRequest
}

func (r *recordingHandler) Deliver(req *channel.DeliveryRequest) error {
	r.deliveries = append(r.deliveries, req)
	return nil
}

// newTestSidecarServer stands up an HMAC-verifying httptest server that records
// deliveries into the handler, mirroring internal/sidecar.Server but in-process.
func newTestSidecarServer(t *testing.T, key []byte, h *recordingHandler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req channel.DeliveryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		sig := r.Header.Get("X-GK-Signature")
		if err := channel.VerifyRequest(&req, key, sig); err != nil {
			http.Error(w, "bad sig", 401)
			return
		}
		_ = h.Deliver(&req)
		w.WriteHeader(202)
	}))
}

// TestPoolNotifiesOnDeadLetter proves the gateway is actually called when a
// job is dead-lettered. Uses a stub sidecar server that records the delivery,
// and a fake adapter with negative TurnDelay so the turn never completes ->
// FailJob -> after max attempts -> dead-letter -> notify.
func TestPoolNotifiesOnDeadLetter(t *testing.T) {
	rec := &recordingHandler{}
	srv := newTestSidecarServer(t, []byte("k"), rec)
	gw := channel.NewGateway(channel.DefaultPolicy(),
		&channel.SidecarClient{BaseURL: srv.URL, Key: []byte("k")})

	db := newTestPoolDB(t)
	adapter := runtime.NewFakeAdapter()
	adapter.TurnDelay = -1 // stuck: turn never completes -> FailJob path
	pool := New(db, adapter, Config{MaxSlots: 1, PollInterval: 15 * time.Millisecond, TurnTimeout: 200 * time.Millisecond})
	pool.SetGateway(gw)

	th, _ := db.CreateThread("dead", "omp", ".")
	db.DB().Exec(`UPDATE agent_threads SET goal=? WHERE id=?`, "go", th.ID)
	job, _ := db.CreateJob(th.ID, "turn")
	// Lower max_attempts to 1 so it dead-letters on the first failed turn.
	db.DB().Exec(`UPDATE jobs SET max_attempts=1 WHERE id=?`, job.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	deadline := time.After(5 * time.Second)
	for {
		got, _ := db.GetJob(job.ID)
		if got != nil && got.Status == gkdb.JobDeadLetter {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not dead-letter within 5s")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	pool.Stop()

	// The gateway must have received a delivery (critical severity -> all
	// channels in DefaultPolicy). This proves the pool->gateway wiring is real.
	if len(rec.deliveries) == 0 {
		t.Fatal("gateway was not called on dead-letter (wiring missing)")
	}
}
