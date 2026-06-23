# Third-Party Notices

## Groundskeeper

Groundskeeper is licensed under the MIT License. See [LICENSE](LICENSE).

## Agent Deck

Groundskeeper is a derivative fork of **Agent Deck**
(https://github.com/asheshgoplani/agent-deck).

- License: MIT
- Copyright (c) 2025 Ashesh Goplani

The Agent Deck source tree (session manager, bubbletea TUI, web UI, CLI
dispatch, statedb persistence layer) is included in this repository largely
intact. The module path was renamed from `github.com/asheshgoplani/agent-deck`
to `github.com/potato-hash/groundskeeper`, the binary from `agent-deck` to
`groundskeeper`, and the XDG app directory from `agent-deck` to `groundskeeper`.
The original MIT license and this copyright notice are preserved as required by
the license.

See [docs/upstream-agent-deck-audit.md](docs/upstream-agent-deck-audit.md) for
the full audit of what was kept, renamed, wrapped, deferred, and where
Groundskeeper's own code attaches.

## roboomp / oh-my-pi (OMP)

Groundskeeper's worker-pool design and OMP RPC integration model are informed by
**roboomp** and **oh-my-pi (OMP)** (https://github.com/can1357/oh-my-pi).

- roboomp is the autonomous worker orchestration layer for OMP; its event-queue
  claim semantics, per-thread serialization, env-scrub, host-tool surface, and
  sidecar credential model are the design references for Groundskeeper's
  `internal/gkdb` job-claim and the planned OMP RPC adapter.
- OMP is the agent runtime that Groundskeeper's workers spawn (`omp --mode rpc`
  over stdio). Groundskeeper does not bundle OMP source; it invokes the
  installed `omp` binary as a subprocess.

See [docs/upstream-roboomp-audit.md](docs/upstream-roboomp-audit.md) for the
audit of the roboomp/OMP design that Groundskeeper's substrate is modeled on.

Groundskeeper is not affiliated with or endorsed by the Agent Deck, roboomp, or
OMP projects. Each upstream project retains its own license; this file records
provenance only.