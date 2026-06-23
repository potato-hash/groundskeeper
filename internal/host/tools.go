// Package host implements Groundskeeper's host-tool bridge and the pa:// URI
// scheme. When the OMP worker emits a host_tool_call frame, the adapter routes
// it here; the bridge executes the privileged action (or creates an approval
// and blocks), then sends a host_tool_result frame back so the agent can
// continue. Without this round-trip the agent hangs on its first tool call.
//
// Host tools are the exclusive privileged write surface (roboomp model): every
// call is audited, risky calls create approval rows, and the result is redacted
// before it enters the audit log. The pa:// scheme exposes Groundskeeper's
// durable tables to the agent as read-only URI resources.
package host

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// ToolCall is a parsed host_tool_call frame.
type ToolCall struct {
	ID         string                 // frame id (for result correlation)
	ToolCallID string                 // OMP tool call id
	ToolName   string                 // e.g. "request_approval", "record_audit"
	Arguments  map[string]any
}

// ToolResult is the host_tool_result frame sent back to OMP.
type ToolResult struct {
	Type   string `json:"type"`         // always "host_tool_result"
	ID     string `json:"id"`           // matches the ToolCall ID
	Result any    `json:"result"`
	IsError bool  `json:"isError,omitempty"`
}

// URIRequest is a parsed host_uri_request frame.
type URIRequest struct {
	ID        string
	Operation string // "read" | "write"
	URL       string
	Content   string // for write
}

// URIResult is the host_uri_result frame sent back to OMP.
type URIResult struct {
	Type        string `json:"type"` // "host_uri_result"
	ID          string `json:"id"`
	Content     string `json:"content,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	IsError     bool   `json:"isError,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Bridge routes host_tool_call and host_uri_request frames to handlers, audits
// them, and produces the result frames to send back to OMP.
type Bridge struct {
	db     *gkdb.DB
	tools  map[string]ToolHandler
	scheme *URIScheme
}

// ToolHandler executes one host tool and returns its result content.
type ToolHandler func(args map[string]any) (content string, isError bool)

// NewBridge creates a bridge with the built-in host tools registered.
func NewBridge(db *gkdb.DB) *Bridge {
	b := &Bridge{
		db:    db,
		tools: make(map[string]ToolHandler),
		scheme: NewURIScheme(db),
	}
	b.registerBuiltins()
	return b
}

// HandleToolCall executes a host tool call, audits it, and returns the result
// frame to send back to OMP.
func (b *Bridge) HandleToolCallInternal(call *ToolCall) *ToolResult {
	// Audit the request — redaction is applied inside RecordAudit.
	detail, _ := json.Marshal(call.Arguments)
	_ = b.db.RecordAudit("", "", "host_tool_call:"+call.ToolName, "agent",
		call.ToolName+" "+string(detail))

	handler, ok := b.tools[call.ToolName]
	if !ok {
		return &ToolResult{Type: "host_tool_result", ID: call.ID,
			Result: map[string]any{"content": fmt.Sprintf("unknown host tool: %s", call.ToolName)},
			IsError: true}
	}
	content, isErr := handler(call.Arguments)
	return &ToolResult{Type: "host_tool_result", ID: call.ID,
		Result: map[string]any{"content": content}, IsError: isErr}
}

// HandleURIRequest resolves a pa:// URI read/write and returns the result frame.
func (b *Bridge) HandleURIRequestInternal(req *URIRequest) *URIResult {
	if req.Operation == "write" {
		// pa:// is read-only by default; writes require explicit registration
		// and approval. Deny unless the scheme handler allows it.
		if !b.scheme.IsWritable(req.URL) {
			return &URIResult{Type: "host_uri_result", ID: req.ID,
				IsError: true, Error: "write to read-only pa:// URI refused: " + req.URL}
		}
		content, err := b.scheme.Write(req.URL, req.Content)
		if err != nil {
			return &URIResult{Type: "host_uri_result", ID: req.ID,
				IsError: true, Error: err.Error()}
		}
		return &URIResult{Type: "host_uri_result", ID: req.ID, Content: content}
	}
	// read
	content, err := b.scheme.Read(req.URL)
	if err != nil {
		return &URIResult{Type: "host_uri_result", ID: req.ID,
			IsError: true, Error: err.Error()}
	}
	return &URIResult{Type: "host_uri_result", ID: req.ID,
		Content: content, ContentType: "text/plain"}
}

// registerBuiltins registers the host tools the prompt requires.
func (b *Bridge) registerBuiltins() {
	b.tools["request_approval"] = b.handleRequestApproval
	b.tools["record_audit"] = b.handleRecordAudit
	b.tools["task_update"] = b.handleTaskUpdate
	b.tools["job_status"] = b.handleJobStatus
	b.tools["notify_user"] = b.handleNotifyUser
	b.tools["draft_message"] = b.handleDraftMessage
}

// handleRequestApproval creates a pending approval row.
func (b *Bridge) handleRequestApproval(args map[string]any) (string, bool) {
	risk, _ := args["risk"].(string)
	summary, _ := args["summary"].(string)
	action, _ := args["action"].(string)
	jobID, _ := args["job_id"].(string)
	if risk == "" {
		risk = gkdb.RiskMedium
	}
	a, err := b.db.RequestApproval(jobID, risk, summary, action)
	if err != nil {
		return fmt.Sprintf("approval request failed: %v", err), true
	}
	return fmt.Sprintf("approval created: %s (risk=%s)", a.ID, a.Risk), false
}

// handleRecordAudit appends an audit event (redacted).
func (b *Bridge) handleRecordAudit(args map[string]any) (string, bool) {
	threadID, _ := args["thread_id"].(string)
	jobID, _ := args["job_id"].(string)
	action, _ := args["action"].(string)
	detail, _ := args["detail"].(string)
	if action == "" {
		return "record_audit: action is required", true
	}
	if err := b.db.RecordAudit(threadID, jobID, action, "host_tool", detail); err != nil {
		return fmt.Sprintf("audit failed: %v", err), true
	}
	return "audited", false
}

// handleTaskUpdate updates a task's status.
func (b *Bridge) handleTaskUpdate(args map[string]any) (string, bool) {
	taskID, _ := args["task_id"].(string)
	status, _ := args["status"].(string)
	if taskID == "" || status == "" {
		return "task_update: task_id and status required", true
	}
	_, err := b.db.DB().Exec(
		`UPDATE tasks SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().Unix(), taskID)
	if err != nil {
		return fmt.Sprintf("task_update failed: %v", err), true
	}
	return "updated", false
}

// handleJobStatus returns a job's status.
func (b *Bridge) handleJobStatus(args map[string]any) (string, bool) {
	jobID, _ := args["job_id"].(string)
	if jobID == "" {
		return "job_status: job_id required", true
	}
	j, err := b.db.GetJob(jobID)
	if err != nil || j == nil {
		return fmt.Sprintf("job not found: %s", jobID), true
	}
	return fmt.Sprintf("job %s: status=%s attempts=%d/%d", j.ID, j.Status, j.Attempts, j.MaxAttempts), false
}

// handleNotifyUser records a notification (delivery is the channel gateway's job).
func (b *Bridge) handleNotifyUser(args map[string]any) (string, bool) {
	threadID, _ := args["thread_id"].(string)
	severity, _ := args["severity"].(string)
	message, _ := args["message"].(string)
	if message == "" {
		return "notify_user: message required", true
	}
	if severity == "" {
		severity = "info"
	}
	_, err := b.db.DB().Exec(
		`INSERT INTO notifications (id, thread_id, severity, message, channels, delivered, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		fmt.Sprintf("ntf-%d", time.Now().UnixNano()), threadID, severity, "", 0, time.Now().Unix())
	if err != nil {
		return fmt.Sprintf("notify failed: %v", err), true
	}
	return "notified", false
}

// handleDraftMessage is a draft-only stub (does not send).
func (b *Bridge) handleDraftMessage(args map[string]any) (string, bool) {
	return "draft_message: not implemented (draft-only stub)", false
}

// IsKnownTool reports whether a tool name is a registered host tool.
func (b *Bridge) IsKnownTool(name string) bool {
	_, ok := b.tools[name]
	return ok
}

// ToolNames returns the registered host tool names (for set_host_tools).
func (b *Bridge) ToolNames() []string {
	out := make([]string, 0, len(b.tools))
	for k := range b.tools {
		out = append(out, k)
	}
	return out
}

// trimSpace helper
func trimSpace(s string) string { return strings.TrimSpace(s) }
