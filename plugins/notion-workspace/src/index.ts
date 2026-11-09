import { setTimeout as delay } from "node:timers/promises";
import { requireKernelService, type RuntimeHost, type RuntimePlugin, type ToolRegistration } from "@wuu/plugin-sdk";

const apiVersion = "2026-03-11";
const blockTypes = ["paragraph", "heading_1", "heading_2", "heading_3", "bulleted_list_item", "numbered_list_item", "to_do", "quote", "toggle"] as const;
const schema = (properties: Record<string, unknown>, required: string[] = []) => ({ type: "object" as const, properties, required, additionalProperties: false });
const idSchema = { type: "string", description: "Notion record UUID from an API result; not a website URL." };
const textSchema = { type: "string", maxLength: 2000 };
const editSchema = { type: "string", description: "Exact last_edited_time from a recent read. Checked before writing, not an atomic compare-and-swap." };
const paging = {
  start_cursor: { type: "string", description: "Pass next_cursor from the previous result verbatim; omit for the first page." },
  page_size: { type: "integer", minimum: 1, maximum: 100, default: 50 },
};
const blocksSchema = { type: "array", maxItems: 100, items: schema({
  type: { type: "string", enum: [...blockTypes] }, text: textSchema,
  checked: { type: "boolean", description: "Only for to_do blocks; defaults to false when creating." },
}, ["type", "text"]) };
const readActivity = { read_only: true, risk: "low" };
const writeActivity = { read_only: false, risk: "medium", reason: "Writes content to the user's Notion workspace." };
const tools: ToolRegistration[] = [
  { id: "search_pages", description: "Search titles of Notion pages shared with the configured integration. Returns one bounded page and opaque next_cursor; not full-text search or a complete workspace inventory.", input_schema: schema({ query: { type: "string", maxLength: 1000 }, ...paging }), activity: readActivity },
  { id: "read_page", description: "Read Notion page metadata and one page of direct child blocks. Nested children and further pages are not included: use read_children with their IDs/cursors. Page properties may have Notion's own truncation limits.", input_schema: schema({ page_id: idSchema, ...paging }, ["page_id"]), activity: readActivity },
  { id: "read_children", description: "Read one bounded page of direct child blocks of a Notion page or block. Follow next_cursor and has_children explicitly; no recursive traversal.", input_schema: schema({ block_id: idSchema, ...paging }, ["block_id"]), activity: readActivity },
  { id: "read_block", description: "Read one Notion block, including its type and last_edited_time. Does not include nested children.", input_schema: schema({ block_id: idSchema }, ["block_id"]), activity: readActivity },
  { id: "create_page", description: "Create a child page under an explicitly chosen Notion parent page, with a title and up to 100 simple plain-text blocks. Does not create database rows or apply templates. Use only when the user requested this external write. Never blindly retry an outcome=unknown result.", input_schema: schema({ parent_page_id: idSchema, title: { ...textSchema, minLength: 1 }, blocks: blocksSchema }, ["parent_page_id", "title"]), activity: writeActivity },
  { id: "append_blocks", description: "Append 1–100 simple plain-text blocks at the end of a Notion page or block. Does not replace, move, or delete existing content. Use only for a requested external write; do not retry outcome=unknown without inspecting the destination.", input_schema: schema({ block_id: idSchema, blocks: { ...blocksSchema, minItems: 1 } }, ["block_id", "blocks"]), activity: writeActivity },
  { id: "update_block_text", description: "Replace one supported Notion block's entire rich_text with plain text, removing its inline formatting/mentions/links. Preserves other fields and children. Requires a recent last_edited_time; the preflight is not atomic. Use only for the requested edit. Read back the block if the outcome is unknown.", input_schema: schema({ block_id: idSchema, text: textSchema, expected_last_edited_time: editSchema }, ["block_id", "text", "expected_last_edited_time"]), activity: { ...writeActivity, destructive: true } },
  { id: "rename_page", description: "Replace a Notion page title after checking a recent last_edited_time. Resolves the existing title property's ID; does not change other properties or content. The preflight is not atomic. Use only for the requested edit.", input_schema: schema({ page_id: idSchema, title: { ...textSchema, minLength: 1 }, expected_last_edited_time: editSchema }, ["page_id", "title", "expected_last_edited_time"]), activity: { ...writeActivity, destructive: true } },
];

type ObjectValue = Record<string, unknown>;
class NotionError extends Error {
  constructor(message: string, readonly details: ObjectValue = {}) { super(message); }
}
function object(value: unknown): ObjectValue {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Expected one JSON object.");
  return value as ObjectValue;
}
function input(value: unknown, keys: string[]): ObjectValue {
  const args = object(value);
  for (const key of Object.keys(args)) if (!keys.includes(key)) throw new Error(`Unknown input field: ${key}`);
  return args;
}
function string(value: unknown, name: string, maximum = 2000, allowEmpty = false): string {
  if (typeof value !== "string" || value.length > maximum || (!allowEmpty && !value.trim())) throw new Error(`${name} must be ${allowEmpty ? "a" : "a nonempty"} string of at most ${maximum} characters.`);
  return value;
}
function recordID(value: unknown, name: string): string {
  const raw = string(value, name, 36);
  if (!/^(?:[a-f\d]{32}|[a-f\d]{8}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{12})$/i.test(raw)) throw new Error(`${name} must be a Notion UUID, not a URL.`);
  return raw.replaceAll("-", "").toLowerCase();
}
function pagination(args: ObjectValue): { page_size: number; start_cursor?: string } {
  const size = args.page_size ?? 50;
  if (typeof size !== "number" || !Number.isInteger(size) || size < 1 || size > 100) throw new Error("page_size must be an integer from 1 to 100.");
  // Notion cursors are opaque and may change format. Do not parse or normalize them.
  if (args.start_cursor !== undefined && (typeof args.start_cursor !== "string" || !args.start_cursor)) throw new Error("start_cursor must be a nonempty string.");
  return { page_size: size, ...(args.start_cursor === undefined ? {} : { start_cursor: args.start_cursor as string }) };
}
function richText(text: string): unknown[] { return text ? [{ type: "text", text: { content: text } }] : []; }
function contentBlocks(value: unknown, required: boolean): ObjectValue[] {
  if (value === undefined && !required) return [];
  if (!Array.isArray(value) || value.length > 100 || (required && value.length === 0)) throw new Error(`blocks must contain ${required ? "1" : "0"}–100 blocks.`);
  return value.map((item) => {
    const block = input(item, ["type", "text", "checked"]);
    if (!blockTypes.includes(block.type as typeof blockTypes[number])) throw new Error("Unsupported block type.");
    const type = block.type as string;
    const text = string(block.text, "block.text", 2000, true);
    if (block.checked !== undefined && (type !== "to_do" || typeof block.checked !== "boolean")) throw new Error("checked is a boolean allowed only for to_do blocks.");
    return { object: "block", type, [type]: { rich_text: richText(text), ...(type === "to_do" ? { checked: block.checked ?? false } : {}) } };
  });
}
function pageTitle(page: ObjectValue): { key: string; value: ObjectValue } {
  for (const [key, value] of Object.entries(object(page.properties))) {
    if (value && typeof value === "object" && !Array.isArray(value) && (value as ObjectValue).type === "title") return { key, value: value as ObjectValue };
  }
  throw new Error("Page response has no title property.");
}
function plainText(value: unknown): string {
  if (!Array.isArray(value)) throw new Error("Expected a rich-text array in the API response.");
  return value.map((part) => {
    const item = object(part);
    return typeof item.plain_text === "string" ? item.plain_text : string(object(item.text).content, "rich-text content", 2000, true);
  }).join("");
}
function verifyRecord(value: ObjectValue, kind: "page" | "block", id?: string): ObjectValue {
  if (value.object !== kind || (id && recordID(value.id, "response.id") !== id)) throw new Error(`Unexpected ${kind} API response.`);
  recordID(value.id, "response.id");
  return value;
}
function list(value: ObjectValue): ObjectValue {
  if (value.object !== "list" || !Array.isArray(value.results) || typeof value.has_more !== "boolean"
    || (value.has_more && (typeof value.next_cursor !== "string" || !value.next_cursor))) throw new Error("Invalid paginated API response.");
  return { results: value.results, has_more: value.has_more, next_cursor: value.next_cursor ?? null, ...(value.request_status === undefined ? {} : { request_status: value.request_status }), traversal: "direct_children_only" };
}
function preflight(record: ObjectValue, expected: unknown): void {
  const timestamp = string(expected, "expected_last_edited_time", 100);
  if (record.last_edited_time !== timestamp) throw new NotionError("The page or block changed. Read it again before editing.", { code: "stale_edit", last_edited_time: record.last_edited_time, outcome: "not_sent" });
}

export function createNotionPlugin(): RuntimePlugin {
  let nextRequestAt = 0;
  let lifetime: AbortSignal;
  async function request(host: RuntimeHost, path: string, method: "GET" | "POST" | "PATCH", body: ObjectValue | undefined, signal: AbortSignal, write = false): Promise<ObjectValue> {
    const encoded = body === undefined ? undefined : JSON.stringify(body);
    if (encoded && Buffer.byteLength(encoded) > 450_000) throw new NotionError("Request exceeds the plugin's 450 KB limit. Split the content into smaller explicit writes.", { outcome: "not_sent" });
    for (let attempt = 0; attempt < 3; attempt++) {
      const wait = nextRequestAt - Date.now();
      if (wait > 5000) throw new NotionError("Notion is rate limited. Wait before making another request.", { code: "rate_limited", retry_after_seconds: Math.ceil(wait / 1000), outcome: "not_sent" });
      if (wait > 0) await delay(wait, undefined, { signal });
      signal.throwIfAborted();
      let response;
      try {
        response = await host.call("host.credentials.request", {
          credential: "integration-token", url: `https://api.notion.com/v1${path}`, method,
          headers: { "Notion-Version": apiVersion, "Content-Type": "application/json" }, ...(encoded === undefined ? {} : { body: encoded }),
        }, { signal });
      } catch {
        // The host owns credentials. Do not surface raw transport errors, which
        // may contain request bodies. A lost write response is not safe to replay.
        throw new NotionError(write ? "The write could not be confirmed. Inspect Notion before retrying; it may already have applied." : "Notion request failed. Check the saved integration token, host credential-store availability, and network connection.", { code: "request_failed", outcome: write ? "unknown" : "not_applicable" });
      }
      nextRequestAt = Math.max(nextRequestAt, Date.now() + 350);
      let data: ObjectValue;
      try { data = object(JSON.parse(response.body)); }
      catch { throw new NotionError("Notion returned an unreadable response.", { status: response.status, outcome: write ? "unknown" : "not_applicable" }); }
      if (response.status >= 200 && response.status < 300) return data;
      const additional = data.additional_data && typeof data.additional_data === "object" ? data.additional_data as ObjectValue : {};
      const header = Object.entries(response.headers).find(([key]) => key.toLowerCase() === "retry-after")?.[1];
      const retryAfter = Number(header ?? additional.retry_after ?? Math.pow(2, attempt));
      const throttled = (response.status === 429 || response.status === 529) && additional.rate_limit_reason !== "public_api_request_blocked";
      if (throttled) {
        const seconds = Number.isFinite(retryAfter) && retryAfter >= 0 ? retryAfter : Math.pow(2, attempt);
        nextRequestAt = Math.max(nextRequestAt, Date.now() + seconds * 1000 + attempt * 100);
        if (attempt < 2 && seconds <= 5) continue;
      }
      const hints: Record<number, string> = {
        401: "The integration token is invalid or revoked; replace it in plugin settings.",
        403: "Check the integration's content capabilities, page access, and workspace limits.",
        404: "The record is missing, trashed, or not shared with this integration.",
        429: "Notion limited or restricted this request; respect Retry-After before trying again.",
        529: "Notion is temporarily overloaded; respect Retry-After before trying again.",
      };
      throw new NotionError(hints[response.status] || `Notion rejected the request (HTTP ${response.status}).`, {
        status: response.status, code: typeof data.code === "string" ? data.code : "api_error",
        ...(throttled ? { retry_after_seconds: Math.max(0, Math.ceil((nextRequestAt - Date.now()) / 1000)) } : {}),
        outcome: write && response.status >= 500 && response.status !== 529 ? "unknown" : "not_applied",
      });
    }
    throw new Error("Notion retry budget exhausted.");
  }
  return {
    initialize(_params, _host, context) {
      lifetime = context.signal;
      return { tools, required_services: [requireKernelService("host.credentials.request")] };
    },
    async executeTool(call, host, execution) {
      const signal = AbortSignal.any([lifetime, execution.signal, AbortSignal.timeout(45_000)]);
      let writeAcknowledged = false;
      let receipt: ObjectValue | undefined;
      try {
        const get = (path: string) => request(host, path, "GET", undefined, signal);
        const write = async (path: string, method: "POST" | "PATCH", body: ObjectValue) => {
          const value = await request(host, path, method, body, signal, true);
          writeAcknowledged = true;
          receipt = { object: value.object, id: value.id, url: value.url };
          return value;
        };
        const children = async (id: string, args: ObjectValue) => {
          const params = pagination(args);
          const query = new URLSearchParams({ page_size: String(params.page_size), ...(params.start_cursor === undefined ? {} : { start_cursor: params.start_cursor }) });
          return list(await get(`/blocks/${id}/children?${query}`));
        };
        let result: ObjectValue;
        switch (call.tool_id) {
          case "search_pages": {
            const args = input(call.arguments, ["query", "page_size", "start_cursor"]);
            const body = { ...pagination(args), filter: { value: "page", property: "object" }, sort: { direction: "descending", timestamp: "last_edited_time" }, ...(args.query === undefined ? {} : { query: string(args.query, "query", 1000, true) }) };
            const found = list(await request(host, "/search", "POST", body, signal));
            result = { ...found, traversal: undefined, search_scope: "shared_page_titles_only" };
            break;
          }
          case "read_page": {
            const args = input(call.arguments, ["page_id", "page_size", "start_cursor"]);
            pagination(args);
            const id = recordID(args.page_id, "page_id");
            const page = verifyRecord(await get(`/pages/${id}`), "page", id);
            result = { page, children: await children(id, args), content_scope: "one_page_of_direct_children", properties_may_be_truncated: true };
            break;
          }
          case "read_children": {
            const args = input(call.arguments, ["block_id", "page_size", "start_cursor"]);
            result = await children(recordID(args.block_id, "block_id"), args);
            break;
          }
          case "read_block": {
            const args = input(call.arguments, ["block_id"]), id = recordID(args.block_id, "block_id");
            result = { block: verifyRecord(await get(`/blocks/${id}`), "block", id), content_scope: "single_block_without_children" };
            break;
          }
          case "create_page": {
            const args = input(call.arguments, ["parent_page_id", "title", "blocks"]);
            const id = recordID(args.parent_page_id, "parent_page_id"), title = string(args.title, "title");
            const blocks = contentBlocks(args.blocks, false);
            const page = verifyRecord(await write("/pages", "POST", { parent: { type: "page_id", page_id: id }, properties: { title: { type: "title", title: richText(title) } }, ...(blocks.length ? { children: blocks } : {}) }), "page");
            if (plainText(pageTitle(page).value.title) !== title || recordID(object(page.parent).page_id, "parent.page_id") !== id) throw new Error("Created page response did not match its requested title and parent.");
            result = { page, blocks_submitted: blocks.length, verification: "page_metadata_response_matches; child_content_not_read_back" };
            break;
          }
          case "append_blocks": {
            const args = input(call.arguments, ["block_id", "blocks"]), id = recordID(args.block_id, "block_id");
            const blocks = contentBlocks(args.blocks, true);
            const appended = list(await write(`/blocks/${id}/children`, "PATCH", { children: blocks, position: { type: "end" } }));
            const returned = appended.results as unknown[];
            if (returned.length !== blocks.length || appended.has_more) throw new Error("Append response did not include every submitted block. Inspect the destination before retrying.");
            returned.forEach((item, index) => {
              const block = verifyRecord(object(item), "block");
              const type = blocks[index].type as string;
              if (block.type !== type || plainText(object(block[type]).rich_text) !== plainText(object(blocks[index][type]).rich_text)) throw new Error("Appended block response did not match its requested text.");
            });
            result = { ...appended, verification: "created_block_responses_match", blocks_appended: returned.length };
            break;
          }
          case "update_block_text": {
            const args = input(call.arguments, ["block_id", "text", "expected_last_edited_time"]), id = recordID(args.block_id, "block_id");
            const text = string(args.text, "text", 2000, true);
            string(args.expected_last_edited_time, "expected_last_edited_time", 100);
            const current = verifyRecord(await get(`/blocks/${id}`), "block", id);
            preflight(current, args.expected_last_edited_time);
            if (!blockTypes.includes(current.type as typeof blockTypes[number])) throw new Error("This block type cannot be edited by update_block_text.");
            const type = current.type as string;
            const block = verifyRecord(await write(`/blocks/${id}`, "PATCH", { [type]: { rich_text: richText(text) } }), "block", id);
            if (block.type !== type || plainText(object(block[type]).rich_text) !== text) throw new Error("Updated block response did not match its requested text.");
            result = { block, verification: "updated_block_response_matches", preflight_atomic: false };
            break;
          }
          case "rename_page": {
            const args = input(call.arguments, ["page_id", "title", "expected_last_edited_time"]), id = recordID(args.page_id, "page_id"), title = string(args.title, "title");
            string(args.expected_last_edited_time, "expected_last_edited_time", 100);
            const current = verifyRecord(await get(`/pages/${id}`), "page", id);
            preflight(current, args.expected_last_edited_time);
            const property = pageTitle(current);
            const key = typeof property.value.id === "string" ? property.value.id : property.key;
            const page = verifyRecord(await write(`/pages/${id}`, "PATCH", { properties: { [key]: { title: richText(title) } } }), "page", id);
            if (plainText(pageTitle(page).value.title) !== title) throw new Error("Updated page response did not match its requested title.");
            result = { page, verification: "updated_title_response_matches", preflight_atomic: false };
            break;
          }
          default: throw new Error(`Unknown Notion tool: ${call.tool_id}`);
        }
        const output = { ok: true, api_version: apiVersion, ...result };
        return { result: { content: [{ type: "text", text: JSON.stringify(output) }], structured_content: output } };
      } catch (error) {
        const output = { ok: false, api_version: apiVersion, error: error instanceof Error ? error.message : "Notion operation failed.",
          ...(error instanceof NotionError ? error.details : { outcome: writeAcknowledged ? "applied_unverified" : "not_sent" }),
          ...(writeAcknowledged ? { receipt } : {}),
        };
        return { result: { is_error: true, content: [{ type: "text", text: JSON.stringify(output) }], structured_content: output } };
      }
    },
  };
}

export default createNotionPlugin();
