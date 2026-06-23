// Package runtime is the runtime-neutral agent adapter layer. It defines the
// interface a coding-agent runtime (OMP RPC, or a fake for tests) must satisfy so
// the rest of Groundskeeper never imports runtime-specific code.
//
// The OMP RPC adapter (Phase 4) will spawn "omp --mode rpc" over stdio and stream
// the JSONL frames as RuntimeEvents. This slice ships only FakeAdapter.
package runtime

import "context"

// Runtime identifies the agent runtime backing a thread (e.g. "omp").
type Runtime string

// RuntimeThreadRef is the handle returned when a thread is started: enough to
// resume it, interrupt it, or correlate it with a worker process.
type RuntimeThreadRef struct {
	Runtime      Runtime
	ProcessID    int    // OS pid of the worker subprocess (0 for the fake)
	SessionDir   string // persistent session directory (resume target)
	WorkspacePath string
}

// RuntimeEvent is the union of events streamed from a running thread. Each
// event is one JSONL frame from the OMP RPC protocol (or its fake equivalent).
//
// The Kind field discriminates the payload. Prompt acknowledgement is NOT
// completion — only AgentEnd marks a turn done. This is the OMP contract: a
// "prompt" frame returns an immediate ack with data.agentInvoked, but the turn
// is still in flight until agent_end.
type RuntimeEvent struct {
	Kind    EventKind
	Payload string // raw frame payload (message text, tool call JSON, error msg)

	// For HostToolCall: the tool name and arguments, so the audit/approvals layer
	// can decide before the privileged action runs.
	ToolName string
	ToolArgs string
}

// EventKind enumerates the OMP RPC frame kinds Groundskeeper cares about.
type EventKind string

const (
	EventReady            EventKind = "ready"              // worker up, accepting prompts
	EventAgentStart       EventKind = "agent_start"         // a turn began
	EventMessageUpdate    EventKind = "message_update"      // assistant message delta
	EventAgentEnd         EventKind = "agent_end"           // turn completed (completion signal)
	EventHostToolCall     EventKind = "host_tool_call"      // privileged tool request from agent
	EventHostURIRequest   EventKind = "host_uri_request"    // URI fetch request
	EventExtensionUIRequest EventKind = "extension_ui_request" // Espalier UI surface
	EventError            EventKind = "error"              // worker error
)

// AgentRuntimeAdapter is the runtime-neutral interface. The fake implements it
// for tests; the OMP RPC adapter (Phase 4) implements it for real workers.
type AgentRuntimeAdapter interface {
	// StartThread spawns a worker for the given workspace/session and returns a
	// ref. A ready event is delivered on the event stream once the worker is up.
	StartThread(ctx context.Context, workspacePath, sessionDir string) (*RuntimeThreadRef, error)

	// ResumeThread attaches to an existing session (session_dir + runtime session id)
	// so a crashed worker can continue the same conversation.
	ResumeThread(ctx context.Context, ref *RuntimeThreadRef) error

	// SendTurn sends a prompt to a started/resumed thread. It returns immediately
	// (prompt acknowledgement) — it does NOT wait for the turn to complete. The
	// caller observes agent_start then agent_end on the event stream.
	SendTurn(ctx context.Context, ref *RuntimeThreadRef, prompt string) error

	// Interrupt cancels the in-flight turn (Ctrl-C equivalent).
	Interrupt(ref *RuntimeThreadRef) error

	// StreamEvents returns the channel of events for a thread. The adapter pushes
	// events; the channel closes when the thread ends or is shut down.
	StreamEvents(ref *RuntimeThreadRef) <-chan RuntimeEvent

	// Shutdown stops the worker and releases resources.
	Shutdown(ref *RuntimeThreadRef) error
}
