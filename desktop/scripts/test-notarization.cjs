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
