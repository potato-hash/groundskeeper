package ui

import (
	"path/filepath"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// TestGkSourceDisabledWhenNoDB verifies the GK section is absent when no
// gk.db exists (Home works when Groundskeeper disabled).
func TestGkSourceDisabledWhenNoDB(t *testing.T) {
	// tryOpenGkSource returns nil when the DB doesn't exist.
	src := tryOpenGkSource()
	if src != nil {
		t.Error("tryOpenGkSource should return nil when no gk.db exists")
	}
}

// TestGkSourceLoadsThreads verifies the GK source loads threads from gkdb.
func TestGkSourceLoadsThreads(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gk.db")
	db, err := gkdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	th, _ := db.CreateThread("test-thread", "omp", "/tmp")

	src := NewGroundskeeperThreadSource(db)
	items, err := src.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ID() != th.ID {
		t.Errorf("item ID = %s, want %s", items[0].ID(), th.ID)
	}
	if !items[0].IsGroundskeeper() {
		t.Error("item should be Groundskeeper")
	}
	if items[0].Source() != "groundskeeper" {
		t.Errorf("source = %s, want groundskeeper", items[0].Source())
	}
}

// TestGkItemsNilSafe verifies gkItems returns nil when source is nil.
func TestGkItemsNilSafe(t *testing.T) {
	h := &Home{}
	if items := h.gkItems(); items != nil {
		t.Error("gkItems should return nil when gkSource is nil")
	}
}

// TestGkFooterLineDisabled verifies the footer is empty when GK is disabled.
func TestGkFooterLineDisabled(t *testing.T) {
	h := &Home{}
	if line := h.gkFooterLine(); line != "" {
		t.Errorf("footer should be empty when GK disabled, got %q", line)
	}
}

// TestAgentDeckSessionItem verifies the Agent Deck adapter wraps Instance.
func TestAgentDeckSessionItem(t *testing.T) {
	item := AgentDeckSessionItem{}
	if item.Source() != "agentdeck" {
		t.Errorf("source = %s, want agentdeck", item.Source())
	}
	if item.IsGroundskeeper() {
		t.Error("Agent Deck item should not be Groundskeeper")
	}
}

// TestGkSectionHeightDisabled verifies section height is 0 when disabled.
func TestGkSectionHeightDisabled(t *testing.T) {
	h := &Home{}
	if h := h.gkSectionHeight(); h != 0 {
		t.Errorf("section height = %d, want 0 when disabled", h)
	}
}
