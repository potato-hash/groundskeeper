# Test Strategy

Groundskeeper uses three test layers, matching the original build prompt.

## Slice gate (focused package tests)

The minimum gate for any change. Runs the packages touched by the slice:

```sh
go test -race ./internal/gkdb/... ./internal/runtime/... ./internal/worker/... \
  ./internal/fleet/... ./internal/channel/... ./internal/sidecar/... \
  ./internal/host/... ./internal/process/... ./internal/appidentity/...
```

This gate must be green before any claim of completion.

## Integration gate (cross-package tests without real OMP)

Tests that cross package boundaries using fake/stub adapters:

```sh
go test -race ./internal/worker/...    # pool + fake adapter + gkdb
go test -race ./internal/watcher/...   # webhook + gkdb (no real omp)
go test -race ./internal/host/...      # host tools + gkdb
go test -race ./internal/process/...  # launcher validation
```

## Upstream compatibility gate (full Agent Deck suite)

The full `go test -race ./...` across all 1,664 Go files. This is the CI gate,
not the slice gate. Known failure classes:

1. **AppDirName fallout**: tests that hardcode `agent-deck` in XDG path
   expectations. Fixed where found (xdg_task6, watcher xdg_paths, coldstart perf,
   add_test, version_nudge, web_cmd, uninstall, conductor, openclaw, cgroup,
   feedback state_xdg). Remaining hits are in legacy-migration tests that
   intentionally reference the OLD name as the migration source (correct).

2. **Network-dependent tests**: `TestLogCgroupIsolationDecision` builds in an
   isolated GOMODCACHE, forcing re-download from proxy.golang.org (network flake).

3. **Timing-flaky tests**: `TestWaitForFreshOutput_UniquePeerStillReads` has a
   2s freshness timeout that pre-existed before the fork.

## Live OMP smoke (optional, build tag `omp_live`)

```sh
go test -race -tags omp_live -timeout 120s ./internal/runtime/... -run TestOmpLive_TurnCompletes
```

Requires `omp` on PATH + a logged-in provider. Excluded from CI unless omp is
installed. The roadmap distinguishes fake-transport tests (always run) from live
OMP smoke (optional).

## What the slice gate covers

| Package | Tests | What they prove |
|---|---|---|
| gkdb | persistence, claim serialization, reset, dead-letter, redaction | durable substrate works |
| runtime | fake events, OMP stub protocol, env scrub, prompt-ack-not-completion | adapter protocol mapping |
| worker | pool claim/dispatch/complete, loop max_turns, retry not new turn, counter persists, dead-letter notify, state machine | worker pool + loop runner |
| host | host tools, pa:// URI, write-deny, redaction | host tool bridge |
| process | launch refusal, allowlist | managed launcher |
| fleet | load counts, render | fleet view |
| channel | HMAC sign/verify, replay, sidecar HTTP | channel gateway |
| sidecar | HMAC server, local_notes, no-credential guard | sidecar handlers |
| appidentity | app names, legacy detection | identity policy |
| watcher | webhook bad-sig, enqueue, unknown-thread reject | watcher ingestion |
