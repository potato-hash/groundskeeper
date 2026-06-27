# Programmatic Tool Calling

Prefer programmatic checks over model judgment.

- Use stable tool-call ids and capture start/update/end with args, result, and error state.
- Treat interrupted tool calls as incomplete.
- Keep tool arguments and outputs redacted before persistence.
- Use property or LLM-judge checks only when a programmatic oracle is not practical.
