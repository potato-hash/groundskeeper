# Groundskeeper

Groundskeeper tends the day; Espalier trains the agent.

Groundskeeper is the autonomous agent shell for Espalier + OMP. It owns durable tasks, approvals, scheduling, channels, and worker orchestration.

## Architecture

```
Groundskeeper daemon
  ├─ task/job DB
  ├─ scheduler
  ├─ approval inbox
  ├─ audit log
  ├─ notification policy
  ├─ channel adapters
  ├─ OMP worker manager
  └─ Espalier Core extension loaded in workers
```

## Install

```sh
npm ci
npm run build
```

## Roadmap

See `docs/roadmap.md` for the current product roadmap.

## Future bases

Agent Deck will be evaluated/forked for session fleet UI.
roboomp will be used as the reference for OMP RPC worker orchestration, sidecar boundaries, and crash recovery.

These are not implemented yet.