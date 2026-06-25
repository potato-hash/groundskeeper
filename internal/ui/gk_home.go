package ui

// Home projection model for Groundskeeper durable threads.
//
// The existing Home renders Agent Deck sessions from session.Instance (tmux-
// backed, lives in flatItems). This file adds an OPTIONAL second projection
// sourced from Groundskeeper's gkdb (agent_threads / jobs / approvals), so
// the same Home screen can surface the durable substrate without faking it
// as a tmux session. When the GK database is absent, every code path here is
// a silent no-op and the Agent Deck UX is exactly as it was.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/potato-hash/groundskeeper/internal/agentpaths"
	"github.com/potato-hash/groundskeeper/internal/fleet"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/session"
)

// HomeItem is the common projection shape used by both Agent Deck sessions
// and Groundskeeper threads. It exists so the GK path doesn't need to lie
// about tmux fields it doesn't have (no socket/pane/runtime session) — GK
// items simply report their real status and IsGroundskeeper()==true.
type HomeItem interface {
	ID() string
	Title() string
	Status() string
	Source() string        // "agentdeck" or "groundskeeper".
	IsGroundskeeper() bool // true iff item came from gkdb.
}

// HomeSource materialises a slice of HomeItem on demand. Load() is called by
// the Home view on every render — keep it cheap (one SQLite read or one in-
// memory slice, no network, no expensive parsing).
type HomeSource interface {
	Name() string
	Load() ([]HomeItem, error)
}

// ----- Agent Deck source (existing sessions) -----

// AgentDeckSessionItem adapts a *session.Instance into a HomeItem without
// touching the live tmux fields. The underlying Instance is held by value
// pointer so existing render paths (flatItems) are unaffected; this adapter
// is only used by the GK projection's tab/enter/p/f/a dispatch path.
type AgentDeckSessionItem struct {
	Inst *session.Instance
}

func (a AgentDeckSessionItem) ID() string {
	if a.Inst == nil {
		return ""
	}
	return a.Inst.ID
}

func (a AgentDeckSessionItem) Title() string {
	if a.Inst == nil {
		return ""
	}
	return a.Inst.Title
}

func (a AgentDeckSessionItem) Status() string {
	if a.Inst == nil {
		return ""
	}
	return string(a.Inst.GetStatusThreadSafe())
}

func (AgentDeckSessionItem) Source() string        { return "agentdeck" }
func (AgentDeckSessionItem) IsGroundskeeper() bool { return false }

// AgentDeckSessionSource wraps the Agent Deck instances slice as a HomeSource.
// Load() never errors — empty session list just means "no rows".
type AgentDeckSessionSource struct {
	Get func() []*session.Instance
}

func (AgentDeckSessionSource) Name() string { return "agentdeck" }

func (s AgentDeckSessionSource) Load() ([]HomeItem, error) {
	if s.Get == nil {
		return nil, nil
	}
	insts := s.Get()
	out := make([]HomeItem, 0, len(insts))
	for _, inst := range insts {
		if inst == nil {
			continue
		}
		out = append(out, AgentDeckSessionItem{Inst: inst})
	}
	return out, nil
}

// ----- Groundskeeper source (durable threads) -----

// GroundskeeperThreadItem wraps a gkdb.ThreadRow. Threads intentionally do
// NOT carry tmux socket/pane fields; this is the whole point of the
// projection — a thread is durable substrate, not a live process.
type GroundskeeperThreadItem struct {
	Thread gkdb.ThreadRow
}

func (g GroundskeeperThreadItem) ID() string          { return g.Thread.ID }
func (g GroundskeeperThreadItem) Title() string       { return g.Thread.Title }
func (g GroundskeeperThreadItem) Status() string      { return g.Thread.Status }
func (GroundskeeperThreadItem) Source() string        { return "groundskeeper" }
func (GroundskeeperThreadItem) IsGroundskeeper() bool { return true }

// Workspace returns the thread's workspace_path (or "" if unset). Used by
// the enter-details handler.
func (g GroundskeeperThreadItem) Workspace() string { return g.Thread.WorkspacePath }

// Runtime returns the agent runtime slug (claude / hermes / etc.).
func (g GroundskeeperThreadItem) Runtime() string { return g.Thread.Runtime }

// CreatedAt returns the thread's creation timestamp (Unix seconds).
func (g GroundskeeperThreadItem) CreatedAt() int64 { return g.Thread.CreatedAt }

// GroundskeeperThreadSource loads threads from a *gkdb.DB. It is the optional
// second projection that appears when gk.db exists on disk.
type GroundskeeperThreadSource struct {
	db *gkdb.DB
}

// NewGroundskeeperThreadSource attaches an already-open gkdb. Caller owns the
// DB lifecycle (Open / Close).
func NewGroundskeeperThreadSource(db *gkdb.DB) *GroundskeeperThreadSource {
	return &GroundskeeperThreadSource{db: db}
}

func (*GroundskeeperThreadSource) Name() string { return "groundskeeper" }

// Load returns every non-archived thread as a HomeItem. Job/approval counts
// are NOT in scope here — they are surfaced via the separate status footer
// driven by fleet.Load, not by this source.
func (s *GroundskeeperThreadSource) Load() ([]HomeItem, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.ListThreads(false)
	if err != nil {
		return nil, fmt.Errorf("gk_source: list threads: %w", err)
	}
	out := make([]HomeItem, 0, len(rows))
	for i := range rows {
		out = append(out, GroundskeeperThreadItem{Thread: rows[i]})
	}
	return out, nil
}

// DB returns the underlying gkdb for handlers that need to mutate state
// (fork / archive / prompt). nil if the source is detached.
func (s *GroundskeeperThreadSource) DB() *gkdb.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// ----- Home wiring (init, render, dispatch) -----

// tryOpenGkSource attempts to open agentpaths.DataDir()+"/gk.db". It is the
// single point where Groundskeeper hooks into Home; every other call site is
// nil-safe. Returns nil if the directory or DB doesn't exist yet, or on any
// error — the Agent Deck UX must never fail because GK is missing.
func tryOpenGkSource() *GroundskeeperThreadSource {
	dir, err := agentpaths.DataDir()
	if err != nil {
		return nil
	}
	dbPath := filepath.Join(dir, "gk.db")
	if _, statErr := os.Stat(dbPath); statErr != nil {
		return nil
	}
	db, err := gkdb.Open(dbPath)
	if err != nil {
		uiLog.Warn("gkdb_open_failed", "error", err.Error(), "path", dbPath)
		return nil
	}
	return NewGroundskeeperThreadSource(db)
}

// gkItems returns the current Groundskeeper projection (nil-safe). Used by
// both the renderer and the keybinding dispatch path.
func (h *Home) gkItems() []HomeItem {
	if h.gkSource == nil {
		return nil
	}
	items, err := h.gkSource.Load()
	if err != nil {
		uiLog.Warn("gk_load_failed", "error", err.Error())
		return nil
	}
	return items
}

// selectedGkItem returns the Groundskeeper thread currently under the GK
// cursor, or nil if the cursor is out of range / no GK rows.
func (h *Home) selectedGkItem() *GroundskeeperThreadItem {
	items := h.gkItems()
	if len(items) == 0 {
		return nil
	}
	if h.gkCursor < 0 || h.gkCursor >= len(items) {
		return nil
	}
	it, ok := items[h.gkCursor].(GroundskeeperThreadItem)
	if !ok {
		return nil
	}
	return &it
}

// clampGkCursor keeps the GK cursor within bounds after a refresh.
func (h *Home) clampGkCursor() {
	items := h.gkItems()
	if h.gkCursor >= len(items) {
		h.gkCursor = len(items) - 1
	}
	if h.gkCursor < 0 {
		h.gkCursor = 0
	}
}

// gkFooterLine renders the durable-substrate status line (running jobs /
// pending approvals / dead letters) when gkdb is present. Returns "" when
// gkSource is nil so the header stays clean.
func (h *Home) gkFooterLine() string {
	if h.gkSource == nil || h.gkSource.DB() == nil {
		return ""
	}
	view, err := fleet.Load(h.gkSource.DB())
	if err != nil {
		uiLog.Warn("fleet_load_failed", "error", err.Error())
		return ""
	}
	_, running, pending, dead := view.Counts()
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sep := dim.Render(" • ")
	parts := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(
			fmt.Sprintf("\u25cf %d running", running)),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(
			fmt.Sprintf("\u25d0 %d pending", pending)),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(
			fmt.Sprintf("\u2715 %d dead", dead)),
	}
	return strings.Join(parts, sep)
}

// gkSectionHeight is the number of lines the GK panel always occupies when
// gkSource != nil. Title (1) + 1 row per thread (capped) + footer (1) —
// keeps the math simple in View(). The renderer clamps to fit.
const (
	gkSectionTitleLines  = 1
	gkSectionFooterLines = 1
)

// gkSectionHeight returns how many vertical lines the GK panel will consume
// at the current gkCursor / item count. Returns 0 when GK is disabled.
func (h *Home) gkSectionHeight() int {
	if h.gkSource == nil {
		return 0
	}
	rows := len(h.gkItems())
	return gkSectionTitleLines + rows + gkSectionFooterLines
}

// renderGkSection renders the "Groundskeeper Threads" sub-list plus the
// fleet status footer. Caller passes the desired height (== gkSectionHeight()).
func (h *Home) renderGkSection(width, height int) string {
	if h.gkSource == nil || height <= 0 {
		return ""
	}
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true)
	if h.gkFocused {
		titleStyle = titleStyle.
			Background(lipgloss.Color("4")).
			Foreground(lipgloss.Color("15"))
	} else {
		titleStyle = titleStyle.Foreground(lipgloss.Color("6"))
	}
	focusTag := "  "
	if h.gkFocused {
		focusTag = " \u25b6 "
	}
	b.WriteString(titleStyle.Render(focusTag + "Groundskeeper Threads"))
	b.WriteString("\n")

	items := h.gkItems()
	if len(items) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).
			Render("  (no threads)"))
		b.WriteString("\n")
	} else {
		for i, it := range items {
			line := renderGkRow(it, width, h.gkFocused && i == h.gkCursor)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if footer := h.gkFooterLine(); footer != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).
			Render("  " + footer))
		b.WriteString("\n")
	}

	return ensureExactHeight(b.String(), height)
}

// renderGkRow renders one thread row. The GK projection is informational,
// not interactive chrome — no prefix glyphs, just a status badge and the
// title, with the row visually inverted when it's the focused/selected row.
func renderGkRow(it HomeItem, width int, selected bool) string {
	statusBadge := lipgloss.NewStyle().Foreground(gkStatusColor(it.Status())).
		Render(fmt.Sprintf("[%s]", it.Status()))
	title := truncateRunes(it.Title(), max(width-20, 4))
	id := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(shortID(it.ID()))

	row := fmt.Sprintf("  %s %s  %s", statusBadge, title, id)
	if selected {
		row = lipgloss.NewStyle().
			Background(lipgloss.Color("4")).
			Foreground(lipgloss.Color("15")).
			Render(truncateRunes(row, width))
	} else {
		row = truncateRunes(row, width)
	}
	return row
}

func gkStatusColor(status string) lipgloss.Color {
	switch status {
	case gkdb.ThreadRunning:
		return lipgloss.Color("2")
	case gkdb.ThreadWaiting:
		return lipgloss.Color("3")
	case gkdb.ThreadBlocked:
		return lipgloss.Color("3")
	case gkdb.ThreadFailed:
		return lipgloss.Color("1")
	case gkdb.ThreadDone:
		return lipgloss.Color("8")
	case gkdb.ThreadArchived:
		return lipgloss.Color("8")
	default:
		return lipgloss.Color("7")
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "\u2026"
		}
		count++
	}
	return s
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// ----- Keybinding dispatch -----

// toggleGkFocus flips between Agent Deck focus (default) and Groundskeeper
// focus. Called by the tab key handler. Safe when GK is disabled.
func (h *Home) toggleGkFocus() {
	if h.gkSource == nil {
		return
	}
	h.gkFocused = !h.gkFocused
	if h.gkFocused {
		h.clampGkCursor()
	}
}

// handleGkKey dispatches p/f/a/enter when gkFocused is true and the GK
// cursor is on a real thread row. Returns true when the key was consumed;
// false means the caller should fall through to the normal tmux path.
// All errors surface via setError so the existing error banner picks them
// up — no new dialog machinery needed for the minimal projection.
func (h *Home) handleGkKey(msg tea.KeyMsg) bool {
	if h.gkSource == nil || !h.gkFocused {
		return false
	}
	it := h.selectedGkItem()
	if it == nil {
		return false
	}
	db := h.gkSource.DB()
	if db == nil {
		return false
	}

	switch msg.String() {
	case "p":
		// ponytail: prompt defaults to the thread's existing goal; a real
		// UI prompt input is a follow-up. SetThreadGoal is idempotent, so
		// re-setting to the same value is safe.
		goal := strings.TrimSpace(it.Thread.Goal)
		if goal == "" {
			goal = "(empty goal \u2014 press e to edit)"
		}
		if err := db.SetThreadGoal(it.Thread.ID, goal); err != nil {
			h.setError(fmt.Errorf("set goal: %w", err))
			return true
		}
		if _, err := db.CreateJob(it.Thread.ID, "turn"); err != nil {
			h.setError(fmt.Errorf("create job: %w", err))
			return true
		}
		h.clampGkCursor()
		return true

	case "f":
		child, err := db.ForkThread(&it.Thread, "")
		if err != nil {
			h.setError(fmt.Errorf("fork: %w", err))
			return true
		}
		h.setError(fmt.Errorf("forked into %s", child.Title))
		return true

	case "a":
		if err := db.ArchiveThread(it.Thread.ID); err != nil {
			h.setError(fmt.Errorf("archive: %w", err))
			return true
		}
		h.clampGkCursor()
		return true

	case "enter":
		// Show thread details via the existing error banner slot — minimal,
		// no new dialog. A real details view is a follow-up.
		h.setError(fmt.Errorf("thread: %s | runtime=%s | status=%s | ws=%s | id=%s",
			it.Thread.Title, it.Thread.Runtime, it.Thread.Status,
			it.Thread.WorkspacePath, it.Thread.ID))
		return true
	}
	return false
}
