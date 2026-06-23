package runtime

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// stubOmpBin builds a small Go program that speaks the omp rpc JSONL protocol
// deterministically (ready -> prompt ACK -> message_update -> prompt_result)
// and returns its path. Using a real Go subprocess avoids shell-quote hell.
func stubOmpBin(t *testing.T) string {
	t.Helper()
	src := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	out := func(o map[string]any) { b, _ := json.Marshal(o); fmt.Println(string(b)) }
	out(map[string]any{"type": "ready"})
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var cmd map[string]any
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			continue
		}
		switch cmd["type"] {
		case "prompt":
			out(map[string]any{"type": "response", "command": "prompt", "success": true})
			time.Sleep(20 * time.Millisecond)
			out(map[string]any{"type": "message_update", "message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "READYTEST"}},
			}})
			out(map[string]any{"type": "prompt_result", "agentInvoked": true})
		case "abort":
			out(map[string]any{"type": "response", "command": "abort", "success": true})
		case "set_host_tools":
			out(map[string]any{"type": "response", "command": "set_host_tools", "success": true,
				"data": map[string]any{"toolNames": []string{}}})
		}
	}
}

`
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "fake-omp")
	cmd := exec.Command("go", "build", "-o", binPath, mainPath)
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	return binPath
}

// drainReady reads and discards the ready event from the stream.
func drainReady(t *testing.T, events <-chan RuntimeEvent) {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Kind != EventReady {
			t.Fatalf("first event = %s, want ready", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ready event within 2s")
	}
}

func TestOmpStartThreadEmitsReady(t *testing.T) {
	a := NewOmpAdapter(OmpAdapterConfig{OmpBin: stubOmpBin(t), Model: "stub/model"})
	ref, err := a.StartThread(context.Background(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(ref)
	drainReady(t, a.StreamEvents(ref))
}

// TestOmpSendTurnStreamsAgentEnd: prompt ACK is NOT completion; prompt_result
// (agent_end) is. The stub emits response(prompt) then message_update then
// prompt_result — we must see agent_end, not stop at the ACK.
func TestOmpSendTurnStreamsAgentEnd(t *testing.T) {
	a := NewOmpAdapter(OmpAdapterConfig{OmpBin: stubOmpBin(t)})
	ref, _ := a.StartThread(context.Background(), t.TempDir(), t.TempDir())
	defer a.Shutdown(ref)
	events := a.StreamEvents(ref)
	drainReady(t, events)

	if err := a.SendTurn(context.Background(), ref, "say READYTEST"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	sawEnd, sawUpdate := false, false
	for {
		select {
		case ev := <-events:
			switch ev.Kind {
			case EventAgentEnd:
				sawEnd = true
			case EventMessageUpdate:
				sawUpdate = true
				if ev.Payload != "READYTEST" {
					t.Errorf("message_update payload = %q, want READYTEST", ev.Payload)
				}
			}
		case <-deadline:
			if !sawEnd {
				t.Fatal("no agent_end (prompt_result) within 3s — prompt ack treated as completion")
			}
			if !sawUpdate {
				t.Fatal("no message_update event observed")
			}
			return
		}
	}
}

func TestOmpInterruptSendsAbort(t *testing.T) {
	a := NewOmpAdapter(OmpAdapterConfig{OmpBin: stubOmpBin(t)})
	ref, _ := a.StartThread(context.Background(), t.TempDir(), t.TempDir())
	defer a.Shutdown(ref)
	events := a.StreamEvents(ref)
	drainReady(t, events)
	// Interrupt writes {type:abort}; no panic / no error is the contract.
	if err := a.Interrupt(ref); err != nil {
		t.Fatalf("interrupt returned error: %v", err)
	}
	// Give the stub a moment to ack; drain any buffered event.
	select {
	case <-events:
	case <-time.After(200 * time.Millisecond):
	}
}

func TestOmpShutdownClosesStream(t *testing.T) {
	a := NewOmpAdapter(OmpAdapterConfig{OmpBin: stubOmpBin(t)})
	ref, _ := a.StartThread(context.Background(), t.TempDir(), t.TempDir())
	events := a.StreamEvents(ref)
	drainReady(t, events)
	if err := a.Shutdown(ref); err != nil {
		t.Fatal(err)
	}
	// Drain any remaining buffered events, then expect the channel to close.
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return // closed as expected
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event channel not closed within 2s")
		}
	}
}

func TestScrubEnvStripsAPIKeys(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-secret",
		"OPENAI_API_KEY=sk-other",
		"HOME=/tmp/u",
		"MY_CUSTOM_VAR=keep",
	}
	out := scrubEnv(in)
	have := map[string]bool{}
	for _, kv := range out {
		have[kv] = true
	}
	if have["ANTHROPIC_API_KEY=sk-secret"] {
		t.Error("ANTHROPIC_API_KEY was not scrubbed")
	}
	if have["OPENAI_API_KEY=sk-other"] {
		t.Error("OPENAI_API_KEY was not scrubbed")
	}
	if !have["PATH=/usr/bin"] {
		t.Error("PATH was wrongly scrubbed")
	}
	if !have["HOME=/tmp/u"] {
		t.Error("HOME was wrongly scrubbed")
	}
}

func TestDirHasTranscript(t *testing.T) {
	d := t.TempDir()
	if dirHasTranscript(d) {
		t.Error("empty dir should have no transcript")
	}
	if err := os.WriteFile(filepath.Join(d, "session.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !dirHasTranscript(d) {
		t.Error("dir with .jsonl should have a transcript")
	}
}

// Ensure io import is used (referenced by the _ = io.Discard below in case a
// future lint flags unused imports if the test shrinks).
var _ = io.Discard
