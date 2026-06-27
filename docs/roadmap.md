# Roadmap

Groundskeeper build phases. Phases 0-3 are this slice; Phase 4+ are unchecked
roadmap entries. The OMP live smoke and Agent Deck TUI integration are explicitly
pending — they are not in this slice.

## Phase 0 — Fork

- [x] Discard TS skeleton; clone Agent Deck Go repo into working dir
- [x] Verify fork builds: `go build ./cmd/groundskeeper`, statedb tests pass

## Phase 1 — Rename and fork identity

- [x] Module path `github.com/potato-hash/groundskeeper`
- [x] Binary `cmd/groundskeeper`, `Makefile BINARY_NAME`
- [x] Import-path find-replace across all `.go` files
- [x] Version `0.1.0-gk`
- [x] XDG app dir `groundskeeper`
- [x] README (Groundskeeper thesis, forked-from credit)
- [x] AGENTS.md (Groundskeeper ownership)
- [x] THIRD_PARTY_NOTICES.md
- [x] Verify build after rename

## Phase 2 — Upstream audit + ownership docs

- [x] docs/upstream-agent-deck-audit.md
- [x] docs/upstream-roboomp-audit.md
- [x] docs/ownership.md
- [x] docs/roadmap.md
- [x] docs/architecture.md

## Phase 3 — Durable substrate + CLI + fake adapter

- [x] internal/gkdb: Open, Migrate, ClaimNextJob, ResetStuckRunning, DeadLetter
- [x] internal/gkdb: AgentThread, Job, Approval, Audit CRUD
- [x] internal/gkdb: redaction trust boundary
- [x] internal/runtime: AgentRuntimeAdapter interface + FakeAdapter
- [x] gk- CLI subcommands (gk-status, gk-thread, gk-approvals)
- [x] Tests (gkdb, runtime, redaction)
- [x] Build + tests pass, CLI smoke

## Phase 4 — OMP RPC adapter

- [x] Spawn `omp --mode rpc` over stdio with cwd=worktree, session_dir, scrubbed env
- [x] StreamEvents: parse ready / agent_start / agent_end / host_tool_call / host_uri_request / extension_ui_request
- [x] Prompt-ack-is-not-completion handling
- [x] ResumeThread via session_dir + --continue iff transcript exists
- [x] OMP live smoke (a real worker completes a turn end-to-end)

## Phase 5 — Worker pool

- [x] Single dispatcher + bounded slot cap (roboomp WorkerPool shape)
- [x] In-memory inflight set reconciled against DB on crash
- [x] Per-task worktree creation — EnsureWorktree wired into pool runJob (git worktree per job; non-git falls back in-place)
- [x] Loop spec runner — loadLoopSpec + runLoop wired into pool (evaluates caps, enqueues next turn)
- [x] gk-daemon CLI (runs the pool; --fake/--model/--slots/--sidecar/--hmac-key)
- [x] gk-job CLI (create/list/show)

## Phase 6 — Agent Deck TUI integration

- [x] internal/fleet: read-only fleet status view (threads/jobs/approvals/dead letters)
- [x] gk-fleet-tui: standalone bubbletea program rendering FleetView (refresh + quit)
- [x] `fleet` CLI command (unified status)
- [x] Unify gk- command surface: unprefixed aliases `threads`/`jobs`/`approvals`

## Phase 7 — Channel gateway

- [x] Notification policy + delivery — Gateway.Send triggered by pool on dead-letter + host_tool_call
- [x] Sidecar credential model (HMAC-SHA256 signed requests; replay-protected; daemon never holds platform creds)
- [x] Approvals routing to channels — pool creates approval on host_tool_call, NotifyApproval by risk

## Phase 8 — Sidecars

- [x] Email sidecar (internal/sidecar EmailHandler — SMTP, credential held in sidecar)
- [x] Calendar/reminder sidecar (internal/sidecar CalendarHandler — credential held in sidecar)
- [x] Contact sidecar (internal/sidecar ContactHandler — address-book lookup, credential held in sidecar)
- [x] HMAC-verified sidecar server + gk-sidecar CLI (--kind email|calendar|contact, --addr, --hmac-key)

## Not in this slice (pending)

- **OMP live smoke** — Phase 4 ✓. Real omp worker completes a turn end-to-end (build tag `omp_live`).
- **Agent Deck TUI integration** — Phase 6 ✓. Fleet view renders in the `gk-fleet-tui` bubbletea program + `fleet` CLI. NOT yet wired into Agent Deck's existing Home tab bar (a larger refactor, deferred).
- **Native Windows installer + Cua Driver path** — current installer is bash for macOS/Linux/WSL. Add a PowerShell installer and Windows-side Cua Driver install flow when Groundskeeper supports native Windows installs.
- **Full 1664-file test suite** — CI gate, not the slice. Slice runs the gkdb/runtime/statedb subset.
