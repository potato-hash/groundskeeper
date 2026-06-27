# Skill Write Gate

Gate proposed skill changes before they become active.

- Classify the edit as Tier 1 only when it touches `skills/`, `prompts/`, or `config/`.
- Run focused tests/evals tied to the failure the skill claims to fix.
- Run Semgrep through MCP before promoting changes that touch subprocesses, secrets, auth, network, deletion, or broker calls.
- Record hypothesis, change id, scorecard ref, and decision.
- Abandon or hold when the eval is interrupted, incomplete, or ambiguous.
