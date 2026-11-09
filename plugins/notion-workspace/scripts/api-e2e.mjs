import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { resolve, dirname } from "node:path";

// Exercise the shipped JSONL runtime, including its public host-request boundary.
// No token, Notion connection, HTTP request, or external content write is made.
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const artifact = resolve(root, ".tmp/notion-workspace-e2e/report.json");
const runtime = resolve(root, "plugins/notion-workspace/package/runtime.mjs");
const process = spawn(globalThis.process.execPath, [runtime], { cwd: root, stdio: ["pipe", "pipe", "pipe"] });
const pageID = "11111111111111111111111111111111";
const childID = "22222222222222222222222222222222";
const blockID = "33333333333333333333333333333333";
const edited = "2026-10-19T10:00:00.000Z";
const rich = (text) => text ? [{ type: "text", text: { content: text }, plain_text: text }] : [];
const page = (title = "Example", id = pageID) => ({ object: "page", id, url: `https://app.notion.com/p/${id}`, last_edited_time: edited, parent: { type: "page_id", page_id: pageID }, properties: { Name: { id: "title", type: "title", title: rich(title) } } });
const block = (text = "Original", type = "paragraph") => ({ object: "block", id: blockID, type, last_edited_time: edited, has_children: true, [type]: { rich_text: rich(text), color: "default" } });
const list = (results = [], next_cursor = null) => ({ object: "list", results, has_more: next_cursor !== null, next_cursor });
const reply = (body, status = 200, headers = {}) => ({ status, headers, body: JSON.stringify(body) });
const pending = new Map();
const plans = [];
const checks = [];
const requests = [];
let sequence = 0;
let stderr = "";
let failure;
process.stderr.on("data", (chunk) => { stderr += chunk; });
const lines = createInterface({ input: process.stdout });
lines.on("line", (line) => {
  try {
    const frame = JSON.parse(line);
    if (frame.method) {
      assert.equal(frame.method, "host.service.call");
      assert.equal(frame.params.service, "host.credentials.request");
      const request = frame.params.params;
      assert.equal(request.credential, "integration-token");
      assert.equal(new URL(request.url).origin, "https://api.notion.com");
      assert.equal(request.headers["Notion-Version"], "2026-03-11");
      assert.equal(request.headers.Authorization, undefined);
      requests.push({ method: request.method, path: new URL(request.url).pathname });
      const plan = plans.shift();
      assert.ok(plan, `Unexpected request: ${request.method} ${request.url}`);
      plan.inspect?.(request);
      if (plan.cancel) process.stdin.write(JSON.stringify({ id: "cancel-e2e", method: "execution.cancel", params: { execution_id: plan.executionID } }) + "\n");
      process.stdin.write(JSON.stringify(plan.error ? { id: frame.id, error: plan.error } : { id: frame.id, result: plan.response }) + "\n");
      return;
    }
    const waiter = pending.get(frame.id);
    assert.ok(waiter, `Unexpected response ${frame.id}`);
    pending.delete(frame.id);
    clearTimeout(waiter.timer);
    waiter.resolve(frame);
  } catch (error) {
    failure = error;
    for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(error); }
    pending.clear();
  }
});
function rpc(method, params) {
  if (failure) return Promise.reject(failure);
  const id = `e2e-${++sequence}`;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Timeout: ${method}`)); }, 10_000);
    pending.set(id, { resolve, reject, timer });
    process.stdin.write(JSON.stringify({ id, method, params }) + "\n");
  });
}
async function tool(id, args, planned = []) {
  assert.equal(plans.length, 0);
  const executionID = `execution-${sequence}`;
  plans.push(...planned.map((plan) => ({ ...plan, executionID })));
  const frame = await rpc("tool.execute", { tool_id: id, tool: id, call_id: `call-${sequence}`, execution_id: executionID, cwd: root, arguments: args });
  assert.equal(frame.error, undefined);
  assert.equal(plans.length, 0, "Not all expected API requests were made");
  return frame.result.result;
}
async function check(name, work) {
  await work();
  checks.push({ name, status: "passed" });
  console.log(`PASS ${name}`);
}
const endpoint = (method, suffix, bodyCheck) => (request) => {
  assert.equal(request.method, method);
  assert.equal(new URL(request.url).pathname, `/v1${suffix}`);
  bodyCheck?.(request.body === undefined ? undefined : JSON.parse(request.body), request);
};
try {
  await check("Built runtime advertises eight bounded JSON-schema tools and credential service", async () => {
    const initialized = await rpc("initialize", { protocol_version: 1, capability_protocol_version: 3, plugin_id: "notion-workspace", plugin_root: dirname(runtime), project_root: root, wuu_home: root, supported_host_services: ["host.service.call"] });
    assert.equal(initialized.error, undefined);
    assert.equal(initialized.result.tools.length, 8);
    for (const tool of initialized.result.tools) {
      assert.equal(tool.input_schema.type, "object");
      assert.equal(tool.input_schema.additionalProperties, false);
      assert.equal(tool.direct_only, undefined, "Document tools must remain Code Mode callable");
    }
    assert.ok(initialized.result.required_services.some((service) => service.name === "host.credentials.request"));
    await rpc("activate", {});
  });
  await check("Title search preserves an opaque continuation cursor", async () => {
    const cursor = "session/token+cursor=next:page";
    const result = await tool("search_pages", { query: "Example", page_size: 2, start_cursor: cursor }, [{
      inspect: endpoint("POST", "/search", (body) => { assert.equal(body.start_cursor, cursor); assert.equal(body.page_size, 2); assert.deepEqual(body.filter, { value: "page", property: "object" }); }),
      response: reply({ ...list([page()], "opaque-next"), request_status: { incomplete_reason: "query_result_limit_reached" } }),
    }]);
    assert.equal(result.structured_content.next_cursor, "opaque-next");
    assert.equal(result.structured_content.request_status.incomplete_reason, "query_result_limit_reached");
    assert.equal(result.structured_content.search_scope, "shared_page_titles_only");
  });
  await check("Read page distinguishes metadata, direct children, and incomplete properties", async () => {
    const result = await tool("read_page", { page_id: pageID }, [
      { inspect: endpoint("GET", `/pages/${pageID}`), response: reply(page()) },
      { inspect: endpoint("GET", `/blocks/${pageID}/children`), response: reply(list([block()], "more")) },
    ]);
    assert.equal(result.structured_content.children.has_more, true);
    assert.equal(result.structured_content.children.results[0].has_children, true);
    assert.equal(result.structured_content.properties_may_be_truncated, true);
    assert.equal(result.structured_content.content_scope, "one_page_of_direct_children");
  });
  await check("Nested pagination sends the exact cursor through URL encoding", async () => {
    const cursor = "opaque/&?+ next";
    const result = await tool("read_children", { block_id: blockID, start_cursor: cursor, page_size: 100 }, [{
      inspect: endpoint("GET", `/blocks/${blockID}/children`, (_body, request) => assert.equal(new URL(request.url).searchParams.get("start_cursor"), cursor)), response: reply(list()),
    }]);
    assert.equal(result.structured_content.has_more, false);
  });
  await check("Page creation uses explicit page parent and returns verified receipt", async () => {
    const result = await tool("create_page", { parent_page_id: pageID, title: "New page", blocks: [{ type: "to_do", text: "Review", checked: true }] }, [{
      inspect: endpoint("POST", "/pages", (body) => {
        assert.deepEqual(body.parent, { type: "page_id", page_id: pageID });
        assert.equal(body.children[0].to_do.checked, true);
        assert.equal(body.properties.title.title[0].text.content, "New page");
      }), response: reply(page("New page", childID)),
    }]);
    assert.equal(result.structured_content.page.id, childID);
    assert.equal(result.structured_content.blocks_submitted, 1);
    assert.match(result.structured_content.verification, /child_content_not_read_back/);
  });
  await check("Append uses the current position API and verifies all returned blocks", async () => {
    const result = await tool("append_blocks", { block_id: pageID, blocks: [{ type: "paragraph", text: "Appended" }] }, [{
      inspect: endpoint("PATCH", `/blocks/${pageID}/children`, (body) => { assert.deepEqual(body.position, { type: "end" }); assert.equal(body.after, undefined); }),
      response: reply(list([block("Appended")])),
    }]);
    assert.equal(result.structured_content.blocks_appended, 1);
  });
  await check("Update preflights the revision and changes rich_text only", async () => {
    const result = await tool("update_block_text", { block_id: blockID, text: "Changed", expected_last_edited_time: edited }, [
      { response: reply(block()) },
      { inspect: endpoint("PATCH", `/blocks/${blockID}`, (body) => { assert.deepEqual(Object.keys(body), ["paragraph"]); assert.deepEqual(Object.keys(body.paragraph), ["rich_text"]); }), response: reply(block("Changed")) },
    ]);
    assert.equal(result.structured_content.block.paragraph.rich_text[0].plain_text, "Changed");
    assert.equal(result.structured_content.preflight_atomic, false);
  });
  await check("Stale edit is refused before any write", async () => {
    const result = await tool("update_block_text", { block_id: blockID, text: "Changed", expected_last_edited_time: "older" }, [{ response: reply(block()) }]);
    assert.equal(result.is_error, true);
    assert.equal(result.structured_content.code, "stale_edit");
    assert.equal(result.structured_content.outcome, "not_sent");
  });
  await check("Unsupported block edits are refused without changing content", async () => {
    const result = await tool("update_block_text", { block_id: blockID, text: "Changed", expected_last_edited_time: edited }, [{ response: reply(block("", "image")) }]);
    assert.equal(result.is_error, true);
    assert.equal(result.structured_content.outcome, "not_sent");
  });
  await check("Rename resolves a title property ID rather than assuming its display name", async () => {
    const result = await tool("rename_page", { page_id: pageID, title: "Renamed", expected_last_edited_time: edited }, [
      { response: reply(page()) },
      { inspect: endpoint("PATCH", `/pages/${pageID}`, (body) => assert.deepEqual(Object.keys(body.properties), ["title"])), response: reply(page("Renamed")) },
    ]);
    assert.equal(result.structured_content.ok, true);
  });
  await check("Input rejects excess blocks, unsupported keys, invalid UUIDs, and fractional page sizes before transport", async () => {
    for (const [name, args] of [
      ["append_blocks", { block_id: pageID, blocks: Array.from({ length: 101 }, () => ({ type: "paragraph", text: "x" })) }],
      ["append_blocks", { block_id: pageID, blocks: [{ type: "paragraph", text: "x", children: [] }] }],
      ["read_page", { page_id: "https://app.notion.com/p/example" }],
      ["read_children", { block_id: blockID, page_size: 1.5 }],
      ["create_page", { parent_page_id: pageID, title: "x".repeat(2001) }],
    ]) {
      const result = await tool(name, args);
      assert.equal(result.is_error, true);
      assert.equal(result.structured_content.outcome, "not_sent");
    }
  });
  await check("Missing credentials fail without exposing raw host diagnostics", async () => {
    const result = await tool("read_block", { block_id: blockID }, [{ error: { code: "credential_request_failed", message: "synthetic-private-token" } }]);
    assert.equal(result.is_error, true);
    assert.ok(!JSON.stringify(result).includes("synthetic-private-token"));
    assert.match(result.structured_content.error, /saved integration token/);
  });
  await check("401 and 403 report actionable authorization failures without retry", async () => {
    for (const status of [401, 403]) {
      const result = await tool("read_block", { block_id: blockID }, [{ response: reply({ object: "error", code: "restricted_resource" }, status) }]);
      assert.equal(result.is_error, true);
      assert.equal(result.structured_content.status, status);
    }
  });
  await check("429 and 529 are retried with bounded Retry-After handling", async () => {
    for (const status of [429, 529]) {
      const result = await tool("read_block", { block_id: blockID }, [
        { response: reply({ object: "error", code: "rate_limited" }, status, { "retry-after": "0" }) },
        { response: reply(block()) },
      ]);
      assert.equal(result.structured_content.ok, true);
    }
  });
  await check("A confirmed blocked API does not retry", async () => {
    const result = await tool("read_block", { block_id: blockID }, [{ response: reply({ object: "error", code: "rate_limited", additional_data: { rate_limit_reason: "public_api_request_blocked" } }, 429) }]);
    assert.equal(result.is_error, true);
  });
  await check("Ambiguous server and transport write failures are never replayed", async () => {
    for (const response of [{ response: reply({ object: "error", code: "service_unavailable" }, 503) }, { error: { code: "credential_request_failed", message: "connection lost" } }]) {
      const result = await tool("create_page", { parent_page_id: pageID, title: "Potentially created" }, [response]);
      assert.equal(result.is_error, true);
      assert.equal(result.structured_content.outcome, "unknown");
    }
  });
  await check("A successful but mismatched write keeps its receipt for reconciliation", async () => {
    const result = await tool("create_page", { parent_page_id: pageID, title: "Requested title" }, [{ response: reply(page("Unexpected title", childID)) }]);
    assert.equal(result.is_error, true);
    assert.equal(result.structured_content.outcome, "applied_unverified");
    assert.equal(result.structured_content.receipt.id, childID);
  });
  await check("Cancelling a preflight prevents the later content write", async () => {
    const result = await tool("update_block_text", { block_id: blockID, text: "Cancelled edit", expected_last_edited_time: edited }, [{ response: reply(block()), cancel: true }]);
    assert.equal(result.is_error, true);
    assert.equal(result.structured_content.ok, false);
  });
  await check("A long server retry interval is surfaced and blocks immediate new requests", async () => {
    const result = await tool("read_block", { block_id: blockID }, [{ response: reply({ object: "error", code: "rate_limited" }, 429, { "retry-after": "60" }) }]);
    assert.equal(result.is_error, true);
    assert.ok(result.structured_content.retry_after_seconds >= 60);
    const immediate = await tool("read_block", { block_id: blockID });
    assert.equal(immediate.structured_content.code, "rate_limited");
    assert.equal(immediate.structured_content.outcome, "not_sent");
  });
  await rpc("shutdown", {});
  assert.equal(stderr, "");
  await mkdir(dirname(artifact), { recursive: true });
  await writeFile(artifact, JSON.stringify({ status: "passed", mode: "built_runtime_with_mocked_host_api", external_requests: 0, real_account_tested: false, checks, requests }, null, 2) + "\n");
  console.log(`Evidence: ${artifact}`);
} catch (error) {
  await mkdir(dirname(artifact), { recursive: true });
  await writeFile(artifact, JSON.stringify({ status: "failed", checks, requests, error: String(error), stderr }, null, 2) + "\n");
  throw error;
} finally {
  lines.close();
  process.stdin.end();
  process.kill();
}
