package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestXDGTask6_UninstallRemovesRunningBinaryOutsideDefaultDirs(t *testing.T) {
	setupTask6XDGEnv(t)

	customDir := filepath.Join(t.TempDir(), "custom-install")
	binPath := filepath.Join(customDir, "groundskeeper")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("mkdir custom install dir: %v", err)
	}
	raw, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(binPath, raw, 0o755); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}

	cmd := exec.Command(binPath, "-test.run=TestXDGTask6HelperProcess", "--", "uninstall-self-delete")
	cmd.Env = append(os.Environ(), "AGENT_DECK_TASK6_HELPER_PROCESS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, string(out))
	}
	if _, err := os.Lstat(binPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall should remove the running custom-dir groundskeeper binary, lstat err=%v\n%s", err, out)
	}
}

func TestXDGTask6_UninstallRemovesPathResolvedBinaryOutsideDefaultDirs(t *testing.T) {
	setupTask6XDGEnv(t)

	customDir := filepath.Join(t.TempDir(), "custom-bin")
	binPath := filepath.Join(customDir, "groundskeeper")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("mkdir custom bin dir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", customDir)

	out := captureStdoutForTask6(t, func() {
		handleUninstall([]string{"-y", "--keep-data", "--keep-tmux-config"})
	})

	if !strings.Contains(out, binPath) {
		t.Fatalf("uninstall output should mention PATH-resolved custom binary %q:\n%s", binPath, out)
	}
	if _, err := os.Lstat(binPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall should remove PATH-resolved custom binary, lstat err=%v\n%s", err, out)
	}
}

func TestShellUninstallRemovesPathResolvedCustomBinary(t *testing.T) {
	bashPath := testBashPath(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	customDir := filepath.Join(root, "custom-bin")
	binPath := filepath.Join(customDir, "groundskeeper")
	for _, dir := range []string{home, customDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(binPath, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	env := []string{
		"HOME=" + home,
		"PATH=" + customDir + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
	}

	dryRun := exec.Command(bashPath, "../../uninstall.sh", "--dry-run", "--keep-data", "--keep-tmux-config")
	dryRun.Env = env
	out, err := dryRun.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall.sh dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), binPath) {
		t.Fatalf("uninstall.sh dry-run should mention PATH-resolved custom binary %q:\n%s", binPath, out)
	}

	remove := exec.Command(bashPath, "../../uninstall.sh", "--non-interactive", "--keep-data", "--keep-tmux-config")
	remove.Env = env
	if out, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("uninstall.sh non-interactive removal failed: %v\n%s", err, out)
	}
	if _, err := os.Lstat(binPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall.sh should remove PATH-resolved custom binary, lstat err=%v", err)
	}
}
