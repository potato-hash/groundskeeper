package gkdb

import (
	"fmt"
	"time"
)

// AuditRow is a row in audit_events.
type AuditRow struct {
	ID        string
	ThreadID  string
	JobID     string
	Action    string
	Actor     string
	Detail    string
	Timestamp int64
}

// RecordAudit appends an audit event. detail is passed through Redact first so
// no sensitive material reaches disk. This is the trust boundary.
func (g *DB) RecordAudit(threadID, jobID, action, actor, detail string) error {
	_, err := g.db.Exec(`INSERT INTO audit_events
		(id, thread_id, job_id, action, actor, detail, timestamp)
		VALUES(?,?,?,?,?,?,?)`,
		newID(), threadID, jobID, action, actor, Redact(detail), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("gkdb: record audit: %w", err)
	}
	return nil
}

// ListAudit returns the most recent audit events (newest first).
func (g *DB) ListAudit(limit int) ([]AuditRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := g.db.Query(`SELECT id, thread_id, job_id, action, actor, detail,
		timestamp FROM audit_events ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("gkdb: list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var a AuditRow
		if err := rows.Scan(&a.ID, &a.ThreadID, &a.JobID, &a.Action, &a.Actor,
			&a.Detail, &a.Timestamp); err != nil {
			return nil, fmt.Errorf("gkdb: list audit scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
