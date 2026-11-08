import { mkdir, readdir, unlink } from "node:fs/promises";
import { basename, join } from "node:path";
import {
  requireKernelService, FILE_LOCK_SERVICE,
  type CapabilityInvokeParams, type RuntimeExecutionContext, type RuntimeHost,
  type RuntimeInitializeParams, type RuntimeInitializeResult, type RuntimePlugin,
  type ServiceInvokeParams, type ToolExecuteParams, type ToolExecuteResult,
  type JSONSchemaObject,
} from "@wuu/plugin-sdk";
import { frontmatter, indexedFiles, INDEX_FILE, MAX_INDEX_BYTES, MAX_INDEX_LINES, MAX_MEMORY_BYTES, missing, promptSection, readNotebookFile, readNotebookSnapshot, readSafeIndex, safeIndex, writeAtomicFile, type Frontmatter } from "./notebook.js";
import { runSessionMemory, sessionMemorySchema } from "./workspace-memory.js";

const CAPABILITY_PROMPT = "agent.system_prompt.section";
const CAPABILITY_CLIENT = "plugin.client.request";
const SESSION_MEMORY_SERVICE = "memory.session";
const NOTEBOOK_MEMORY_SERVICE = "memory.notebook";
const READ_STATS_KEY = "notebook.reads";

interface FileInfo extends Frontmatter { name: string; content: string }
type ReadStats = Record<string, { reads: number; last_read_at: string }>;

function object(value: unknown): Record<string, unknown> {
  if (value == null) return {};
  if (typeof value !== "object" || Array.isArray(value)) throw new Error("invalid tool arguments: expected an object");
  return value as Record<string, unknown>;
}

export function string(value: unknown): string {
  if (value == null) return "";
  if (typeof value !== "string") throw new Error("invalid tool arguments: expected a string");
  return value;
}

export function objectSchema(properties: Record<string, unknown> = {}, ...required: string[]): JSONSchemaObject {
  return { type: "object", properties, required, additionalProperties: false };
}
export function stringField(description: string): Record<string, unknown> { return { type: "string", description }; }
function jsonResult(value: unknown): ToolExecuteResult {
  return { result: { content: [{ type: "text", text: JSON.stringify(value) }] } };
}

class MemoryPlugin implements RuntimePlugin {
  private lifecycleHost!: RuntimeHost;
  private notebook = "";
  private workspaceStateDir = "";
  private available = true;
  private readStatsWrites: Promise<void> = Promise.resolve();

  initialize(params: RuntimeInitializeParams, host: RuntimeHost): RuntimeInitializeResult {
    this.lifecycleHost = host;
    const home = params.wuu_home.trim();
    if (!home) throw new Error("memory plugin requires wuu_home");
    this.notebook = join(home, "memory");
    this.workspaceStateDir = params.workspace_state_dir?.trim() ?? "";
    this.available = params.product_domain !== "personal";
    if (!this.available) return { tools: [], capabilities: [], provided_services: [] };
    if (params.product_domain && params.product_domain !== "coding") throw new Error("unsupported memory product domain");
    return {
      tools: [
        { id: "memory_list", description: "List the user's durable memory notebook files. Use only when durable cross-project user preferences, feedback, references, or lessons are relevant.", input_schema: objectSchema(), activity: { read_only: true, concurrency_safe: true, risk: "low" } },
        { id: "memory_read", description: "Read one Markdown file from the user's durable memory notebook. MEMORY.md is the index; topic files contain the actual memories.", input_schema: objectSchema({ file: stringField("Notebook filename such as MEMORY.md or feedback_testing.md.") }, "file"), activity: { read_only: true, concurrency_safe: true, risk: "low" } },
        { id: "memory_write", description: "Create or replace one Markdown file in the user's durable memory notebook. Saving a memory requires updating both its topic file and the MEMORY.md index.", input_schema: objectSchema({ file: stringField("Notebook Markdown filename."), content: stringField("Complete replacement Markdown content.") }, "file", "content") },
        { id: "memory_delete", description: "Delete one topic file from the user's durable memory notebook. Also remove its pointer from MEMORY.md. MEMORY.md itself cannot be deleted.", input_schema: objectSchema({ file: stringField("Topic Markdown filename to delete.") }, "file") },
        { id: "session_memory", description: "Read or update workspace and current-session memory. project_memory holds stable workspace knowledge: a short MEMORY.md index plus topic pages selected with file. summary holds compact recoverable task state; checkpoint is only for legacy compatibility; notes are session scratch notes.", input_schema: sessionMemorySchema() },
      ],
      capabilities: [
        { id: CAPABILITY_PROMPT, kind: "transform", version: 1 },
        { id: CAPABILITY_CLIENT, kind: "decision", version: 1 },
      ],
      required_services: [
        ...(["host.storage.get", "host.storage.compare_exchange"] as const).map(requireKernelService),
        { name: FILE_LOCK_SERVICE, major_version: 1, required: true },
      ],
      provided_services: [{
        name: SESSION_MEMORY_SERVICE, version: "1.1.0",
        methods: ["status", "read", "append", "replace", "delete"].map((name) => ({ name, input_schema: `memory.session.${name}.request.v1`, output_schema: `memory.session.${name}.response.v1` })),
      }, {
        name: NOTEBOOK_MEMORY_SERVICE, version: "1.1.0",
        methods: [
          ...["index", "read"].map((name) => ({ name, input_schema: `memory.notebook.${name}.request.v1`, output_schema: "memory.notebook.snapshot.v1" })),
          { name: "list", input_schema: "memory.notebook.list.request.v1", output_schema: "memory.notebook.list.v1" },
        ],
      }],
    };
  }

  async activate(): Promise<void> {
    if (!this.available) return;
    if (!this.notebook.trim()) throw new Error("memory: notebook path is required");
    await mkdir(this.notebook, { recursive: true, mode: 0o755 });
  }

  async executeTool(call: ToolExecuteParams, host: RuntimeHost, execution: RuntimeExecutionContext): Promise<ToolExecuteResult> {
    if (!this.available) throw new Error("Coding Memory is unavailable in the Personal domain");
    if (call.tool_id === "memory_list") return jsonResult({ files: await this.readNotebook() });
    const input = object(call.arguments);
    if (call.tool_id === "session_memory") {
      return jsonResult(await runSessionMemory(this.workspaceStateDir, {
        action: string(input.action), target: string(input.target), content: string(input.content), source: string(input.source), file: string(input.file),
      }, call.session_id?.trim() || call.thread_id?.trim() || "", this.lifecycleHost, execution));
    }
    const file = string(input.file);
    switch (call.tool_id) {
      case "memory_read": {
        const content = await this.readFile(file);
        await this.recordRead(file.trim(), host);
        return jsonResult({ file, content });
      }
      case "memory_write": {
        const content = string(input.content);
        if (Buffer.byteLength(content) > MAX_MEMORY_BYTES) throw new Error(`memory content exceeds ${MAX_MEMORY_BYTES} bytes`);
        await writeAtomicFile(this.safePath(file, true), content, 0o600, false);
        return jsonResult({ file, written: true });
      }
      case "memory_delete":
        await unlink(this.safePath(file, false)).catch((error: unknown) => { if (!missing(error)) throw error; });
        return jsonResult({ file, deleted: true });
      default: throw new Error(`unknown memory tool ${JSON.stringify(call.tool_id)}`);
    }
  }

  async invokeCapability(call: CapabilityInvokeParams, host: RuntimeHost, execution: RuntimeExecutionContext): Promise<{ output: unknown }> {
    if (!this.available) throw new Error("Coding Memory is unavailable in the Personal domain");
    switch (call.capability) {
      case CAPABILITY_PROMPT: {
        const projectIndex = this.workspaceStateDir ? await readSafeIndex(join(this.workspaceStateDir, "memory"), true) : undefined;
        return { output: { text: promptSection(this.notebook, await readSafeIndex(this.notebook), projectIndex) } };
      }
      case CAPABILITY_CLIENT: {
        const input = object(call.input);
        return { output: { result: await this.clientRequest(string(input.method), input.input, host) } };
      }
      default: throw new Error(`unknown memory capability ${JSON.stringify(call.capability)}`);
    }
  }

  async invokeService(call: ServiceInvokeParams, host: RuntimeHost, execution: RuntimeExecutionContext): Promise<unknown> {
    if (!this.available) throw new Error("Coding Memory is unavailable in the Personal domain");
    if (call.service === NOTEBOOK_MEMORY_SERVICE) return call.method === "list" ? this.listNotebook(host) : readNotebookSnapshot(this.notebook, call.method, object(call.params));
    if (call.service !== SESSION_MEMORY_SERVICE) throw new Error(`memory plugin does not provide service ${JSON.stringify(call.service)}`);
    const params = object(call.params);
    return runSessionMemory(this.workspaceStateDir, {
      action: call.method, target: string(params.target), content: string(params.content), source: string(params.source).trim() || call.caller.trim(), file: string(params.file),
    }, string(params.thread_id), this.lifecycleHost, execution);
  }

  private async clientRequest(method: string, raw: unknown, host: RuntimeHost): Promise<unknown> {
    switch (method.trim()) {
      case "memory.read": return { index_raw: await this.readFileOptional(INDEX_FILE), files: await this.readNotebook() };
      default: throw new Error(`unknown memory client method ${JSON.stringify(method)}`);
    }
  }

  private async readStats(host: RuntimeHost): Promise<{ raw: string | null; stats: ReadStats }> {
    const { value } = await host.call("host.storage.get", { scope: "user", key: READ_STATS_KEY });
    return { raw: value, stats: value === null ? {} : JSON.parse(value) as ReadStats };
  }

  // Read counts only rank notes for Dream review, so they never fail a read.
  // Workspace processes share user storage; compare-exchange keeps their counts.
  private async recordRead(file: string, host: RuntimeHost): Promise<void> {
    if (file.toLowerCase() === INDEX_FILE.toLowerCase()) return;
    const write = this.readStatsWrites.then(async () => {
      for (let attempt = 0; attempt < 5; attempt++) {
        const { raw, stats } = await this.readStats(host);
        stats[file] = { reads: (stats[file]?.reads ?? 0) + 1, last_read_at: new Date().toISOString() };
        if ((await host.call("host.storage.compare_exchange", { scope: "user", key: READ_STATS_KEY, expected: raw, value: JSON.stringify(stats) })).swapped) return;
      }
      throw new Error("concurrent updates kept changing the read statistics");
    }).catch((error: unknown) => { console.error("memory read statistics update failed:", error); });
    this.readStatsWrites = write;
    await write;
  }

  private async listNotebook(host: RuntimeHost): Promise<unknown> {
    const { stats } = await this.readStats(host);
    const rawIndex = await this.readFileOptional(INDEX_FILE);
    const indexed = indexedFiles(rawIndex);
    const files = (await this.readNotebook()).map(({ name, content, ...meta }) => ({
      file: name, ...meta, bytes: Buffer.byteLength(content), indexed: indexed.has(name),
      reads: stats[name]?.reads ?? 0, ...(stats[name] ? { last_read_at: stats[name].last_read_at } : {}),
    }));
    const existing = new Set(files.map((file) => file.file));
    const trimmed = rawIndex.trim();
    return {
      version: 1,
      index: {
        found: rawIndex !== "",
        bytes: Buffer.byteLength(trimmed), lines: trimmed ? trimmed.split("\n").length : 0,
        max_bytes: MAX_INDEX_BYTES, max_lines: MAX_INDEX_LINES, truncated: safeIndex(rawIndex, MAX_INDEX_BYTES).truncated,
      },
      files,
      dangling: [...indexed].filter((file) => !existing.has(file)).sort(compareNames),
    };
  }

  private safePath(name: string, allowIndex: boolean): string {
    name = name.trim();
    if (!name || name !== basename(name) || !name.endsWith(".md")) throw new Error("memory file must be a plain .md filename");
    if (!allowIndex && name.toLowerCase() === INDEX_FILE.toLowerCase()) throw new Error("MEMORY.md cannot be deleted");
    return join(this.notebook, name);
  }

  private async readFile(name: string): Promise<string> {
    const path = this.safePath(name, true);
    return (await readNotebookFile(this.notebook, basename(path))).toString();
  }

  private async readFileOptional(name: string): Promise<string> {
    try { return await this.readFile(name); }
    catch (error) { if (missing(error)) return ""; throw error; }
  }

  private async readNotebook(): Promise<FileInfo[]> {
    const entries = await readdir(this.notebook, { withFileTypes: true });
    const files: FileInfo[] = [];
    for (const entry of entries) {
      if (entry.isDirectory() || entry.name.startsWith(".") || !entry.name.endsWith(".md") || entry.name.toLowerCase() === INDEX_FILE.toLowerCase()) continue;
      const content = await this.readFile(entry.name);
      files.push({ ...frontmatter(content), name: entry.name, content });
    }
    return files.sort((a, b) => compareNames(a.name, b.name));
  }
}

function compareNames(a: string, b: string): number { return Buffer.compare(Buffer.from(a), Buffer.from(b)); }

export function createMemoryPlugin(): RuntimePlugin { return new MemoryPlugin(); }

export default createMemoryPlugin();
