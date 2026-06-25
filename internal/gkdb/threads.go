package gkdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Thread status enum. Mirrors the agent lifecycle.
const (
	ThreadIdle     = "idle"
	ThreadRunning  = "running"
	ThreadWaiting  = "waiting"
	ThreadBlocked  = "blocked"
	ThreadDone     = "done"
	ThreadFailed   = "failed"
	ThreadArchived = "archived"
)

// ThreadTurn status enum (the ThreadTurn state machine).
//
// queued   → turn created, not yet sent to the runtime
// sent     → prompt sent to the runtime (prompt ack received)
// streaming → agent is producing output (between agent_start and agent_end)
// waiting_approval → a host_tool_call requiring approval parked the turn
// completed → agent_end received, turn done
// failed    → turn failed (process exit, timeout, or error)
// cancelled → turn was interrupted/aborted
const (
	TurnQueued          = "queued"
	TurnSent            = "sent"
	TurnStreaming       = "streaming"
	TurnWaitingApproval = "waiting_approval"
	TurnCompleted       = "completed"
	TurnFailed          = "failed"
	TurnCancelled       = "cancelled"
)

// ThreadRow is a row in agent_threads.
type ThreadRow struct {
	ID               string
	Title            string
	Runtime          string
	Status           string
	WorkspacePath    string
	SessionDir       string
	RuntimeSessionID string
	ParentThreadID   string
	Goal             string
	CreatedAt        int64
	UpdatedAt        int64
}

// CreateThread inserts a new agent thread in 'idle' status and returns it.
func (g *DB) CreateThread(title, runtime, workspacePath string) (*ThreadRow, error) {
	now := time.Now().Unix()
	id := newID()
	t := &ThreadRow{
		ID:            id,
		Title:         title,
		Runtime:       runtime,
		Status:        ThreadIdle,
		WorkspacePath: workspacePath,
		SessionDir:    defaultThreadSessionDir(id),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err := g.db.Exec(`INSERT INTO agent_threads
		(id, title, runtime, status, workspace_path, session_dir,
		 runtime_session_id, parent_thread_id, goal, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Runtime, t.Status, t.WorkspacePath, t.SessionDir,
		t.RuntimeSessionID, t.ParentThreadID, t.Goal, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("gkdb: create thread: %w", err)
	}
	return t, nil
}

func defaultThreadSessionDir(threadID string) string {
	base := ""
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" && filepath.IsAbs(dataHome) {
		base = filepath.Join(dataHome, "groundskeeper")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		base = filepath.Join(home, ".local", "share", "groundskeeper")
	} else {
		base = filepath.Join(os.TempDir(), "groundskeeper")
	}
	return filepath.Join(base, "sessions", threadID)
}

// ListThreads returns all non-archived threads (or all if includeArchived).
func (g *DB) ListThreads(includeArchived bool) ([]ThreadRow, error) {
	q := `SELECT id, title, runtime, status, workspace_path, session_dir,
		runtime_session_id, parent_thread_id, goal, created_at, updated_at
		FROM agent_threads`
	if !includeArchived {
		q += ` WHERE status != 'archived'`
	}
	q += ` ORDER BY created_at ASC`
	rows, err := g.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("gkdb: list threads: %w", err)
	}
	defer rows.Close()
	var out []ThreadRow
	for rows.Next() {
		var t ThreadRow
		if err := rows.Scan(&t.ID, &t.Title, &t.Runtime, &t.Status, &t.WorkspacePath,
			&t.SessionDir, &t.RuntimeSessionID, &t.ParentThreadID, &t.Goal,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("gkdb: list threads scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetThread returns one thread by id, or (nil, nil) if not found.
func (g *DB) GetThread(id string) (*ThreadRow, error) {
	var t ThreadRow
	err := g.db.QueryRow(`SELECT id, title, runtime, status, workspace_path,
		session_dir, runtime_session_id, parent_thread_id, goal, created_at,
		updated_at FROM agent_threads WHERE id=?`, id).
		Scan(&t.ID, &t.Title, &t.Runtime, &t.Status, &t.WorkspacePath, &t.SessionDir,
			&t.RuntimeSessionID, &t.ParentThreadID, &t.Goal, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: get thread: %w", err)
	}
	return &t, nil
}

// ArchiveThread sets a thread's status to 'archived'.
func (g *DB) ArchiveThread(id string) error {
	_, err := g.db.Exec(
		`UPDATE agent_threads SET status='archived', updated_at=? WHERE id=?`,
		time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("gkdb: archive thread: %w", err)
	}
	return nil
}

// SetThreadGoal sets the thread's goal (the prompt for the next turn).
func (g *DB) SetThreadGoal(id, goal string) error {
	_, err := g.db.Exec(
		`UPDATE agent_threads SET goal=?, updated_at=? WHERE id=?`,
		goal, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("gkdb: set goal: %w", err)
	}
	return nil
}

// ForkThread creates a child thread preserving the parent's runtime, workspace,
// and session metadata but NOT its running process. The child starts idle.
func (g *DB) ForkThread(parent *ThreadRow, title string) (*ThreadRow, error) {
	if title == "" {
		title = parent.Title + " (fork)"
	}
	child, err := g.CreateThread(title, parent.Runtime, parent.WorkspacePath)
	if err != nil {
		return nil, err
	}
	// Preserve the parent's session dir so the fork can resume the same
	// conversation, and record the parentage.
	_, err = g.db.Exec(
		`UPDATE agent_threads SET parent_thread_id=?, session_dir=?, runtime_session_id=? WHERE id=?`,
		parent.ID, parent.SessionDir, parent.RuntimeSessionID, child.ID)
	if err != nil {
		return nil, fmt.Errorf("gkdb: fork: %w", err)
	}
	child.ParentThreadID = parent.ID
	child.SessionDir = parent.SessionDir
	child.RuntimeSessionID = parent.RuntimeSessionID
	return child, nil
}
