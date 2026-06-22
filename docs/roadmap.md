# Groundskeeper roadmap

Groundskeeper is the autonomous agent shell for Espalier + OMP.

## Phase 0 — Extracted substrate

- [x] Durable task/job ledger extracted from Espalier Core. (`src/substrate.ts`)
- [x] Approval objects extracted.
- [x] Audit log extracted.
- [ ] Scheduler/recurrence extracted. (Schedule logic moved but not yet adapted to Groundskeeper daemon)
- [ ] Notification policy extracted.
- [ ] CLI status command.

## Phase 1 — OMP worker manager

- [ ] Launch `omp --mode rpc`.
- [ ] Attach Espalier Core extension.
- [ ] Stream events.
- [ ] Persist worker/session refs.
- [ ] Restart stuck workers.
- [ ] Dead-letter failed jobs.

## Phase 2 — Agent threads and loops

- [ ] AgentThread table.
- [ ] LoopSpec table.
- [ ] turn queue.
- [ ] stop conditions.
- [ ] max turns/cost/wall-time/tool-call budgets.
- [ ] fork/resume/archive semantics.

## Phase 3 — Agent Deck integration

- [ ] Evaluate Agent Deck fork.
- [ ] Map Groundskeeper AgentThread to Agent Deck session.
- [ ] Add approvals/status views.
- [ ] Add worker/fleet control.

## Phase 4 — roboomp integration pattern

- [ ] Port/adapt roboomp worker pool pattern.
- [ ] Per-task session directories.
- [ ] Per-task workspaces/worktrees.
- [ ] host tools and `pa://` resources.
- [ ] sidecar trust boundaries.
- [ ] crash recovery.

## Phase 5 — Channels and sidecars

- [ ] local notifications
- [ ] email draft/send sidecar
- [ ] calendar sidecar
- [ ] reminders sidecar
- [ ] contacts/identity graph
- [ ] channel gateway