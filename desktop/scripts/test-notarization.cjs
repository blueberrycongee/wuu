const assert = require("node:assert/strict");
const { test } = require("node:test");
const { mkdtempSync, writeFileSync, readFileSync, rmSync } = require("node:fs");
const { join } = require("node:path");
const { tmpdir } = require("node:os");

const id = "34de341a-df39-4d2f-bb26-d2e154f56ead";
const networkError = () => Object.assign(new Error("Command failed"), { stderr: "NSURLErrorDomain Code=-1009: offline" });

for (const scenario of [
  { name: "recovers network failures without resubmitting", replies: [networkError(), { id, status: "In Progress" }, { id, status: "Accepted" }] },
  { name: "rejects invalid submissions", replies: [{ id, status: "Invalid" }], error: /Invalid/ },
  { name: "fails closed after consecutive network failures", replies: [networkError(), networkError()], error: /network/i },
  { name: "does not retry authentication failures", replies: [Object.assign(new Error("Command failed"), { stderr: "HTTP status code: 401" })], error: /query/i },
  { name: "fails closed when processing exceeds the polling limit", replies: Array(4).fill({ id, status: "In Progress" }), error: /waiting/i },
  { name: "rejects a response for a different submission", replies: [{ id: "another-id", status: "Accepted" }], error: /submission/i },
]) {
  test(`notarization ${scenario.name}`, async () => {
    const { notarizeArchive } = require("./notarization.cjs");
    const dir = mkdtempSync(join(tmpdir(), "wuu-notary-test-"));
    const archive = join(dir, "app.zip");
    const receipt = join(dir, "receipt.json");
    writeFileSync(archive, "signed archive");
    const calls = [];
    const replies = [...scenario.replies];
    const run = (_command, args) => {
      calls.push(args);
      if (args[1] === "submit") return JSON.stringify({ id });
      assert.equal(args[1], "info");
      assert.equal(args[2], id);
      const reply = replies.shift();
      assert.ok(reply, "unexpected query retry");
      if (reply instanceof Error) throw reply;
      return JSON.stringify(reply);
    };
    try {
      const result = notarizeArchive(archive, receipt, { run, sleep: async () => {}, maxPolls: 4, maxNetworkErrors: 2 });
      if (scenario.error) await assert.rejects(result, scenario.error);
      else assert.equal((await result).status, "Accepted");
      assert.equal(calls.filter((args) => args[1] === "submit").length, 1);
      assert.equal(JSON.parse(readFileSync(receipt)).id, id);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
}

test("notarization resumes only the exact recorded archive", async () => {
  const { notarizeArchive } = require("./notarization.cjs");
  const dir = mkdtempSync(join(tmpdir(), "wuu-notary-resume-"));
  const archive = join(dir, "app.zip");
  const receipt = join(dir, "receipt.json");
  writeFileSync(archive, "signed archive");
  let submissions = 0;
  const run = (_command, args) => {
    if (args[1] === "submit") { submissions++; return JSON.stringify({ id }); }
    return JSON.stringify({ id, status: "Accepted" });
  };
  try {
    await notarizeArchive(archive, receipt, { run });
    await notarizeArchive(archive, receipt, { run });
    assert.equal(submissions, 1);
    writeFileSync(archive, "different signed archive");
    await assert.rejects(notarizeArchive(archive, receipt, { run }), /hash/i);
    assert.equal(submissions, 1);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("notarization never retries an upload with an unknown outcome", async () => {
  const { notarizeArchive } = require("./notarization.cjs");
  const dir = mkdtempSync(join(tmpdir(), "wuu-notary-upload-"));
  const archive = join(dir, "app.zip");
  writeFileSync(archive, "signed archive");
  let calls = 0;
  try {
    await assert.rejects(notarizeArchive(archive, join(dir, "receipt.json"), {
      run: () => { calls++; throw networkError(); },
    }), /upload/i);
    assert.equal(calls, 1);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("ticket stapling retries a lost CloudKit connection against the same app or DMG", async () => {
  const { stapleArtifact } = require("./notarization.cjs");
  for (const artifact of ["release/wuu.app", "release/wuu.dmg"]) {
    const calls = [];
    let waits = 0;
    await stapleArtifact(artifact, {
      run: (command, args) => {
        calls.push([command, ...args]);
        if (calls.length === 1) throw Object.assign(new Error("Command failed"), {
          status: 68, stdout: 'Error Domain=NSURLErrorDomain Code=-1005 "The network connection was lost."',
        });
        return "";
      },
      sleep: async () => { waits++; },
    });
    assert.deepEqual(calls, Array(2).fill(["xcrun", "stapler", "staple", artifact]));
    assert.equal(waits, 1);
  }
});

test("ticket stapling fails closed when transient errors exhaust the retry limit", async () => {
  const { stapleArtifact } = require("./notarization.cjs");
  const failure = Object.assign(new Error("stapler timed out"), { code: "ETIMEDOUT" });
  let calls = 0;
  let waits = 0;
  await assert.rejects(stapleArtifact("release/wuu.app", {
    run: () => { calls++; throw failure; },
    sleep: async () => { waits++; },
  }), (error) => error === failure);
  assert.equal(calls, 3);
  assert.equal(waits, 2);
});

test("ticket stapling does not retry invalid artifacts or unexplained CloudKit failures", async () => {
  const { stapleArtifact } = require("./notarization.cjs");
  for (const failure of [
    Object.assign(new Error("Invalid code signature"), { status: 65 }),
    Object.assign(new Error("CloudKit's response is inconsistent with expectations"), { status: 68 }),
  ]) {
    let calls = 0;
    await assert.rejects(stapleArtifact("release/wuu.app", {
      run: () => { calls++; throw failure; },
      sleep: async () => { assert.fail("permanent failures must not retry"); },
    }), (error) => error === failure);
    assert.equal(calls, 1);
  }
});

test("ticket stapling preserves final command diagnostics", async (t) => {
  const { stapleArtifact } = require("./notarization.cjs");
  const failure = Object.assign(new Error("Command failed"), {
    status: 68, stdout: "stapler service diagnostic\n", stderr: "ticket lookup failed\n",
  });
  const stdout = [];
  const stderr = [];
  t.mock.method(process.stdout, "write", (chunk) => { stdout.push(chunk); return true; });
  t.mock.method(process.stderr, "write", (chunk) => { stderr.push(chunk); return true; });
  await assert.rejects(stapleArtifact("release/wuu.app", {
    run: () => { throw failure; },
  }), (error) => error === failure);
  assert.equal(stdout.join(""), failure.stdout);
  assert.equal(stderr.join(""), failure.stderr);
});

test("accepted notarization resumes without uploading or querying Apple again", async () => {
  const { notarizeArchive } = require("./notarization.cjs");
  const dir = mkdtempSync(join(tmpdir(), "wuu-accepted-"));
  const archive = join(dir, "app.zip"), receipt = join(dir, "receipt.json");
  writeFileSync(archive, "signed archive");
  try {
    await notarizeArchive(archive, receipt, { run: (_cmd, args) => JSON.stringify(args[1] === "submit" ? { id } : { id, status: "Accepted" }) });
    await notarizeArchive(archive, receipt, { run: () => assert.fail("accepted archive must not contact Apple again") });
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("unknown upload outcome remains blocked across process restarts", async () => {
  const { notarizeArchive } = require("./notarization.cjs");
  const dir = mkdtempSync(join(tmpdir(), "wuu-unknown-"));
  const archive = join(dir, "app.zip"), receipt = join(dir, "receipt.json");
  writeFileSync(archive, "signed archive");
  try {
    await assert.rejects(notarizeArchive(archive, receipt, { run: () => { throw networkError(); } }), /upload/i);
    await assert.rejects(notarizeArchive(archive, receipt, { run: () => assert.fail("must not resubmit an ambiguous upload") }), /unknown/i);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
