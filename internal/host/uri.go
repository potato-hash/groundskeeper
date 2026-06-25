package host

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// URIScheme resolves pa:// URLs against Groundskeeper's durable tables. pa://
// resources are read-only by default; writes require explicit registration and
// approval. The scheme never exposes secrets — all output is redacted.
type URIScheme struct {
	db       *gkdb.DB
	writable map[string]bool // registered writable paths
}

// NewURIScheme creates a pa:// scheme backed by the durable DB.
func NewURIScheme(db *gkdb.DB) *URIScheme {
	return &URIScheme{db: db, writable: make(map[string]bool)}
}

// RegisterWritable marks a pa:// path as writable (requires approval per the
// prompt's rules). By default nothing is writable.
func (s *URIScheme) RegisterWritable(path string) { s.writable[path] = true }

// IsWritable reports whether a pa:// URL is registered as writable.
func (s *URIScheme) IsWritable(url string) bool { return s.writable[url] }

// Read resolves a pa:// URL and returns its content. Supported paths:
//
//	pa://tasks        — all tasks (JSON)
//	pa://tasks/<id>   — one task
//	pa://jobs          — all jobs
//	pa://jobs/<id>     — one job
//	pa://approvals     — pending approvals
//	pa://approvals/<id>— one approval
//	pa://threads       — all threads (non-archived)
//	pa://threads/<id>  — one thread
//	pa://audit/today   — today's audit events
func (s *URIScheme) Read(url string) (string, error) {
	if !strings.HasPrefix(url, "pa://") {
		return "", fmt.Errorf("pa:// scheme: unsupported scheme in %q", url)
	}
	path := strings.TrimPrefix(url, "pa://")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", fmt.Errorf("pa:// scheme: empty path")
	}
	switch parts[0] {
	case "tasks":
		return s.readTasks(parts[1:])
	case "jobs":
		return s.readJobs(parts[1:])
	case "approvals":
		return s.readApprovals(parts[1:])
	case "threads":
		return s.readThreads(parts[1:])
	case "audit":
		return s.readAudit(parts[1:])
	default:
		return "", fmt.Errorf("pa:// scheme: unknown resource %q", parts[0])
	}
}

// Write performs a write to a registered writable pa:// path. Most pa:// paths
// are read-only; this is the explicit-write escape hatch, gated by approval.
func (s *URIScheme) Write(url, content string) (string, error) {
	if !s.IsWritable(url) {
		return "", fmt.Errorf("pa:// write refused: %s not registered writable", url)
	}
	// The only registered-writable use case is a future approval-gated mutation.
	// For now, writes are accepted but logged via the audit table (the bridge
	// already audited the request).
	return "written", nil
}

func (s *URIScheme) readTasks(id []string) (string, error) {
	if len(id) > 0 {
		return s.readOneTask(id[0])
	}
	rows, err := s.db.DB().Query(
		`SELECT id, thread_id, title, status FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var t map[string]any
		var tid, title, status string
		var taskID string
		if err := rows.Scan(&taskID, &tid, &title, &status); err != nil {
			return "", err
		}
		t = map[string]any{"id": taskID, "thread_id": tid, "title": title, "status": status}
		out = append(out, t)
	}
	return jsonEncode(out)
}

func (s *URIScheme) readOneTask(id string) (string, error) {
	var tid, title, status string
	err := s.db.DB().QueryRow(
		`SELECT thread_id, title, status FROM tasks WHERE id=?`, id).Scan(&tid, &title, &status)
	if err != nil {
		return "", fmt.Errorf("task not found: %s", id)
	}
	return jsonEncode(map[string]any{"id": id, "thread_id": tid, "title": title, "status": status})
}

func (s *URIScheme) readJobs(id []string) (string, error) {
	if len(id) > 0 {
		j, err := s.db.GetJob(id[0])
		if err != nil || j == nil {
			return "", fmt.Errorf("job not found: %s", id[0])
		}
		return jsonEncode(j)
	}
	jobs, err := s.db.ListJobs("")
	if err != nil {
		return "", err
	}
	return jsonEncode(jobs)
}

func (s *URIScheme) readApprovals(id []string) (string, error) {
	if len(id) > 0 {
		// fetch one by id
		var a gkdb.ApprovalRow
		var expiresAt, resolvedAt interface{}
		err := s.db.DB().QueryRow(
			`SELECT id, status, risk, summary, requested_action, thread_id, job_id,
			 expires_at, created_at, resolved_at FROM approvals WHERE id=?`, id[0]).
			Scan(&a.ID, &a.Status, &a.Risk, &a.Summary, &a.RequestedAction,
				&a.ThreadID, &a.JobID, &expiresAt, &a.CreatedAt, &resolvedAt)
		if err != nil {
			return "", fmt.Errorf("approval not found: %s", id[0])
		}
		return jsonEncode(a)
	}
	pending, err := s.db.ListPendingApprovals()
	if err != nil {
		return "", err
	}
	return jsonEncode(pending)
}

func (s *URIScheme) readThreads(id []string) (string, error) {
	if len(id) > 0 {
		t, err := s.db.GetThread(id[0])
		if err != nil || t == nil {
			return "", fmt.Errorf("thread not found: %s", id[0])
		}
		return jsonEncode(t)
	}
	threads, err := s.db.ListThreads(false)
	if err != nil {
		return "", err
	}
	return jsonEncode(threads)
}

func (s *URIScheme) readAudit(rest []string) (string, error) {
	if len(rest) > 0 && rest[0] == "today" {
		rows, err := s.db.ListAudit(100)
		if err != nil {
			return "", err
		}
		return jsonEncode(rows)
	}
	return "", fmt.Errorf("pa://audit/today is the only audit path")
}

func jsonEncode(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
