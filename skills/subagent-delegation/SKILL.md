# Subagent Delegation

Use short fork/join delegation for bounded work.

- Use `delegate_task` for quick isolated checks with a clear return value.
- Use Kanban for named workers, retries, worktrees, review, human handoff, or long-lived state.
- Keep delegated tasks scoped to the current repo and current trust boundary.
- Record worker result or blocker before joining.
