package releasetests

import (
	"os"
	"path/filepath"
	"regexp"
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

func TestReleaseVersionIsStableForGitHubLatest(t *testing.T) {
	body := readRepoFile(t, "cmd/groundskeeper/main.go")
	m := regexp.MustCompile(`var Version = "([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("cmd/groundskeeper/main.go must define var Version")
	}
	version := m[1]
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("release Version %q must be stable semver so GitHub releases/latest can serve install binaries", version)
	}
}

func TestHomebrewVerifyDoesNotRunStaleChecksOnReleaseTags(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/homebrew-verify.yml")
	script := readRepoFile(t, "scripts/verify-homebrew-install.sh")
	installer := readRepoFile(t, "install.sh")

	for _, stale := range []string{"tags:", "'v*'", `"v*"`, "schedule:"} {
		if strings.Contains(workflow, stale) {
			t.Fatalf("homebrew verifier must not run on release tags or schedules without a Groundskeeper tap; found %q", stale)
		}
	}
	for _, stale := range []string{"asheshgoplani", "agent-deck"} {
		if strings.Contains(workflow, stale) || strings.Contains(script, stale) {
			t.Fatalf("homebrew verifier still contains stale Agent Deck tap wiring %q", stale)
		}
	}
	if !strings.Contains(script, "potato-hash") || !strings.Contains(script, "groundskeeper") {
		t.Fatal("homebrew verifier should be wired to Groundskeeper when enabled")
	}
	if !strings.Contains(workflow, "'install.sh'") {
		t.Fatal("homebrew verifier should run on installer changes")
	}
	if strings.Contains(installer, "brew install potato-hash/tap/groundskeeper") {
		t.Fatal("installer must not advertise Homebrew until a Groundskeeper tap exists")
	}
}

func TestPublicInstallCopyReflectsReleaseBinaryPath(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	goreleaser := readRepoFile(t, ".goreleaser.yml")

	for _, stale := range []string{
		"This is a development build, not a release.",
		"Until the first Groundskeeper release exists",
	} {
		if strings.Contains(readme, stale) {
			t.Fatalf("README still contains stale prerelease install copy %q", stale)
		}
	}
	for _, want := range []string{
		"Public installs prefer the latest release binary.",
		"Go 1.25.11 or\nnewer is required only for prerelease/source-fallback testing.",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing current release install copy %q", want)
		}
	}
	for _, want := range []string{
		"export OLLAMA_CLOUD_API_KEY='<your ollama cloud key>'",
		"bash -s -- --non-interactive --run-setup --model ollama-cloud/glm-5.2 --verify-model",
	} {
		if !strings.Contains(goreleaser, want) {
			t.Fatalf("GoReleaser release notes must advertise full-stack install command; missing %q", want)
		}
	}
	if strings.Contains(goreleaser, "curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/install.sh | bash") {
		t.Fatal("GoReleaser release notes must not advertise binary-only quick install as the primary path")
	}
}

func TestPublicInstallSmokeWorkflowRunsMacOSSecretBackedSmoke(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/public-install-smoke.yml")
	helper := readRepoFile(t, "scripts/run-public-install-smoke-workflow.sh")
	readme := readRepoFile(t, "README.md")
	workflowReadme := readRepoFile(t, ".github/workflows/README.md")

	for _, want := range []string{
		"workflow_dispatch:",
		"run-name: public-install-smoke ${{ github.ref_name }} ${{ inputs.dispatch_id }}",
		"dispatch_id:",
		"runs-on: macos-latest",
		"Require trusted workflow ref",
		`if [[ "${GITHUB_REF_NAME}" != "main" ]]; then`,
		"OLLAMA_CLOUD_API_KEY: ${{ secrets.OLLAMA_CLOUD_API_KEY }}",
		"GK_SMOKE_REF: ${{ github.sha }}",
		"GK_SMOKE_USE_API_RAW: '1'",
		"GK_SMOKE_INSTALL_DIR",
		"XDG_DATA_HOME",
		"contents/scripts/smoke-public-install.sh?ref=${GK_SMOKE_REF}",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("public install smoke workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{"GK_SMOKE_VERIFY_MODEL=0", "GK_SMOKE_VERIFY_MODEL:"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("public install smoke workflow must not skip model verification with %q", forbidden)
		}
	}
	if !strings.Contains(workflow, "api.github.com/repos/${GITHUB_REPOSITORY}/contents/scripts/smoke-public-install.sh?ref=${GK_SMOKE_REF}") {
		t.Fatal("public install smoke workflow must fetch the smoke wrapper from the pinned API raw ref")
	}
	for _, forbidden := range []string{"inputs.ref", "-f ref=", "GK_SMOKE_REF: ${{ inputs."} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("public install smoke workflow must not run secret-backed scripts from arbitrary refs; found %q", forbidden)
		}
	}
	if strings.Contains(workflow, "raw.githubusercontent.com") {
		t.Fatal("public install smoke workflow should use the API raw endpoint for fresh-ref testing")
	}
	for _, want := range []string{
		"gh secret set OLLAMA_CLOUD_API_KEY --repo potato-hash/groundskeeper",
		"gh workflow run public-install-smoke.yml --repo potato-hash/groundskeeper --ref main",
		"scripts/run-public-install-smoke-workflow.sh",
		"GitHub Contents API raw endpoint",
		"same trusted\nmain commit that ran the workflow",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing public smoke workflow command %q", want)
		}
	}
	if !strings.Contains(workflowReadme, "public-install-smoke.yml") ||
		!strings.Contains(workflowReadme, "secrets.OLLAMA_CLOUD_API_KEY") ||
		!strings.Contains(workflowReadme, "GitHub Contents API raw endpoint") ||
		!strings.Contains(workflowReadme, "scripts/run-public-install-smoke-workflow.sh") ||
		!strings.Contains(workflowReadme, "`github.sha` on `main`") ||
		!strings.Contains(workflowReadme, "cannot execute dispatcher-selected refs") {
		t.Fatal("workflow README must document the manual public install smoke")
	}
	for _, want := range []string{
		`gh secret list --repo "$REPO"`,
		`grep -Fxq OLLAMA_CLOUD_API_KEY`,
		`[[ "$REF" == "main" ]]`,
		`dispatch_id="gk-smoke-$(date +%s)-$$"`,
		`expected_title="public-install-smoke $REF $dispatch_id"`,
		`gh workflow run "$WORKFLOW" --repo "$REPO" --ref "$REF" -f "dispatch_id=$dispatch_id"`,
		`select(.displayTitle == \"$expected_title\" and .event == \"workflow_dispatch\" and .headBranch == \"$REF\")`,
		`gh run watch "$run_id" --repo "$REPO" --exit-status`,
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("public smoke workflow helper missing %q", want)
		}
	}
	for _, forbidden := range []string{"--body", "OLLAMA_CLOUD_API_KEY=", "printenv OLLAMA_CLOUD_API_KEY"} {
		if strings.Contains(helper, forbidden) {
			t.Fatalf("public smoke workflow helper must not accept or print secret values; found %q", forbidden)
		}
	}
	if strings.Contains(helper, `-f "ref=$REF"`) || strings.Contains(helper, "contains(\\\"$dispatch_id\\\")") {
		t.Fatal("public smoke workflow helper must not dispatch arbitrary script refs or use loose run matching")
	}
}

func TestGoTestWorkflowCoversInstallerFixtures(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/go-test.yml")

	for _, want := range []string{
		"'Makefile'",
		"'README.md'",
		"'.goreleaser.yml'",
		"'.github/workflows/README.md'",
		"'.github/workflows/go-test.yml'",
		"'.github/workflows/homebrew-verify.yml'",
		"'.github/workflows/public-install-smoke.yml'",
		"'.github/workflows/release-snapshot.yml'",
		"'install.sh'",
		"'uninstall.sh'",
		"'scripts/run-public-install-smoke-workflow.sh'",
		"'scripts/smoke-public-install.sh'",
		"'scripts/verify-homebrew-install.sh'",
		"'scripts/verify-install-state.sh'",
	} {
		if count := strings.Count(workflow, want); count < 2 {
			t.Fatalf("Go test workflow path filters should include %s for pull_request and push; found %d", want, count)
		}
	}
}

func TestUninstallCopyUsesCurrentGroundskeeperState(t *testing.T) {
	shellUninstall := readRepoFile(t, "uninstall.sh")
	goUninstall := readRepoFile(t, "cmd/groundskeeper/main.go")
	goPaths := readRepoFile(t, "cmd/groundskeeper/uninstall_paths.go")

	for name, body := range map[string]string{
		"uninstall.sh":              shellUninstall,
		"cmd/groundskeeper/main.go": goUninstall,
	} {
		for _, want := range []string{
			"Keep Groundskeeper XDG state and legacy pre-XDG data",
			"Groundskeeper XDG state and legacy pre-XDG data were preserved.",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing current uninstall copy %q", name, want)
			}
		}
		for _, stale := range []string{
			"XDG config/data/cache and legacy ~/.agent-deck",
			"XDG config/data/cache + legacy",
		} {
			if strings.Contains(body, stale) {
				t.Fatalf("%s still uses stale legacy Agent Deck uninstall copy %q", name, stale)
			}
		}
		if strings.Contains(body, "Keep Groundskeeper XDG data and legacy pre-XDG data") {
			t.Fatalf("%s still uses stale legacy Agent Deck uninstall copy", name)
		}
	}

	for _, body := range []string{shellUninstall, goPaths} {
		for _, want := range []string{
			"Data directory (gk.db and managed Espalier checkout)",
			"Legacy pre-XDG data directory",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("uninstall path labels missing %q", want)
			}
		}
	}
	if !strings.Contains(goUninstall, "Including: gk.db, managed Espalier checkout, sessions, runtime state") {
		t.Fatal("Go uninstaller summary should describe current Groundskeeper data contents")
	}
}

func TestWorkflowReadmeUsesGroundskeeperReleaseCopy(t *testing.T) {
	body := readRepoFile(t, ".github/workflows/README.md")

	for _, want := range []string{
		"automation for Groundskeeper",
		"cmd/groundskeeper/main.go",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("workflow README missing Groundskeeper release copy %q", want)
		}
	}
	for _, stale := range []string{
		"automation for agent-deck",
		"cmd/agent-deck/main.go",
	} {
		if strings.Contains(body, stale) {
			t.Fatalf("workflow README still contains stale release copy %q", stale)
		}
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
