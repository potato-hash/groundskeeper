package gkdb

import (
	"database/sql"
	"fmt"
	"time"
)

// Approval status and risk enums.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalExpired  = "expired"

	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// ApprovalRow is a row in approvals.
type ApprovalRow struct {
	ID              string
	Status          string
	Risk            string
	Summary         string
	RequestedAction string
	ThreadID        string
	JobID           string
	ExpiresAt       int64 // 0 = no expiry
	CreatedAt       int64
	ResolvedAt      int64 // 0 = unresolved
}

// RequestApproval creates a pending approval and returns it.
func (g *DB) RequestApproval(jobID, risk, summary, action string) (*ApprovalRow, error) {
	// Resolve the owning thread from the job so the thread_id FK is satisfied.
	threadID := ""
	if jobID != "" {
		if j, err := g.GetJob(jobID); err != nil {
			return nil, fmt.Errorf("gkdb: request approval: lookup job: %w", err)
		} else if j != nil {
			threadID = j.ThreadID
		}
	}
	now := time.Now().Unix()
	a := &ApprovalRow{
		ID:              newID(),
		Status:          ApprovalPending,
		Risk:            risk,
		Summary:         summary,
		RequestedAction: action,
		ThreadID:        threadID,
		JobID:           jobID,
		CreatedAt:       now,
	}
	// Use NULL (not empty string) for thread_id/job_id when absent, so the
	// FK constraints are satisfied (empty "" is NOT NULL in SQLite).
	var threadArg, jobArg any
	if threadID != "" {
		threadArg = threadID
	}
	if jobID != "" {
		jobArg = jobID
	}
	_, err := g.db.Exec(`INSERT INTO approvals
		(id, status, risk, summary, requested_action, thread_id, job_id,
		 expires_at, created_at, resolved_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Status, a.Risk, a.Summary, a.RequestedAction, threadArg,
		jobArg, nil, a.CreatedAt, nil)
	if err != nil {
		return nil, fmt.Errorf("gkdb: request approval: %w", err)
	}
	return a, nil
}

// ListPendingApprovals returns all approvals still pending.
func (g *DB) ListPendingApprovals() ([]ApprovalRow, error) {
	rows, err := g.db.Query(`SELECT id, status, risk, summary, requested_action,
		thread_id, job_id, expires_at, created_at, resolved_at
		FROM approvals WHERE status='pending' ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("gkdb: list pending approvals: %w", err)
	}
	defer rows.Close()
	var out []ApprovalRow
	for rows.Next() {
		var a ApprovalRow
		var expiresAt, resolvedAt sql.NullInt64
		var threadID, jobID sql.NullString
		if err := rows.Scan(&a.ID, &a.Status, &a.Risk, &a.Summary,
			&a.RequestedAction, &threadID, &jobID, &expiresAt, &a.CreatedAt,
			&resolvedAt); err != nil {
			return nil, fmt.Errorf("gkdb: list pending scan: %w", err)
		}
		if threadID.Valid {
			a.ThreadID = threadID.String
		}
		if jobID.Valid {
			a.JobID = jobID.String
		}
		if expiresAt.Valid {
			a.ExpiresAt = expiresAt.Int64
		}
		if resolvedAt.Valid {
			a.ResolvedAt = resolvedAt.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveApproval sets an approval to approved or rejected and stamps resolved_at.
func (g *DB) ResolveApproval(id string, approved bool, resolvedBy string) error {
	status := ApprovalRejected
	if approved {
		status = ApprovalApproved
	}
	_, err := g.db.Exec(
		`UPDATE approvals SET status=?, resolved_at=? WHERE id=?`,
		status, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("gkdb: resolve approval: %w", err)
	}
	return nil
}
