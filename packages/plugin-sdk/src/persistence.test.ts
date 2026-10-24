import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { cp, copyFile, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { documentStore, documentStoreDescriptor, type StateExportPage, type StateExportPageRequest } from "./persistence.js";
import type { RuntimeHost } from "./index.js";

const calls: { method: string; params: unknown }[] = [];
const expectedRevision = "9007199254740993";
const expected = { revision: expectedRevision, documents: [{ namespace: "history", key: "x/02", value: null }], next_cursor: "x/02" };
const host = { call: async (method: string, params: unknown) => { calls.push({ method, params }); return expected; } } as RuntimeHost;
const store = documentStore(host);
const query = { namespace: "history", prefix: "x/", start: "x/01", end: "x/09", reverse: true, limit: 1, cursor: "x/03", at_revision: expectedRevision };
const view = await store.read(query);
if (JSON.stringify(calls) !== JSON.stringify([{ method: "host.service.call", params: { service: "state.documents", method: "read", params: query } }])) throw new Error("bounded query changed across SDK service boundary");
if (view.revision !== expectedRevision || view.next_cursor !== "x/02" || view.documents.length !== 1) throw new Error("page revision or continuation lost");
console.log("persistence bounded wire contract passed");

// Existing providers must retain full-export schemas until they opt in.
for (const options of [{}, { compact: true, import: true }, { paged_export: false }]) {
  const descriptor = documentStoreDescriptor(options);
  assert.equal(descriptor.methods?.find(method => method.name === "export")?.input_schema, "state.documents.export.input.v1");
  assert.equal(descriptor.methods?.find(method => method.name === "export")?.output_schema, "state.documents.export.output.v1");
}
const paged = documentStoreDescriptor({ paged_export: true, compact: true, import: true });
assert.deepEqual(paged.methods?.map(method => method.name), ["read", "commit", "export", "compact", "import"]);
assert.equal(paged.methods?.find(method => method.name === "export")?.input_schema, "state.documents.export.input.v2");
assert.equal(paged.methods?.find(method => method.name === "export")?.output_schema, "state.documents.export.output.v2");
assert.equal(paged.methods?.find(method => method.name === "export")?.preparation, true);
assert.equal(paged.methods?.find(method => method.name === "read")?.input_schema, "state.documents.read.input.v1");

const page: StateExportPage<null> = {
  format: "page.v1", revision: expectedRevision, generation: "generation-one", section: "documents",
  documents: [{ namespace: "notes", key: "\u{10000}", value: null }], receipts: [], done: false,
};
const exportCalls: unknown[] = [];
let response: unknown = page;
const failure = new Error("state_conflict");
const exportStore = documentStore({ call: async (method: string, params: unknown) => {
  exportCalls.push({ method, params });
  if (response === failure) throw failure;
  return response;
} } as RuntimeHost);
const first: StateExportPageRequest = { section: "documents", target_bytes: 1024, max_items: 2 };
assert.equal(await exportStore.exportPage<null>(first), page);
const continuation: StateExportPageRequest = {
  ...first, at_revision: page.revision, generation: page.generation,
  after: { namespace: "notes", key: "\u{10000}" },
};
assert.equal(await exportStore.exportPage<null>(continuation), page);
const receipts: StateExportPageRequest = {
  section: "receipts", at_revision: page.revision, generation: page.generation,
  after: { revision: "9007199254740992" }, target_bytes: 1024, max_items: 2,
};
await exportStore.exportPage(receipts);
const snapshot = { version: 1, revision: expectedRevision, documents: page.documents, receipts: [] };
response = snapshot;
assert.equal(await exportStore.export(), snapshot);
response = failure;
await assert.rejects(exportStore.exportPage(continuation), error => error === failure);
assert.deepEqual(exportCalls, [first, continuation, receipts, undefined, continuation].map(page => ({
  method: "host.service.call", params: { service: "state.documents", method: "export", params: page ? { page } : {} },
})), "export must preserve pins and decimal revisions without retry or full-export fallback");

// @ts-expect-error Receipt export must pin the same revision and generation.
const unpinnedReceipts: StateExportPageRequest = { section: "receipts", target_bytes: 1024, max_items: 2 };
// @ts-expect-error Document continuation must pin the revision and generation.
const unpinnedDocuments: StateExportPageRequest = { section: "documents", after: { namespace: "notes", key: "x" }, target_bytes: 1024, max_items: 2 };
const wrongCursor: StateExportPageRequest = {
  section: "receipts", at_revision: expectedRevision, generation: "generation-one", target_bytes: 1024, max_items: 2,
  // @ts-expect-error Document cursors cannot continue the receipt section.
  after: { namespace: "notes", key: "x" },
};
void [unpinnedReceipts, unpinnedDocuments, wrongCursor];
// @ts-expect-error A first document page must supply revision and generation together.
const partialPin: StateExportPageRequest = { section: "documents", at_revision: expectedRevision, target_bytes: 1024, max_items: 2 };
void partialPin;
console.log("persistence paged export wire contract passed");

// Exercise the external fixture through real JSONL, with only the public SDK
// installed beside it. Receipt bytes must survive export and a process restart.
const dir = await mkdtemp(join(tmpdir(), "wuu-paged-provider-"));
const installedSDK = join(dir, "node_modules/@wuu/plugin-sdk");
async function startProvider() {
  const child = spawn(process.execPath, ["--experimental-strip-types", "provider.ts"], {
    cwd: dir, env: { ...process.env, WUU_ACCEPTANCE_STATE: join(dir, "state.json") }, stdio: ["pipe", "pipe", "pipe"],
  });
  const exited = once(child, "exit");
  let stderr = "";
  child.stderr.on("data", data => { stderr += data; });
  const lines = createInterface({ input: child.stdout });
  const responses = lines[Symbol.asyncIterator]();
  const timer = setTimeout(() => child.kill(), 15000);
  let id = 0;
  async function request(method: string, params?: unknown) {
    const requestId = String(++id);
    child.stdin.write(JSON.stringify({ id: requestId, method, params }) + "\n");
    const line = await responses.next();
    assert.equal(line.done, false, stderr);
    const response = JSON.parse(line.value!);
    assert.equal(response.id, requestId);
    return response;
  }
  return {
    request,
    invoke: (method: string, params: unknown) => request("service.invoke", { service: "state.documents", method, params }),
    async shutdown() {
      assert.equal((await request("shutdown")).error, undefined);
      child.stdin.end();
      assert.equal((await exited)[0], 0, stderr);
    },
    async dispose() { clearTimeout(timer); child.kill(); lines.close(); await exited; },
  };
}
let provider: Awaited<ReturnType<typeof startProvider>> | undefined;
try {
  await mkdir(installedSDK, { recursive: true });
  await cp(new URL("./", import.meta.url), join(installedSDK, "dist"), { recursive: true });
  await copyFile(new URL("../package.json", import.meta.url), join(installedSDK, "package.json"));
  await copyFile(new URL("../../../scripts/replaceable-architecture-acceptance/fixtures/provider.ts", import.meta.url), join(dir, "provider.ts"));
  await writeFile(join(dir, "package.json"), JSON.stringify({ private: true, type: "module" }));
  provider = await startProvider();
  await provider.request("initialize", {});
  await provider.request("activate");
  const tx = {
    id: "html-<>&\u2028\u2029", expected_revision: "0", writes: [{ namespace: "n<&", key: "k>&", value: {
      html: "<>&\u2028\u2029", literal: "\\u003c", unicode: "中😀", normal: [true, null, 42],
    } }],
  };
  // encoding/json v1 escapes HTML and line separators, including object keys
  // and metadata strings. The literal backslash sequence is not an HTML rune.
  const canonical = String.raw`{"id":"html-\u003c\u003e\u0026\u2028\u2029","expected_revision":"0","writes":[{"namespace":"n\u003c\u0026","key":"k\u003e\u0026","value":{"html":"\u003c\u003e\u0026\u2028\u2029","literal":"\\u003c","unicode":"中😀","normal":[true,null,42]}}]}`;
  assert.deepEqual(JSON.parse(canonical), tx);
  const expectedDigest = createHash("sha256").update(canonical).digest("hex");
  const committed = await provider.invoke("commit", tx);
  assert.equal(committed.error, undefined);
  const full = (await provider.invoke("export", {})).result;
  const first = (await provider.invoke("export", { page: { section: "documents", target_bytes: 33554432, max_items: 10000 } })).result;
  const receiptPage = (await provider.invoke("export", { page: { section: "receipts", at_revision: first.revision, generation: first.generation, target_bytes: 1024, max_items: 1 } })).result;
  const invalid = [];
  for (const page of [
    { section: "documents", target_bytes: 33554433, max_items: 1 },
    { section: "documents", target_bytes: 1024, max_items: 10001 },
  ]) invalid.push((await provider.invoke("export", { page })).error?.code);
  await provider.shutdown(); await provider.dispose();
  provider = await startProvider();
  await provider.request("initialize", {}); await provider.request("activate");
  const retried = await provider.invoke("commit", tx);
  const changed = await provider.invoke("commit", { ...tx, writes: [{ ...tx.writes[0], value: "changed" }] });
  assert.deepEqual({
    fullDigest: full.receipts[0].digest, pageDigest: receiptPage.receipts[0].digest,
    document: first.documents[0], done: first.done, invalid,
    retried, changedCode: changed.error?.code,
  }, {
    fullDigest: expectedDigest, pageDigest: expectedDigest,
    document: tx.writes[0], done: true, invalid: ["invalid_request", "invalid_request"],
    retried: { id: retried.id, result: committed.result }, changedCode: "state_operation_conflict",
  });
  await provider.shutdown();
  console.log("external persistence canonical receipts and page limits passed");
} finally {
  await provider?.dispose();
  await rm(dir, { recursive: true, force: true });
}

// The optional checkpoint travels through the same public service binding.
const checkpointCalls: unknown[] = [];
const committedCheckpoint = { version: 1, revision: "1", documents_digest: "a".repeat(64), receipts_digest: "b".repeat(64) };
const checkpointStore = documentStore({ call: async (method: string, params: unknown) => { checkpointCalls.push({ method, params }); return committedCheckpoint; } } as unknown as RuntimeHost);
assert.deepEqual(documentStoreDescriptor({ checkpoint: true }).methods?.find(method => method.name === "checkpoint"), {
  name: "checkpoint", input_schema: "state.documents.checkpoint.input.v1", output_schema: "state.documents.checkpoint.output.v1", preparation: true,
});
assert.equal(await checkpointStore.checkpoint(), committedCheckpoint);
assert.deepEqual(checkpointCalls, [{ method: "host.service.call", params: { service: "state.documents", method: "checkpoint", params: {} } }]);
