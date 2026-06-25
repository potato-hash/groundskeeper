package releasetests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseLocalUsesGroundskeeperEntryPoint(t *testing.T) {
	body := readRepoFile(t, "Makefile")

	for _, want := range []string{
		"go run ./cmd/groundskeeper",
		"cmd/groundskeeper/main.go",
		"--repo potato-hash/groundskeeper",
		"Run 'groundskeeper' to start",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Makefile release/install wiring missing %q", want)
		}
	}
	for _, stale := range []string{
		"go run ./cmd/agent-deck",
		"cmd/agent-deck/main.go",
		"--repo asheshgoplani/agent-deck",
		"Run 'agent-deck' to start",
	} {
		if strings.Contains(body, stale) {
			t.Fatalf("Makefile still contains stale release/install wiring %q", stale)
		}
	}
}

func TestReleaseSnapshotWatchesGroundskeeperEntryPoint(t *testing.T) {
	body := readRepoFile(t, ".github/workflows/release-snapshot.yml")

	if !strings.Contains(body, "'cmd/groundskeeper/**'") {
		t.Fatal("release snapshot workflow must run when cmd/groundskeeper changes")
	}
	if strings.Contains(body, "cmd/agent-deck") {
		t.Fatal("release snapshot workflow still watches stale cmd/agent-deck path")
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}
