package main

import (
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
	t.Setenv("AGENTDECK_SUPPRESS_TMUX_WARNING", "1")

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
	t.Setenv("AGENTDECK_SUPPRESS_TMUX_WARNING", "1")

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
	if !strings.Contains(out, "Global OMP config write skipped in non-interactive mode") {
		t.Fatalf("setup output missing non-interactive skip\n--- output ---\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "config.yml")); !os.IsNotExist(err) {
		t.Fatalf("non-interactive setup wrote OMP config, stat err=%v", err)
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
		"--verify-model",
		"maybe_run_first_run_setup",
		"Run first-run setup now? [Y/n]",
		"setup_args+=(--non-interactive --install-missing)",
		"</dev/tty",
		"--non-interactive --install-missing",
		`if [[ "$SETUP_MODE" == "run" ]]; then`,
		"GOPROXY=direct",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install.sh missing %q", want)
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

func TestRedactedCommandOutputHidesProviderKeys(t *testing.T) {
	t.Setenv("OLLAMA_CLOUD_API_KEY", "temporary-test-key")

	got := redactedCommandOutput([]byte("failed with temporary-test-key"))
	if strings.Contains(got, "temporary-test-key") {
		t.Fatalf("redactedCommandOutput leaked provider key: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedCommandOutput missing redaction marker: %q", got)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestWriteRecommendedOmpConfigCreatesGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, backup, changed, err := writeRecommendedOmpConfig("test/provider")
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

	_, backup, changed, err := writeRecommendedOmpConfig("test/provider")
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
