package worker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureWorktree creates a per-task git worktree at branch from the given
// repository, so concurrent tasks never collide on a shared checkout (roboomp
// SandboxManager.ensure_workspace equivalent). Returns the worktree path.
//
// If the repo is not a git repo, it returns the repo path unchanged (the task
// runs in-place). This degrades gracefully for non-git workspaces.
func EnsureWorktree(repo, branch string) (string, error) {
	// Check it's a git repo.
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return repo, nil // not a git repo; run in-place
	}
	wtDir, err := os.MkdirTemp("", "gk-worktree-*")
	if err != nil {
		return "", fmt.Errorf("worktree: tempdir: %w", err)
	}
	os.RemoveAll(wtDir) // git worktree add creates it
	branchArg := branch
	if branchArg == "" {
		branchArg = "main"
	}
	cmd := exec.Command("git", "worktree", "add", "-b", branchArg, wtDir, "HEAD")
	cmd.Dir = repo
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(wtDir)
		return "", fmt.Errorf("worktree: git worktree add: %w", err)
	}
	return wtDir, nil
}

// RemoveWorktree cleans up a per-task worktree (git worktree remove + rmdir).
func RemoveWorktree(repo, wtPath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repo
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	_ = os.RemoveAll(wtPath)
	return nil
}

// shortID returns the first 8 chars of an id (for branch names / display).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
