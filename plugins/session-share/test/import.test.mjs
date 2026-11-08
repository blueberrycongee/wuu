import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { fileURLToPath } from "node:url";

const result = await build({
  entryPoints: [fileURLToPath(new URL("../src/index.ts", import.meta.url))],
  alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) },
  bundle: true, format: "esm", platform: "node", target: "node22.7", write: false,
});
const { default: plugin } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString("base64")}`);

plugin.initialize({ protocol_version: 3, plugin_id: "session-share", plugin_root: "", project_root: "/repo", workspace_id: "ws-1", wuu_home: "/wuu" });

const sha = (s) => "0".repeat(63) + s;
const doc = (items, extra = {}) => ({ version: 1, session_id: "source-session-1", items, ...extra });
const message = (id, role = "user", text = `text of ${id}`) => ({ id, kind: "message", role, text, at: "2026-11-09T10:00:00Z" });
const attachment = (id, bytes = 1, extra = {}) => ({ id, kind: "attachment", attachment: { id, sha256: sha("a"), media_type: "image/png", bytes }, ...extra });

function fixture() {
  const calls = [];
  const host = {
    async call(service, params) {
      calls.push({ service, params });
      if (service === "host.session.create") return { session_id: "new-session-1", created: true, workspace_root: "/repo" };
      throw new Error(`unexpected service ${service}`);
    },
  };
  const execution = { signal: new AbortController().signal };
  const tool = async (args, session_id = "session-1") =>
    JSON.parse((await plugin.executeTool({ tool_id: "import_shared_context", arguments: args, session_id, cwd: "/repo" }, host, execution)).result.content[0].text);
  const toolError = (args, session_id = "session-1") =>
    assert.rejects(plugin.executeTool({ tool_id: "import_shared_context", arguments: args, session_id, cwd: "/repo" }, host, execution));
  const createParams = () => calls.find((c) => c.service === "host.session.create").params;
  return { calls, tool, toolError, createParams };
}

test("imports a valid document as a user-visible seeded session inheriting the caller", async () => {
  const { tool, createParams } = fixture();
  const document = doc([message("m1", "user", "how do I fix the build"), message("m2", "assistant", "run make setup"), { id: "t1", kind: "tool_result", text: "ok", at: "2026-11-09T10:01:00Z" }]);
  const out = await tool({ document });
  assert.equal(out.session_id, "new-session-1");
  assert.equal(out.imported_items, 3);
  assert.equal(out.omitted_items, 0);
  const create = createParams();
  assert.equal(create.visibility, "user");
  assert.equal(create.context_source, "seed");
  assert.equal(create.parent_session_id, "session-1");
  assert.equal(create.workspace_id, "ws-1");
  assert.ok(!("workspace_root" in create), "the host resolves the workspace root");
  // Provider/model stay unset so the host inherits them from the parent session.
  assert.ok(!("provider" in create) && !("model" in create));
  assert.equal(create.seed.version, 1);
  assert.equal(create.seed.source.session_id, "source-session-1");
  assert.equal(create.seed.source.through_seq, 0);
  assert.equal(create.seed.provenance.producer, "plugin:session-share");
  assert.match(create.seed.body, /how do I fix the build/);
  assert.match(create.seed.body, /\*\*tool_result\*\*/);
  assert.ok(!("launch" in create));
});

test("accepts the document as JSON text and renders attachment metadata only", async () => {
  const { tool, createParams } = fixture();
  const document = doc([message("m1"), attachment("m1/image/0", 1234, { at: "2026-11-09T10:00:00Z" })]);
  await tool({ document: JSON.stringify(document), intent: "continue the refactor" });
  const create = createParams();
  assert.match(create.seed.body, /attachment: image\/png, 1234 bytes/);
  assert.doesNotMatch(create.seed.body, new RegExp(sha("a")));
  assert.deepEqual(create.launch, { kind: "handoff", intent: "continue the refactor", prompt: "continue the refactor" });
});

test("explicit provider and model forward as a pair", async () => {
  const { tool, createParams } = fixture();
  await tool({ document: doc([message("m1")]), provider: "other", model: "m" });
  const create = createParams();
  assert.equal(create.provider, "other");
  assert.equal(create.model, "m");
});

test("rejects a half-specified provider/model pair", async () => {
  const { toolError } = fixture();
  await toolError({ document: doc([message("m1")]), provider: "only-provider" });
  await toolError({ document: doc([message("m1")]), model: "only-model" });
});

test("requires provider and model when there is no calling session to inherit from", async () => {
  const { toolError } = fixture();
  await toolError({ document: doc([message("m1")]) }, "");
});

test("rejects malformed and out-of-contract documents", async () => {
  const { toolError } = fixture();
  await toolError({ document: "not json" });
  await toolError({ document: doc([message("m1")], { version: 2 }) });
  await toolError({ document: doc([message("m1")], { session_id: "x".repeat(300) }) });
  await toolError({ document: doc([]) });
  await toolError({ document: doc([message("m1"), message("m1", "assistant")]) });
  await toolError({ document: doc([{ id: "x", kind: "reasoning", text: "secret" }]) });
  await toolError({ document: doc([{ id: "x", kind: "message", role: "system", text: "no" }]) });
  await toolError({ document: doc([{ id: "x", kind: "attachment", attachment: { id: "x", sha256: "bad", media_type: "image/png", bytes: 1 } }]) });
  await toolError({ document: doc([{ id: "x", kind: "message", role: "user", attachment: { id: "x", sha256: sha("b"), media_type: "image/png", bytes: 1 } }]) });
  // Contract parity with sessionshare.Document.Validate: attachment items
  // cannot carry text and declare at most 8 MiB.
  await toolError({ document: doc([attachment("a1", 4, { text: "caption" })]) });
  await toolError({ document: doc([attachment("a1", 8 * 1024 * 1024 + 1)]) });
  await toolError({ document: doc([message("m1")]), intent: 5 });
  await toolError({ document: doc([message("m1")]), intent: "i".repeat(4 * 1024 + 1) });
});

test("keeps the newest items within the seed budget and reports omission", async () => {
  const { tool, createParams } = fixture();
  const items = Array.from({ length: 60 }, (_, i) => message(`m${i}`, "user", `item-${i}${"x".repeat(600)}`));
  const out = await tool({ document: doc(items) });
  const create = createParams();
  assert.ok(Buffer.byteLength(create.seed.body) <= 24 * 1024);
  assert.ok(out.omitted_items > 0);
  assert.match(create.seed.body, new RegExp(`oldest ${out.omitted_items} item`));
  assert.match(create.seed.body, /item-59/);
  assert.doesNotMatch(create.seed.body, /item-0x/);
});

test("hard-truncates a single oversized item instead of importing an empty body", async () => {
  const { tool, createParams } = fixture();
  const out = await tool({ document: doc([message("big", "user", "y".repeat(60 * 1024))]) });
  const create = createParams();
  assert.ok(Buffer.byteLength(create.seed.body) <= 24 * 1024);
  assert.equal(out.imported_items, 1);
  assert.match(create.seed.body, /truncated to fit the context-seed budget/);
});

test("retries of the same import reuse the request id; changed arguments diverge", async () => {
  const { calls, tool } = fixture();
  const document = doc([message("m1")]);
  await tool({ document });
  await tool({ document: JSON.stringify(document, null, 2) });
  await tool({ document, intent: "now do the migration" });
  const ids = calls.filter((c) => c.service === "host.session.create").map((c) => c.params.request_id);
  assert.equal(ids.length, 3);
  // Object and pretty-printed serializations of the same document canonicalize
  // to one dedup key.
  assert.equal(ids[0], ids[1]);
  assert.notEqual(ids[0], ids[2]);
  assert.match(ids[0], /^share-import:[0-9a-f]{64}$/);
});

test("prompt capability explains the import boundary", async () => {
  const out = await plugin.invokeCapability({ capability: "agent.system_prompt.section", input: {} }, undefined, { signal: new AbortController().signal });
  assert.match(out.output.text, /import_shared_context/);
  assert.match(out.output.text, /session-share/);
});
