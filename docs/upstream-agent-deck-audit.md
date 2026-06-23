# Upstream Audit — Agent Deck

Source of truth for the Groundskeeper fork. Audited from
`github.com/asheshgoplani/agent-deck` at the `main` HEAD cloned during the fork
(Go 1.25.11, MIT). Every claim below is grounded in a file path in the cloned
tree that is now present in this repository under the renamed module path
`github.com/potato-hash/groundskeeper`.

## What Agent Deck is

A terminal session manager for AI coding agents. It launches and supervises
agent processes (Claude, Codex, Gemini, Copilot, etc.) inside tmux panes,
surfaces them in a bubbletea TUI and a web UI, and persists the session fleet in
SQLite. It is the session/fleet UI layer that Groundskeeper wraps with a durable
autonomous substrate.

## Build, language, license

- **Language/toolchain:** Go 1.25.11 (pinned in `go.mod` and `Makefile`
  `GOTOOLCHAIN=go1.25.11`). Local Go 1.26.2 satisfies it.
- **Build:** `make build` -> `go build -ldflags "-X main.Version=..." -o ./build/groundskeeper ./cmd/groundskeeper`. `make build` depends on `make css` (Tailwind v4 standalone CLI); the backend slice does not require CSS.
- **Tests:** `go test -race ./...`. Uses `testify`, `goleak`, `teatest`
  (`charmbracelet/x/exp/teatest`). The full suite is the CI gate, not the fork
  slice.
- **License:** MIT, Copyright (c) 2025 Ashesh Goplani (`LICENSE`).

## Architecture (what we keep)

| Concern | Location | Notes |
|---|---|---|
| CLI dispatch | `cmd/groundskeeper/main.go` | `extractProfileFlag` then `switch args[0]` at ~line 241. New `gk-*` cases are additive here. |
| Version | `cmd/groundskeeper/main.go` `var Version` | now `"0.1.0-gk"`; overridden via `-ldflags -X main.Version`. |
| Session model | `internal/session/instance.go` | `Instance` struct; `Status` enum (`running`/`waiting`/`idle`/`error`/`starting`/`stopped`/`queued`) plus `Substate` (Honest-Status-v2). |
| Persistence (UI sessions) | `internal/statedb/statedb.go` | `StateDB` over `modernc.org/sqlite`. WAL + `busy_timeout(5000)` + `foreign_keys(on)` via DSN pragmas (lines 304-323). `SchemaVersion = 13`. `withBusyRetry` for SQLITE_BUSY. Backup-on-drop data-loss safeguards (2026-06-04 incident family). |
| Config | TOML `config.toml` + JSON `config.json`, resolved via `internal/agentpaths` | XDG base dirs. |
| State dir layout | `~/.local/share/groundskeeper/profiles/<profile>/state.db` | XDG data dir = `agentpaths.DataDir()` + `AppDirName` (now `"groundskeeper"`). Legacy dir `~/.agent-deck` is the migration source. |
| TUI | `internal/ui/` | bubbletea. |
| Web UI | `internal/web/` | HTTP server, websocket bridge, static assets. |
| Conductor | `internal/session/conductor.go` + embedded Python bridge (`conductor/`) | Supervisor sessions. |
| Watcher | `internal/watcher/engine.go` | Event-driven triggers. |
| Notifications | `cmd/groundskeeper/notify_daemon_cmd.go` | notify daemon. |
| Session fork | session fork with `--with-state` and `-w` branch, git and jj backends (`internal/vcsbackend/`) | |
| Costs | `internal/costs/` | cost-event tracking. |
| Platform | `internal/platform/` | macOS/Linux/WSL specifics. |

## What we keep (unchanged, builds cleanly)

- The entire session manager, TUI, web UI, conductor, watcher, costs, and statedb.
- `internal/statedb/statedb.go` `Open` pattern (WAL + busy_timeout DSN) — **reused
  as the template** for `internal/gkdb/db.go`.
- `internal/agentpaths` — XDG resolution (only `AppDirName` changed).

## What we rename

- Module path: `github.com/asheshgoplani/agent-deck` -> `github.com/potato-hash/groundskeeper` (456 `.go` files, mechanical find-replace; `go mod tidy`).
- Binary: `cmd/agent-deck` -> `cmd/groundskeeper`; `Makefile BINARY_NAME`.
- Version: `1.9.75` -> `0.1.0-gk`.
- XDG app dir: `agent-deck` -> `groundskeeper` (`internal/agentpaths/paths.go` `AppDirName`).
- `agentpaths` migrate tests: XDG destination expectations `agent-deck` -> `groundskeeper` (legacy source dir `.agent-deck` intentionally unchanged — it is the migration source).

## What we wrap

- `cmd/groundskeeper/main.go` dispatch: Groundskeeper `gk-*` cases are **additive**
  to Agent Deck's existing `add`/`list`/`session`/`status`/etc. They do not replace
  existing commands. The `gk-` prefix avoids collision until the TUI integration
  phase decides whether to unify the surface.

## What we delete or defer

- Agent Deck's `README.md` (55 KB) — replaced by the Groundskeeper README.
- Agent Deck's `AGENTS.md` — replaced by the Groundskeeper ownership model (already in `AGENTS.md`).
- Agent Deck's `.gitignore` `agent-deck`/`.agent-deck/` entries — updated for `groundskeeper`.
- The 1664-file test suite is the CI gate, not this slice. Slice runs
  `./internal/statedb/... ./internal/gkdb/... ./internal/runtime/... ./cmd/groundskeeper/...`.
- CSS/Tailwind tooling: `make build` -> `make css` (Tailwind v4 standalone). If the
  Tailwind binary is unavailable, `go build ./cmd/groundskeeper` is the sufficient
  backend-slice gate (web UI is Phase 9+, not this slice).

## Extension points (where Groundskeeper attaches)

1. **`cmd/groundskeeper/main.go` dispatch switch** — new `gk-status`/`gk-thread`/`gk-approvals` cases.
2. **`internal/gkdb/` (new)** — separate package, separate DB file (`gk.db`), reusing the statedb `Open` pattern. No schema coupling with `statedb`.
3. **`internal/runtime/` (new)** — runtime-neutral `AgentRuntimeAdapter` interface + fake. OMP RPC adapter is Phase 4.

## Risks

- **AppDirName change breaks tests that hardcode `agent-deck` XDG paths.** Hit in
  `internal/agentpaths/migrate_test.go` — fixed by updating XDG destination
  expectations to `groundskeeper` (legacy source `.agent-deck` unchanged). If
  other tests across the 1664-file suite fail the same way, fix their temp-dir
  setup; do not revert the rename.
- **Full suite runtime.** The repo has thousands of tests; this slice runs a
  representative subset. The full suite is CI's job.
- **External dependency resolution after module-path rename.** `go mod tidy`
  succeeded with no resolution failures (only the module path changed, not
  external deps).

## Build commands (slice gate)

```sh
go build ./cmd/groundskeeper                    # backend build
go test -race ./internal/statedb/...            # forked session store intact
go test -race ./internal/gkdb/... ./internal/runtime/...  # new backend
go vet ./...
make build                                       # full build (needs Tailwind for CSS only)
```
