// Package fleet renders Groundskeeper's durable substrate (threads, jobs,
// approvals, dead letters) as a read-only status view for the TUI and CLI.
// It is the Phase 6 integration surface: the bubbletea TUI embeds FleetView as
// a panel; the CLI prints the same data via the `fleet` command.
package fleet

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// View is a snapshot of the fleet's durable state, materialized for rendering.
type View struct {
	Threads    []gkdb.ThreadRow
	RunningJobs []gkdb.JobRow
	Pending    []gkdb.ApprovalRow
	DeadLetters []gkdb.JobRow
}

// Load materializes a View from the durable DB (one read pass).
func Load(db *gkdb.DB) (*View, error) {
	v := &View{}
	threads, err := db.ListThreads(false)
	if err != nil {
		return nil, fmt.Errorf("fleet: list threads: %w", err)
	}
	v.Threads = threads
	running, err := db.ListJobs(gkdb.JobRunning)
	if err != nil {
		return nil, fmt.Errorf("fleet: list running jobs: %w", err)
	}
	v.RunningJobs = running
	pending, err := db.ListPendingApprovals()
	if err != nil {
		return nil, fmt.Errorf("fleet: list approvals: %w", err)
	}
	v.Pending = pending
	dead, err := db.ListJobs(gkdb.JobDeadLetter)
	if err != nil {
		return nil, fmt.Errorf("fleet: list dead letters: %w", err)
	}
	v.DeadLetters = dead
	return v, nil
}

// Counts returns headline numbers for a status line.
func (v *View) Counts() (threads, running, pending, dead int) {
	return len(v.Threads), len(v.RunningJobs), len(v.Pending), len(v.DeadLetters)
}

// RenderText renders the view as a plain-text table (CLI output).
func (v *View) RenderText() string {
	var b strings.Builder
	threads, running, pending, dead := v.Counts()
	fmt.Fprintf(&b, "threads: %d  running jobs: %d  pending approvals: %d  dead letters: %d\n",
		threads, running, pending, dead)
	if len(v.Threads) > 0 {
		fmt.Fprintln(&b, "\nThreads:")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tTITLE\tRUNTIME\tSTATUS")
		for _, t := range v.Threads {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", shortID(t.ID), t.Title, t.Runtime, t.Status)
		}
		tw.Flush()
	}
	if len(v.RunningJobs) > 0 {
		fmt.Fprintln(&b, "\nRunning jobs:")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tTHREAD\tKIND\tATTEMPTS")
		for _, j := range v.RunningJobs {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d/%d\n", shortID(j.ID), shortID(j.ThreadID), j.Kind, j.Attempts, j.MaxAttempts)
		}
		tw.Flush()
	}
	if len(v.Pending) > 0 {
		fmt.Fprintln(&b, "\nPending approvals:")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tRISK\tSUMMARY\tJOB")
		for _, a := range v.Pending {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", shortID(a.ID), a.Risk, truncate(a.Summary, 40), shortID(a.JobID))
		}
		tw.Flush()
	}
	if len(v.DeadLetters) > 0 {
		fmt.Fprintln(&b, "\nDead letters:")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tTHREAD\tKIND")
		for _, j := range v.DeadLetters {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", shortID(j.ID), shortID(j.ThreadID), j.Kind)
		}
		tw.Flush()
	}
	return b.String()
}

// RenderTUI renders the view with lipgloss styling for the bubbletea TUI.
// It produces a bordered panel with a title and the headline counts plus the
// threads table.
func (v *View) RenderTUI(width int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		Padding(0, 1).Width(width)

	threads, running, pending, dead := v.Counts()
	header := titleStyle.Render("Groundskeeper Fleet")
	line := fmt.Sprintf("%s %d  %s %d  %s %d  %s %d",
		labelStyle.Render("threads:"), threads,
		labelStyle.Render("running:"), running,
		labelStyle.Render("pending:"), pending,
		labelStyle.Render("dead:"), dead)

	var b strings.Builder
	b.WriteString(valueStyle.Render(line) + "\n")
	if len(v.Threads) > 0 {
		b.WriteString("\n")
		for _, t := range v.Threads {
			marker := "○"
			if t.Status == gkdb.ThreadRunning {
				marker = "●"
			} else if t.Status == gkdb.ThreadArchived {
				marker = "·"
			}
			fmt.Fprintf(&b, "%s %s  %s\n", marker, truncate(t.Title, 24), t.Status)
		}
	}
	return border.Render(header + "\n" + b.String())
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
