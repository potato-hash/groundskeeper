# Architecture

Groundskeeper is the durable shell around a fleet of OMP workers. One page.

```
                 +---------------------------+
                 |     Groundskeeper daemon  |
                 |  (cmd/groundskeeper)      |
                 +---------------------------+
                 |                           |
   CLI gk-*  --> | gkdb (SQLite WAL)         |
                 |  agent_threads             |
                 |  thread_turns              |
                 |  loop_specs                |
                 |  tasks                     |
                 |  jobs  <-- ClaimNextJob ---+---> worker pool (Phase 5)
                 |  approvals                 |      |
                 |  audit_events              |      | spawn omp --mode rpc (stdio)
                 |  notifications              |      v
                 |  worker_processes          |   +-------------------+
                 |  dead_letters              |   |  OMP worker       |
                 |                            |   |  (agent runtime)  |
                 |  internal/runtime          |   |  + Espalier Core  |
                 |   AgentRuntimeAdapter      |   |    (extension)    |
                 |   (fake now, OMP Phase 4)  |   +-------------------+
                 +---------------------------+
                              |
                              | sidecar (HMAC, Phase 7+)
                              v
                       channel gateway (email/calendar/slack)
```

## The durable DB (gk.db)

`internal/gkdb` owns ten tables in a SQLite database separate from Agent Deck's
per-profile `state.db` (no schema coupling). It reuses Agent Deck's `statedb.Open`
pattern — `modernc.org/sqlite` via `database/sql`, WAL mode,
`busy_timeout(5000)` and `foreign_keys(on)` passed as per-connection DSN pragmas
(because busy_timeout/foreign_keys are per-connection in SQLite, not persistent
on the file like WAL). On close it checkpoints the WAL (`PRAGMA
wal_checkpoint(TRUNCATE)`).

The job queue is the heart. `ClaimNextJob(now)` runs inside `BEGIN IMMEDIATE`,
selects the oldest queued job past `next_run_at` whose `thread_id` has no
currently-`running` job (anti-join), and flips it to `running`. This enforces
per-thread serialization in SQL — two workers cannot run the same thread, and a
single worker never double-claims. `ResetStuckRunning` requeues anything still
`running` at daemon start so a crash between claim and complete does not strand
tasks.

The audit log (`audit_events`) is the trust boundary. Every external action is
recorded; `Redact()` strips sensitive material before insert (authorization
headers, access-material prefixes, labeled key-value pairs). Over-redaction is
preferred over leaking. The function is tested; it is the one place secrets could
reach disk.

## The runtime adapter

`internal/runtime.AgentRuntimeAdapter` is runtime-neutral: `StartThread`,
`ResumeThread`, `SendTurn`, `Interrupt`, `StreamEvents`, `Shutdown`. The only
implementation in this slice is `FakeAdapter` (deterministic, no subprocess). The
OMP RPC adapter (Phase 4) will spawn `omp --mode rpc` over stdio with
`cwd=worktree`, `session_dir`, a scrubbed environment, and stream the JSONL
frames (`ready`/`agent_start`/`agent_end`/`host_tool_call`/`host_uri_request`/
`extension_ui_request`) as `RuntimeEvent`s. Espalier Core runs inside the worker
as an extension and surfaces through `extension_ui_request`/`response` frames;
Groundskeeper passes them through and does not interpret them.

## What is NOT here

- **Provider auth, model registry, coding-tool runtime, the Pi/OMP session loop**
  — OMP owns these. Groundskeeper spawns OMP and records the lifecycle.
- **Espalier learning ledger / learned routines / jj+eval gates** — Espalier Core,
  loaded as an OMP extension inside the worker. Groundskeeper never imports it.
- **The worker pool, channel gateway, sidecars** — Phase 5+. The DB schema and the
  adapter interface exist now; the live orchestration is roadmap.

## Why two SQLite files

`gk.db` (Groundskeeper durable substrate) and `state.db` (Agent Deck per-profile
session store) are separate so a schema migration in one never risks the other and
so the two can be opened/locked independently. If consolidation is ever wanted,
the gkdb tables can migrate into statedb — a later decision, not needed now.
