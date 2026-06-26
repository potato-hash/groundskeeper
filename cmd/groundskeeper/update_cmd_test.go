package main

import (
	"os"
	"strings"
	"testing"
)

// fakeBrewRunner records calls and returns canned outputs/errors per call,
// so tests can simulate brew's stdout/stderr without touching the real binary.
type fakeBrewRunner struct {
	calls   [][]string
	outputs []string
	errs    []error
}

func (f *fakeBrewRunner) Run(args ...string) ([]byte, error) {
	n := len(f.calls)
	f.calls = append(f.calls, args)
	var out []byte
	if n < len(f.outputs) {
		out = []byte(f.outputs[n])
	}
	var err error
	if n < len(f.errs) {
		err = f.errs[n]
	}
	return out, err
}

// TestUpdate_DetectsNoVersionBump_FailsLoudly_RegressionFor954 reproduces #954
// (reported by @alexandergharibian): `groundskeeper update` printed
//
//	Already up-to-date. Warning: 1.8.3 already installed
//	✓ Updated to v1.9.4
//
// even though brew explicitly refused to upgrade. The CLI must instead fail
// loudly with a clear message — never claim success when brew said no.
func TestUpdate_DetectsNoVersionBump_FailsLoudly_RegressionFor954(t *testing.T) {
	runner := &fakeBrewRunner{
		outputs: []string{
			// `brew update` — metadata fetch succeeds.
			"Already up-to-date.\n",
			// `brew upgrade potato-hash/tap/groundskeeper` — brew refuses
			// because the tap formula still pins the old version. Exit 0.
			"Warning: groundskeeper 1.8.3 already installed\n",
		},
		errs: []error{nil, nil},
	}

	err := runHomebrewUpgradeWith(runner, "brew upgrade potato-hash/tap/groundskeeper")
	if err == nil {
		t.Fatalf("expected error when brew refuses upgrade (regression #954: CLI printed '✓ Updated' while brew said 'already installed')")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "did not upgrade") {
		t.Errorf("error must clearly state brew did not upgrade; got: %v", err)
	}
	if !strings.Contains(msg, "954") {
		t.Errorf("error should cite issue #954 so users can trace; got: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 brew calls (update + upgrade); got %d: %+v", len(runner.calls), runner.calls)
	}
	if len(runner.calls[0]) == 0 || runner.calls[0][0] != "update" {
		t.Errorf("first call should be `brew update`; got %v", runner.calls[0])
	}
	if len(runner.calls[1]) == 0 || runner.calls[1][0] != "upgrade" {
		t.Errorf("second call should be `brew upgrade`; got %v", runner.calls[1])
	}
}

// TestUpdate_AcceptsRealUpgrade_NoFalseFailure guards against over-shooting the
// #954 fix: when brew actually upgrades, runHomebrewUpgradeWith must return nil.
func TestUpdate_AcceptsRealUpgrade_NoFalseFailure(t *testing.T) {
	runner := &fakeBrewRunner{
		outputs: []string{
			"Already up-to-date.\n",
			"==> Upgrading potato-hash/tap/groundskeeper\n==> Pouring groundskeeper-1.9.4.bottle.tar.gz\n🍺  /opt/homebrew/Cellar/groundskeeper/1.9.4: 5 files\n",
		},
		errs: []error{nil, nil},
	}
	if err := runHomebrewUpgradeWith(runner, "brew upgrade potato-hash/tap/groundskeeper"); err != nil {
		t.Fatalf("real-upgrade output should not be flagged as a refused upgrade; got: %v", err)
	}
}

func TestUpdateCommandUsesGroundskeeperCopy(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"run: groundskeeper update",
		"Skipped. Run 'groundskeeper update' later.",
		"Usage: groundskeeper update [options]",
		"Run 'groundskeeper update' to install.",
		"Restart groundskeeper to use the new version.",
		"Restart groundskeeper to use this version.",
		"brew did not upgrade groundskeeper",
		"brew untap potato-hash/tap && brew tap potato-hash/tap",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("main.go missing Groundskeeper update copy %q", want)
		}
	}
	for _, stale := range []string{
		"agent-deck update",
		"Restart agent-deck to use the new version.",
		"Restart agent-deck to use this version.",
		"brew did not upgrade agent-deck",
		"brew untap asheshgoplani/tap",
	} {
		if strings.Contains(string(body), stale) {
			t.Fatalf("main.go still contains stale update copy %q", stale)
		}
	}
}
