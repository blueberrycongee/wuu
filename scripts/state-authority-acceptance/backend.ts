import { createHash, randomUUID } from "node:crypto";
import { closeSync, existsSync, fsyncSync, openSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { dirname, join } from "node:path";
import type { StateExportPageRequest, StateQuery, StateTransaction } from "@wuu/plugin-sdk";

// Acceptance backend, not a production database. Synchronous handlers serialize
// all writers in this one HTTP server; clients may be on independent hosts.
// Stable identity + data + receipts are published atomically on server storage.
const file = process.argv[2]!;
let storeID = randomUUID();
let state = { generation: randomUUID(), revision: "0", documents: [] as { namespace: string; key: string; value: unknown }[], receipts: [] as { id: string; revision: string; digest: string }[] };
function reject(code: string, message: string): never {
  throw Object.assign(new Error(message), { code });
}
function loadState() {
  if (!existsSync(file)) return;
  const saved = JSON.parse(readFileSync(file, "utf8"));
  storeID = saved.store_id ?? storeID;
  state = { ...saved, generation: saved.generation ?? state.generation };
}
const compare = (a: string, b: string) => Buffer.compare(Buffer.from(a), Buffer.from(b));

function publish(next: unknown) {
    const temporary = join(dirname(file), "state.pending");
    const fd = openSync(temporary, "w", 0o600);
    try { writeFileSync(fd, JSON.stringify(next)); fsyncSync(fd); } finally { closeSync(fd); }
    renameSync(temporary, file);
    const directory = openSync(dirname(file), "r");
    try { fsyncSync(directory); } finally { closeSync(directory); }
}
const lostReply = Symbol("durable commit response deliberately lost");
function invoke(method: string, params: unknown): unknown {
    if (method === "authority") return { version: 1, mode: "provider", store_id: storeID,
      storage_topology: "multi-host", execution_topology: "single-host", coordinator: storeID,
      cas: "store", durable_receipts: true };
    if (method === "read") {
      const query = params as StateQuery;
      const limit = query.limit ?? 0;
      if (!query.namespace.trim() || !Number.isInteger(limit) || limit < 0 || limit > 10000
        || (query.end && compare(query.start ?? "", query.end) > 0)
        || (query.cursor && query.at_revision === undefined)) reject("invalid_request", "invalid read selectors");
      if (query.at_revision !== undefined && query.at_revision !== state.revision) reject("state_conflict", "page revision changed");
      const matches = state.documents.filter(row => row.namespace === query.namespace
        && row.key.startsWith(query.prefix ?? "") && (query.keys === undefined || query.keys.includes(row.key))
        && (!query.start || compare(row.key, query.start) >= 0) && (!query.end || compare(row.key, query.end) < 0)
        && (!query.cursor || (query.reverse ? compare(row.key, query.cursor) < 0 : compare(row.key, query.cursor) > 0)))
        .sort((a, b) => query.reverse ? compare(b.key, a.key) : compare(a.key, b.key));
      const documents = limit ? matches.slice(0, limit) : matches;
      return { revision: state.revision, documents,
        ...(limit && matches.length > limit ? { next_cursor: documents[documents.length - 1].key } : {}) };
    }
    if (method === "export") {
      const page = (params as { page?: StateExportPageRequest }).page;
      if (page === undefined) return { version: 1, revision: state.revision, documents: state.documents, receipts: state.receipts };
      if (!page || (page.section !== "documents" && page.section !== "receipts")
        || !Number.isSafeInteger(page.target_bytes) || page.target_bytes <= 0 || page.target_bytes > 33554432
        || !Number.isSafeInteger(page.max_items) || page.max_items <= 0 || page.max_items > 10000
        || (page.at_revision !== undefined && (typeof page.at_revision !== "string" || !/^(0|[1-9]\d*)$/.test(page.at_revision)))
        || (page.generation !== undefined && (typeof page.generation !== "string" || !page.generation))
        || ((page.at_revision === undefined) !== (page.generation === undefined))
        || ((page.section === "receipts" || page.after !== undefined) && (page.at_revision === undefined || page.generation === undefined))) {
        reject("invalid_request", "invalid export page or missing revision and generation");
      }
      if (page.after !== undefined && (typeof page.after !== "object" || page.after === null
        || (page.section === "documents" ? typeof page.after.namespace !== "string" || !page.after.namespace.trim()
          || typeof page.after.key !== "string" || !page.after.key || "revision" in page.after
          : typeof page.after.revision !== "string" || !/^(0|[1-9]\d*)$/.test(page.after.revision)
            || BigInt(page.after.revision) > BigInt(page.at_revision) || "namespace" in page.after || "key" in page.after))) {
        reject("invalid_request", "invalid export cursor");
      }
      if (page.generation !== undefined && page.generation !== state.generation) reject("state_binding_changed", "export generation changed");
      if (page.at_revision !== undefined && page.at_revision !== state.revision) reject("state_conflict", "export revision changed");
      const result = { format: "page.v1", revision: state.revision, generation: state.generation, section: page.section,
        documents: [] as typeof state.documents, receipts: [] as typeof state.receipts, done: true };
      const { target_bytes, max_items } = page;
      if (Buffer.byteLength(JSON.stringify(result)) > target_bytes) reject("invalid_request", "export target cannot fit the page envelope");
      function takePage<T>(records: T[], items: T[]) {
        for (const [index, record] of records.entries()) {
          items.push(record);
          result.done = index === records.length - 1;
          const bytes = Buffer.byteLength(JSON.stringify(result));
          if (items.length > 1 && bytes > target_bytes) {
            items.pop(); result.done = false; return;
          }
          if (items.length >= max_items || bytes > target_bytes) return;
        }
      }
      if (page.section === "documents") {
        const after = page.after;
        takePage(state.documents.filter(row => !after || compare(row.namespace, after.namespace) > 0
          || (row.namespace === after.namespace && compare(row.key, after.key) > 0))
          .sort((a, b) => compare(a.namespace, b.namespace) || compare(a.key, b.key)), result.documents);
      } else {
        const after = page.after;
        takePage(state.receipts.filter(row => !after || BigInt(row.revision) > BigInt(after.revision))
          .sort((a, b) => BigInt(a.revision) < BigInt(b.revision) ? -1 : BigInt(a.revision) > BigInt(b.revision) ? 1 : 0), result.receipts);
      }
      return result;
    }
    if (method !== "commit") reject("method_not_found", "unknown method");
    const tx = params as StateTransaction;
    // Match Go encoding/json v1 HTML and line-separator escaping in receipts.
    const canonical = JSON.stringify(tx).replace(/[<>&\u2028\u2029]/g,
      char => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`);
    const digest = createHash("sha256").update(canonical).digest("hex");
    const prior = state.receipts.find(receipt => receipt.id === tx.id);
    if (prior) {
      if (prior.digest !== digest) reject("state_operation_conflict", "operation has different intent");
      return { id: prior.id, revision: prior.revision };
    }
    if (tx.expected_revision !== state.revision) reject("state_conflict", "revision changed");
    if (!tx.id || !tx.writes?.length) reject("invalid_request", "operation and writes are required");
    const documents = state.documents.slice();
    for (const write of tx.writes) {
      const index = documents.findIndex(row => row.namespace === write.namespace && row.key === write.key);
      if (index >= 0) documents.splice(index, 1);
      if (!write.delete) documents.push({ namespace: write.namespace, key: write.key, value: write.value });
    }
    const receipt = { id: tx.id, revision: (BigInt(state.revision) + 1n).toString() };
    const next = { store_id: storeID, generation: state.generation, revision: receipt.revision, documents, receipts: [...state.receipts, { ...receipt, digest }] };
    publish(next);
    state = next;
    return tx.id === "unknown-response" ? lostReply : receipt;
}
loadState();
// Provision the backend identity durably even before its first business write.
if (!existsSync(file)) publish({ ...state, store_id: storeID });
const server = createServer(async (request, response) => {
  try {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const params = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
    if (request.url === "/admin/import") {
      if (state.revision !== "0" || state.documents.length || state.receipts.length) reject("state_conflict", "import requires an empty stopped target");
      const snapshot = params.snapshot;
      if (!params.store_id || snapshot.version !== 1 || BigInt(snapshot.receipts.length) !== BigInt(snapshot.revision)) reject("invalid_request", "invalid complete transfer");
      storeID = params.store_id;
      state = { generation: randomUUID(), revision: snapshot.revision, documents: snapshot.documents, receipts: snapshot.receipts };
      publish({ ...state, store_id: storeID });
      response.end(JSON.stringify({ revision: state.revision }));
      return;
    }
    const method = (request.url ?? "/").slice(1);
    const expected = request.headers["x-documents-store-id"];
    if (expected !== undefined && expected !== storeID || method !== "authority" && expected === undefined) {
      reject("state_binding_changed", "captured dataset identity changed or is missing");
    }
    const result = invoke(method, params);
    if (result === lostReply) { response.destroy(); return; }
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify(result));
  } catch (error) {
    response.statusCode = 400;
    const failure = error as Error & { code?: string };
    response.end(JSON.stringify({ error: { code: failure.code ?? "state_io_error", message: failure.message } }));
  }
});
server.listen(0, "127.0.0.1", () => {
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("missing HTTP address");
  process.stdout.write(JSON.stringify({ url: `http://127.0.0.1:${address.port}` }) + "\n");
});
