# Ownership Map

What Groundskeeper owns, and what it deliberately does not. This is the contract
that keeps the boundaries clean: Groundskeeper is the *shell around* OMP workers
and Espalier Core, never a reimplementation of either.

## Groundskeeper owns

| Concern | Where | Status |
|---|---|---|
| Durable task/job ledger | `internal/gkdb` (agent_threads, jobs, dead_letters) | Phase 3 |
| Scheduler and recurrence | `internal/gkdb` (loop_specs) | Phase 3 (schema); runner Phase 5 |
| Approvals inbox | `internal/gkdb` (approvals) | Phase 3 |
| Audit log for external actions | `internal/gkdb` (audit_events) + `redaction.go` | Phase 3 |
| Notification policy | `internal/gkdb` (notifications) | Phase 3 (schema); delivery Phase 7 |
| Channel gateway | roadmap | Phase 7+ |
| OMP RPC worker manager | `internal/runtime` (adapter) | Phase 3 (fake); Phase 4 (OMP) |
| Agent Deck UI/fleet integration | forked tree (TUI + web UI) | forked, builds; TUI integration Phase 6 |
| roboomp-style worker orchestration | planned worker pool | Phase 5 |
| Worker process tracking | `internal/gkdb` (worker_processes) | Phase 3 (schema); live tracking Phase 5 |
| Future email/calendar/reminder/contact sidecars | roadmap | Phase 8+ |

## Groundskeeper does NOT own

| Concern | Owned by | Why |
|---|---|---|
| Provider login | OMP/Pi | Auth is delegated; Groundskeeper never holds provider credentials in-process (sidecar model). |
| Model registry | OMP/Pi | Model selection is the runtime's job. |
| Coding tool runtime | OMP/Pi | The agent loop, tool execution, and sandboxing are OMP's. |
| Pi/OMP session loop | OMP/Pi | OMP runs the agent conversation; Groundskeeper manages the lifecycle *around* it. |
| Espalier learning ledger | Espalier Core | Loaded as an OMP extension in workers; Groundskeeper never imports Espalier internals. |
| Espalier learned routines | Espalier Core | Same — extension-owned. |
| Espalier jj/eval gates | Espalier Core | Same — extension-owned. |

## The boundary rule

Groundskeeper talks to OMP only via the stdio JSONL RPC protocol and records the
lifecycle state (thread, session_dir, workspace, runtime_session_id) in its own
DB. It never imports OMP or Espalier packages. Espalier Core runs *inside* the
OMP worker as an extension and surfaces through the `extension_ui_request`/
`response` RPC frames; Groundskeeper passes those frames through, it does not
interpret them.
