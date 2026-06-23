package main

// gk-fleet-tui: a minimal bubbletea program that renders the Groundskeeper
// fleet status (threads/jobs/approvals/dead letters) and refreshes every
// second. This is the reachable TUI surface for the Phase 6 fleet view — a
// standalone program the user launches with `gk-fleet-tui`, distinct from
// Agent Deck's full Home model. Wiring FleetView into Home's tab bar is a
// larger refactor deferred to a follow-up; this proves the panel renders in a
// real bubbletea program.

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/potato-hash/groundskeeper/internal/fleet"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

type fleetModel struct {
	db    *gkdb.DB
	view  *fleet.View
	err   error
	width int
}

type tickMsg time.Time

func handleGkFleetTUI(args []string) {
	db := openGk()
	defer db.Close()
	m := &fleetModel{db: db, width: 80}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "gk-fleet-tui: %v\n", err)
		os.Exit(1)
	}
}

func (m *fleetModel) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *fleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			return m, m.refresh()
		}
	case tickMsg:
		return m, m.refresh()
	}
	return m, nil
}

func (m *fleetModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("gk-fleet-tui: error: %v\npress q to quit", m.err)
	}
	if m.view == nil {
		return "loading fleet…\n"
	}
	return m.view.RenderTUI(m.width) + "\n\n[r] refresh  [q] quit"
}

func (m *fleetModel) refresh() tea.Cmd {
	return func() tea.Msg {
		v, err := fleet.Load(m.db)
		if err != nil {
			m.err = err
			return tickMsg(time.Now())
		}
		m.view = v
		m.err = nil
		return tickMsg(time.Now())
	}
}

// keep the import alive if Init's tea.Tick path changes
var _ = context.Background
