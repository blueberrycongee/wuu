import { createHash } from "node:crypto";
import { closeSync, existsSync, fsyncSync, openSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { runJSONLRuntime, RuntimeHostServiceError, type RuntimePlugin, type StateQuery, type StateTransaction } from "@wuu/plugin-sdk";

// A deliberately small external fixture, not a production backend. One SDK
// runtime serializes calls; the atomic file preserves receipts across restart.
const file = process.env.WUU_ACCEPTANCE_STATE!;
let state = { revision: "0", documents: [] as { namespace: string; key: string; value: unknown }[], receipts: [] as { id: string; revision: string; digest: string }[] };
function reject(code: string, message: string): never {
  throw new RuntimeHostServiceError({ code, message });
}
const plugin: RuntimePlugin = {
  initialize() {
    return { protocol_version: 3, lifecycle_version: 2, provided_services: [{ name: "state.documents", version: "1.0.0", methods: [
      { name: "read", input_schema: "state.documents.read.input.v1", output_schema: "state.documents.read.output.v1", cleanup: true },
      { name: "commit", input_schema: "state.documents.commit.input.v1", output_schema: "state.documents.commit.output.v1", cleanup: true },
      { name: "export", input_schema: "state.documents.export.input.v1", output_schema: "state.documents.export.output.v1" },
    ] }] };
  },
  activate() { if (existsSync(file)) state = JSON.parse(readFileSync(file, "utf8")); },
  invokeService({ method, params }) {
    if (method === "read") {
      const query = params as StateQuery;
      if (!query.namespace) reject("invalid_request", "namespace is required");
      return { revision: state.revision, documents: state.documents.filter(row => row.namespace === query.namespace
        && row.key.startsWith(query.prefix ?? "") && (query.keys === undefined || query.keys.includes(row.key)))
        .sort((a, b) => Buffer.compare(Buffer.from(a.key), Buffer.from(b.key))) };
    }
    if (method === "export") return { version: 1, ...state };
    if (method !== "commit") reject("method_not_found", "unknown method");
    const tx = params as StateTransaction;
    const digest = createHash("sha256").update(JSON.stringify(tx)).digest("hex");
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
    const next = { revision: receipt.revision, documents, receipts: [...state.receipts, { ...receipt, digest }] };
    const temporary = join(dirname(file), "state.pending");
    const fd = openSync(temporary, "w", 0o600);
    try { writeFileSync(fd, JSON.stringify(next)); fsyncSync(fd); } finally { closeSync(fd); }
    renameSync(temporary, file);
    const directory = openSync(dirname(file), "r");
    try { fsyncSync(directory); } finally { closeSync(directory); }
    state = next;
    return receipt;
  },
};
await runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout });
