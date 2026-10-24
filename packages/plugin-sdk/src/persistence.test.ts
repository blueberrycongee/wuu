import assert from "node:assert/strict";
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
