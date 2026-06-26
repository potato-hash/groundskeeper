// issue1206_install_checksum_test.go pins the supply-chain hardening for
// install.sh (audit H1, sec-secrets-REPORT.md). The documented entry point is
// `curl -fsSL .../install.sh | bash`, which downloads a release tarball and runs
// the binary inside it. Pre-fix the installer extracted and installed the
// download with NO integrity check: a tampered release asset, an account/token
// compromise, or a CDN/MITM swap executes arbitrary code on every installing
// machine. The fix fetches the release `checksums.txt` and `sha256sum -c`-style
// verifies the tarball, aborting BEFORE extraction on any mismatch/missing entry.
//
// Two invariants guard it:
//  1. Structural: install.sh fetches checksums.txt and calls the verifier
//     before `tar -xzf`, and aborts (exit 1) on failure.
//  2. Functional: the verify_download_checksum shell function (sourced in
//     isolation) returns success only on an exact SHA-256 match.
package releasetests

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func installScriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "install.sh")
}

func installScriptBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(installScriptPath(t))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	return string(raw)
}

// TestInstallScript_VerifiesChecksumBeforeExtract asserts install.sh fetches
// checksums.txt and verifies the download before the `tar -xzf` extraction.
func TestInstallScript_VerifiesChecksumBeforeExtract(t *testing.T) {
	body := installScriptBody(t)

	if !strings.Contains(body, "checksums.txt") {
		t.Fatalf("install.sh never fetches checksums.txt — downloaded asset is unverified (H1)")
	}
	if !regexp.MustCompile(`verify_download_checksum`).MatchString(body) {
		t.Fatalf("install.sh has no verify_download_checksum step (H1)")
	}

	downloadIdx := strings.Index(body, `curl -fsSL "$DOWNLOAD_URL"`)
	if downloadIdx < 0 {
		t.Fatalf("install.sh no longer downloads release assets with DOWNLOAD_URL — update this test")
	}
	installPath := body[downloadIdx:]
	verifyOffset := strings.Index(installPath, `verify_download_checksum "$TMP_DIR/groundskeeper.tar.gz"`)
	extractOffset := strings.Index(installPath, "tar -xzf")
	verifyIdx := downloadIdx + verifyOffset
	extractIdx := downloadIdx + extractOffset
	if extractOffset < 0 {
		t.Fatalf("install.sh no longer extracts with `tar -xzf` — update this test")
	}
	if verifyOffset < 0 || verifyIdx > extractIdx {
		t.Fatalf("checksum verification must run BEFORE `tar -xzf` (verifyIdx=%d, extractIdx=%d)", verifyIdx, extractIdx)
	}
}

// TestInstallScript_AbortsOnChecksumFailure asserts the verification path exits
// non-zero (fails closed) rather than warning-and-continuing.
func TestInstallScript_AbortsOnChecksumFailure(t *testing.T) {
	body := installScriptBody(t)
	// The verify call must be guarded such that failure leads to `exit 1`.
	// Accept either `if ! verify_download_checksum ...; then ... exit 1` or a
	// `verify_download_checksum ... || { ... exit 1; }` shape.
	re := regexp.MustCompile(`(?s)verify_download_checksum.*?exit 1`)
	if !re.MatchString(body) {
		t.Fatalf("a failed verify_download_checksum must `exit 1` (fail closed) — H1")
	}
}

func TestInstallScript_FallsBackToLocalSourceCheckoutWhenLatestMissing(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		"building from local source checkout",
		`-f "go.mod"`,
		`-d "cmd/groundskeeper"`,
		"go build -o",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should support local source fallback when latest release lookup fails; missing %q", want)
		}
	}
}

func TestInstallScript_FallsBackToPublicModuleWhenLatestMissing(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		"building from public source module",
		`GOPROXY=direct GOBIN="$INSTALL_DIR" go install`,
		`github.com/${REPO}/cmd/groundskeeper@main`,
		`mv -f "$INSTALL_DIR/groundskeeper" "$INSTALL_DIR/$BINARY_NAME"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should support public module fallback when latest release lookup fails; missing %q", want)
		}
	}
}

func TestInstallScript_PreflightsSupportedGoForSourceFallback(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		`SOURCE_BUILD_MIN_GO_VERSION="1.25.11"`,
		"github_api_curl()",
		`-H "Authorization: Bearer ${GITHUB_TOKEN}"`,
		"installed_go_version()",
		"go_version_at_least()",
		"source_build_go_ok()",
		"latest_release_installable()",
		"release_json_has_install_assets()",
		"latest_release_unavailable_reason()",
		"Groundskeeper source builds require Go ${SOURCE_BUILD_MIN_GO_VERSION} or newer.",
		"latest_release_installable && return 0",
		`Latest release ${LATEST_RELEASE_TAG} is missing`,
		`[[ -f "go.mod" && -d "cmd/groundskeeper" ]] && source_build_go_ok`,
		"elif source_build_go_ok; then",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should preflight a supported Go version before source fallback; missing %q", want)
		}
	}
}

func TestInstallScript_AuthenticatesGitHubReleaseAPIWhenTokenIsAvailable(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		`github_api_curl "https://api.github.com/repos/${REPO}/releases/latest"`,
		`github_api_curl "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should use authenticated GitHub API helper for release metadata; missing %q", want)
		}
	}
}

func TestInstallScript_NormalizesReleaseAssetCountFallback(t *testing.T) {
	body := installScriptBody(t)

	if strings.Contains(body, `grep -c '"browser_download_url"' || echo "0"`) {
		t.Fatal("install.sh must not append a second zero after grep -c reports no release assets")
	}
	for _, want := range []string{
		`tr ',' '\n' | grep -c '"browser_download_url"' || true`,
		`[[ "$ASSET_COUNT" =~ ^[0-9]+$ ]]`,
		`ASSET_COUNT=0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should normalize release asset counts before numeric comparison; missing %q", want)
		}
	}
}

func TestInstallScript_UsesGroundskeeperInstallerCopy(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		"Groundskeeper only supports macOS and Linux.",
		"Groundskeeper requires tmux to function.",
		"Groundskeeper works best with mouse scroll and clipboard support.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing Groundskeeper copy %q", want)
		}
	}
	for _, stale := range []string{
		"Agent Deck only supports macOS and Linux.",
		"Agent Deck requires tmux to function.",
		"Agent Deck works best with mouse scroll and clipboard support.",
	} {
		if strings.Contains(body, stale) {
			t.Fatalf("install.sh still contains stale installer copy %q", stale)
		}
	}
}

func TestInstallScript_SuppressesTmuxWarningDuringVersionProbe(t *testing.T) {
	body := installScriptBody(t)

	for _, want := range []string{
		`env GROUNDSKEEPER_SUPPRESS_TMUX_WARNING=1 AGENTDECK_SUPPRESS_TMUX_WARNING=1 "$INSTALL_DIR/$BINARY_NAME" version &> /dev/null`,
		`INSTALLED_VERSION=$(env GROUNDSKEEPER_SUPPRESS_TMUX_WARNING=1 AGENTDECK_SUPPRESS_TMUX_WARNING=1 "$INSTALL_DIR/$BINARY_NAME" version 2>&1 || echo "unknown")`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh should keep its final version probe output clean; missing %q", want)
		}
	}
}

func sourceAndCheckReleaseInstallable(t *testing.T, releaseJSON, version, goos, goarch string) int {
	t.Helper()
	script := installScriptPath(t)
	prog := `set +e
export AGENT_DECK_INSTALL_SH_SOURCE_ONLY=1
source "$1"
release_json_has_install_assets "$2" "$3" "$4" "$5"
echo "RC=$?"`
	cmd := exec.Command("bash", "-c", prog, "bash", script, releaseJSON, version, goos, goarch)
	out, _ := cmd.CombinedOutput()
	m := regexp.MustCompile(`RC=(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("could not parse release asset check exit code from output:\n%s", out)
	}
	if m[1] == "0" {
		return 0
	}
	return 1
}

func TestReleaseJSONRequiresPlatformAssetAndChecksum(t *testing.T) {
	const version = "v1.2.3"
	good := `{"tag_name":"v1.2.3","assets":[{"name":"checksums.txt"},{"name":"groundskeeper_1.2.3_darwin_arm64.tar.gz"}]}`
	missingPlatform := `{"tag_name":"v1.2.3","assets":[{"name":"checksums.txt"},{"name":"groundskeeper_1.2.3_linux_arm64.tar.gz"}]}`
	missingChecksums := `{"tag_name":"v1.2.3","assets":[{"name":"groundskeeper_1.2.3_darwin_arm64.tar.gz"}]}`

	if rc := sourceAndCheckReleaseInstallable(t, good, version, "darwin", "arm64"); rc != 0 {
		t.Fatalf("expected latest release with platform tarball and checksums.txt to be installable, got rc=%d", rc)
	}
	if rc := sourceAndCheckReleaseInstallable(t, missingPlatform, version, "darwin", "arm64"); rc == 0 {
		t.Fatal("latest release without this platform tarball must not be treated as installable")
	}
	if rc := sourceAndCheckReleaseInstallable(t, missingChecksums, version, "darwin", "arm64"); rc == 0 {
		t.Fatal("latest release without checksums.txt must not be treated as installable")
	}
}

// sourceAndVerify sources install.sh in isolation (without running main) and
// invokes verify_download_checksum with the given args, returning its exit code.
func sourceAndVerify(t *testing.T, file, asset, checksums string) int {
	t.Helper()
	script := installScriptPath(t)
	// AGENT_DECK_INSTALL_SH_SOURCE_ONLY suppresses the `main "$@"` invocation so
	// the function definitions load without performing an install.
	prog := `set +e
export AGENT_DECK_INSTALL_SH_SOURCE_ONLY=1
source "$1"
verify_download_checksum "$2" "$3" "$4"
echo "RC=$?"`
	cmd := exec.Command("bash", "-c", prog, "bash", script, file, asset, checksums)
	out, _ := cmd.CombinedOutput()
	m := regexp.MustCompile(`RC=(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("could not parse verifier exit code from output:\n%s", out)
	}
	switch m[1] {
	case "0":
		return 0
	default:
		// Non-zero; map to a single sentinel so callers assert "failed".
		if m[1] == "1" {
			return 1
		}
		// Preserve specific codes (2=missing entry, 3=no tool) for assertions.
		var code int
		for _, c := range m[1] {
			code = code*10 + int(c-'0')
		}
		return code
	}
}

func TestVerifyDownloadChecksum_Functional(t *testing.T) {
	dir := t.TempDir()
	asset := "groundskeeper_1.2.3_linux_amd64.tar.gz"
	file := filepath.Join(dir, asset)
	content := []byte("pretend-this-is-a-release-tarball")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	t.Run("matching checksum verifies", func(t *testing.T) {
		checksums := good + "  " + asset + "\n"
		if rc := sourceAndVerify(t, file, asset, checksums); rc != 0 {
			t.Fatalf("expected exit 0 for matching checksum, got %d", rc)
		}
	})

	t.Run("mismatched checksum aborts", func(t *testing.T) {
		bad := strings.Repeat("0", 64)
		checksums := bad + "  " + asset + "\n"
		if rc := sourceAndVerify(t, file, asset, checksums); rc == 0 {
			t.Fatalf("expected non-zero exit for mismatched checksum, got 0")
		}
	})

	t.Run("missing entry aborts", func(t *testing.T) {
		checksums := good + "  some-other-asset.tar.gz\n"
		if rc := sourceAndVerify(t, file, asset, checksums); rc == 0 {
			t.Fatalf("expected non-zero exit when asset absent from checksums.txt, got 0")
		}
	})
}
