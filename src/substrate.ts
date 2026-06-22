// 24/7 personal assistant substrate — durable jobs, recurrence, approvals,
// audit log, /what-are-you-doing, notification policy, crash/retry/dead-letter.
// Extracted from Espalier Core — Groundskeeper owns the agent shell.

import { newId } from "./ids.js";
import { SqliteCli, sqlString } from "./sqlite.js";

export type AssistantJob = {
  id: string;
  title: string;
  cron?: string | undefined;
  status: "scheduled" | "running" | "complete" | "failed" | "dead_letter";
  retryCount: number;
  maxRetries: number;
  lastError?: string | undefined;
  nextRunAt?: string | undefined;
  createdAt: string;
  updatedAt: string;
};

export type ApprovalObject = {
  id: string;
  jobId: string;
  kind: "job_run" | "external_side_effect";
  status: "pending" | "approved" | "rejected" | "expired";
  approvedBy?: string | undefined;
  reason?: string | undefined;
  createdAt: string;
  resolvedAt?: string | undefined;
};

export type AuditEntry = {
  id: string;
  jobId?: string | undefined;
  action: string;
  actor: string;
  detail: string;
  timestamp: string;
};

export type NotificationPolicy = {
  channels: ("console")[];
  minSeverity: "info" | "warn" | "error";
  quietHours?: { start: string; end: string } | undefined;
};

const DEFAULT_NOTIFICATION_POLICY: NotificationPolicy = {
  channels: ["console"],
  minSeverity: "info",
};

type JobRow = { id: string; title: string; cron: string | null; status: string; retry_count: number; max_retries: number; last_error: string | null; next_run_at: string | null; created_at: string; updated_at: string };
type ApprovalRow = { id: string; job_id: string; kind: string; status: string; approved_by: string | null; reason: string | null; created_at: string; resolved_at: string | null };
type AuditRow = { id: string; job_id: string | null; action: string; actor: string; detail: string; timestamp: string };

export class AssistantSubstrate {
  private db: SqliteCli;

  constructor(dbPath: string) {
    this.db = new SqliteCli(dbPath);
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS assistant_jobs (
        id TEXT PRIMARY KEY,
        title TEXT NOT NULL,
        cron TEXT,
        status TEXT NOT NULL DEFAULT 'scheduled',
        retry_count INTEGER NOT NULL DEFAULT 0,
        max_retries INTEGER NOT NULL DEFAULT 3,
        last_error TEXT,
        next_run_at TEXT,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
      );
      CREATE TABLE IF NOT EXISTS assistant_approvals (
        id TEXT PRIMARY KEY,
        job_id TEXT NOT NULL,
        kind TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        approved_by TEXT,
        reason TEXT,
        created_at TEXT NOT NULL,
        resolved_at TEXT
      );
      CREATE TABLE IF NOT EXISTS assistant_audit (
        id TEXT PRIMARY KEY,
        job_id TEXT,
        action TEXT NOT NULL,
        actor TEXT NOT NULL,
        detail TEXT NOT NULL,
        timestamp TEXT NOT NULL
      );
    `);
  }

  scheduleJob(title: string, options: { cron?: string; maxRetries?: number } = {}): AssistantJob {
    const id = newId("job");
    const now = new Date().toISOString();
    const cron = options.cron;
    const maxRetries = options.maxRetries ?? 3;
    const nextRunAt = cron ? this.nextCronRun(cron) : undefined;
    this.db.exec(`INSERT INTO assistant_jobs (id, title, cron, status, retry_count, max_retries, next_run_at, created_at, updated_at) VALUES (${sqlString(id)}, ${sqlString(title)}, ${cron ? sqlString(cron) : "NULL"}, 'scheduled', 0, ${maxRetries}, ${nextRunAt ? sqlString(nextRunAt) : "NULL"}, ${sqlString(now)}, ${sqlString(now)})`);
    this.audit(id, "job_scheduled", "system", `Scheduled job: ${title}`);
    return { id, title, cron, status: "scheduled", retryCount: 0, maxRetries, nextRunAt, createdAt: now, updatedAt: now };
  }

  startJob(jobId: string): { ok: boolean; error?: string } {
    const job = this.getJob(jobId);
    if (!job) return { ok: false, error: `job ${jobId} not found` };
    if (job.status === "dead_letter") return { ok: false, error: `job ${jobId} is in dead-letter state` };
    const now = new Date().toISOString();
    this.db.exec(`UPDATE assistant_jobs SET status = 'running', updated_at = ${sqlString(now)} WHERE id = ${sqlString(jobId)}`);
    this.audit(jobId, "job_started", "system", `Started job: ${job.title}`);
    return { ok: true };
  }

  completeJob(jobId: string): { ok: boolean } {
    const now = new Date().toISOString();
    this.db.exec(`UPDATE assistant_jobs SET status = 'complete', updated_at = ${sqlString(now)} WHERE id = ${sqlString(jobId)}`);
    this.audit(jobId, "job_completed", "system", `Completed job ${jobId}`);
    return { ok: true };
  }

  failJob(jobId: string, error: string): { ok: boolean; deadLettered: boolean } {
    const job = this.getJob(jobId);
    if (!job) return { ok: false, deadLettered: false };
    const retryCount = job.retryCount + 1;
    const deadLettered = retryCount >= job.maxRetries;
    const now = new Date().toISOString();
    const status = deadLettered ? "dead_letter" : "failed";
    this.db.exec(`UPDATE assistant_jobs SET status = ${sqlString(status)}, retry_count = ${retryCount}, last_error = ${sqlString(error)}, updated_at = ${sqlString(now)} WHERE id = ${sqlString(jobId)}`);
    this.audit(jobId, deadLettered ? "job_dead_lettered" : "job_failed", "system", `${deadLettered ? "Dead-lettered" : "Failed"} job: ${error}`);
    return { ok: true, deadLettered };
  }

  retryJob(jobId: string): { ok: boolean; error?: string } {
    const job = this.getJob(jobId);
    if (!job) return { ok: false, error: `job ${jobId} not found` };
    if (job.status === "dead_letter") return { ok: false, error: `job ${jobId} is dead-lettered and cannot be retried` };
    const now = new Date().toISOString();
    this.db.exec(`UPDATE assistant_jobs SET status = 'scheduled', next_run_at = ${sqlString(now)}, updated_at = ${sqlString(now)} WHERE id = ${sqlString(jobId)}`);
    this.audit(jobId, "job_retried", "system", `Retried job after ${job.retryCount} failures`);
    return { ok: true };
  }

  getJob(jobId: string): AssistantJob | undefined {
    const rows = this.db.query<JobRow>(`SELECT * FROM assistant_jobs WHERE id = ${sqlString(jobId)}`);
    return rows.length === 0 ? undefined : fromJobRow(rows[0]!);
  }

  listJobs(status?: AssistantJob["status"]): AssistantJob[] {
    const sql = status
      ? `SELECT * FROM assistant_jobs WHERE status = ${sqlString(status)} ORDER BY created_at DESC`
      : `SELECT * FROM assistant_jobs ORDER BY created_at DESC`;
    return this.db.query<JobRow>(sql).map(fromJobRow);
  }

  requestApproval(jobId: string, kind: ApprovalObject["kind"], reason: string): ApprovalObject {
    const id = newId("approval");
    const now = new Date().toISOString();
    this.db.exec(`INSERT INTO assistant_approvals (id, job_id, kind, status, reason, created_at) VALUES (${sqlString(id)}, ${sqlString(jobId)}, ${sqlString(kind)}, 'pending', ${sqlString(reason)}, ${sqlString(now)})`);
    this.audit(jobId, "approval_requested", "system", `Approval requested: ${kind} — ${reason}`);
    return { id, jobId, kind, status: "pending", reason, createdAt: now };
  }

  resolveApproval(approvalId: string, approved: boolean, approvedBy: string): { ok: boolean; error?: string } {
    const now = new Date().toISOString();
    const status = approved ? "approved" : "rejected";
    // Check if the approval exists and is pending
    const rows = this.db.query<{ id: string }>(`SELECT id FROM assistant_approvals WHERE id = ${sqlString(approvalId)} AND status = 'pending'`);
    if (rows.length === 0) return { ok: false, error: `approval ${approvalId} not found or already resolved` };
    this.db.exec(`UPDATE assistant_approvals SET status = ${sqlString(status)}, approved_by = ${sqlString(approvedBy)}, resolved_at = ${sqlString(now)} WHERE id = ${sqlString(approvalId)}`);
    this.audit(undefined, "approval_resolved", approvedBy, `Approval ${approvalId}: ${status}`);
    return { ok: true };
  }

  listPendingApprovals(): ApprovalObject[] {
    return this.db.query<ApprovalRow>(`SELECT * FROM assistant_approvals WHERE status = 'pending' ORDER BY created_at DESC`).map(fromApprovalRow);
  }

  whatAreYouDoing(): { running: AssistantJob[]; pendingApprovals: number; deadLettered: AssistantJob[] } {
    return {
      running: this.listJobs("running"),
      pendingApprovals: this.listPendingApprovals().length,
      deadLettered: this.listJobs("dead_letter"),
    };
  }

  notify(jobId: string | undefined, severity: NotificationPolicy["minSeverity"], message: string, policy: NotificationPolicy = DEFAULT_NOTIFICATION_POLICY): { delivered: boolean; channels: string[] } {
    const severityRank = { info: 0, warn: 1, error: 2 };
    if (severityRank[severity] < severityRank[policy.minSeverity]) return { delivered: false, channels: [] };
    const channels: string[] = [];
    if (policy.channels.includes("console")) channels.push("console");
    this.audit(jobId, "notification", "system", `[${severity}] ${message}`);
    return { delivered: channels.length > 0, channels };
  }

  audit(jobId: string | undefined, action: string, actor: string, detail: string): void {
    const id = newId("audit");
    const timestamp = new Date().toISOString();
    this.db.exec(`INSERT INTO assistant_audit (id, job_id, action, actor, detail, timestamp) VALUES (${sqlString(id)}, ${jobId ? sqlString(jobId) : "NULL"}, ${sqlString(action)}, ${sqlString(actor)}, ${sqlString(detail)}, ${sqlString(timestamp)})`);
  }

  auditLog(limit = 100): AuditEntry[] {
    return this.db.query<AuditRow>(`SELECT * FROM assistant_audit ORDER BY timestamp DESC LIMIT ${limit}`).map(fromAuditRow);
  }

  // ponytail: naive cron parser — handles "0 9 * * *" style expressions only.
  private nextCronRun(cron: string): string {
    const parts = cron.split(/\s+/);
    const [minute, hour] = parts;
    const now = new Date();
    const next = new Date(now);
    if (minute && minute !== "*") next.setMinutes(Number(minute), 0, 0);
    if (hour && hour !== "*") next.setHours(Number(hour));
    if (next <= now) next.setDate(next.getDate() + 1);
    return next.toISOString();
  }
}

function fromJobRow(r: JobRow): AssistantJob {
  return { id: r.id, title: r.title, cron: r.cron ?? undefined, status: r.status as AssistantJob["status"], retryCount: r.retry_count, maxRetries: r.max_retries, lastError: r.last_error ?? undefined, nextRunAt: r.next_run_at ?? undefined, createdAt: r.created_at, updatedAt: r.updated_at };
}

function fromApprovalRow(r: ApprovalRow): ApprovalObject {
  return { id: r.id, jobId: r.job_id, kind: r.kind as ApprovalObject["kind"], status: r.status as ApprovalObject["status"], approvedBy: r.approved_by ?? undefined, reason: r.reason ?? undefined, createdAt: r.created_at, resolvedAt: r.resolved_at ?? undefined };
}

function fromAuditRow(r: AuditRow): AuditEntry {
  return { id: r.id, jobId: r.job_id ?? undefined, action: r.action, actor: r.actor, detail: r.detail, timestamp: r.timestamp };
}