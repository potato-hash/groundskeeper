package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeAdapter is a deterministic AgentRuntimeAdapter with no real subprocess.
// It emits the ready -> agent_start -> agent_end sequence per turn, optionally
// after a configurable delay. Used by tests and as the only adapter in the
// Phase 3 slice (the OMP RPC adapter is Phase 4).
//
// Prompt acknowledgement is not completion: SendTurn returns immediately, but
// agent_end is what marks the turn done. The fake enforces this contract.
//
// Send/close safety: every event send and the Shutdown close are serialized by
// a per-thread sendMu so a goroutine never sends on a closed channel.
type FakeAdapter struct {
	// TurnDelay is the delay between agent_start and agent_end. Zero = immediate.
	// Negative = never emit agent_end (simulates a stuck worker).
	TurnDelay time.Duration
	// TurnError emits a runtime error instead of running the turn.
	TurnError string

	mu      sync.Mutex
	threads map[string]*fakeThread
}

type fakeThread struct {
	ref    *RuntimeThreadRef
	events chan RuntimeEvent
	done   chan struct{}
	closed bool
	sendMu sync.Mutex // serializes sends vs Shutdown's close
}

// NewFakeAdapter returns a FakeAdapter with immediate turns (TurnDelay=0).
func NewFakeAdapter() *FakeAdapter {
	return &FakeAdapter{threads: make(map[string]*fakeThread)}
}

func (f *FakeAdapter) StartThread(ctx context.Context, workspacePath, sessionDir string) (*RuntimeThreadRef, error) {
	ref := &RuntimeThreadRef{
		Runtime:       "fake",
		ProcessID:     0,
		SessionDir:    sessionDir,
		WorkspacePath: workspacePath,
	}
	ft := &fakeThread{
		ref:    ref,
		events: make(chan RuntimeEvent, 16),
		done:   make(chan struct{}),
	}
	key := threadKey(ref)
	f.mu.Lock()
	f.threads[key] = ft
	f.mu.Unlock()
	// Emit ready immediately (the worker is "up"). Safe: channel is open.
	ft.send(RuntimeEvent{Kind: EventReady})
	return ref, nil
}

func (f *FakeAdapter) ResumeThread(ctx context.Context, ref *RuntimeThreadRef) error {
	// The fake has no persistent state to resume; a resume is a no-op that keeps
	// the thread handle valid. A missing thread is created on demand.
	key := threadKey(ref)
	f.mu.Lock()
	ft, ok := f.threads[key]
	if !ok {
		ft = &fakeThread{
			ref:    ref,
			events: make(chan RuntimeEvent, 16),
			done:   make(chan struct{}),
		}
		f.threads[key] = ft
	}
	f.mu.Unlock()
	ft.send(RuntimeEvent{Kind: EventReady})
	return nil
}

func (f *FakeAdapter) SendTurn(ctx context.Context, ref *RuntimeThreadRef, prompt string) error {
	ft := f.lookup(ref)
	if ft == nil {
		return fmt.Errorf("runtime: unknown thread %s", threadKey(ref))
	}
	// agent_start is emitted synchronously so the caller sees the turn begin.
	// agent_end is emitted after TurnDelay (or immediately, or never if negative).
	go func() {
		if f.TurnError != "" {
			ft.send(RuntimeEvent{Kind: EventError, Payload: f.TurnError})
			return
		}
		if !ft.send(RuntimeEvent{Kind: EventAgentStart, Payload: prompt}) {
			return // shut down before start
		}
		if f.TurnDelay < 0 {
			return // stuck: never emit agent_end
		}
		if f.TurnDelay > 0 {
			select {
			case <-time.After(f.TurnDelay):
			case <-ft.done:
				return
			}
		}
		ft.send(RuntimeEvent{Kind: EventAgentEnd, Payload: prompt})
	}()
	return nil
}

func (f *FakeAdapter) Interrupt(ref *RuntimeThreadRef) error {
	ft := f.lookup(ref)
	if ft == nil {
		return fmt.Errorf("runtime: unknown thread %s", threadKey(ref))
	}
	// Emit an agent_end to unblock anyone waiting on completion (interrupt ends
	// the in-flight turn). Best-effort via send (drops if shutting down).
	ft.send(RuntimeEvent{Kind: EventAgentEnd, Payload: "[interrupted]"})
	return nil
}

func (f *FakeAdapter) StreamEvents(ref *RuntimeThreadRef) <-chan RuntimeEvent {
	ft := f.lookup(ref)
	if ft == nil {
		ch := make(chan RuntimeEvent)
		close(ch)
		return ch
	}
	return ft.events
}

func (f *FakeAdapter) Shutdown(ref *RuntimeThreadRef) error {
	key := threadKey(ref)
	f.mu.Lock()
	ft, ok := f.threads[key]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	ft.sendMu.Lock()
	if !ft.closed {
		ft.closed = true
		close(ft.done)   // unblock any delay select
		close(ft.events) // safe: no send holds sendMu now
	}
	ft.sendMu.Unlock()
	f.mu.Lock()
	delete(f.threads, key)
	f.mu.Unlock()
	return nil
}

// send pushes ev onto the event channel unless the thread has been shut down.
// Returns true if sent, false if the thread is closed (caller should abort).
func (ft *fakeThread) send(ev RuntimeEvent) bool {
	ft.sendMu.Lock()
	defer ft.sendMu.Unlock()
	if ft.closed {
		return false
	}
	ft.events <- ev
	return true
}

func (f *FakeAdapter) lookup(ref *RuntimeThreadRef) *fakeThread {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threads[threadKey(ref)]
}

func threadKey(ref *RuntimeThreadRef) string {
	if ref == nil {
		return ""
	}
	return ref.SessionDir + "|" + ref.WorkspacePath
}
