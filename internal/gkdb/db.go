// Package gkdb is Groundskeeper's durable substrate: agent threads, jobs,
// approvals, audit, and the worker-process ledger. It is a SQLite (WAL) store
// separate from Agent Deck's per-profile statedb — no schema coupling.
//
// The Open pattern mirrors internal/statedb.Open: modernc.org/sqlite via
// database/sql, WAL mode persistent on the file, and busy_timeout/foreign_keys
// as per-connection DSN pragmas (they are per-connection in SQLite, so a one-shot
// db.Exec only affects whichever pool connection ran it).
package gkdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the Groundskeeper durable database.
type DB struct {
	db   *sql.DB
	path string
}

// Open creates or opens the Groundskeeper database at dbPath with WAL mode and
// busy timeout. The parent directory is created if missing. Migrate is run
// automatically so callers never see a missing schema.
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("gkdb: mkdir: %w", err)
	}

	// busy_timeout and foreign_keys are PER-CONNECTION pragmas; pass them in the
	// DSN so every pooled connection gets them, not just the one that happened
	// to run a one-shot Exec. Same rationale as statedb.Open.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("gkdb: open: %w", err)
	}

	// WAL is persistent on the file, not per-connection.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("gkdb: wal mode: %w", err)
	}

	gk := &DB{db: db, path: dbPath}
	if err := gk.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return gk, nil
}

// Close checkpoints the WAL into the main file and closes the database.
func (g *DB) Close() error {
	_, _ = g.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return g.db.Close()
}

// DB returns the underlying *sql.DB for advanced use (e.g. testing).
func (g *DB) DB() *sql.DB { return g.db }

// Path returns the on-disk database path (empty for an in-memory DB).
func (g *DB) Path() string { return g.path }
