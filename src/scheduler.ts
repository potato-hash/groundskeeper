// Scheduler — extracted from Espalier Core but not yet adapted to the
// Groundskeeper daemon. The original logic depended on Espalier's Kanban
// and Config types, which are not available here.
// ponytail: stub — reimplementation for the Groundskeeper daemon is Phase 0
// in docs/roadmap.md.

export type ScheduleInput = {
  kind: "nightly" | "session-end";
  tier2GateAttempts: number;
  substantiveSessionsSinceNudge: number;
  failureToSuccess: boolean;
};

export function queueScheduledRuns(_input: ScheduleInput): never {
  throw new Error("scheduler not yet adapted to Groundskeeper daemon — see docs/roadmap.md Phase 0");
}