package gkdb

import (
	"database/sql"
	"fmt"
)

// LoopSpecRow is a row in loop_specs.
type LoopSpecRow struct {
	ID             string
	ThreadID       string
	Mode           string
	Prompt         string
	MaxTurns       int64
	MaxWallMinutes int64
	MaxToolCalls   int64
	MaxCostUSD     float64
	StopWhen       string
	Enabled        bool
}

// CreateLoopSpec inserts a loop spec for a thread.
func (g *DB) CreateLoopSpec(threadID, mode, prompt string, maxTurns, maxWall, maxTools int64, maxCost float64, stopWhen string) (*LoopSpecRow, error) {
	id := newID()
	_, err := g.db.Exec(`INSERT INTO loop_specs
		(id, thread_id, mode, prompt, max_turns, max_wall_minutes, max_tool_calls,
		 max_cost_usd, stop_when, enabled)
		VALUES(?,?,?,?,?,?,?,?,?,1)`,
		id, threadID, mode, prompt, maxTurns, maxWall, maxTools, maxCost, stopWhen)
	if err != nil {
		return nil, fmt.Errorf("gkdb: create loop_spec: %w", err)
	}
	return &LoopSpecRow{ID: id, ThreadID: threadID, Mode: mode, Prompt: prompt,
		MaxTurns: maxTurns, MaxWallMinutes: maxWall, MaxToolCalls: maxTools,
		MaxCostUSD: maxCost, StopWhen: stopWhen, Enabled: true}, nil
}

// GetLoopSpec returns the enabled loop spec for a thread (nil if none).
func (g *DB) GetLoopSpec(threadID string) (*LoopSpecRow, error) {
	var s LoopSpecRow
	var maxCost float64
	err := g.db.QueryRow(
		`SELECT id, thread_id, mode, prompt, max_turns, max_wall_minutes,
		        max_tool_calls, max_cost_usd, stop_when, enabled
		 FROM loop_specs WHERE thread_id=? AND enabled=1 LIMIT 1`, threadID).
		Scan(&s.ID, &s.ThreadID, &s.Mode, &s.Prompt, &s.MaxTurns, &s.MaxWallMinutes,
			&s.MaxToolCalls, &maxCost, &s.StopWhen, &s.Enabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gkdb: get loop_spec: %w", err)
	}
	s.MaxCostUSD = maxCost
	return &s, nil
}

// SetLoopEnabled enables or disables a thread's loop spec.
func (g *DB) SetLoopEnabled(threadID string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := g.db.Exec(
		`UPDATE loop_specs SET enabled=? WHERE thread_id=?`, v, threadID)
	if err != nil {
		return fmt.Errorf("gkdb: set loop enabled: %w", err)
	}
	return nil
}
