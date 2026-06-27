# Dangerous Command Approval

Require explicit human approval for destructive or host-privileged commands.

- Run subprocesses through Espalier's bounded command runner: no shell, timeout, output cap, redaction.
- Refuse broad deletion, credential exfiltration, firewall/security toggles, or arbitrary host execution.
- Ask for confirmation only when the safe target and blast radius are clear.
- Never bypass broker capability boundaries.
- Log the refusal/approval decision without secrets.
