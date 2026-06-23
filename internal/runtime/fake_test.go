package runtime

import (
	"context"
	"testing"
	"time"
)

// TestFakeStartThreadEmitsReady: StartThread produces a ready event.
func TestFakeStartThreadEmitsReady(t *testing.T) {
	f := NewFakeAdapter()
	ref, err := f.StartThread(context.Background(), "/tmp/ws", "/tmp/sess")
	if err != nil {
		t.Fatal(err)
	}
	events := f.StreamEvents(ref)
	select {
	case ev := <-events:
		if ev.Kind != EventReady {
			t.Fatalf("first event = %s, want ready", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no ready event within 1s")
	}
}

// TestFakeSendTurnSequence: SendTurn produces agent_start then agent_end, in
// that order. Prompt acknowledgement (SendTurn returning) is NOT completion —
// agent_end is.
func TestFakeSendTurnSequence(t *testing.T) {
	f := NewFakeAdapter()
	ref, _ := f.StartThread(context.Background(), "/tmp/ws", "/tmp/sess")
	events := f.StreamEvents(ref)
	<-events // drain ready

	if err := f.SendTurn(context.Background(), ref, "do the thing"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Kind != EventAgentStart {
			t.Fatalf("expected agent_start, got %s", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no agent_start within 1s")
	}
	select {
	case ev := <-events:
		if ev.Kind != EventAgentEnd {
			t.Fatalf("expected agent_end, got %s", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no agent_end within 1s — prompt ack was treated as completion")
	}
}

// TestFakePromptAckIsNotCompletion: SendTurn returns before agent_end is
// emitted, even with a delay. The return is the ack; agent_end comes later.
func TestFakePromptAckIsNotCompletion(t *testing.T) {
	f := NewFakeAdapter()
	f.TurnDelay = 50 * time.Millisecond
	ref, _ := f.StartThread(context.Background(), "/tmp/ws", "/tmp/sess")
	events := f.StreamEvents(ref)
	<-events // drain ready

	start := time.Now()
	err := f.SendTurn(context.Background(), ref, "hi")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 50*time.Millisecond {
		t.Fatalf("SendTurn blocked %v (treated ack as completion); should return immediately", elapsed)
	}
	<-events // drain agent_start
	select {
	case ev := <-events:
		if ev.Kind != EventAgentEnd {
			t.Fatalf("expected agent_end after delay, got %s", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no agent_end after delay")
	}
}

// TestFakeInterruptEndsTurn: Interrupt emits an agent_end so a blocked caller
// unblocks.
func TestFakeInterruptEndsTurn(t *testing.T) {
	f := NewFakeAdapter()
	f.TurnDelay = -1 // never emit agent_end on its own (stuck)
	ref, _ := f.StartThread(context.Background(), "/tmp/ws", "/tmp/sess")
	events := f.StreamEvents(ref)
	<-events // drain ready
	_ = f.SendTurn(context.Background(), ref, "hi")
	<-events // drain agent_start

	if err := f.Interrupt(ref); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Kind != EventAgentEnd {
			t.Fatalf("interrupt should emit agent_end, got %s", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt did not produce agent_end")
	}
}

// TestFakeShutdownClosesStream: after Shutdown the event channel is closed.
func TestFakeShutdownClosesStream(t *testing.T) {
	f := NewFakeAdapter()
	ref, _ := f.StartThread(context.Background(), "/tmp/ws", "/tmp/sess")
	events := f.StreamEvents(ref)
	<-events // drain the buffered ready event
	if err := f.Shutdown(ref); err != nil {
		t.Fatal(err)
	}
	_, ok := <-events
	if ok {
		t.Fatal("event channel should be closed after shutdown")
	}
}
