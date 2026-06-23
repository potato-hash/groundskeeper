//go:build omp_live

// Live OMP smoke tests. Run with: go test -race -tags omp_live ./internal/runtime/...
// These require the real omp binary + a logged-in provider (ollama-cloud/glm-5.2
// by default). They are excluded from the normal test suite by the build tag.

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func ompModel() string {
	if m := os.Getenv("GK_OMP_MODEL"); m != "" {
		return m
	}
	return "ollama-cloud/glm-5.2"
}

func skipIfNoOmp(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("omp"); err != nil {
		t.Skip("omp not on PATH; skipping live smoke")
	}
}

// TestOmpLive_TurnCompletes is the live smoke: a real omp worker receives a
// prompt and emits ready -> message_update -> agent_end end-to-end. This proves
// the adapter's protocol mapping against the real omp binary.
func TestOmpLive_TurnCompletes(t *testing.T) {
	skipIfNoOmp(t)
	a := NewOmpAdapter(OmpAdapterConfig{
		Model:     ompModel(),
		ExtraArgs: []string{"--no-tools", "--no-lsp"},
	})
	ws := t.TempDir()
	sd := filepath.Join(t.TempDir(), "session")
	ref, err := a.StartThread(context.Background(), ws, sd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(ref)
	events := a.StreamEvents(ref)

	select {
	case ev := <-events:
		if ev.Kind != EventReady {
			t.Fatalf("first event = %s, want ready", ev.Kind)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no ready event within 10s")
	}

	if err := a.SendTurn(context.Background(), ref,
		"Reply with exactly the word READYTEST and nothing else."); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(90 * time.Second)
	sawEnd := false
	for {
		select {
		case ev := <-events:
			if ev.Kind == EventAgentEnd {
				sawEnd = true
			}
			if ev.Kind == EventMessageUpdate && ev.Payload != "" {
				n := len(ev.Payload)
				if n > 60 {
					n = 60
				}
				t.Logf("message_update: %s", ev.Payload[:n])
			}
		case <-deadline:
			if !sawEnd {
				t.Fatal("live omp did not emit agent_end within 90s")
			}
			return
		}
	}
}
