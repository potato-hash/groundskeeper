// Package process is the policy-aware launcher for OMP RPC subprocesses. ALL
// omp --mode rpc spawns must go through ProcessOmpRpc — watchers, channels, and
// the webhook may enqueue jobs only, never spawn OMP directly.
//
// The launcher validates the launch request (thread_id, job_id, worker_id,
// session_dir, workspace_path required), records an audit event, and returns
// the configured *exec.Cmd. A launch without the required fields is refused.
package process

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// LaunchRequest is the authorization payload for an OMP RPC subprocess launch.
type LaunchRequest struct {
	Command       string   // must be "omp"
	Args          []string // must include "--mode", "rpc"
	ThreadID      string   // required
	JobID         string   // required
	WorkerID      string   // required
	SessionDir    string   // required
	WorkspacePath string   // required
	Auditor       *gkdb.DB // for recording the launch audit event
}

// ProcessOmpRpc validates the request, audits it, and returns the exec.Cmd. It
// is the ONLY sanctioned way to spawn an OMP RPC subprocess. Direct
// exec.Command("omp", "--mode", "rpc") outside this function is a policy
// violation.
func ProcessOmpRpc(ctx context.Context, req *LaunchRequest) (*exec.Cmd, error) {
	if err := validate(req); err != nil {
		return nil, err
	}
	// Audit the launch.
	if req.Auditor != nil {
		_ = req.Auditor.RecordAudit(req.ThreadID, req.JobID, "omp_launch", "process",
			"worker="+req.WorkerID+" session="+req.SessionDir+" ws="+req.WorkspacePath)
	}
	// #nosec G204 -- validate() restricts req.Command to the fixed "omp"
	// binary and requires "--mode rpc"; args are constructed internally by the
	// runtime adapter and passed without a shell.
	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	cmd.Dir = req.WorkspacePath
	return cmd, nil
}

// validate checks the launch request has all required fields and the command
// is omp with --mode rpc. Returns an error if any field is missing or the
// command/args are wrong.
func validate(req *LaunchRequest) error {
	if req.Command != "omp" {
		return fmt.Errorf("process: refused — command must be 'omp', got %q", req.Command)
	}
	if !hasModeRpc(req.Args) {
		return fmt.Errorf("process: refused — args must include --mode rpc")
	}
	if req.ThreadID == "" {
		return fmt.Errorf("process: refused — thread_id is required")
	}
	if req.JobID == "" {
		return fmt.Errorf("process: refused — job_id is required")
	}
	if req.WorkerID == "" {
		return fmt.Errorf("process: refused — worker_id is required")
	}
	if req.SessionDir == "" {
		return fmt.Errorf("process: refused — session_dir is required")
	}
	if req.WorkspacePath == "" {
		return fmt.Errorf("process: refused — workspace_path is required")
	}
	return nil
}

// hasModeRpc checks that the args contain "--mode" followed by "rpc".
func hasModeRpc(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--mode" && args[i+1] == "rpc" {
			return true
		}
	}
	return false
}

// AllowedLaunchFiles is the allowlist of files that may call exec.Command for
// omp. The static audit test checks that no file outside this list calls
// exec.Command("omp", "--mode", "rpc").
var AllowedLaunchFiles = []string{
	"internal/runtime/omp.go",      // the OMP adapter
	"internal/process/launcher.go", // the launcher itself
}

// IsAllowed reports whether a file path is in the allowlist.
func IsAllowed(path string) bool {
	for _, f := range AllowedLaunchFiles {
		if f == path {
			return true
		}
	}
	return false
}

// _ keeps time import alive for future use (e.g. launch timeout).
var _ = time.Now
