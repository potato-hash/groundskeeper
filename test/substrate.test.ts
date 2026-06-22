import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { AssistantSubstrate, type NotificationPolicy } from "../src/substrate.js";

function fixture(): { substrate: AssistantSubstrate; tmp: string } {
  const tmp = mkdtempSync(join(tmpdir(), "groundskeeper-"));
  const substrate = new AssistantSubstrate(join(tmp, "assistant.sqlite"));
  return { substrate, tmp };
}

test("scheduleJob creates a durable job with recurrence", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("Daily eval batch", { cron: "0 9 * * *", maxRetries: 5 });
    assert.equal(job.status, "scheduled");
    assert.equal(job.cron, "0 9 * * *");
    assert.equal(job.maxRetries, 5);
    assert.ok(job.nextRunAt);
    const retrieved = substrate.getJob(job.id);
    assert.ok(retrieved);
    assert.equal(retrieved?.title, "Daily eval batch");
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("startJob transitions to running and completeJob finishes", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("One-off task");
    assert.equal(substrate.startJob(job.id).ok, true);
    assert.equal(substrate.getJob(job.id)?.status, "running");
    assert.equal(substrate.completeJob(job.id).ok, true);
    assert.equal(substrate.getJob(job.id)?.status, "complete");
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("failJob increments retry and dead-letters after max retries", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("Failing task", { maxRetries: 2 });
    const r1 = substrate.failJob(job.id, "network error");
    assert.equal(r1.deadLettered, false);
    assert.equal(substrate.getJob(job.id)?.status, "failed");
    assert.equal(substrate.getJob(job.id)?.retryCount, 1);
    const r2 = substrate.failJob(job.id, "network error again");
    assert.equal(r2.deadLettered, true);
    assert.equal(substrate.getJob(job.id)?.status, "dead_letter");
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("retryJob reschedules a failed job but refuses dead-lettered jobs", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("Retryable task", { maxRetries: 2 });
    substrate.failJob(job.id, "timeout");
    assert.equal(substrate.retryJob(job.id).ok, true);
    assert.equal(substrate.getJob(job.id)?.status, "scheduled");
    // Exhaust retries to dead-letter
    substrate.failJob(job.id, "timeout");
    substrate.failJob(job.id, "timeout");
    const retryResult = substrate.retryJob(job.id);
    assert.equal(retryResult.ok, false);
    assert.match(retryResult.error ?? "", /dead-lettered/);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("requestApproval and resolveApproval enforce operator gate", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("External reminder");
    const approval = substrate.requestApproval(job.id, "external_side_effect", "send reminder via external side effect");
    assert.equal(approval.status, "pending");
    assert.equal(substrate.listPendingApprovals().length, 1);
    assert.equal(substrate.resolveApproval(approval.id, true, "operator").ok, true);
    assert.equal(substrate.listPendingApprovals().length, 0);
    // Cannot resolve twice
    const second = substrate.resolveApproval(approval.id, false, "operator");
    assert.equal(second.ok, false);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("whatAreYouDoing reports running jobs, pending approvals, and dead-lettered jobs", () => {
  const { substrate, tmp } = fixture();
  try {
    const j1 = substrate.scheduleJob("Running task");
    substrate.startJob(j1.id);
    const j2 = substrate.scheduleJob("Dead task", { maxRetries: 1 });
    substrate.failJob(j2.id, "crash");
    const j3 = substrate.scheduleJob("Approval task");
    substrate.requestApproval(j3.id, "job_run", "needs approval");
    const status = substrate.whatAreYouDoing();
    assert.equal(status.running.length, 1);
    assert.equal(status.pendingApprovals, 1);
    assert.equal(status.deadLettered.length, 1);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("audit log records all actions", () => {
  const { substrate, tmp } = fixture();
  try {
    const job = substrate.scheduleJob("Audited task");
    substrate.startJob(job.id);
    substrate.completeJob(job.id);
    const log = substrate.auditLog();
    assert.ok(log.length >= 3);
    assert.ok(log.some((e) => e.action === "job_scheduled"));
    assert.ok(log.some((e) => e.action === "job_started"));
    assert.ok(log.some((e) => e.action === "job_completed"));
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("notify respects severity threshold and channels", () => {
  const { substrate, tmp } = fixture();
  try {
    const policy: NotificationPolicy = { channels: ["console"], minSeverity: "warn" };
    const info = substrate.notify(undefined, "info", "low priority", policy);
    assert.equal(info.delivered, false);
    const warn = substrate.notify(undefined, "warn", "important", policy);
    assert.equal(warn.delivered, true);
    assert.ok(warn.channels.includes("console"));
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("jobs persist across substrate instances (durable ledger)", () => {
  const tmp = mkdtempSync(join(tmpdir(), "groundskeeper-durable-"));
  try {
    const s1 = new AssistantSubstrate(join(tmp, "assistant.sqlite"));
    const job = s1.scheduleJob("Persistent task");
    const s2 = new AssistantSubstrate(join(tmp, "assistant.sqlite"));
    const retrieved = s2.getJob(job.id);
    assert.ok(retrieved);
    assert.equal(retrieved?.title, "Persistent task");
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});