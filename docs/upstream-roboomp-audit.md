# Upstream Audit — roboomp / OMP

Design reference for Groundskeeper's worker pool and OMP RPC integration. This
audit captures the roboomp autonomous-worker-orchestration model and the OMP RPC
JSONL protocol that Groundskeeper's `internal/gkdb` job-claim and the planned
`internal/runtime` OMP adapter are modeled on.

roboomp is the autonomous worker orchestration layer for oh-my-pi (OMP)
(https://github.com/can1357/oh-my-pi). Groundskeeper does not bundle roboomp or
OMP source; it invokes the installed `omp` binary as a subprocess and reuses
roboomp's *design*, not its code. The findings below are from the roboomp/OMP
audit that grounded this fork's architecture.

## SQLite WAL event queue

roboomp persists a durable work queue in SQLite (WAL mode). The `events` table
holds queued tasks. Claiming the next task is a per-issue anti-join run inside
`BEGIN IMMEDIATE` so two workers never take the same task and a single thread
never runs two tasks at once:

- `claim_next_event` selects the oldest queued event whose `thread_id` has no
  currently-`running` event (anti-join subquery against the running set), then
  flips it to `running` and increments its attempt counter — all inside one
  immediate transaction so the claim is atomic under WAL's single-writer
  serialization.

Groundskeeper mirrors this in `internal/gkdb/schema.go` `ClaimNextJob`: `BEGIN
IMMEDIATE`, SELECT oldest queued job past `next_run_at` WHERE no running job
shares its `thread_id`, UPDATE to `running`, bump `attempts`. Per-thread
serialization is enforced in SQL, not in Go locks.

## WorkerPool

roboomp's `WorkerPool` (queue.py) is a single dispatcher plus a bounded
concurrency cap (SlotPool or Semaphore). A goroutine-equivalent pulls claimed
events off the queue and hands each to a worker slot; the in-memory inflight set
tracks what is running so a crash can be reconciled against the DB. The pool
does not own persistence — the DB does — it only owns *live* dispatch.

Groundskeeper's planned worker pool (Phase 5) follows the same shape: the DB is
the source of truth, the pool is ephemeral.

## Per-task worktree

Each task gets its own git worktree (`SandboxManager.ensure_workspace`) so
concurrent tasks never collide on a shared checkout. The worktree path becomes
the `cwd` the agent subprocess runs in. Groundskeeper records this as
`worker_processes.workspace_path` and `agent_threads.workspace_path`.

## Persistent session_dir

roboomp keeps a per-thread `.omp-session` directory. A thread resumes
(`--continue`) iff a prior JSONL transcript exists in its session dir; otherwise
it starts fresh. Groundskeeper stores `agent_threads.session_dir` and
`runtime_session_id` so a crashed worker can resume the same OMP conversation
rather than restart from scratch.

## omp spawn

roboomp's `RpcClient` (worker.py) spawns `omp` with:
- `cwd` = the per-task worktree
- `session_dir` = the persistent `.omp-session` dir
- a scrubbed environment (see below)
- `custom_tools` = `host_tools.build(bindings)` — the privileged write surface

Groundskeeper's OMP RPC adapter (Phase 4) will spawn `omp --mode rpc` over stdio
with the same `cwd`/`session_dir`/env-scrub shape, but the *host tools* are
Groundskeeper-owned (the audit log + approvals gate is what makes a privileged
tool call safe to run unattended).

## Env scrub

Before spawning the agent subprocess, roboomp blanks sensitive environment
variables so the agent never sees raw provider credentials. The exact scrubbed
variable names live in `robomp worker.py _SCRUBBED_ENV_KEYS`. The privileged
credential lives in a sidecar (see below), never in the agent's env.

Groundskeeper applies the same scrub at spawn time and additionally redacts any
residual sensitive material before it enters the audit table
(`internal/gkdb/redaction.go`).

## Host tools as exclusive privileged write surface

roboomp exposes ~14 host tools as the *only* way the agent can perform
privileged writes (filesystem outside the worktree, shell, network, GitHub,
etc.). Every host-tool call is audited into a `tool_calls` table. The agent
itself runs with no direct privileged access — all privileged actions are
funneled through the audited host-tool RPC.

This is the core security model Groundskeeper adopts: the audit log + approvals
gate *is* the privileged surface. An unapproved or unaudited privileged action is
impossible by construction.

## Sidecar holds the privileged credential

roboomp routes privileged requests (e.g. GitHub API calls) through a sidecar
(gh-proxy) using HMAC-signed requests. The orchestrator never holds the
credential; the sidecar does, and only accepts signed requests from the
orchestrator. A compromised agent process cannot exfiltrate the credential
because it never has it.

Groundskeeper's channel gateway (roadmap) follows the same split: the daemon
holds an HMAC signing key, the sidecar holds the platform credential.

## Crash recovery

At startup, roboomp's `reset_stuck_running` flips every job still marked
`running` back to `queued` — a crash between claiming and completing would
otherwise strand tasks in `running` forever. Groundskeeper mirrors this in
`internal/gkdb/schema.go` `ResetStuckRunning`: `UPDATE jobs SET status=queued,
next_run_at=NULL WHERE status=running`, called once at daemon start.

## OMP RPC JSONL protocol

OMP's `--mode rpc` speaks newline-delimited JSON over stdio. The frames
Groundskeeper's adapter must understand:

- **`ready`** — the worker process is up and accepting prompts.
- **`prompt` (immediate ack)** — sending a prompt returns an immediate ack
  carrying `data.agentInvoked`; this is *not* completion. The turn is in flight.
- **`agent_end`** — the turn completed. This, not the ack, marks completion.
- **`host_tool_call` / `host_tool_result`** — a privileged host-tool request from
  the agent and the result back. This is the audit/approvals interception point.
- **`host_uri_request` / `host_uri_result`** — URI fetch requests (read surface).
- **`prompt_result`** — the final result payload for a turn.
- **`extension_ui_request` / response** — Espalier Core (loaded as an extension
  in the worker) can surface UI through this channel.

Groundskeeper's `internal/runtime/adapter.go` `RuntimeEvent` union mirrors these
frames: `ready`, `agent_start`, `message_update`, `agent_end`,
`host_tool_call`, `host_uri_request`, `extension_ui_request`, `error`.

## What Groundskeeper takes vs. rebuilds

| roboomp/OMP concept | Groundskeeper handling |
|---|---|
| SQLite WAL event queue + anti-join claim | Rebuilt in Go in `internal/gkdb` (ClaimNextJob) |
| `reset_stuck_running` crash recovery | Rebuilt in Go (ResetStuckRunning) |
| WorkerPool single-dispatcher + slot cap | Planned Phase 5 worker pool (Go) |
| Per-task worktree | `agent_threads.workspace_path` (recorded); worktree creation deferred to Phase 5 |
| Persistent session_dir + `--continue` | `agent_threads.session_dir` + `runtime_session_id` (recorded); resume logic Phase 4 |
| env scrub at spawn | Rebuilt in Go at the OMP adapter spawn site (Phase 4); redaction at audit (this slice) |
| Host tools as audited privileged surface | Groundskeeper's audit + approvals *are* the privileged surface; host-tool shim Phase 4+ |
| Sidecar credential (HMAC) | Channel gateway roadmap (Phase 7+) |
| OMP RPC JSONL protocol | `internal/runtime` adapter (Phase 4); fake adapter in this slice |

## Why not vendor roboomp

roboomp is Python; Groundskeeper is Go. Vendoring would mean a Python runtime
dependency and a second persistence layer. Groundskeeper rebuilds the durable
parts in Go (reusing Agent Deck's `modernc.org/sqlite` + the statedb `Open`
pattern) and talks to OMP only via the stdio JSONL protocol, keeping the agent
runtime at arm's length.
