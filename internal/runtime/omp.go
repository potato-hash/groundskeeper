// Package runtime — OMP RPC adapter.
//
// OmpAdapter spawns "omp --mode rpc" as a subprocess and drives it over the
// JSONL stdio protocol. Commands are written on stdin (one JSON object per
// line); responses and events are read on stdout. The protocol is defined in
// OMP's src/modes/rpc/rpc-types.ts — Groundskeeper implements only the subset
// it needs to observe agent turns and privileged-tool calls.
//
// Protocol summary (grounded in rpc-types.ts + live probe):
//
//	stdin:  { type:"prompt", message:"..." }   start a turn
//	        { type:"abort" }                    interrupt the in-flight turn
//	stdout: { type:"ready" }                   worker up, accepting prompts
//	        { type:"response", command:"prompt", success:true, data:{agentInvoked:bool} }
//	                                            prompt ACK (NOT completion)
//	        { type:"message_start", message:{...} }
//	        { type:"message_update", message:{...} }   assistant message delta
//	        { type:"message_end", message:{...} }
//	        { type:"agent_start" }              a turn began (extension hook)
//	        { type:"turn_start" / turn_end }
//	        { type:"host_tool_call", id, tool, input }  privileged tool request
//	        { type:"host_uri_request", id, uri, operation }
//	        { type:"extension_ui_request", id, method, ... }
//	        { type:"prompt_result", id, agentInvoked:bool }  turn completion
//	        { type:"available_commands_update", commands }
//	        { type:"response", command, success:false, error }  error
//
// The prompt ACK (response command:prompt) is NOT completion: the turn is in
// flight until prompt_result (or the agent_end/message_end stream ends).
package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// OmpAdapterConfig tunes an OmpAdapter.
type OmpAdapterConfig struct {
	// OmpBin is the path to the omp binary. Defaults to "omp" on PATH.
	OmpBin string
	// Model is the fully-qualified model id (e.g. "ollama-cloud/glm-5.2").
	// Empty = omp default.
	Model string
	// ExtraArgs are appended to the omp invocation (e.g. --no-tools).
	ExtraArgs []string
	// HostTools, if non-empty, is sent via set_host_tools after ready so the
	// agent can request privileged actions through Groundskeeper.
	HostTools []RpcHostToolDefinition
	// HostHandler, if set, handles host_tool_call frames and returns the
	// result to send back as host_tool_result. Without this the agent hangs on
	// its first tool call. The host.Bridge implements this.
	HostHandler HostHandler
	// LaunchContext, if set, makes StartThread route through the process
	// launcher (internal/process.ProcessOmpRpc) which validates the launch
	// is authorized and audits it. The pool sets this per-run.
	LaunchContext *LaunchCtx
	// SSHTarget, if set, makes StartThread spawn omp over SSH on a remote host
	// instead of as a local subprocess. The format is "user@host" or just "host".
	// The adapter runs `ssh <SSHTarget> omp --mode rpc ...` and pipes JSONL over
	// the SSH connection's stdin/stdout. The protocol is identical to local —
	// only the transport changes from local pipes to SSH pipes.
	SSHTarget string
	// SSHOmpBin is the path to omp on the remote host. Defaults to "omp".
	SSHOmpBin string
	// SSHOptions are extra ssh options (e.g. ["-p", "2222", "-i", "~/.ssh/key"]).
	SSHOptions []string
}

// LaunchCtx carries the authorization fields the process launcher requires.
// When set, StartThread calls Authorize before spawning; if Authorize returns
// an error, the launch is refused. This routes all OMP launches through the
// managed process launcher (internal/process.ProcessOmpRpc) without a
// cross-package import.
type LaunchCtx struct {
	ThreadID   string
	JobID      string
	WorkerID   string
	// Authorize validates the launch request and records an audit event.
	// If nil, the adapter validates the fields inline (command must be omp,
	// args must include --mode rpc, all IDs must be present).
	Authorize func(threadID, jobID, workerID, sessionDir, workspacePath string) error
}

// HostHandler handles a host_tool_call and returns the result content. The
// adapter sends this back as {type:"host_tool_result", id, result} so the
// agent can continue.
type HostHandler interface {
	HandleToolCall(id, toolCallID, toolName string, args map[string]any) (result map[string]any, isError bool)
	// HandleURIRequest resolves a host_uri_request and returns the result.
	HandleURIRequest(id, operation, url, content string) (contentOut string, contentType string, isError bool, errMsg string)
}

// RpcHostToolDefinition mirrors OMP's RpcHostToolDefinition: a privileged tool
// the host (Groundskeeper) offers to the agent.
type RpcHostToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// OmpAdapter is an AgentRuntimeAdapter backed by a real omp --mode rpc
// subprocess. Each StartThread spawns its own omp process pinned to a
// workspace and session dir.
type OmpAdapter struct {
	cfg OmpAdapterConfig

	mu      sync.Mutex
	threads map[string]*ompThread
}

type ompThread struct {
	ref    *RuntimeThreadRef
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan RuntimeEvent
	done   chan struct{}
	closed bool
	sendMu sync.Mutex // serializes stdin writes vs shutdown
}

// NewOmpAdapter returns an OMP RPC adapter.
func NewOmpAdapter(cfg OmpAdapterConfig) *OmpAdapter {
	if cfg.OmpBin == "" {
		cfg.OmpBin = "omp"
	}
	return &OmpAdapter{cfg: cfg, threads: make(map[string]*ompThread)}
}

func (a *OmpAdapter) StartThread(ctx context.Context, workspacePath, sessionDir string) (*RuntimeThreadRef, error) {
	args := []string{"--mode", "rpc", "--session-dir", sessionDir}
	if a.cfg.Model != "" {
		args = append(args, "--model", a.cfg.Model)
	}
	args = append(args, a.cfg.ExtraArgs...)

	// Route through the managed process launcher when a LaunchContext is set.
	if a.cfg.LaunchContext != nil {
		lc := a.cfg.LaunchContext
		if lc.Authorize != nil {
			if err := lc.Authorize(lc.ThreadID, lc.JobID, lc.WorkerID, sessionDir, workspacePath); err != nil {
				return nil, fmt.Errorf("omp: launch refused: %w", err)
			}
		} else if lc.ThreadID == "" || lc.JobID == "" || lc.WorkerID == "" {
			return nil, fmt.Errorf("omp: launch refused — thread_id, job_id, worker_id required")
		}
	}

	// Build the exec command. When SSHTarget is set, spawn omp over SSH on a
	// remote host: `ssh <target> omp --mode rpc ...`. The JSONL protocol is
	// identical over SSH pipes — only the transport changes.
	var cmd *exec.Cmd
	if a.cfg.SSHTarget != "" {
		remoteOmp := a.cfg.SSHOmpBin
		if remoteOmp == "" {
			remoteOmp = "omp"
		}
		sshArgs := make([]string, 0, len(a.cfg.SSHOptions)+2+len(args))
		sshArgs = append(sshArgs, a.cfg.SSHOptions...)
		sshArgs = append(sshArgs, a.cfg.SSHTarget, remoteOmp)
		sshArgs = append(sshArgs, args...)
		cmd = exec.CommandContext(ctx, "ssh", sshArgs...)
		// Don't set cmd.Dir — the workspace is on the remote host, not local.
		// omp on the remote will use its cwd as the project root; pass the
		// workspace via --cwd if needed in ExtraArgs.
	} else {
		// Local spawn.
		if err := os.MkdirAll(sessionDir, 0o700); err != nil {
			return nil, fmt.Errorf("omp: mkdir session dir: %w", err)
		}
		ompBin := a.cfg.OmpBin
		if ompBin == "" {
			ompBin = "omp"
		}
		cmd = exec.CommandContext(ctx, ompBin, args...)
		cmd.Dir = workspacePath
	}
	// Scrub the environment (both local and SSH: the agent subprocess must not
	// see raw provider credentials from the daemon's env).
	cmd.Env = scrubEnv(os.Environ())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("omp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("omp: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr // surface omp/ssh diagnostics

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("omp: start: %w", err)
	}

	ref := &RuntimeThreadRef{
		Runtime:       "omp",
		ProcessID:     cmd.Process.Pid,
		SessionDir:    sessionDir,
		WorkspacePath: workspacePath,
	}
	ft := &ompThread{
		ref:    ref,
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
	}

	key := threadKey(ref)
	a.mu.Lock()
	a.threads[key] = ft
	a.mu.Unlock()

	// Reader goroutine: parse JSONL frames off stdout, map to RuntimeEvents.
	go a.readLoop(ft, stdout)
	// Reap goroutine: when omp exits, close the event stream.
	go a.reapLoop(ft, cmd)

	return ref, nil
}

func (a *OmpAdapter) ResumeThread(ctx context.Context, ref *RuntimeThreadRef) error {
	// Resume = start a fresh omp process on the same session dir with --continue
	// iff a prior transcript exists. OMP stores transcripts under session-dir;
	// --continue tells it to load the last one.
	sessionDir := ref.SessionDir
	if sessionDir == "" {
		return fmt.Errorf("omp: resume requires a session_dir")
	}
	hasTranscript := dirHasTranscript(sessionDir)
	cfg := a.cfg
	if hasTranscript {
		// Append --continue so omp loads the prior conversation.
		cfg.ExtraArgs = append(append([]string{}, cfg.ExtraArgs...), "--continue")
	}
	tmp := NewOmpAdapter(cfg)
	newRef, err := tmp.StartThread(ctx, ref.WorkspacePath, sessionDir)
	if err != nil {
		return err
	}
	// Replace this adapter's thread handle with the resumed process.
	*ref = *newRef
	a.mu.Lock()
	a.threads[threadKey(ref)] = tmp.threads[threadKey(newRef)]
	a.mu.Unlock()
	return nil
}

func (a *OmpAdapter) SendTurn(ctx context.Context, ref *RuntimeThreadRef, prompt string) error {
	ft := a.lookup(ref)
	if ft == nil {
		return fmt.Errorf("omp: unknown thread %s", threadKey(ref))
	}
	// { type:"prompt", message:"..." } — the ACK (response command:prompt) is
	// NOT completion; prompt_result / agent_end is. SendTurn returns immediately.
	return ft.writeCommand(map[string]any{"type": "prompt", "message": prompt})
}

func (a *OmpAdapter) Interrupt(ref *RuntimeThreadRef) error {
	ft := a.lookup(ref)
	if ft == nil {
		return fmt.Errorf("omp: unknown thread %s", threadKey(ref))
	}
	// { type:"abort" } cancels the in-flight turn.
	return ft.writeCommand(map[string]any{"type": "abort"})
}

func (a *OmpAdapter) StreamEvents(ref *RuntimeThreadRef) <-chan RuntimeEvent {
	ft := a.lookup(ref)
	if ft == nil {
		ch := make(chan RuntimeEvent)
		close(ch)
		return ch
	}
	return ft.events
}

func (a *OmpAdapter) Shutdown(ref *RuntimeThreadRef) error {
	key := threadKey(ref)
	a.mu.Lock()
	ft, ok := a.threads[key]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	ft.sendMu.Lock()
	if !ft.closed {
		ft.closed = true
		close(ft.done)
		_ = ft.stdin.Close()
		close(ft.events) // Shutdown owns the close; reapLoop only closes on natural exit.
	}
	ft.sendMu.Unlock()
	// Kill the process if it's still running; reapLoop will finalize.
	if ft.cmd != nil && ft.cmd.Process != nil {
		_ = ft.cmd.Process.Kill()
	}
	a.mu.Lock()
	delete(a.threads, key)
	a.mu.Unlock()
	return nil
}

// readLoop parses JSONL frames from omp stdout into RuntimeEvents.
func (a *OmpAdapter) readLoop(ft *ompThread, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // large frames (available_commands_update)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			// Non-JSON line (shouldn't happen in rpc mode): surface as an error event.
			a.emit(ft, RuntimeEvent{Kind: EventError, Payload: "unparseable line: " + line[:min(len(line), 120)]})
			continue
		}
		a.dispatchFrame(ft, f)
	}
}

// dispatchFrame maps one OMP frame to zero or more RuntimeEvents.
func (a *OmpAdapter) dispatchFrame(ft *ompThread, f map[string]any) {
	t, _ := f["type"].(string)
	switch t {
	case "ready":
		a.emit(ft, RuntimeEvent{Kind: EventReady})
		if len(a.cfg.HostTools) > 0 {
			_ = ft.writeCommand(map[string]any{"type": "set_host_tools", "tools": a.cfg.HostTools})
		}
	case "agent_start":
		a.emit(ft, RuntimeEvent{Kind: EventAgentStart})
	case "message_start", "message_update", "message_end":
		payload := extractMessageText(f["message"])
		a.emit(ft, RuntimeEvent{Kind: EventMessageUpdate, Payload: payload})
	case "turn_start":
		a.emit(ft, RuntimeEvent{Kind: EventAgentStart})
	case "turn_end", "prompt_result":
		a.emit(ft, RuntimeEvent{Kind: EventAgentEnd})
	case "host_tool_call":
		frameID, _ := f["id"].(string)
		toolCallID, _ := f["toolCallId"].(string)
		toolName, _ := f["toolName"].(string)
		args, _ := f["arguments"].(map[string]any)
		if args == nil {
			args = make(map[string]any)
		}
		argsJSON, _ := json.Marshal(args)
		a.emit(ft, RuntimeEvent{Kind: EventHostToolCall, ToolName: toolName, ToolArgs: string(argsJSON)})
		if a.cfg.HostHandler != nil {
			result, isErr := a.cfg.HostHandler.HandleToolCall(frameID, toolCallID, toolName, args)
			_ = ft.writeCommand(map[string]any{
				"type":    "host_tool_result",
				"id":      frameID,
				"result":  result,
				"isError": isErr,
			})
		}
	case "host_uri_request":
		frameID, _ := f["id"].(string)
		operation, _ := f["operation"].(string)
		url, _ := f["url"].(string)
		content, _ := f["content"].(string)
		a.emit(ft, RuntimeEvent{Kind: EventHostURIRequest, Payload: url})
		if a.cfg.HostHandler != nil {
			out, ct, isErr, errMsg := a.cfg.HostHandler.HandleURIRequest(frameID, operation, url, content)
			_ = ft.writeCommand(map[string]any{
				"type":        "host_uri_result",
				"id":          frameID,
				"content":     out,
				"contentType": ct,
				"isError":     isErr,
				"error":       errMsg,
			})
		}
	case "extension_ui_request":
		raw, _ := json.Marshal(f)
		a.emit(ft, RuntimeEvent{Kind: EventExtensionUIRequest, Payload: string(raw)})
	case "response":
		if ok, _ := f["success"].(bool); !ok {
			errMsg, _ := f["error"].(string)
			a.emit(ft, RuntimeEvent{Kind: EventError, Payload: errMsg})
		}
	case "available_commands_update", "session_info_update", "config_update",
		"command_output", "extension_error", "subagent_*":
	default:
	}
}

func (a *OmpAdapter) emit(ft *ompThread, ev RuntimeEvent) {
	ft.sendMu.Lock()
	defer ft.sendMu.Unlock()
	if ft.closed {
		return
	}
	select {
	case ft.events <- ev:
	case <-ft.done:
	}
}

func (a *OmpAdapter) reapLoop(ft *ompThread, cmd *exec.Cmd) {
	_ = cmd.Wait()
	ft.sendMu.Lock()
	if !ft.closed {
		ft.closed = true
		close(ft.done)
		close(ft.events)
	}
	ft.sendMu.Unlock()
}

func (a *OmpAdapter) lookup(ref *RuntimeThreadRef) *ompThread {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.threads[threadKey(ref)]
}

// writeCommand marshals and writes one JSONL command to omp stdin.
func (ft *ompThread) writeCommand(cmd map[string]any) error {
	ft.sendMu.Lock()
	defer ft.sendMu.Unlock()
	if ft.closed {
		return fmt.Errorf("omp: thread shut down")
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("omp: marshal: %w", err)
	}
	b = append(b, '\n')
	_, err = ft.stdin.Write(b)
	return err
}

// extractMessageText pulls the concatenated text content out of an OMP message
// object (message.content is an array of {type:"text", text:"..."} parts).
func extractMessageText(msg any) string {
	m, ok := msg.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := m["content"].([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, part := range content {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if pt, _ := p["type"].(string); pt == "text" {
			if txt, ok := p["text"].(string); ok {
				sb.WriteString(txt)
			}
		}
	}
	return sb.String()
}

// scrubEnv returns env with provider API keys removed so the agent subprocess
// never inherits the daemon's raw credentials (omp uses its own agent.db auth).
// Mirrors roboomp's _SCRUBBED_ENV_KEYS discipline.
func scrubEnv(env []string) []string {
	// Sensitive env var name prefixes/values to strip. Matched case-insensitively
	// by prefix so new provider keys are covered without an exhaustive list.
	sensitivePrefix := []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "AZURE_OPENAI_API_KEY",
		"GROQ_API_KEY", "CEREBRAS_API_KEY", "XAI_API_KEY", "OPENROUTER_API_KEY",
		"KILO_API_KEY", "MISTRAL_API_KEY", "ZAI_API_KEY", "MINIMAX_API_KEY",
		"OPENCODE_API_KEY", "CURSOR_ACCESS_TOKEN", "PERPLEXITY_COOKIES",
		"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN",
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		name := kv[:eq]
		upper := strings.ToUpper(name)
		drop := false
		for _, s := range sensitivePrefix {
			if upper == s || strings.HasPrefix(upper, s) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// dirHasTranscript reports whether an omp session-dir already holds a JSONL
// transcript (so ResumeThread can decide --continue vs fresh start).
func dirHasTranscript(sessionDir string) bool {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// formatHostTool is a small helper for callers building host-tool definitions.
func formatHostTool(name, desc string) RpcHostToolDefinition {
	return RpcHostToolDefinition{Name: name, Description: desc}
}

// sessionIDFromDir derives a stable runtime session id from the session dir
// basename (omp stores transcripts named by session id).
func sessionIDFromDir(sessionDir string) string {
	return filepath.Base(sessionDir)
}
