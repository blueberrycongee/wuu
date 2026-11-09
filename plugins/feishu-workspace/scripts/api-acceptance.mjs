// Process-level acceptance of the generated plugin and public host-service wire.
// All API responses are synthetic. This script never opens a network connection.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";
import { once } from "node:events";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const output = resolve(process.env.WUU_FEISHU_E2E_OUTPUT || join(root, ".tmp/e2e/feishu-workspace"));
await mkdir(output, { recursive: true });
const child = spawn(process.execPath, [join(root, "plugins/feishu-workspace/package/runtime.mjs")], { cwd: root, stdio: ["pipe", "pipe", "pipe"] });
const closed = once(child, "exit");
const pending = new Map(), frames = [], cases = [];
let sequence = 0, identity = "user", stderr = "", calls = [], respond;
child.stderr.on("data", value => { stderr += value; });
function send(frame) { frames.push({ direction: "host", ...frame }); child.stdin.write(JSON.stringify(frame) + "\n"); }
const lineReader = createInterface({ input: child.stdout });
lineReader.on("line", line => {
  const frame = JSON.parse(line);
  frames.push({ direction: "plugin", ...frame });
  if (!frame.method) { const item = pending.get(frame.id); pending.delete(frame.id); item?.resolve(frame); return; }
  try {
    assert.equal(frame.method, "host.service.call");
    assert.equal(frame.params.method, "call");
    if (frame.params.service === "host.settings.get") {
      assert.equal(frame.params.params.key, "token-kind");
      send({ id: frame.id, result: { value: identity } });
    } else {
      assert.equal(frame.params.service, "host.credentials.request");
      const request = frame.params.params;
      assert.equal(request.credential, "access-token");
      assert.equal(new URL(request.url).origin, "https://open.feishu.cn");
      assert.ok(!JSON.stringify(request).includes("Authorization"));
      calls.push(request);
      const result = respond(request, frame);
      if (result !== undefined) send({ id: frame.id, ...result });
    }
  } catch (error) {
    send({ id: frame.id, error: { message: error.message } });
  }
});
function rpc(method, params) {
  const id = `host-${++sequence}`;
  const promise = new Promise((resolve, reject) => { pending.set(id, { resolve, reject }); });
  send({ id, method, params });
  return promise;
}
function ok(data) { return { result: { status: 200, headers: {}, body: JSON.stringify({ code: 0, msg: "success", data }) } }; }
function apiError(status, code, headers = {}) { return { result: { status, headers, body: JSON.stringify({ code, msg: "synthetic API failure" }) } }; }
function tool(name, input, execution_id = `execution-${sequence}`) {
  return rpc("tool.execute", { tool_id: name, tool: `feishu-workspace.${name}`, call_id: `call-${sequence}`, execution_id, cwd: root, arguments: input });
}
function result(frame) { assert.equal(frame.error, undefined, JSON.stringify(frame.error)); return frame.result.result.structured_content; }
async function scenario(name, run) {
  calls = []; identity = "user";
  respond = () => { throw new Error("Unexpected HTTP request"); };
  await run();
  cases.push({ name, passed: true, api_requests: calls.length });
  console.log(`PASS ${name}`);
}
const document = { document_id: "doxSynthetic", revision_id: 7, title: "Synthetic document" };
const writeArgs = { document: document.document_id, revision_id: 7, operation_id: "synthetic-operation-1" };
const watchdog = setTimeout(() => child.kill("SIGKILL"), 20000);
try {
  const initialized = await rpc("initialize", { protocol_version: 1, capability_protocol_version: 3, plugin_id: "feishu-workspace", plugin_root: root, project_root: root, wuu_home: output, supported_host_services: ["host.service.call"] });
  assert.equal(initialized.error, undefined);
  assert.equal(initialized.result.tools.length, 5);
  assert.ok(initialized.result.tools.every(item => !item.direct_only));
  await scenario("user search pagination and 199-result API window", async () => {
    respond = request => {
      const body = JSON.parse(request.body);
      assert.equal(request.method, "POST");
      assert.deepEqual(body.docs_types, ["doc"]);
      assert.ok(body.offset + body.count < 200);
      if (body.offset === 198) assert.equal(body.count, 1);
      return ok({ docs_entities: [{ docs_token: "doxSynthetic", docs_type: "docx", title: "Synthetic" }], has_more: true, total: 250 });
    };
    assert.equal(result(await tool("feishu_search_documents", { query: "Synthetic" })).next_offset, 1);
    const last = result(await tool("feishu_search_documents", { query: "Synthetic", offset: 198, page_size: 50 }));
    assert.equal(last.has_more, false); assert.equal(last.search_window_exhausted, true);
  });
  await scenario("tenant token cannot call user search", async () => {
    identity = "tenant";
    assert.match(result(await tool("feishu_search_documents", { query: "Synthetic" })).error.message, /user_access_token/);
    assert.equal(calls.length, 0);
  });
  await scenario("plain text pagination preserves emoji and carries metadata", async () => {
    respond = request => request.url.endsWith("/raw_content") ? ok({ content: "A🙂Z" }) : ok({ document });
    const first = result(await tool("feishu_read_document", { document: "https://team.feishu.cn/docx/doxSynthetic?from=copy", limit: 2 }));
    assert.equal(first.content, "A🙂"); assert.equal(first.next_offset, 3); assert.equal(first.document.revision_id, 7);
    const second = result(await tool("feishu_read_document", { document: "doxSynthetic", offset: first.next_offset }));
    assert.equal(second.content, "Z"); assert.equal(second.has_more, false);
  });
  await scenario("block pages retain revision and opaque cursor", async () => {
    respond = request => {
      const url = new URL(request.url);
      if (!url.pathname.endsWith("/blocks")) return ok({ document });
      assert.equal(url.searchParams.get("document_revision_id"), "7");
      return ok({ items: [{ block_id: "blockSynthetic", block_type: 2, text: { elements: [{ text_run: { content: "Synthetic" } }] } }], has_more: !url.searchParams.has("page_token"), ...(url.searchParams.has("page_token") ? {} : { page_token: "opaque+/=cursor" }) });
    };
    const first = result(await tool("feishu_read_document", { document: "doxSynthetic", format: "blocks" }));
    assert.equal(first.next_page_token, "opaque+/=cursor");
    const second = result(await tool("feishu_read_document", { document: "doxSynthetic", format: "blocks", revision_id: first.revision_id, page_token: first.next_page_token }));
    assert.equal(second.has_more, false);
  });
  await scenario("create sends one empty-document operation", async () => {
    respond = request => { assert.equal(request.method, "POST"); assert.deepEqual(JSON.parse(request.body), { title: "Synthetic", folder_token: "folderSynthetic" }); return ok({ document }); };
    assert.equal(result(await tool("feishu_create_document", { title: "Synthetic", folder_token: "folderSynthetic" })).document.document_id, "doxSynthetic");
    assert.equal(calls.length, 1);
  });
  await scenario("append and precise update send revision and stable operation ID", async () => {
    respond = request => {
      const url = new URL(request.url);
      assert.equal(url.searchParams.get("document_revision_id"), "7");
      assert.equal(url.searchParams.get("client_token"), "synthetic-operation-1");
      if (request.method === "POST") assert.deepEqual(JSON.parse(request.body), { index: -1, children: [{ block_type: 4, heading2: { elements: [{ text_run: { content: "Literal **text**" } }] } }] });
      else { assert.equal(request.method, "PATCH"); assert.deepEqual(JSON.parse(request.body), { update_text_elements: { elements: [{ text_run: { content: "Replacement" } }] } }); }
      return ok({ document_revision_id: 8, client_token: "synthetic-operation-1", ...(request.method === "POST" ? { children: [{ block_id: "newBlock" }] } : { block: { block_id: "blockSynthetic" } }) });
    };
    assert.equal(result(await tool("feishu_append_blocks", { ...writeArgs, blocks: [{ kind: "heading2", text: "Literal **text**" }] })).document_revision_id, 8);
    assert.equal(result(await tool("feishu_update_block", { ...writeArgs, block_id: "blockSynthetic", text: "Replacement" })).document_revision_id, 8);
    assert.equal(calls.length, 2);
  });
  await scenario("reject unsupported URL, wiki token URL, root mutation and missing revision before HTTP", async () => {
    for (const document of ["https://evil.example/docx/id", "https://team.feishu.cn/wiki/node", "https://feishu.cn.evil.example/docx/id"]) {
      assert.equal(result(await tool("feishu_read_document", { document })).error.kind, "validation");
    }
    assert.equal(result(await tool("feishu_update_block", { ...writeArgs, block_id: "doxSynthetic", text: "No" })).error.kind, "validation");
    assert.equal(result(await tool("feishu_append_blocks", { document: "doxSynthetic", operation_id: "id", blocks: [{ text: "No" }] })).error.kind, "validation");
    assert.equal(calls.length, 0);
  });
  await scenario("rate limits and auth failures do not auto-retry writes", async () => {
    for (const [status, code, kind] of [[400, 99991400, "rate_limit"], [429, 99991401, "rate_limit"], [401, 99991677, "authentication"], [403, 99991672, "permission"]]) {
      respond = () => apiError(status, code, { "retry-after": "3" });
      const value = result(await tool("feishu_create_document", { title: "Synthetic" }));
      assert.equal(value.error.kind, kind);
      if (kind === "rate_limit") assert.equal(value.error.retry_after_seconds, 3);
    }
    assert.equal(calls.length, 4);
  });
  await scenario("uncertain mutation is surfaced and never replayed", async () => {
    respond = () => ({ error: { message: "Synthetic transport disconnected" } });
    const value = result(await tool("feishu_create_document", { title: "Synthetic" }));
    assert.equal(value.error.write_may_have_succeeded, true);
    assert.match(value.error.guidance, /duplicates/); assert.equal(calls.length, 1);
    respond = () => apiError(500, 1771001);
    assert.equal(result(await tool("feishu_append_blocks", { ...writeArgs, blocks: [{ text: "Synthetic" }] })).error.write_may_have_succeeded, true);
    assert.equal(calls.length, 2);
  });
  await scenario("missing or inconsistent write receipts never report verified success", async () => {
    respond = () => ok({});
    assert.equal(result(await tool("feishu_create_document", { title: "Synthetic" })).error.status, "applied_unverified");
    assert.equal(result(await tool("feishu_append_blocks", { ...writeArgs, blocks: [{ text: "Synthetic" }] })).error.status, "applied_unverified");
    respond = () => ok({ block: { block_id: "wrongBlock" } });
    const update = result(await tool("feishu_update_block", { ...writeArgs, block_id: "blockSynthetic", text: "Replacement" }));
    assert.equal(update.error.status, "applied_unverified"); assert.equal(update.error.receipt.block.block_id, "wrongBlock");
    respond = () => ({ result: { status: 200, headers: {}, body: JSON.stringify({ code: 0 }) } });
    assert.equal(result(await tool("feishu_create_document", { title: "Synthetic" })).error.write_may_have_succeeded, true);
    assert.equal(calls.length, 4);
  });
  await scenario("execution cancellation interrupts a pending host request", async () => {
    let requested;
    const reached = new Promise(resolve => { requested = resolve; });
    respond = () => { requested(); };
    const reading = tool("feishu_read_document", { document: "doxSynthetic" }, "cancel-reading");
    await reached;
    send({ id: "cancel", method: "execution.cancel", params: { execution_id: "cancel-reading" } });
    assert.ok((await reading).error); assert.equal(calls.length, 1);
  });
  await rpc("shutdown");
  child.stdin.end();
  assert.deepEqual(await closed, [0, null]);
} catch (error) {
  cases.push({ name: "acceptance failure", passed: false, error: error.stack });
  process.exitCode = 1;
  child.kill("SIGKILL");
  console.error(error);
} finally {
  clearTimeout(watchdog);
  await writeFile(join(output, "results.json"), JSON.stringify({ kind: "synthetic-api-process-acceptance", external_requests: 0, cases, stderr }, null, 2) + "\n");
  await writeFile(join(output, "protocol.jsonl"), frames.map(frame => JSON.stringify(frame)).join("\n") + "\n");
  console.log(`Evidence: ${output}`);
}
