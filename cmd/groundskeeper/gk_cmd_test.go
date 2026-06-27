package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/host"
	"gopkg.in/yaml.v3"
)

func TestEspalierExtensionPathResolvesPackageRoot(t *testing.T) {
	dir := t.TempDir()
	entrypoint := filepath.Join(dir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := espalierExtensionPath(dir); got != entrypoint {
		t.Fatalf("espalierExtensionPath(%q) = %q, want %q", dir, got, entrypoint)
	}
	if got := esplalierArgs(dir); len(got) != 2 || got[0] != "--extension" || got[1] != entrypoint {
		t.Fatalf("esplalierArgs(%q) = %#v, want --extension %q", dir, got, entrypoint)
	}
}

func TestEspalierExtensionPathRejectsPackageWithoutEntrypoint(t *testing.T) {
	dir := t.TempDir()

	if got := espalierExtensionPath(dir); got != "" {
		t.Fatalf("espalierExtensionPath(%q) = %q, want empty string for missing entrypoint", dir, got)
	}
	if got := esplalierArgs(dir); got != nil {
		t.Fatalf("esplalierArgs(%q) = %#v, want nil for missing entrypoint", dir, got)
	}
}

func TestResolveEspalierPathUsesNearbySibling(t *testing.T) {
	root := t.TempDir()
	gkNested := filepath.Join(root, "groundskeeper", "cmd", "groundskeeper")
	espalierDir := filepath.Join(root, "espalier")
	if err := os.MkdirAll(gkNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(espalierDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(gkNested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	t.Setenv("GK_ESPALIER_PATH", "")
	t.Setenv("HOME", filepath.Join(root, "home"))

	want, err := filepath.EvalSymlinks(espalierDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveEspalierPath(); got != want {
		t.Fatalf("resolveEspalierPath() = %q, want nearby sibling %q", got, want)
	}
}

func TestResolveEspalierPathFallsBackToManagedDataDir(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("GK_ESPALIER_PATH", "")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.Chdir(empty); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	want := filepath.Join(data, "groundskeeper", "espalier")
	if got := resolveEspalierPath(); got != want {
		t.Fatalf("resolveEspalierPath() = %q, want managed path %q", got, want)
	}
}

func TestHostToolDefinitionsIncludeObjectParametersSchema(t *testing.T) {
	db, err := gkdb.Open(filepath.Join(t.TempDir(), "gk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	defs := hostToolDefinitions(host.NewBridge(db))
	if len(defs) == 0 {
		t.Fatal("expected host tool definitions")
	}
	for _, def := range defs {
		if def.Parameters == nil {
			t.Fatalf("%s parameters schema is nil", def.Name)
		}
		if got := def.Parameters["type"]; got != "object" {
			t.Fatalf("%s parameters schema type = %v, want object", def.Name, got)
		}
		if _, ok := def.Parameters["properties"].(map[string]any); !ok {
			t.Fatalf("%s parameters properties missing or wrong type: %#v", def.Name, def.Parameters["properties"])
		}
	}
}

func TestSetupNonInteractiveReportsEspalierEntrypoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	prependStubOMP(t)

	espalierDir := filepath.Join(home, "espalier")
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GK_ESPALIER_PATH", espalierDir)

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--model", "test/provider", "--espalier-path", espalierDir})
	})
	for _, want := range []string{
		"Groundskeeper Setup — Non-interactive mode",
		"Running without prompts",
		"[OK] extension entrypoint: " + entrypoint,
		"groundskeeper gk-daemon --model test/provider --slots 2 --espalier-path " + entrypoint,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestSetupNonInteractiveDoesNotWriteOmpConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	prependStubOMP(t)

	espalierDir := filepath.Join(home, "espalier")
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--espalier-path", espalierDir})
	})
	if !strings.Contains(out, "Global OMP config write skipped in non-interactive mode") {
		t.Fatalf("setup output missing non-interactive skip\n--- output ---\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "config.yml")); !os.IsNotExist(err) {
		t.Fatalf("non-interactive setup wrote OMP config, stat err=%v", err)
	}
}

func TestSetupNonInteractiveWritesOmpConfigWhenFlagSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	prependStubOMP(t)

	espalierDir := filepath.Join(home, "espalier")
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--write-omp-config", "--espalier-path", espalierDir})
	})
	if !strings.Contains(out, "OMP config created:") {
		t.Fatalf("setup output missing config write confirmation\n--- output ---\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "config.yml")); err != nil {
		t.Fatalf("non-interactive --write-omp-config did not write OMP config: %v", err)
	}
}

func TestSetupNonInteractiveExitsWhenRequiredPiecesMissing(t *testing.T) {
	if os.Getenv("GK_SETUP_MISSING_HELPER") == "1" {
		home := os.Getenv("GK_TEST_HOME")
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		os.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		os.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		os.Setenv("PATH", filepath.Join(home, "empty-bin"))
		os.Setenv("BUN_INSTALL", "")
		handleSetup([]string{"--non-interactive", "--espalier-path", filepath.Join(home, "missing-espalier")})
		return
	}

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "empty-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSetupNonInteractiveExitsWhenRequiredPiecesMissing")
	cmd.Env = append(os.Environ(), "GK_SETUP_MISSING_HELPER=1", "GK_TEST_HOME="+home, "BUN_INSTALL=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("setup unexpectedly succeeded\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"Setup incomplete.",
		"omp is not installed or discoverable",
		"Espalier extension entrypoint is missing",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup missing failure detail %q\n--- output ---\n%s", want, body)
		}
	}
}

func TestSetupInstallMissingReplacesEmptyEspalierDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	prependStubOMP(t)
	prependStubGitAndBun(t)

	espalierDir := filepath.Join(home, "empty-espalier")
	if err := os.MkdirAll(espalierDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--install-missing", "--espalier-path", espalierDir})
	})
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	for _, want := range []string{
		"exists but is empty",
		"Cloning Espalier to " + espalierDir,
		"[OK] Espalier installed and built",
		"Setup complete!",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q\n--- output ---\n%s", want, out)
		}
	}
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("expected stub build to create Espalier entrypoint: %v", err)
	}
}

func TestSetupInstallMissingRepairsInterruptedManagedEspalierCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	prependStubOMP(t)
	prependStubGitAndBun(t)

	espalierDir := managedEspalierPath()
	staleCloneMarker := filepath.Join(espalierDir, ".git", "objects", "partial")
	if err := os.MkdirAll(filepath.Dir(staleCloneMarker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleCloneMarker, []byte("interrupted clone\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--install-missing", "--espalier-path", espalierDir})
	})
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	for _, want := range []string{
		"looks like an interrupted managed Espalier checkout",
		"Cloning Espalier to " + espalierDir,
		"[OK] Espalier installed and built",
		"Setup complete!",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q\n--- output ---\n%s", want, out)
		}
	}
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("expected stub build to create Espalier entrypoint: %v", err)
	}
	if _, err := os.Stat(staleCloneMarker); !os.IsNotExist(err) {
		t.Fatalf("expected stale interrupted clone marker to be removed, stat err=%v", err)
	}
}

func TestLookupBunFindsHomeBunBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUN_INSTALL", "")
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	bunPath := filepath.Join(home, ".bun", "bin", "bun")
	if err := os.MkdirAll(filepath.Dir(bunPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bunPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := lookupBun(); got != bunPath {
		t.Fatalf("lookupBun() = %q, want %q", got, bunPath)
	}
}

func TestLookupBunFindsBunInstallBin(t *testing.T) {
	home := t.TempDir()
	bunInstall := filepath.Join(home, "custom-bun")
	t.Setenv("HOME", filepath.Join(home, "unused-home"))
	t.Setenv("BUN_INSTALL", bunInstall)
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	bunPath := filepath.Join(bunInstall, "bin", "bun")
	if err := os.MkdirAll(filepath.Dir(bunPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bunPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := lookupBun(); got != bunPath {
		t.Fatalf("lookupBun() = %q, want %q", got, bunPath)
	}
}

func TestLookupOMPFindsBunInstallBin(t *testing.T) {
	home := t.TempDir()
	bunInstall := filepath.Join(home, "custom-bun")
	t.Setenv("HOME", filepath.Join(home, "unused-home"))
	t.Setenv("BUN_INSTALL", bunInstall)
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	ompPath := filepath.Join(bunInstall, "bin", "omp")
	if err := os.MkdirAll(filepath.Dir(ompPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ompPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := lookupOMP(); got != ompPath {
		t.Fatalf("lookupOMP() = %q, want %q", got, ompPath)
	}
}

func TestInstallBunUsesPipefail(t *testing.T) {
	body, err := os.ReadFile("gk_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	want := `exec.Command("bash", "-o", "pipefail", "-c", "curl -fsSL https://bun.sh/install | bash")`
	if !strings.Contains(string(body), want) {
		t.Fatalf("installBun must use pipefail so curl failures abort the installer")
	}
}

func TestSetupInstallMissingInstallsBunForEspalierBuild(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
	}
	writeStub("omp", "#!/usr/bin/env sh\nexit 0\n")
	writeStub("tmux", "#!/usr/bin/env sh\nexit 0\n")
	writeStub("git", "#!/usr/bin/env sh\nif [ \"$1\" = \"clone\" ]; then mkdir -p \"$3\"; printf '{\"scripts\":{\"build\":\"bun build\"}}\\n' > \"$3/package.json\"; exit 0; fi\nexit 1\n")
	writeStub("curl", `#!/usr/bin/env sh
cat <<'INSTALL_BUN'
mkdir -p "$HOME/.bun/bin"
cat > "$HOME/.bun/bin/bun" <<'BUN'
#!/usr/bin/env sh
if [ "$1" = "install" ]; then
  exit 0
fi
if [ "$1" = "run" ] && [ "$2" = "build" ]; then
  mkdir -p dist/extensions
  printf 'export default function() {}\n' > dist/extensions/index.js
  exit 0
fi
if [ "$1" = "--version" ]; then
  printf '1.3.14\n'
  exit 0
fi
exit 1
BUN
chmod +x "$HOME/.bun/bin/bun"
INSTALL_BUN
`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
	t.Setenv("BUN_INSTALL", "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")

	espalierDir := filepath.Join(home, "espalier")
	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--install-missing", "--espalier-path", espalierDir})
	})
	for _, want := range []string{
		"Installing Bun for Espalier builds",
		"Cloning Espalier to " + espalierDir,
		"[OK] Espalier installed and built",
		"Setup complete!",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q\n--- output ---\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".bun", "bin", "bun")); err != nil {
		t.Fatalf("expected setup to install bun stub: %v", err)
	}
	if _, err := os.Stat(filepath.Join(espalierDir, "dist", "extensions", "index.js")); err != nil {
		t.Fatalf("expected setup to build Espalier with installed bun: %v", err)
	}
}

func TestSetupRefusesNonEmptyNonEspalierDir(t *testing.T) {
	if os.Getenv("GK_SETUP_BAD_ESPALIER_HELPER") == "1" {
		home := os.Getenv("GK_TEST_HOME")
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		os.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		os.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		os.Setenv("GROUNDSKEEPER_SUPPRESS_TMUX_WARNING", "1")
		prependStubOMP(t)
		handleSetup([]string{"--non-interactive", "--install-missing", "--espalier-path", filepath.Join(home, "not-espalier")})
		return
	}

	home := t.TempDir()
	badDir := filepath.Join(home, "not-espalier")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "README.md"), []byte("user data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(badDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSetupRefusesNonEmptyNonEspalierDir")
	cmd.Env = append(os.Environ(), "GK_SETUP_BAD_ESPALIER_HELPER=1", "GK_TEST_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("setup unexpectedly succeeded\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"does not look like an Espalier checkout",
		"missing " + filepath.Join(badDir, "package.json"),
		"Move or remove that directory",
		"Setup incomplete.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup missing failure detail %q\n--- output ---\n%s", want, body)
		}
	}
	if _, err := os.Stat(filepath.Join(badDir, "README.md")); err != nil {
		t.Fatalf("setup removed non-empty non-Espalier directory contents: %v", err)
	}
}

func TestSetupFailsWhenRequiredDependenciesMissing(t *testing.T) {
	if os.Getenv("GK_SETUP_DEPS_HELPER") == "1" {
		home := os.Getenv("GK_TEST_HOME")
		bin := filepath.Join(home, "bin")
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		os.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		os.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		os.Setenv("PATH", bin)
		handleSetup([]string{"--non-interactive", "--espalier-path", filepath.Join(home, "espalier")})
		return
	}

	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "omp"), []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'omp test'; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join(home, "espalier", "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSetupFailsWhenRequiredDependenciesMissing")
	cmd.Env = append(os.Environ(), "GK_SETUP_DEPS_HELPER=1", "GK_TEST_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("setup unexpectedly succeeded without tmux/git\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"Setup incomplete.",
		"tmux is required but not installed",
		"git is required for Espalier clone/worktrees",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup missing dependency failure detail %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "Setup complete!") {
		t.Fatalf("setup printed success despite missing dependencies\n--- output ---\n%s", body)
	}
}

func TestSetupReportsAuthBrokerWithoutPrintingValue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	value := "auth-broker-fixture-value"
	t.Setenv("OMP_AUTH_BROKER_TOKEN", value)
	prependStubOMP(t)
	prependStubTool(t, "tmux", "#!/bin/sh\nexit 0\n")
	prependStubTool(t, "git", "#!/bin/sh\nexit 0\n")

	espalierDir := filepath.Join(home, "espalier")
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--model", "openai/gpt-5.2", "--espalier-path", espalierDir})
	})
	if !strings.Contains(out, "OMP auth-broker env configured") {
		t.Fatalf("setup output missing auth-broker status\n--- output ---\n%s", out)
	}
	if strings.Contains(out, "[NOT FOUND] No OMP credential store") {
		t.Fatalf("setup reported missing credentials despite auth broker\n--- output ---\n%s", out)
	}
	if strings.Contains(out, value) {
		t.Fatalf("setup printed auth broker value\n--- output ---\n%s", out)
	}
}

func TestSetupDoesNotRunOmpVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	value := "version-output-sentinel"
	t.Setenv("GK_OMP_VERSION_OUTPUT", value)
	marker := filepath.Join(home, "omp-version-called")
	t.Setenv("GK_OMP_VERSION_MARKER", marker)
	prependStubTool(t, "omp", "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' \"$GK_OMP_VERSION_OUTPUT\"; touch \"$GK_OMP_VERSION_MARKER\"; exit 0; fi\nexit 0\n")
	prependStubTool(t, "tmux", "#!/bin/sh\nexit 0\n")
	prependStubTool(t, "git", "#!/bin/sh\nexit 0\n")

	espalierDir := filepath.Join(home, "espalier")
	entrypoint := filepath.Join(espalierDir, "dist", "extensions", "index.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("export default function() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		handleSetup([]string{"--non-interactive", "--model", "openai/gpt-5.2", "--espalier-path", espalierDir})
	})
	if strings.Contains(out, value) {
		t.Fatalf("setup printed output from omp --version\n--- output ---\n%s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("setup invoked omp --version, marker stat err=%v\n--- output ---\n%s", err, out)
	}
}

func TestSetupHelpMatchesHermesPolishSurface(t *testing.T) {
	body, err := os.ReadFile("gk_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, want := range []string{
		`Usage: groundskeeper setup [options]`,
		`Configure the full Groundskeeper stack: OMP, Espalier Core,`,
		`Safe to re-run: existing installs, Espalier checkouts, and`,
		`Options:`,
		`Examples:`,
		`groundskeeper setup --non-interactive --install-missing --model ollama-cloud/glm-5.2 --verify-model`,
		`--memory-backend string`,
		`--hindsight-url string`,
		`--recommended-skills`,
		`openai-codex/gpt-5.5:xhigh`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("setup help missing Hermes-polish copy %q", want)
		}
	}
}

func TestInstallScriptOffersFirstRunSetup(t *testing.T) {
	cmd := exec.Command("bash", "-n", "../../install.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh has invalid syntax: %v\n%s", err, out)
	}

	body, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"--run-setup",
		"--skip-setup",
		"--model <model>",
		"--memory-backend <name>",
		"--hindsight-url <url>",
		"--recommended-skills",
		"--verify-model",
		"maybe_run_first_run_setup",
		"Run first-run setup now? [Y/n]",
		"setup_args+=(--non-interactive --install-missing)",
		`setup_args+=(--memory-backend "$SETUP_MEMORY_BACKEND")`,
		`setup_args+=(--hindsight-url "$SETUP_HINDSIGHT_URL")`,
		`setup_args+=(--recommended-skills)`,
		"</dev/tty",
		"--non-interactive --install-missing",
		`if [[ "$SETUP_MODE" == "run" ]]; then`,
		"GOPROXY=direct",
		"preflight_source_build_prereq",
		"latest_release_unavailable_reason",
		"SOURCE_BUILD_MIN_GO_VERSION=\"1.25.11\"",
		"ensure_bun_for_first_run_setup",
		"run_without_sensitive_env bash -o pipefail -c 'curl -fsSL https://bun.sh/install | bash'",
		"$HOME/.bun/bin/bun",
		"if ! ensure_bun_for_first_run_setup; then",
		"return 1\n    fi\n\n    if [[ \"$SKIP_OPTIONAL_DEPS\" == \"true\" ]]; then",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	if strings.Contains(script, `export OLLAMA_CLOUD_API_KEY="$OLLAMA_API_KEY"`) {
		t.Fatal("install.sh should not globally alias OLLAMA_API_KEY into OLLAMA_CLOUD_API_KEY")
	}
}

func TestInstallScriptSupportsOptInCuaDriverInstall(t *testing.T) {
	cmd := exec.Command("bash", "-n", "../../install.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh has invalid syntax: %v\n%s", err, out)
	}

	body, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"--install-cua-driver",
		"INSTALL_CUA_DRIVER=false",
		"INSTALL_CUA_DRIVER=true",
		"find_cua_driver()",
		"install_cua_driver()",
		"run_without_sensitive_env bash -o pipefail -c 'curl -fsSL https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/scripts/install.sh | bash -s -- --bin-dir \"$1\"'",
		"irm https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/scripts/install.ps1 | iex",
		"○ cua-driver (optional computer-use driver; install with --install-cua-driver)",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install.sh missing Cua Driver install support %q", want)
		}
	}
}

func TestInstallScriptDoesNotRunOmpVersionInDependencySummary(t *testing.T) {
	body, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	if strings.Contains(script, "omp --version") {
		t.Fatal("install.sh dependency summary must not run omp --version")
	}
	if !strings.Contains(script, "✓ omp found") {
		t.Fatal("install.sh should report discovered omp without printing subprocess output")
	}
}

func TestInstallScriptPreflightsGoWhenNoRelease(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	bin := t.TempDir()
	writeInstallNoReleaseStubs(t, bin, nil)

	cmd := exec.Command(bashPath, "../../install.sh", "--non-interactive")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+bin,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh unexpectedly succeeded without release or Go\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"No latest release found",
		"Pre-release installs fall back to building github.com/potato-hash/groundskeeper/cmd/groundskeeper@main",
		"Install Go 1.25.11 or newer, then re-run the same install command.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install preflight output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "tmux is not installed") {
		t.Fatalf("install.sh checked tmux before source-build Go preflight\n--- output ---\n%s", body)
	}
}

func TestInstallScriptPreflightsOldGoWhenNoRelease(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	bin := t.TempDir()
	writeInstallNoReleaseStubs(t, bin, map[string]string{
		"go": `#!/bin/sh
if [ "$1" = "env" ] && [ "$2" = "GOVERSION" ]; then
  printf 'go1.24.13\n'
  exit 0
fi
if [ "$1" = "version" ]; then
  printf 'go version go1.24.13 linux/amd64\n'
  exit 0
fi
exit 1
`,
	})

	cmd := exec.Command(bashPath, "../../install.sh", "--non-interactive")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+bin,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh unexpectedly succeeded without release and with old Go\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"No latest release found",
		"Go 1.24.13 is too old",
		"Groundskeeper source builds require Go 1.25.11 or newer.",
		"Install Go 1.25.11 or newer, then re-run the same install command.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install old-Go preflight output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "tmux is not installed") {
		t.Fatalf("install.sh checked tmux before source-build Go version preflight\n--- output ---\n%s", body)
	}
}

func TestInstallScriptFailsWithoutTmuxInNonInteractiveMode(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	bin := t.TempDir()
	stubs := map[string]string{
		"curl": "#!/bin/sh\nprintf '{\"tag_name\":\"v0.0.1\"}\\n'\n",
		"grep": "#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in *tag_name*) printf '%s\\n' \"$line\";; esac; done\n",
		"sed":  "#!/bin/sh\nprintf 'v0.0.1\\n'\n",
		"tr":   "#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in Darwin) printf 'darwin\\n';; *) printf '%s\\n' \"$line\";; esac; done\n",
		"uname": `#!/bin/sh
case "$1" in
  -m) printf 'arm64\n' ;;
  *) printf 'Darwin\n' ;;
esac
`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bashPath, "../../install.sh", "--non-interactive")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+bin,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh unexpectedly succeeded without tmux\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"tmux is not installed.",
		"Groundskeeper requires tmux to function.",
		"Error: tmux is required but was not found after automatic install attempts.",
		"Install tmux, then re-run the same install command.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install missing tmux output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "Installation successful!") {
		t.Fatalf("install.sh claimed success without tmux\n--- output ---\n%s", body)
	}
}

func TestInstallScriptAbortsRunSetupWhenBunInstallFails(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	bin := t.TempDir()
	stubs := map[string]string{
		"curl": "#!/bin/sh\nexit 22\n",
		"go": `#!/bin/sh
if [ "$1" = "env" ] && [ "$2" = "GOVERSION" ]; then
  printf 'go1.25.11\n'
  exit 0
fi
	if [ "$1" = "install" ]; then
	  mkdir -p "$GOBIN"
	  cat > "$GOBIN/groundskeeper" <<-'GK'
	#!/bin/sh
	case "$1" in
	  version) printf 'groundskeeper test\n'; exit 0 ;;
	  setup) printf 'setup should not run\n'; exit 42 ;;
	esac
	exit 0
	GK
  chmod +x "$GOBIN/groundskeeper"
  exit 0
fi
exit 1
`,
		"tmux": "#!/bin/sh\nif [ \"$1\" = \"-V\" ]; then printf 'tmux test\\n'; fi\nexit 0\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bashPath, "../../install.sh", "--non-interactive", "--run-setup", "--model", "test/provider")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh unexpectedly succeeded when --run-setup failed\n%s", out)
	}
	body := string(out)
	for _, want := range []string{
		"Groundskeeper binary installed",
		"Bun is required to build Espalier during first-run setup.",
		"Error: Bun install failed.",
		"First-run setup did not complete.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "setup should not run") {
		t.Fatalf("install.sh ran setup after Bun install failed\n--- output ---\n%s", body)
	}
	if strings.Contains(body, "Installation successful!") {
		t.Fatalf("install.sh claimed full success before setup completed\n--- output ---\n%s", body)
	}
}

func TestInstallScriptExportsHomeBunBinForRunSetup(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	bin := t.TempDir()
	setupMarker := filepath.Join(home, "setup-called")
	stubs := map[string]string{
		"curl": `#!/bin/sh
case "$*" in
  *bun.sh/install*)
    cat <<'INSTALL_BUN'
mkdir -p "$HOME/.bun/bin"
cat > "$HOME/.bun/bin/bun" <<'BUN'
#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '1.3.14\n'
  exit 0
fi
exit 0
BUN
chmod +x "$HOME/.bun/bin/bun"
INSTALL_BUN
    exit 0
    ;;
esac
exit 22
`,
		"go": fmt.Sprintf(`#!/bin/sh
if [ "$1" = "env" ] && [ "$2" = "GOVERSION" ]; then
  printf 'go1.25.11\n'
  exit 0
fi
if [ "$1" = "install" ]; then
  mkdir -p "$GOBIN"
	  cat > "$GOBIN/groundskeeper" <<-'GK'
#!/bin/sh
case "$1" in
  version) printf 'groundskeeper test\n'; exit 0 ;;
  setup)
    command -v bun >/dev/null || { printf 'bun missing from setup PATH\n'; exit 43; }
    saw_memory=false
    saw_url=false
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --memory-backend)
          [ "$2" = "hindsight" ] || { printf 'bad memory backend\n'; exit 44; }
          saw_memory=true
          shift 2
          ;;
        --hindsight-url)
          [ "$2" = "http://hindsight.test:8888" ] || { printf 'bad hindsight URL\n'; exit 45; }
          saw_url=true
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    [ "$saw_memory" = true ] && [ "$saw_url" = true ] || { printf 'memory flags missing\n'; exit 46; }
    bun --version
    printf 'setup saw bun\n'
    touch %q
    exit 0
    ;;
esac
exit 0
GK
  chmod +x "$GOBIN/groundskeeper"
  exit 0
fi
exit 1
`, setupMarker),
		"tmux": "#!/bin/sh\nif [ \"$1\" = \"-V\" ]; then printf 'tmux test\\n'; fi\nexit 0\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bashPath, "../../install.sh", "--non-interactive", "--run-setup", "--model", "test/provider", "--memory-backend", "hindsight", "--hindsight-url", "http://hindsight.test:8888")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed even though Bun install succeeded: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"Bun available at " + filepath.Join(home, ".bun", "bin", "bun"),
		"setup saw bun",
		"Get started:",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if _, err := os.Stat(setupMarker); err != nil {
		t.Fatalf("setup stub did not run successfully: %v\n--- output ---\n%s", err, body)
	}
}

func testBashPath(t *testing.T) string {
	t.Helper()
	bashPath := "/bin/bash"
	if _, err := os.Stat(bashPath); err == nil {
		return bashPath
	}
	found, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	return found
}

func writeInstallNoReleaseStubs(t *testing.T, bin string, extra map[string]string) {
	t.Helper()
	stubs := map[string]string{
		"curl":  "#!/bin/sh\nexit 22\n",
		"grep":  "#!/bin/sh\nquiet=0\ncase \"$1\" in -q|-qi|-iq) quiet=1; shift ;; esac\npattern=$1\nshift\nfound=1\nif [ \"$#\" -gt 0 ]; then\n  for file in \"$@\"; do\n    [ -r \"$file\" ] || continue\n    while IFS= read -r line; do\n      case \"$line\" in *\"$pattern\"*) found=0; [ \"$quiet\" = 1 ] || printf '%s\\n' \"$line\" ;; esac\n    done < \"$file\"\n  done\nelse\n  while IFS= read -r line; do\n    case \"$line\" in *\"$pattern\"*) found=0; [ \"$quiet\" = 1 ] || printf '%s\\n' \"$line\" ;; esac\n  done\nfi\nexit \"$found\"\n",
		"sed":   "#!/bin/sh\ncat\n",
		"tr":    "#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in Linux) printf 'linux\\n' ;; Darwin) printf 'darwin\\n' ;; *) printf '%s\\n' \"$line\" ;; esac; done\n",
		"uname": "#!/bin/sh\ncase \"$1\" in -m) printf 'x86_64\\n' ;; *) printf 'Linux\\n' ;; esac\n",
	}
	for name, script := range extra {
		stubs[name] = script
	}
	for name, script := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
	}
}

func TestShellUninstallUsesGroundskeeperPathsAndMarkers(t *testing.T) {
	cmd := exec.Command("bash", "-n", "../../uninstall.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("uninstall.sh has invalid syntax: %v\n%s", err, out)
	}

	body, err := os.ReadFile("../../uninstall.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"https://github.com/potato-hash/groundskeeper",
		"raw.githubusercontent.com/potato-hash/groundskeeper",
		"Keep Groundskeeper XDG state and legacy pre-XDG data",
		"Data directory (gk.db and managed Espalier checkout)",
		"Legacy pre-XDG data directory",
		"$(xdg_path XDG_DATA_HOME .local/share)",
		"prompt_read()",
		"</dev/tty",
		`[[ -z "${HOME:-}" || "${HOME:0:1}" != "/" ]]`,
		`[[ -z "$base" || "${base:0:1}" != "/" ]]`,
		"prompt_read -n 1 -r",
		"# Groundskeeper configuration",
		"# End Groundskeeper configuration",
		"groundskeeper-backup-",
		"${#TAR_ARGS[@]} -gt 4",
		".tmux.conf.bak.groundskeeper-uninstall",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("uninstall.sh missing %q", want)
		}
	}
	if strings.Contains(script, "read -p") {
		t.Fatal("uninstall.sh must not use bare read -p; prompts need prompt_read for curl|bash use")
	}
	for _, stale := range []string{
		"asheshgoplani/groundskeeper",
		"~/.groundskeeper",
		".tmux.conf.bak.agentdeck-uninstall",
		"# groundskeeper configuration",
		"# End groundskeeper configuration",
	} {
		if strings.Contains(script, stale) {
			t.Fatalf("uninstall.sh still contains stale text %q", stale)
		}
	}
}

func TestShellUninstallRejectsMissingHome(t *testing.T) {
	bashPath := testBashPath(t)
	cmd := exec.Command(bashPath, "../../uninstall.sh", "--dry-run")
	cmd.Env = []string{
		"HOME=",
		"PATH=/usr/bin:/bin",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("uninstall.sh unexpectedly succeeded without HOME\n%s", out)
	}
	if !strings.Contains(string(out), "cannot resolve an absolute HOME") {
		t.Fatalf("uninstall.sh missing HOME guard error\n--- output ---\n%s", out)
	}
}

func TestShellUninstallIgnoresRelativeXDGHome(t *testing.T) {
	bashPath := testBashPath(t)
	home := t.TempDir()
	cmd := exec.Command(bashPath, "../../uninstall.sh", "--dry-run")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"XDG_CONFIG_HOME=relative-config",
		"XDG_DATA_HOME=relative-data",
		"XDG_CACHE_HOME=relative-cache",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall.sh dry-run failed: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		filepath.Join(home, ".config", "groundskeeper"),
		filepath.Join(home, ".local", "share", "groundskeeper"),
		filepath.Join(home, ".cache", "groundskeeper"),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("uninstall.sh missing fallback path %q\n--- output ---\n%s", want, body)
		}
	}
	for _, forbidden := range []string{
		"relative-config/groundskeeper",
		"relative-data/groundskeeper",
		"relative-cache/groundskeeper",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("uninstall.sh used relative XDG path %q\n--- output ---\n%s", forbidden, body)
		}
	}
}

func TestShellUninstallDryRunAndRemovalUseCurrentDataLabels(t *testing.T) {
	bashPath := testBashPath(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdgConfig := filepath.Join(root, "xdg-config")
	xdgData := filepath.Join(root, "xdg-data")
	xdgCache := filepath.Join(root, "xdg-cache")
	locations := []string{
		filepath.Join(xdgConfig, "groundskeeper"),
		filepath.Join(xdgData, "groundskeeper"),
		filepath.Join(xdgCache, "groundskeeper"),
		filepath.Join(home, ".agent-deck"),
	}
	for _, dir := range locations {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	env := []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_CONFIG_HOME=" + xdgConfig,
		"XDG_DATA_HOME=" + xdgData,
		"XDG_CACHE_HOME=" + xdgCache,
	}

	dryRun := exec.Command(bashPath, "../../uninstall.sh", "--dry-run", "--keep-tmux-config")
	dryRun.Env = env
	out, err := dryRun.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall.sh dry-run failed: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"Config directory",
		"Data directory (gk.db and managed Espalier checkout)",
		"Cache directory",
		"Legacy pre-XDG data directory",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("uninstall.sh dry-run missing label %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "legacy directory:") {
		t.Fatalf("uninstall.sh dry-run still uses generic legacy label\n--- output ---\n%s", body)
	}
	for _, dir := range locations {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("dry-run should preserve %s: %v", dir, err)
		}
	}

	remove := exec.Command(bashPath, "../../uninstall.sh", "--non-interactive", "--keep-tmux-config")
	remove.Env = env
	if out, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("uninstall.sh non-interactive removal failed: %v\n%s", err, out)
	}
	for _, dir := range locations {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("uninstall.sh should remove %s, stat err=%v", dir, err)
		}
	}
}

func TestInstallStateVerifierScansWithoutPrintingSecretValues(t *testing.T) {
	cmd := exec.Command("bash", "-n", "../../scripts/verify-install-state.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify-install-state.sh has invalid syntax: %v\n%s", err, out)
	}

	body, err := os.ReadFile("../../scripts/verify-install-state.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"raw.githubusercontent.com/potato-hash/groundskeeper/main/scripts/verify-install-state.sh",
		"find_executable",
		"$HOME/.local/bin/groundskeeper",
		"$HOME/.local/bin/omp",
		"$ESPALIER_ROOT/package.json",
		"dist/extensions/index.js",
		"$data_dir/gk.db",
		"$HOME/.omp",
		`[[ -z "$base" || "${base:0:1}" != "/" ]]`,
		"add_scan_dir",
		`add_scan_dir "$(dirname "$gk_bin")"`,
		`add_scan_dir "$(dirname "$omp_bin")"`,
		"grep -lF -- \"$value\"",
		"sensitive value from $name persisted in $hit",
		"Summary:",
		"Next commands:",
		"Remediation:",
		"groundskeeper setup --install-missing --model",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("verify-install-state.sh missing %q", want)
		}
	}
	if strings.Contains(script, "persisted value") {
		t.Fatal("verify-install-state.sh appears to describe printing persisted secret values")
	}
}

func TestInstallStateVerifierPrintsSummaryWithoutSecretValues(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	data := filepath.Join(home, "data")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	espalier := filepath.Join(data, "groundskeeper", "espalier")
	for _, dir := range []string{
		bin,
		filepath.Join(data, "groundskeeper"),
		filepath.Join(config, "groundskeeper"),
		filepath.Join(cache, "groundskeeper"),
		filepath.Join(espalier, "node_modules"),
		filepath.Join(espalier, "dist", "extensions"),
		filepath.Join(home, ".omp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"groundskeeper", "omp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "groundskeeper", "gk.db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(espalier, "package.json"), []byte(`{"name":"espalier"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(espalier, "dist", "extensions", "index.js"), []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/verify-install-state.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_DATA_HOME=" + data,
		"XDG_CONFIG_HOME=" + config,
		"XDG_CACHE_HOME=" + cache,
		"GK_SMOKE_MODEL=test/model",
		"OLLAMA_CLOUD_API_KEY=summary-fixture-secret",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify-install-state.sh failed: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"Summary:",
		"groundskeeper: " + filepath.Join(bin, "groundskeeper"),
		"omp: " + filepath.Join(bin, "omp"),
		"Espalier extension: " + filepath.Join(espalier, "dist", "extensions", "index.js"),
		"Espalier package manifest: " + filepath.Join(espalier, "package.json"),
		"Groundskeeper DB: " + filepath.Join(data, "groundskeeper", "gk.db"),
		"Secret scan roots:",
		bin,
		"Next commands:",
		"groundskeeper gk-daemon --model test/model --slots 2 --espalier-path",
		"Install-state verification passed.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("verify output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "summary-fixture-secret") {
		t.Fatalf("verify output printed a secret value\n--- output ---\n%s", body)
	}
}

func TestInstallStateVerifierScansBinaryStateFiles(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	data := filepath.Join(home, "data")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	espalier := filepath.Join(data, "groundskeeper", "espalier")
	ompAgent := filepath.Join(home, ".omp", "agent")
	sensitiveName := "OLLAMA_CLOUD_" + "API" + "_KEY"
	for _, dir := range []string{
		bin,
		filepath.Join(data, "groundskeeper"),
		filepath.Join(config, "groundskeeper"),
		filepath.Join(cache, "groundskeeper"),
		filepath.Join(espalier, "node_modules"),
		filepath.Join(espalier, "dist", "extensions"),
		ompAgent,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"groundskeeper", "omp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string][]byte{
		filepath.Join(data, "groundskeeper", "gk.db"):             []byte("db"),
		filepath.Join(espalier, "package.json"):                   []byte(`{"name":"espalier"}` + "\n"),
		filepath.Join(espalier, "dist", "extensions", "index.js"): []byte("export default {}\n"),
		filepath.Join(ompAgent, "agent.db"):                       []byte("sqlite\x00binary-state-sentinel-value\x00tail"),
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "../../scripts/verify-install-state.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_DATA_HOME=" + data,
		"XDG_CONFIG_HOME=" + config,
		"XDG_CACHE_HOME=" + cache,
		sensitiveName + "=binary-state-sentinel-value",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verify-install-state.sh unexpectedly passed with secret persisted in binary state\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "sensitive value from "+sensitiveName+" persisted in "+filepath.Join(ompAgent, "agent.db")) {
		t.Fatalf("verify output missing binary persistence failure\n--- output ---\n%s", body)
	}
	if strings.Contains(body, "binary-state-sentinel-value") {
		t.Fatalf("verify output printed a secret value\n--- output ---\n%s", body)
	}
}

func TestInstallStateVerifierScansInstallDirs(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	data := filepath.Join(home, "data")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	espalier := filepath.Join(data, "groundskeeper", "espalier")
	secretName := "INSTALL_SCAN_" + "API" + "_KEY"
	secretValue := "install-dir-secret-value"
	for _, dir := range []string{
		bin,
		filepath.Join(data, "groundskeeper"),
		filepath.Join(config, "groundskeeper"),
		filepath.Join(cache, "groundskeeper"),
		filepath.Join(espalier, "node_modules"),
		filepath.Join(espalier, "dist", "extensions"),
		filepath.Join(home, ".omp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "groundskeeper"), []byte("#!/usr/bin/env sh\n# "+secretValue+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "omp"), []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{
		filepath.Join(data, "groundskeeper", "gk.db"):             []byte("db"),
		filepath.Join(espalier, "package.json"):                   []byte(`{"name":"espalier"}` + "\n"),
		filepath.Join(espalier, "dist", "extensions", "index.js"): []byte("export default {}\n"),
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "../../scripts/verify-install-state.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_DATA_HOME=" + data,
		"XDG_CONFIG_HOME=" + config,
		"XDG_CACHE_HOME=" + cache,
		secretName + "=" + secretValue,
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verify-install-state.sh unexpectedly passed with secret persisted in install dir\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "sensitive value from "+secretName+" persisted in "+filepath.Join(bin, "groundskeeper")) {
		t.Fatalf("verify output missing install-dir persistence failure\n--- output ---\n%s", body)
	}
	if strings.Contains(body, secretValue) {
		t.Fatalf("verify output printed a secret value\n--- output ---\n%s", body)
	}
}

func TestInstallStateVerifierRequiresEspalierPackageManifest(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	data := filepath.Join(home, "data")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	espalier := filepath.Join(data, "groundskeeper", "espalier")
	for _, dir := range []string{
		bin,
		filepath.Join(data, "groundskeeper"),
		filepath.Join(config, "groundskeeper"),
		filepath.Join(cache, "groundskeeper"),
		filepath.Join(espalier, "node_modules"),
		filepath.Join(espalier, "dist", "extensions"),
		filepath.Join(home, ".omp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"groundskeeper", "omp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "groundskeeper", "gk.db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(espalier, "dist", "extensions", "index.js"), []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/verify-install-state.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_DATA_HOME=" + data,
		"XDG_CONFIG_HOME=" + config,
		"XDG_CACHE_HOME=" + cache,
		"GK_SMOKE_MODEL=test/model",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verify-install-state.sh unexpectedly passed without Espalier package.json\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "Espalier package manifest missing: "+filepath.Join(espalier, "package.json")) {
		t.Fatalf("verify output missing package manifest failure\n--- output ---\n%s", body)
	}
	if !strings.Contains(body, "Install-state verification failed with 1 issue(s).") {
		t.Fatalf("verify output missing failure summary\n--- output ---\n%s", body)
	}
}

func TestPublicInstallSmokeScriptRejectsLeakedSecretOutput(t *testing.T) {
	cmd := exec.Command("bash", "-n", "../../scripts/smoke-public-install.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("smoke-public-install.sh has invalid syntax: %v\n%s", err, out)
	}

	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf '%s\\n' \"$OLLAMA_CLOUD_API_KEY\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=k9xQz",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+filepath.Join(home, "verify-unused.sh"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly succeeded when installer output leaked a secret\n%s", out)
	}
	body := string(out)
	if strings.Contains(body, "k9xQz") {
		t.Fatalf("smoke-public-install.sh printed leaked secret\n--- output ---\n%s", body)
	}
	if !strings.Contains(body, "sensitive value from OLLAMA_CLOUD_API_KEY appeared in install output") {
		t.Fatalf("smoke-public-install.sh missing leak failure detail\n--- output ---\n%s", body)
	}
}

func TestPublicInstallSmokeScriptRejectsLeakedVerifierOutput(t *testing.T) {
	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	sensitiveName := "SMOKE_" + "API" + "_KEY"
	sensitiveValue := "smoke-fixture-value"
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install clean\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	verifyBody := fmt.Sprintf("#!/usr/bin/env sh\nprintenv %s\n", sensitiveName)
	if err := os.WriteFile(verifyStub, []byte(verifyBody), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		sensitiveName+"="+sensitiveValue,
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_VERIFY_MODEL=0",
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly succeeded when verifier output leaked a secret\n%s", out)
	}
	body := string(out)
	if strings.Contains(body, sensitiveValue) {
		t.Fatalf("smoke-public-install.sh printed leaked verifier secret\n--- output ---\n%s", body)
	}
	if !strings.Contains(body, "sensitive value from "+sensitiveName+" appeared in verifier output") {
		t.Fatalf("smoke-public-install.sh missing verifier leak failure detail\n--- output ---\n%s", body)
	}
}

func TestPublicInstallSmokeScriptRunsVerifierAfterCleanInstall(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	argsPath := filepath.Join(home, "install-args.txt")
	espalierPathLog := filepath.Join(home, "espalier-path.txt")
	bunInstallLog := filepath.Join(home, "bun-install.txt")
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	installBody := `#!/usr/bin/env sh
printf '%s\n' "$@" > "$HOME/install-args.txt"
printf '%s\n' "$GK_ESPALIER_PATH" > "$HOME/espalier-path.txt"
printf '%s\n' "$BUN_INSTALL" > "$HOME/bun-install.txt"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf 'install clean\n'
`
	if err := os.WriteFile(installStub, []byte(installBody), 0o755); err != nil {
		t.Fatal(err)
	}
	verifyBody := "#!/usr/bin/env sh\nfound=$(command -v groundskeeper) || exit 7\n[ \"$found\" = \"$GK_SMOKE_INSTALL_DIR/groundskeeper\" ] || exit 8\nprintf 'verify clean\\n'\nprintf '[OK] secret persistence scan passed across 3 dirs\\n'\n"
	if err := os.WriteFile(verifyStub, []byte(verifyBody), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"XDG_DATA_HOME=",
		"GK_ESPALIER_PATH=",
		"GK_SMOKE_BUN_INSTALL=",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_VERIFY_MODEL=0",
		"GK_SMOKE_INSTALL_DIR="+installDir,
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-public-install.sh failed on clean stubs: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"install clean",
		"verify clean",
		"installer output did not contain sensitive environment values",
		"install-state verifier reported secret persistence scan evidence",
		"public install smoke completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("smoke output missing %q\n--- output ---\n%s", want, body)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--dir\n"+installDir+"\n") {
		t.Fatalf("smoke installer args missing custom --dir %q\n--- args ---\n%s", installDir, args)
	}
	espalierPath, err := os.ReadFile(espalierPathLog)
	if err != nil {
		t.Fatal(err)
	}
	wantEspalierPath := filepath.Join(home, ".local", "share", "groundskeeper", "espalier")
	if strings.TrimSpace(string(espalierPath)) != wantEspalierPath {
		t.Fatalf("smoke did not default GK_ESPALIER_PATH to managed data dir: got %q want %q", strings.TrimSpace(string(espalierPath)), wantEspalierPath)
	}
	bunInstall, err := os.ReadFile(bunInstallLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(bunInstall)) != filepath.Join(home, ".bun") {
		t.Fatalf("smoke did not default BUN_INSTALL under HOME: got %q", strings.TrimSpace(string(bunInstall)))
	}
}

func TestPublicInstallSmokeScriptRequiresVerifierSecretScanEvidence(t *testing.T) {
	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install clean\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify without evidence\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_VERIFY_MODEL=0",
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly succeeded without verifier scan evidence\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "install-state verifier did not report secret persistence scan evidence") {
		t.Fatalf("smoke output missing verifier evidence failure\n--- output ---\n%s", body)
	}
	if strings.Contains(body, "public install smoke completed") {
		t.Fatalf("smoke reported completion after missing verifier evidence\n--- output ---\n%s", body)
	}
}

func TestPublicInstallSmokeScriptRejectsSecretBearingUntrustedOverrides(t *testing.T) {
	home := t.TempDir()
	curlDir := filepath.Join(home, "curl-bin")
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	if err := os.MkdirAll(curlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(curlDir, "curl"), []byte("#!/usr/bin/env sh\nprintf 'curl should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		env  []string
	}{
		{name: "repo override", env: []string{"GK_SMOKE_REPO=attacker/groundskeeper"}},
		{name: "ref override", env: []string{"GK_SMOKE_REF=feature"}},
		{name: "install URL override", env: []string{"GK_SMOKE_INSTALL_URL=file://" + installStub}},
		{name: "verify URL override", env: []string{"GK_SMOKE_VERIFY_URL=file://" + verifyStub}},
		{name: "verification disabled but credentials present", env: []string{
			"GK_SMOKE_VERIFY_MODEL=0",
			"GK_SMOKE_INSTALL_URL=file://" + installStub,
			"GK_SMOKE_VERIFY_URL=file://" + verifyStub,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
			cmd.Env = append(os.Environ(),
				"HOME="+home,
				"PATH="+curlDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"OLLAMA_CLOUD_API_KEY=smoke-fixture-value",
				"GK_SMOKE_ALLOW_UNTRUSTED=",
			)
			cmd.Env = append(cmd.Env, tc.env...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("smoke-public-install.sh unexpectedly allowed untrusted secret-bearing source\n%s", out)
			}
			body := string(out)
			if !strings.Contains(body, "secret-bearing public smoke only runs trusted Groundskeeper scripts") {
				t.Fatalf("smoke output missing untrusted-source failure\n--- output ---\n%s", body)
			}
			for _, leaked := range []string{"smoke-fixture-value", "install should not run", "verify should not run", "curl should not run"} {
				if strings.Contains(body, leaked) {
					t.Fatalf("smoke output leaked or executed %q\n--- output ---\n%s", leaked, body)
				}
			}
		})
	}
}

func TestPublicInstallSmokeScriptRejectsUntrustedOverridesWithUnrelatedSecret(t *testing.T) {
	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	secretName := "SMOKE_SECRET_TOKEN"
	secretValue := "unrelated-fixture-secret"
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		secretName+"="+secretValue,
		"GK_SMOKE_VERIFY_MODEL=0",
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
		"GK_SMOKE_ALLOW_UNTRUSTED=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly allowed untrusted overrides with unrelated secret\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "secret-bearing public smoke only runs trusted Groundskeeper scripts") {
		t.Fatalf("smoke output missing untrusted-source failure\n--- output ---\n%s", body)
	}
	for _, leaked := range []string{secretValue, "install should not run", "verify should not run"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("smoke output leaked or executed %q\n--- output ---\n%s", leaked, body)
		}
	}
}

func TestPublicInstallSmokeScriptRejectsUntrustedOverridesWithOmpCredentialStore(t *testing.T) {
	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	agentDir := filepath.Join(home, ".omp", "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent.db"), []byte("sqlite fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_VERIFY_MODEL=0",
		"GK_SMOKE_INSTALL_URL=file://" + installStub,
		"GK_SMOKE_VERIFY_URL=file://" + verifyStub,
		"GK_SMOKE_ALLOW_UNTRUSTED=",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly allowed untrusted overrides with OMP credential store\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "secret-bearing public smoke only runs trusted Groundskeeper scripts") {
		t.Fatalf("smoke output missing untrusted-source failure\n--- output ---\n%s", body)
	}
	for _, leaked := range []string{"install should not run", "verify should not run"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("smoke output executed %q\n--- output ---\n%s", leaked, body)
		}
	}
}

func TestPublicInstallSmokeScriptRejectsOllamaCloudVerificationFromUntrustedOverridesWithoutCredentials(t *testing.T) {
	home := t.TempDir()
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	if err := os.WriteFile(installStub, []byte("#!/usr/bin/env sh\nprintf 'install should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=",
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly allowed ollama-cloud verification from local overrides\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "secret-bearing public smoke only runs trusted Groundskeeper scripts") {
		t.Fatalf("smoke output missing untrusted-source failure\n--- output ---\n%s", body)
	}
	for _, leaked := range []string{"install should not run", "verify should not run"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("smoke output executed %q\n--- output ---\n%s", leaked, body)
		}
	}
}

func TestInstallStateVerifierFindsOmpUnderBunInstall(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	bunInstall := filepath.Join(home, "custom-bun")
	data := filepath.Join(home, "data")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	espalier := filepath.Join(data, "groundskeeper", "espalier")
	for _, dir := range []string{
		bin,
		filepath.Join(bunInstall, "bin"),
		filepath.Join(data, "groundskeeper"),
		filepath.Join(config, "groundskeeper"),
		filepath.Join(cache, "groundskeeper"),
		filepath.Join(espalier, "node_modules"),
		filepath.Join(espalier, "dist", "extensions"),
		filepath.Join(home, ".omp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "groundskeeper"), []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ompPath := filepath.Join(bunInstall, "bin", "omp")
	if err := os.WriteFile(ompPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{
		filepath.Join(data, "groundskeeper", "gk.db"):             []byte("db"),
		filepath.Join(espalier, "package.json"):                   []byte(`{"name":"espalier"}` + "\n"),
		filepath.Join(espalier, "dist", "extensions", "index.js"): []byte("export default {}\n"),
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "../../scripts/verify-install-state.sh")
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"BUN_INSTALL=" + bunInstall,
		"XDG_DATA_HOME=" + data,
		"XDG_CONFIG_HOME=" + config,
		"XDG_CACHE_HOME=" + cache,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify-install-state.sh failed with omp under BUN_INSTALL: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"omp: " + ompPath,
		filepath.Join(bunInstall, "bin"),
		"Install-state verification passed.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("verify output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

func TestPublicInstallSmokeScriptRequiresModelVerificationMarker(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	installBody := `#!/usr/bin/env sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf 'install skipped marker\n'
`
	if err := os.WriteFile(installStub, []byte(installBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify should not run\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_MODEL=test/provider",
		"GK_SMOKE_INSTALL_DIR="+installDir,
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke-public-install.sh unexpectedly succeeded without model verification marker\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "model verification was requested") {
		t.Fatalf("smoke output missing model verification failure\n--- output ---\n%s", body)
	}
	if strings.Contains(body, "verify should not run") {
		t.Fatalf("smoke ran verifier after missing model verification marker\n--- output ---\n%s", body)
	}
}

func TestPublicInstallSmokeScriptAcceptsModelVerificationMarker(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	argsPath := filepath.Join(home, "install-args.txt")
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	installBody := `#!/usr/bin/env sh
printf '%s\n' "$@" > "$HOME/install-args.txt"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf '[OK] OMP model smoke test passed\n'
`
	if err := os.WriteFile(installStub, []byte(installBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify after model\\n'\nprintf '[OK] secret persistence scan passed across 3 dirs\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_MODEL=test/provider",
		"GK_SMOKE_INSTALL_DIR="+installDir,
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-public-install.sh failed despite model verification marker: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"[OK] OMP model smoke test passed",
		"verify after model",
		"public install smoke completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("smoke output missing %q\n--- output ---\n%s", want, body)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--verify-model\n") {
		t.Fatalf("smoke installer args missing --verify-model\n--- args ---\n%s", args)
	}
}

func TestPublicInstallSmokeScriptAllowsAuthBrokerForOllamaCloudSmoke(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	authBrokerName := "OMP_AUTH_BROKER_" + "TO" + "KEN"
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	installBody := `#!/usr/bin/env sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf '[OK] OMP model smoke test passed\n'
`
	if err := os.WriteFile(installStub, []byte(installBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify broker\\n'\nprintf '[OK] secret persistence scan passed across 3 dirs\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		authBrokerName+"=broker-fixture-value",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_INSTALL_DIR="+installDir,
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-public-install.sh rejected broker auth for ollama-cloud smoke: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"[OK] OMP model smoke test passed",
		"verify broker",
		"public install smoke completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("smoke output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

func TestPublicInstallSmokeScriptAllowsOmpCredentialStoreForOllamaCloudSmoke(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	installStub := filepath.Join(home, "install.sh")
	verifyStub := filepath.Join(home, "verify.sh")
	if err := os.MkdirAll(filepath.Join(home, ".omp", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".omp", "agent", "agent.db"), []byte("sqlite fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installBody := `#!/usr/bin/env sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf '[OK] OMP model smoke test passed\n'
`
	if err := os.WriteFile(installStub, []byte(installBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifyStub, []byte("#!/usr/bin/env sh\nprintf 'verify store\\n'\nprintf '[OK] secret persistence scan passed across 3 dirs\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"OLLAMA_CLOUD_API_KEY=",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=1",
		"GK_SMOKE_INSTALL_DIR="+installDir,
		"GK_SMOKE_INSTALL_URL=file://"+installStub,
		"GK_SMOKE_VERIFY_URL=file://"+verifyStub,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-public-install.sh rejected OMP credential store for ollama-cloud smoke: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"[OK] OMP model smoke test passed",
		"verify store",
		"public install smoke completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("smoke output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

func TestPublicInstallSmokeScriptCanFetchThroughGitHubContentsAPI(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	ref := "trusted-sha-fixture"
	curlLog := filepath.Join(home, "curl-args.txt")
	curlDir := filepath.Join(home, "curl-bin")
	if err := os.MkdirAll(curlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	curlStub := `#!/usr/bin/env sh
for arg in "$@"; do
  printf '%s\n' "$arg" >> "$HOME/curl-args.txt"
  url="$arg"
done
case "$url" in
  *install.sh*)
    cat <<'INSTALL'
#!/usr/bin/env sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--dir" ]; then
    mkdir -p "$2"
    printf '#!/usr/bin/env sh\nprintf groundskeeper\n' > "$2/groundskeeper"
    chmod +x "$2/groundskeeper"
    break
  fi
  shift
done
printf 'install via api\n'
printf '[OK] OMP model smoke test passed\n'
INSTALL
    ;;
  *verify-install-state.sh*)
    cat <<'VERIFY'
#!/usr/bin/env sh
command -v groundskeeper >/dev/null || exit 7
printf 'verify via api\n'
printf '[OK]   secret persistence scan passed across 3 dirs\n'
VERIFY
    ;;
  *)
    exit 22
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(curlDir, "curl"), []byte(curlStub), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "../../scripts/smoke-public-install.sh")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+curlDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_ACTIONS=true",
		"GITHUB_REF_NAME=main",
		"GITHUB_REPOSITORY=potato-hash/groundskeeper",
		"GITHUB_SHA="+ref,
		"GK_SMOKE_REPO=potato-hash/groundskeeper",
		"GK_SMOKE_REF="+ref,
		"GK_SMOKE_INSTALL_URL=",
		"GK_SMOKE_VERIFY_URL=",
		"GK_SMOKE_USE_API_RAW=1",
		"GITHUB_TOKEN=github-token-fixture",
		"OLLAMA_CLOUD_API_KEY=smoke-fixture-value",
		"OLLAMA_API_KEY=",
		"OMP_AUTH_BROKER_TOKEN=",
		"GK_SMOKE_ALLOW_UNTRUSTED=",
		"GK_SMOKE_MODEL=test/provider",
		"GK_SMOKE_INSTALL_DIR="+installDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-public-install.sh failed with API raw stubs: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"https://api.github.com/repos/potato-hash/groundskeeper/contents/install.sh?ref=" + ref,
		"https://api.github.com/repos/potato-hash/groundskeeper/contents/scripts/verify-install-state.sh?ref=" + ref,
		"install via api",
		"[OK] OMP model smoke test passed",
		"verify via api",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("smoke output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "smoke-fixture-value") {
		t.Fatalf("smoke output printed credential value\n--- output ---\n%s", body)
	}
	args, err := os.ReadFile(curlLog)
	if err != nil {
		t.Fatal(err)
	}
	argLog := string(args)
	if got := strings.Count(argLog, "Accept: application/vnd.github.raw"); got != 2 {
		t.Fatalf("curl args should include GitHub raw Accept header twice, got %d\n--- args ---\n%s", got, argLog)
	}
	if got := strings.Count(argLog, "Authorization: Bearer github-token-fixture"); got != 2 {
		t.Fatalf("curl args should include GitHub token Authorization header twice, got %d\n--- args ---\n%s", got, argLog)
	}
	for _, want := range []string{
		"https://api.github.com/repos/potato-hash/groundskeeper/contents/install.sh?ref=" + ref,
		"https://api.github.com/repos/potato-hash/groundskeeper/contents/scripts/verify-install-state.sh?ref=" + ref,
	} {
		if !strings.Contains(argLog, want) {
			t.Fatalf("curl args missing %q\n--- args ---\n%s", want, argLog)
		}
	}
}

func TestSetupCommandEnvAliasesOllamaAPIKeyForOllamaCloud(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "temporary-test-key")
	t.Setenv("OLLAMA_CLOUD_API_KEY", "")

	env := setupCommandEnv("ollama-cloud/glm-5.2")
	if !containsEnv(env, "OLLAMA_CLOUD_API_KEY=temporary-test-key") {
		t.Fatalf("setupCommandEnv did not alias OLLAMA_API_KEY for ollama-cloud model: %#v", env)
	}
}

func TestSetupCommandEnvPassesOnlySelectedProviderCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "provider-key")
	t.Setenv("GITHUB_TOKEN", "should-not-pass")

	env := setupCommandEnv("openai/gpt-5.2")
	if !containsEnv(env, "OPENAI_API_KEY=provider-key") {
		t.Fatalf("setupCommandEnv did not pass selected provider credential: %#v", env)
	}
	if containsEnv(env, "GITHUB_TOKEN=should-not-pass") {
		t.Fatalf("setupCommandEnv leaked unrelated token env: %#v", env)
	}
}

func TestRedactedCommandOutputHidesProviderKeys(t *testing.T) {
	t.Setenv("OLLAMA_CLOUD_API_KEY", "temporary-test-key")
	t.Setenv("GITHUB_TOKEN", "github-token-value")

	env := []string{
		"OLLAMA_CLOUD_API_KEY=temporary-test-key",
		"GITHUB_TOKEN=github-token-value",
	}
	got := redactedCommandOutput([]byte("failed with temporary-test-key and github-token-value"), env)
	if strings.Contains(got, "temporary-test-key") || strings.Contains(got, "github-token-value") {
		t.Fatalf("redactedCommandOutput leaked provider key: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedCommandOutput missing redaction marker: %q", got)
	}
}

func TestVerifyOmpModelRefreshesAndPrompts(t *testing.T) {
	ompPath, callLog := writeOmpModelStub(t, `#!/usr/bin/env sh
printf '%s\n' "$*" >> "$GK_OMP_CALL_LOG"
if [ "$1" = "models" ] && [ "$2" = "refresh" ]; then
  [ "$(printenv "$GK_EXPECTED_CRED_ENV")" = "$GK_EXPECTED_CRED_VALUE" ] || exit 41
  exit 0
fi
if [ "$1" = "--model" ] && [ "$2" = "test-provider/model" ] && [ "$3" = "--no-session" ] && [ "$4" = "--max-time=60" ] && [ "$5" = "-p" ]; then
  printf 'GK_OK\n'
  exit 0
fi
exit 42
`)
	configureVerifyOmpModelEnv(t, callLog, "test-provider/model", "verify-model-sentinel")

	if err := verifyOmpModel(ompPath, "test-provider/model"); err != nil {
		t.Fatalf("verifyOmpModel failed: %v", err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	body := string(calls)
	for _, want := range []string{
		"models refresh\n",
		"--model test-provider/model --no-session --max-time=60 -p Reply exactly: GK_OK\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("verifyOmpModel call log missing %q\n--- calls ---\n%s", want, body)
		}
	}
}

func TestVerifyOmpModelRedactsRefreshFailure(t *testing.T) {
	ompPath, callLog := writeOmpModelStub(t, `#!/usr/bin/env sh
printf '%s\n' "$*" >> "$GK_OMP_CALL_LOG"
if [ "$1" = "models" ] && [ "$2" = "refresh" ]; then
  printf 'refresh failed with %s\n' "$(printenv "$GK_EXPECTED_CRED_ENV")"
  exit 17
fi
exit 42
`)
	value := "refresh-failure-sentinel"
	configureVerifyOmpModelEnv(t, callLog, "test-provider/model", value)

	err := verifyOmpModel(ompPath, "test-provider/model")
	if err == nil {
		t.Fatal("verifyOmpModel unexpectedly passed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "refresh OMP model catalog") {
		t.Fatalf("verifyOmpModel error missing refresh context: %q", msg)
	}
	if strings.Contains(msg, value) {
		t.Fatalf("verifyOmpModel leaked credential value: %q", msg)
	}
	if !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("verifyOmpModel error missing redaction marker: %q", msg)
	}
}

func TestVerifyOmpModelRedactsUnexpectedResponse(t *testing.T) {
	ompPath, callLog := writeOmpModelStub(t, `#!/usr/bin/env sh
printf '%s\n' "$*" >> "$GK_OMP_CALL_LOG"
if [ "$1" = "models" ] && [ "$2" = "refresh" ]; then
  exit 0
fi
printf 'unexpected response with %s\n' "$(printenv "$GK_EXPECTED_CRED_ENV")"
exit 0
`)
	value := "unexpected-response-sentinel"
	configureVerifyOmpModelEnv(t, callLog, "test-provider/model", value)

	err := verifyOmpModel(ompPath, "test-provider/model")
	if err == nil {
		t.Fatal("verifyOmpModel unexpectedly passed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unexpected OMP smoke response") {
		t.Fatalf("verifyOmpModel error missing unexpected-response context: %q", msg)
	}
	if strings.Contains(msg, value) {
		t.Fatalf("verifyOmpModel leaked credential value: %q", msg)
	}
	if !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("verifyOmpModel error missing redaction marker: %q", msg)
	}
}

func TestBuildEspalierRedactsStreamingSubprocessOutput(t *testing.T) {
	espalier := t.TempDir()
	if err := os.WriteFile(filepath.Join(espalier, "package.json"), []byte(`{"name":"espalier"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	leaked := "stream-redaction-fixture-value"
	t.Setenv("OPENAI_API_KEY", leaked)
	prependStubTool(t, "bun", "#!/usr/bin/env sh\nif [ \"$1\" = \"install\" ]; then printf 'install saw "+leaked+"\\n'; exit 0; fi\nif [ \"$1\" = \"run\" ] && [ \"$2\" = \"build\" ]; then mkdir -p dist/extensions; printf 'export default {}\\n' > dist/extensions/index.js; printf 'build saw "+leaked+"\\n'; exit 0; fi\nexit 1\n")

	out, err := captureStdoutStderr(t, func() error {
		return buildEspalier(espalier)
	})
	if err != nil {
		t.Fatalf("buildEspalier failed: %v\n%s", err, out)
	}
	if strings.Contains(out, leaked) {
		t.Fatalf("buildEspalier streamed a sensitive env value\n--- output ---\n%s", out)
	}
	if strings.Count(out, "[REDACTED]") < 2 {
		t.Fatalf("buildEspalier output missing redaction markers\n--- output ---\n%s", out)
	}
}

func TestBuildEspalierRequiresExtensionEntrypoint(t *testing.T) {
	espalier := t.TempDir()
	if err := os.WriteFile(filepath.Join(espalier, "package.json"), []byte(`{"name":"espalier"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prependStubTool(t, "bun", "#!/usr/bin/env sh\nif [ \"$1\" = \"install\" ]; then exit 0; fi\nif [ \"$1\" = \"run\" ] && [ \"$2\" = \"build\" ]; then exit 0; fi\nexit 1\n")

	err := buildEspalier(espalier)
	if err == nil {
		t.Fatal("buildEspalier unexpectedly succeeded without dist/extensions/index.js")
	}
	want := "Espalier build did not create extension entrypoint: " + filepath.Join(espalier, "dist", "extensions", "index.js")
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("buildEspalier error = %q, want %q", err, want)
	}
}

func captureStdoutStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	oldOut := os.Stdout
	oldErr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	os.Stdout = w
	os.Stderr = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	out := <-done
	_ = r.Close()
	return out, runErr
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func writeOmpModelStub(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "omp")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(dir, "calls.log")
}

func configureVerifyOmpModelEnv(t *testing.T, callLog, model, value string) {
	t.Helper()
	names := providerCredentialEnvNames(model)
	if len(names) == 0 {
		t.Fatalf("model %q produced no credential env names", model)
	}
	name := names[0]
	t.Setenv(name, value)
	t.Setenv("GK_OMP_CALL_LOG", callLog)
	t.Setenv("GK_EXPECTED_CRED_ENV", name)
	t.Setenv("GK_EXPECTED_CRED_VALUE", value)
}

func prependStubOMP(t *testing.T) {
	t.Helper()
	prependStubTool(t, "omp", "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'omp test'; exit 0; fi\nexit 0\n")
}

func prependStubGitAndBun(t *testing.T) {
	t.Helper()
	prependStubTool(t, "git", "#!/usr/bin/env sh\nif [ \"$1\" = \"clone\" ]; then mkdir -p \"$3\"; printf '{\"scripts\":{\"build\":\"bun build\"}}\\n' > \"$3/package.json\"; exit 0; fi\nexit 1\n")
	prependStubTool(t, "bun", "#!/usr/bin/env sh\nif [ \"$1\" = \"install\" ]; then exit 0; fi\nif [ \"$1\" = \"run\" ] && [ \"$2\" = \"build\" ]; then mkdir -p dist/extensions; printf 'export default function() {}\\n' > dist/extensions/index.js; exit 0; fi\nexit 1\n")
}

func prependStubTool(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestWriteRecommendedOmpConfigCreatesGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, backup, changed, err := writeRecommendedOmpConfig("test/provider", "mnemopi", "")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("writeRecommendedOmpConfig reported unchanged on fresh config")
	}
	if backup != "" {
		t.Fatalf("fresh config backup = %q, want empty", backup)
	}
	if path != filepath.Join(home, ".omp", "agent", "config.yml") {
		t.Fatalf("config path = %q", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["memory"].(map[string]any)["backend"] != "mnemopi" {
		t.Fatalf("memory.backend = %#v", cfg["memory"])
	}
	if cfg["modelRoles"].(map[string]any)["default"] != "test/provider" {
		t.Fatalf("modelRoles.default = %#v", cfg["modelRoles"])
	}
	for _, tt := range []struct {
		path []string
		want any
	}{
		{[]string{"providers", "webSearch"}, "codex"},
		{[]string{"setupVersion"}, 1},
		{[]string{"tui", "textSizing"}, true},
		{[]string{"loop", "mode"}, "prompt"},
		{[]string{"autoResume"}, true},
		{[]string{"lsp", "formatOnWrite"}, true},
		{[]string{"lsp", "diagnosticsOnEdit"}, true},
		{[]string{"bash", "autoBackground", "enabled"}, true},
		{[]string{"bashInterceptor", "enabled"}, true},
		{[]string{"checkpoint", "enabled"}, true},
		{[]string{"vault", "enabled"}, true},
		{[]string{"github", "enabled"}, true},
		{[]string{"plan", "defaultOnStartup"}, true},
		{[]string{"task", "eager"}, "always"},
		{[]string{"task", "enableLsp"}, true},
		{[]string{"task", "maxRecursionDepth"}, 3},
		{[]string{"task", "isolation", "mode"}, "auto"},
		{[]string{"task", "isolation", "merge"}, "patch"},
	} {
		got := any(cfg)
		for _, key := range tt.path {
			next, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("%s parent = %#v, want map", strings.Join(tt.path, "."), got)
			}
			got = next[key]
		}
		if got != tt.want {
			t.Fatalf("%s = %#v, want %#v", strings.Join(tt.path, "."), got, tt.want)
		}
	}
}

func TestWriteRecommendedOmpConfigWritesHindsight(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, backup, changed, err := writeRecommendedOmpConfig("test/provider", "hindsight", "http://hindsight.test:8888")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("writeRecommendedOmpConfig reported unchanged on fresh config")
	}
	if backup != "" {
		t.Fatalf("fresh config backup = %q, want empty", backup)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["memory"].(map[string]any)["backend"] != "hindsight" {
		t.Fatalf("memory.backend = %#v", cfg["memory"])
	}
	if cfg["hindsight"].(map[string]any)["baseURL"] != "http://hindsight.test:8888" {
		t.Fatalf("hindsight defaults missing: %#v", cfg["hindsight"])
	}
	if _, ok := cfg["mnemopi"]; ok {
		t.Fatalf("hindsight config should not add mnemopi defaults: %#v", cfg["mnemopi"])
	}
}

func TestWriteRecommendedOmpConfigMergesWithoutOverwriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".omp", "agent", "config.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "tools:\n  approvalMode: always-ask\ncustom: keep\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	_, backup, changed, err := writeRecommendedOmpConfig("test/provider", "mnemopi", "")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("writeRecommendedOmpConfig reported unchanged while adding missing defaults")
	}
	if backup == "" {
		t.Fatal("expected backup for existing config")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["custom"] != "keep" {
		t.Fatalf("custom key was not preserved: %#v", cfg)
	}
	if cfg["tools"].(map[string]any)["approvalMode"] != "always-ask" {
		t.Fatalf("tools.approvalMode was overwritten: %#v", cfg["tools"])
	}
	if cfg["memory"].(map[string]any)["backend"] != "mnemopi" {
		t.Fatalf("memory defaults missing: %#v", cfg["memory"])
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}
