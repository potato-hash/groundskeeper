# Cron Scheduled Runs

Use scheduled runs only for bounded, budget-capped maintenance.

- Pin the command, cwd, environment, and max runtime.
- Do not run background work that competes with interactive model limits.
- Record scorecards for eval/gate jobs.
- Treat partial, timed-out, or rate-limited runs as failures.
