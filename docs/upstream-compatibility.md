# Upstream Compatibility

Groundskeeper is a fork of Agent Deck. This document tracks the compatibility
track for the AppDirName rename and the known test-failure classes.

## App identity policy

- **New writes** use `groundskeeper` (via `agentpaths.AppDirName`).
- **Legacy `agent-deck` paths** are read-only fallbacks (migration sources).
- The legacy `.agent-deck` directory is the migration source and intentionally
  keeps the old name.
- `internal/appidentity` centralizes this policy.

## Central path layer

All XDG path resolution goes through `internal/agentpaths`:
- `ConfigDir()` → `$XDG_CONFIG_HOME/groundskeeper` (fallback `~/.config/groundskeeper`)
- `DataDir()` → `$XDG_DATA_HOME/groundskeeper` (fallback `~/.local/share/groundskeeper`)
- `CacheDir()` → `$XDG_CACHE_HOME/groundskeeper` (fallback `~/.cache/groundskeeper`)
- `LegacyDir()` → `~/.agent-deck` (the migration source, intentionally old name)

## Known test-failure classes in the full suite

### 1. AppDirName fallout (mostly fixed)

Tests that hardcode `agent-deck` in XDG path expectations. The production code
resolves to `groundskeeper`; tests that expected `agent-deck` were updated to
`groundskeeper` (preserving `.agent-deck` legacy references). Fixed files:

- `cmd/groundskeeper/xdg_task6_test.go`
- `internal/watcher/xdg_paths_test.go`
- `cmd/groundskeeper/coldstart_perf_test.go`
- `cmd/groundskeeper/add_test.go`
- `cmd/groundskeeper/version_nudge_test.go`
- `cmd/groundskeeper/web_cmd_test.go`
- `internal/watcher/engine.go` (fallback paths)
- `internal/feedback/state_xdg_test.go`

Remaining `agent-deck` references in tests are in legacy-migration tests that
intentionally reference the OLD name as the migration source (correct behavior).

### 2. Network-dependent tests (environmental, not code)

- `TestLogCgroupIsolationDecision_WiredIntoBootstrap`: builds the binary in an
  isolated GOMODCACHE temp dir, forcing re-download from proxy.golang.org.
  Fails on network flakes, not on code issues.

### 3. Timing-flaky tests (pre-existing)

- `TestWaitForFreshOutput_UniquePeerStillReads`: 2s freshness timeout.
  Pre-existed before the fork; not AppDirName-related.

## VCS

jj is the source-of-truth workflow. The working copy is tracked automatically.
No commit is needed for working-copy changes. `jj diff` shows the TS skeleton
removed and Go files added.
