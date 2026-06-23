package sidecar

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/potato-hash/groundskeeper/internal/channel"
)

// LocalNotesHandler is the first sidecar the build prompt requires: a harmless
// local stub that reads/writes notes under .groundskeeper/notes with no network
// and no secrets. Delete requires approval. This replaces the email/calendar/
// contact sidecars the prompt explicitly said NOT to start with.
type LocalNotesHandler struct {
	// NotesDir is the root for note files (default: .groundskeeper/notes under cwd).
	NotesDir string
}

func (h *LocalNotesHandler) Deliver(req *channel.DeliveryRequest) error {
	if h.NotesDir == "" {
		h.NotesDir = ".groundskeeper/notes"
	}
	var p struct {
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
		Op       string `json:"op"`   // "write" or "delete"
		Name     string `json:"name"` // note filename
	}
	if err := decodeRaw(req.Payload, &p); err != nil {
		return fmt.Errorf("local_notes: decode: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("local_notes: name is required")
	}
	path := filepath.Join(h.NotesDir, p.Name)
	switch p.Op {
	case "write", "":
		if err := os.MkdirAll(h.NotesDir, 0o700); err != nil {
			return fmt.Errorf("local_notes: mkdir: %w", err)
		}
		return os.WriteFile(path, []byte(p.Message), 0o600)
	case "delete":
		// Delete requires approval — the bridge/host-tool layer gates this.
		// The sidecar itself performs the delete only after the approval is
		// resolved; here we check for a sentinel in the payload.
		if p.Message != "approved" {
			return fmt.Errorf("local_notes: delete requires approval")
		}
		return os.Remove(path)
	default:
		return fmt.Errorf("local_notes: unknown op %q", p.Op)
	}
}
