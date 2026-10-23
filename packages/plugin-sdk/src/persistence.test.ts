import { documentStore } from "./persistence.js";
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
