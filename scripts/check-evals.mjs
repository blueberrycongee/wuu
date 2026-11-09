#!/usr/bin/env node

import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, realpathSync, statSync } from "node:fs";
import { dirname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const resultsRoot = resolve(repoRoot, "evals/results");

if (process.argv.includes("--self-test")) {
  selfTest();
} else {
  const records = findResultRecords(resultsRoot);
  for (const recordPath of records) validateRecordFile(recordPath);
  console.log(`public eval records valid: ${records.length}`);
}

function findResultRecords(root) {
  const records = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const path = resolve(root, entry.name);
    if (entry.isDirectory()) records.push(...findResultRecords(path));
    if (entry.isFile() && entry.name === "result.json") records.push(path);
  }
  return records.sort();
}

function validateRecordFile(recordPath) {
  let record;
  try {
    record = JSON.parse(readFileSync(recordPath, "utf8"));
  } catch (error) {
    fail(`${relative(repoRoot, recordPath)}: invalid JSON: ${error.message}`);
  }
  validateRecord(record, relative(repoRoot, recordPath));

  const recordDir = dirname(recordPath);
  const realRecordDir = realpathSync(recordDir);
  for (const artifact of record.artifacts) {
    const artifactPath = resolve(recordDir, artifact.path);
    if (!existsSync(artifactPath) || !statSync(artifactPath).isFile()) {
      fail(`${relative(repoRoot, recordPath)}: missing artifact ${artifact.path}`);
    }
    const realArtifact = realpathSync(artifactPath);
    if (realArtifact !== realRecordDir && !realArtifact.startsWith(`${realRecordDir}${sep}`)) {
      fail(`${relative(repoRoot, recordPath)}: artifact escapes its result directory: ${artifact.path}`);
    }
    const digest = createHash("sha256").update(readFileSync(realArtifact)).digest("hex");
    if (digest !== artifact.sha256) {
      fail(`${relative(repoRoot, recordPath)}: checksum mismatch for ${artifact.path}`);
    }
  }
}

function validateRecord(record, label) {
  requireObject(record, label);
  requireEqual(record.schema_version, "1.0", `${label}.schema_version`);
  requireString(record.id, `${label}.id`, /^[a-z0-9][a-z0-9._-]+$/);
  requireString(record.title, `${label}.title`);

  requireObject(record.case, `${label}.case`);
  requireString(record.case.id, `${label}.case.id`);
  requireString(record.case.source, `${label}.case.source`);
  requireString(record.case.prompt_artifact, `${label}.case.prompt_artifact`);

  requireObject(record.run, `${label}.run`);
  requireDate(record.run.started_at, `${label}.run.started_at`);
  requireDate(record.run.finished_at, `${label}.run.finished_at`);
  requireString(record.run.command, `${label}.run.command`);
  requireString(record.run.wuu_version, `${label}.run.wuu_version`);
  requireString(record.run.wuu_commit, `${label}.run.wuu_commit`, /^[0-9a-f]{7,40}$/);
  requireEqual(record.run.dirty, false, `${label}.run.dirty`);

  requireObject(record.environment, `${label}.environment`);
  requireString(record.environment.os, `${label}.environment.os`);
  requireString(record.environment.arch, `${label}.environment.arch`);

  requireArray(record.models, `${label}.models`, 1);
  for (const [index, model] of record.models.entries()) {
    requireObject(model, `${label}.models[${index}]`);
    requireString(model.provider, `${label}.models[${index}].provider`);
    requireString(model.model, `${label}.models[${index}].model`);
    requireObject(model.settings, `${label}.models[${index}].settings`);
  }

  requireObject(record.outcome, `${label}.outcome`);
  if (!new Set(["passed", "failed", "error"]).has(record.outcome.status)) {
    fail(`${label}.outcome.status: expected passed, failed, or error`);
  }
  requireNumber(record.outcome.score, `${label}.outcome.score`, 0, 1);
  requireString(record.outcome.grader, `${label}.outcome.grader`);

  requireObject(record.metrics, `${label}.metrics`);
  for (const field of ["duration_ms", "input_tokens", "output_tokens"]) {
    requireInteger(record.metrics[field], `${label}.metrics.${field}`, 0);
  }
  if (record.metrics.cost_usd !== null) {
    requireNumber(record.metrics.cost_usd, `${label}.metrics.cost_usd`, 0);
  }
  requireString(record.metrics.cost_source, `${label}.metrics.cost_source`);

  requireArray(record.artifacts, `${label}.artifacts`, 2);
  const artifactPaths = new Set();
  for (const [index, artifact] of record.artifacts.entries()) {
    requireObject(artifact, `${label}.artifacts[${index}]`);
    requireString(artifact.path, `${label}.artifacts[${index}].path`);
    if (artifact.path.startsWith("/") || artifact.path.split(/[\\/]/).includes("..")) {
      fail(`${label}.artifacts[${index}].path: must stay inside the result directory`);
    }
    if (artifactPaths.has(artifact.path)) fail(`${label}: duplicate artifact ${artifact.path}`);
    artifactPaths.add(artifact.path);
    requireString(artifact.sha256, `${label}.artifacts[${index}].sha256`, /^[0-9a-f]{64}$/);
    requireString(artifact.kind, `${label}.artifacts[${index}].kind`);
  }
  if (!artifactPaths.has(record.case.prompt_artifact)) {
    fail(`${label}.case.prompt_artifact: must reference a listed artifact`);
  }

  requireObject(record.disclosure, `${label}.disclosure`);
  requireEqual(record.disclosure.contains_secrets, false, `${label}.disclosure.contains_secrets`);
  requireEqual(record.disclosure.contains_personal_data, false, `${label}.disclosure.contains_personal_data`);
  requireArray(record.disclosure.redactions, `${label}.disclosure.redactions`, 0);
  requireArray(record.limitations, `${label}.limitations`, 1);
  record.limitations.forEach((value, index) => requireString(value, `${label}.limitations[${index}]`));
}

function selfTest() {
  const valid = {
    schema_version: "1.0",
    id: "self-test",
    title: "validator self-test",
    case: { id: "case", source: "self-test", prompt_artifact: "prompt.md" },
    run: {
      started_at: "2026-01-01T00:00:00Z",
      finished_at: "2026-01-01T00:00:01Z",
      command: "wuu eval",
      wuu_version: "v0.0.0",
      wuu_commit: "abcdef0",
      dirty: false
    },
    environment: { os: "test", arch: "test" },
    models: [{ provider: "test", model: "test", settings: {} }],
    outcome: { status: "passed", score: 1, grader: "test" },
    metrics: { duration_ms: 1, input_tokens: 0, output_tokens: 0, cost_usd: null, cost_source: "unavailable" },
    artifacts: [
      { path: "prompt.md", sha256: "0".repeat(64), kind: "prompt" },
      { path: "report.json", sha256: "1".repeat(64), kind: "raw_report" }
    ],
    disclosure: { contains_secrets: false, contains_personal_data: false, redactions: [] },
    limitations: ["self-test only"]
  };
  validateRecord(valid, "self-test");
  let rejected = false;
  try {
    validateRecord({ ...valid, run: { ...valid.run, dirty: true } }, "self-test-invalid");
  } catch {
    rejected = true;
  }
  if (!rejected) fail("self-test: dirty record was accepted");
  console.log("public eval validator self-test passed");
}

function requireObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) fail(`${label}: expected object`);
}

function requireArray(value, label, minimum) {
  if (!Array.isArray(value) || value.length < minimum) fail(`${label}: expected at least ${minimum} items`);
}

function requireString(value, label, pattern) {
  if (typeof value !== "string" || value.trim() === "") fail(`${label}: expected non-empty string`);
  if (pattern && !pattern.test(value)) fail(`${label}: invalid format`);
}

function requireDate(value, label) {
  requireString(value, label);
  if (Number.isNaN(Date.parse(value))) fail(`${label}: invalid date-time`);
}

function requireInteger(value, label, minimum) {
  if (!Number.isInteger(value) || value < minimum) fail(`${label}: expected integer >= ${minimum}`);
}

function requireNumber(value, label, minimum, maximum = Infinity) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < minimum || value > maximum) {
    fail(`${label}: expected number between ${minimum} and ${maximum}`);
  }
}

function requireEqual(value, expected, label) {
  if (value !== expected) fail(`${label}: expected ${JSON.stringify(expected)}`);
}

function fail(message) {
  throw new Error(message);
}
