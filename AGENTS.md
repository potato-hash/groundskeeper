# Groundskeeper Agent Instructions

Groundskeeper is the autonomous agent shell around OMP workers and Espalier Core.

Groundskeeper owns:
- durable task/job ledger
- scheduler and recurrence
- approvals inbox
- audit log for external actions
- notification policy
- channel gateway
- OMP RPC worker manager
- Agent Deck UI/fleet integration
- roboomp-style worker orchestration
- future email/calendar/reminder/contact sidecars

Groundskeeper does not own:
- provider login
- model registry
- coding tool runtime
- Pi/OMP session loop
- Espalier learning ledger
- Espalier learned routines
- Espalier jj/eval gates

Provider auth is delegated to OMP/Pi.
Espalier Core is loaded into OMP/Pi sessions as an extension.

Use jj as the source-of-truth workflow. Use Ponytail mode for development.