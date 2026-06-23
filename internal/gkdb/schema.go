package gkdb

import (
	"database/sql"
	"fmt"
	"time"
)

// SchemaVersion tracks the gkdb schema version. Bump when adding migrations.
const SchemaVersion = 2

// Migrate creates the Groundskeeper durable tables if they do not exist and
// records the schema version. CREATE TABLE IF NOT EXISTS makes this idempotent.
func (g *DB) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_threads (
			id                 TEXT PRIMARY KEY,
			title              TEXT NOT NULL,
			runtime            TEXT NOT NULL,
			status             TEXT NOT NULL DEFAULT 'idle',
			workspace_path      TEXT NOT NULL DEFAULT '',
			session_dir        TEXT NOT NULL DEFAULT '',
			runtime_session_id TEXT NOT NULL DEFAULT '',
			parent_thread_id   TEXT NOT NULL DEFAULT '',
			goal               TEXT NOT NULL DEFAULT '',
			created_at         INTEGER NOT NULL,
			updated_at         INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS thread_turns (
			id         TEXT PRIMARY KEY,
			thread_id  TEXT NOT NULL,
			prompt     TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'pending',
			started_at INTEGER,
			ended_at   INTEGER,
			error      TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS loop_specs (
			id               TEXT PRIMARY KEY,
			thread_id        TEXT NOT NULL,
			mode             TEXT NOT NULL DEFAULT 'single',
			prompt           TEXT NOT NULL DEFAULT '',
			max_turns        INTEGER NOT NULL DEFAULT 0,
			max_wall_minutes INTEGER NOT NULL DEFAULT 0,
			max_tool_calls   INTEGER NOT NULL DEFAULT 0,
			max_cost_usd     REAL NOT NULL DEFAULT 0,
			stop_when        TEXT NOT NULL DEFAULT '',
			enabled          INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id         TEXT PRIMARY KEY,
			thread_id  TEXT NOT NULL,
			title      TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id           TEXT PRIMARY KEY,
			thread_id    TEXT NOT NULL,
			task_id      TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'queued'
			CHECK (status IN ('queued','running','waiting_runtime','waiting_approval','done','retry','failed','dead_letter')),
			kind         TEXT NOT NULL DEFAULT 'turn',
			attempts     INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			next_run_at  INTEGER,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL,
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS approvals (
			id               TEXT PRIMARY KEY,
			status           TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending','approved','rejected','expired')),
			risk             TEXT NOT NULL DEFAULT 'medium'
				CHECK (risk IN ('low','medium','high')),
			summary          TEXT NOT NULL DEFAULT '',
			requested_action TEXT NOT NULL DEFAULT '',
			thread_id        TEXT,  -- nullable: approvals can be created without a thread
			job_id           TEXT,  -- nullable: approvals can be created without a job
			expires_at       INTEGER,
			created_at       INTEGER NOT NULL,
			resolved_at      INTEGER,
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE,
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id        TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL DEFAULT '',
			job_id    TEXT NOT NULL DEFAULT '',
			action    TEXT NOT NULL,
			actor     TEXT NOT NULL DEFAULT '',
			detail    TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id         TEXT PRIMARY KEY,
			thread_id  TEXT NOT NULL DEFAULT '',
			severity   TEXT NOT NULL DEFAULT 'info',
			message    TEXT NOT NULL DEFAULT '',
			channels   TEXT NOT NULL DEFAULT '',
			delivered  INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS worker_processes (
			id             TEXT PRIMARY KEY,
			thread_id      TEXT NOT NULL DEFAULT '',
			pid            INTEGER,
			session_dir    TEXT NOT NULL DEFAULT '',
			workspace_path TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL DEFAULT 'starting',
			started_at     INTEGER NOT NULL,
			ended_at       INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS dead_letters (
			id         TEXT PRIMARY KEY,
			job_id     TEXT NOT NULL DEFAULT '',
			reason     TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS loop_runs (
			id                TEXT PRIMARY KEY,
			thread_id         TEXT NOT NULL,
			loop_spec_id      TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL DEFAULT 'active'
				CHECK (status IN ('active','stopped','completed','failed')),
			turns_enqueued    INTEGER NOT NULL DEFAULT 0,
			turns_started     INTEGER NOT NULL DEFAULT 0,
			turns_completed   INTEGER NOT NULL DEFAULT 0,
			attempts          INTEGER NOT NULL DEFAULT 0,
			started_at        INTEGER NOT NULL,
			ended_at          INTEGER,
			stop_reason       TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (thread_id) REFERENCES agent_threads(id) ON DELETE CASCADE
		)`,
		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_next_run ON jobs(status, next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_thread ON jobs(thread_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status)`,
		`CREATE INDEX IF NOT EXISTS idx_loop_runs_thread ON loop_runs(thread_id)`,
		`CREATE INDEX IF NOT EXISTS idx_loop_runs_status ON loop_runs(status)`,
	}
	for _, s := range stmts {
		if _, err := g.db.Exec(s); err != nil {
			return fmt.Errorf("gkdb: migrate: %w (stmt: %s)", err, firstLine(s))
		}
	}
	// Migration: add loop_run_id and turn_id columns to jobs for v2+ schemas.
	// Must run AFTER the CREATE TABLE (the stmts loop above), because ALTER
	// TABLE needs the table to exist first. Ignored on re-run (duplicate column).
	g.migrateColumns()
	_, err := g.db.Exec(
		`INSERT INTO schema_meta(key, value) VALUES('version', ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		fmt.Sprintf("%d", SchemaVersion),
	)
	if err != nil {
		return fmt.Errorf("gkdb: set schema version: %w", err)
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// migrateColumns adds columns to the jobs table that CREATE TABLE IF NOT
// EXISTS cannot (it only creates missing tables, not missing columns). Each
// ALTER TABLE is attempted and a "duplicate column" error is silently ignored
// (the column already exists from a prior migration). This is idempotent.
func (g *DB) migrateColumns() {
	for _, col := range []string{
		"ALTER TABLE jobs ADD COLUMN loop_run_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE jobs ADD COLUMN turn_id TEXT NOT NULL DEFAULT ''",
	} {
		_, _ = g.db.Exec(col) // ignore "duplicate column" on re-run
	}
}

// ClaimNextJob atomically claims the oldest queued job that is due (next_run_at
// is NULL or <= now) and whose thread has no job already running. This enforces
// per-thread serialization in SQL, mirroring roboomp's claim_next_event anti-join
// inside BEGIN IMMEDIATE. Returns (nil, nil) when no job is claimable.
//
// The anti-join: a job is eligible only if NOT EXISTS a running job sharing its
// thread_id. Without it, two workers could each claim a different queued job for
// the same thread and run them concurrently — which the per-thread serialization
// model forbids.
func (g *DB) ClaimNextJob(now time.Time) (*JobRow, error) {
	tx, err := g.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("gkdb: claim begin: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(`
		SELECT id, thread_id, task_id, status, kind, attempts, max_attempts,
		       next_run_at, created_at, updated_at
		FROM jobs
		WHERE status = 'queued'
		  AND (next_run_at IS NULL OR next_run_at <= ?)
		  AND NOT EXISTS (
			SELECT 1 FROM jobs j2
			WHERE j2.thread_id = jobs.thread_id AND j2.status = 'running'
		  )
		ORDER BY next_run_at IS NULL, next_run_at ASC, created_at ASC
		LIMIT 1`, now.Unix())

	var j JobRow
	var nextRun sql.NullInt64
	err = row.Scan(&j.ID, &j.ThreadID, &j.TaskID, &j.Status, &j.Kind,
		&j.Attempts, &j.MaxAttempts, &nextRun, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: claim select: %w", err)
	}
	if nextRun.Valid {
		j.NextRunAt = nextRun.Int64
	}

	j.Attempts++
	_, err = tx.Exec(
		`UPDATE jobs SET status='running', attempts=?, updated_at=? WHERE id=?`,
		j.Attempts, now.Unix(), j.ID)
	if err != nil {
		return nil, fmt.Errorf("gkdb: claim update: %w", err)
	}
	j.Status = "running"
	j.UpdatedAt = now.Unix()

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("gkdb: claim commit: %w", err)
	}
	return &j, nil
}

// ResetStuckRunning requeues every job still marked running. Called once at
// daemon start so a crash between claiming and completing does not strand tasks
// in 'running' forever. Mirrors roboomp's reset_stuck_running. Returns the count
// requeued.
func (g *DB) ResetStuckRunning() (int, error) {
	res, err := g.db.Exec(
		`UPDATE jobs SET status='queued', next_run_at=NULL WHERE status='running'`)
	if err != nil {
		return 0, fmt.Errorf("gkdb: reset stuck: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeadLetter moves a job that has exhausted its attempts to the dead_letters
// table and marks the job dead_letter. Returns true if the job was dead-lettered
// (attempts >= max_attempts), false if it still has retries left. The caller is
// responsible for requeuing on false.
func (g *DB) DeadLetter(jobID, reason string) error {
	tx, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("gkdb: deadletter begin: %w", err)
	}
	defer tx.Rollback()

	var attempts, maxAttempts int64
	var threadID string
	err = tx.QueryRow(
		`SELECT attempts, max_attempts, thread_id FROM jobs WHERE id=?`, jobID).
		Scan(&attempts, &maxAttempts, &threadID)
	if err != nil {
		return fmt.Errorf("gkdb: deadletter lookup: %w", err)
	}

	if attempts < maxAttempts {
		return nil // not exhausted; caller should requeue
	}

	id := newID()
	_, err = tx.Exec(
		`INSERT INTO dead_letters(id, job_id, reason, created_at) VALUES(?,?,?,?)`,
		id, jobID, reason, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("gkdb: deadletter insert: %w", err)
	}
	_, err = tx.Exec(
		`UPDATE jobs SET status='dead_letter', updated_at=? WHERE id=?`,
		time.Now().Unix(), jobID)
	if err != nil {
		return fmt.Errorf("gkdb: deadletter mark: %w", err)
	}
	return tx.Commit()
}
