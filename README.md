# Groundskeeper

> Autonomous agent shell around OMP workers and Espalier Core.

Groundskeeper is the durable backend for a fleet of autonomous coding agents. It owns the long-lived substrate an agent fleet needs to run unattended — agent threads, scheduled jobs, an approvals inbox, an audit log of every external action, a worker pool, and a runtime-neutral adapter for spawning agent processes. OMP workers do the coding; Groundskeeper owns everything around them that has to survive a crash.

## What Groundskeeper owns

- **Durable task/job ledger** — agent threads, recurring jobs, dead-letter queue, persisted in SQLite (WAL).
- **Scheduler and recurrence** — loop specs with max-turns / max-wall-minutes / max-cost caps.
- **Approvals inbox** — risk-gated human approval before external actions.
- **Audit log** — every external action recorded, with redaction at the trust boundary.
- **Worker pool** — per-thread serialization, crash recovery (stuck-`running` requeued on startup).
- **OMP RPC adapter** — spawns OMP workers via `omp --mode rpc` over stdio (roadmap).
- **Channel gateway** — notifications to email/calendar/Slack sidecars (roadmap).

## What Groundskeeper does not own

- **Provider login** — delegated to OMP/Pi.
- **Model registry** — delegated to OMP/Pi.
- **Coding tool runtime** — delegated to OMP/Pi.
- **Pi/OMP session loop** — OMP runs the agent; Groundskeeper manages the lifecycle around it.
- **Espalier learning ledger / learned routines / jj+eval gates** — Espalier Core is loaded into OMP sessions as an extension; Groundskeeper never imports Espalier internals.

## Status

Phases 0–8 are implemented:

- **Phase 0–3:** fork, rename, upstream audit docs, durable substrate (gk.db), CLI, fake adapter.
- **Phase 4:** OMP RPC adapter (`internal/runtime` OmpAdapter) — spawns `omp --mode rpc`, streams the JSONL protocol, verified against the real omp binary.
- **Phase 5:** Worker pool (`internal/worker`) — single dispatcher, bounded slots, crash recovery, per-task worktrees, loop-spec runner. `gk-daemon` runs it.
- **Phase 6:** Fleet view (`internal/fleet`) + unprefixed CLI commands (`fleet`, `threads`, `jobs`, `approvals`).
- **Phase 7:** Channel gateway (`internal/channel`) — notification policy, HMAC-signed delivery, approval routing.
- **Phase 8:** Sidecars (`internal/sidecar`) — email/calendar/contact handlers behind an HMAC-verified HTTP server; the daemon never holds platform credentials.

This is a development build, not a release. The bubbletea TUI panel is embeddable but not yet auto-rendered in Agent Deck's home view.

## Forked from

Agent Deck (https://github.com/asheshgoplani/agent-deck), MIT License, Copyright (c) 2025 Ashesh Goplani. Groundskeeper is a derivative fork under the same MIT license. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [docs/upstream-agent-deck-audit.md](docs/upstream-agent-deck-audit.md).

Agent Deck's session manager, TUI (bubbletea), and web UI remain in this tree and build cleanly; Groundskeeper's durable backend (`internal/gkdb`, `internal/runtime`) is additive and uses a separate SQLite database file (`gk.db`) so the two do not couple schemas.

## Build

```sh
go build ./cmd/groundskeeper
```

`make build` also works if the Tailwind v4 standalone CLI is installed (web-UI-only CSS); the backend slice does not require it.

## CLI (Groundskeeper additions)

Groundskeeper subcommands are prefixed `gk-` to coexist with Agent Deck's existing command surface until the TUI is integrated:

```sh
groundskeeper gk-status              # counts: threads, running jobs, pending approvals, dead letters
groundskeeper gk-thread create --title "fix leak" --runtime omp --workspace .
groundskeeper gk-thread list
groundskeeper gk-thread show <id>
groundskeeper gk-thread archive <id>
groundskeeper gk-approvals list
groundskeeper gk-approvals approve <id>
groundskeeper gk-approvals reject <id>
```

Data lives at `~/.local/share/groundskeeper/gk.db`.

## Development

- **VCS:** jj is the source-of-truth workflow.
- **Mode:** Ponytail (lazy-first — see the build ladder in the agent config).
- **Tests:** `go test -race ./internal/gkdb/... ./internal/runtime/...` for the new backend; `go test -race ./internal/statedb/...` confirms the forked session store still works.

## License

MIT. See [LICENSE](LICENSE).