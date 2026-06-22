import { createHash, randomUUID } from "node:crypto";

export type IdPrefix =
  | "session"
  | "turn"
  | "tool"
  | "jj"
  | "scorecard"
  | "lcm"
  | "payload"
  | "mem"
  | "task"
  | "trace"
  | "rollback"
  | "apply"
  | "oracle"
  | "proposal"
  | "reminder"
  | "job"
  | "approval"
  | "audit";

export function newId(prefix: IdPrefix): string {
  return `${prefix}_${randomUUID()}`;
}

export function hashText(text: string): string {
  return createHash("sha256").update(text).digest("hex");
}
