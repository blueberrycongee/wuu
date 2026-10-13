import { randomBytes } from "node:crypto";
import { lstat, mkdir, readdir, unlink } from "node:fs/promises";
import { basename, join } from "node:path";
import {
  requireKernelService, FILE_LOCK_SERVICE,
  type CapabilityInvokeParams, type RuntimeExecutionContext, type RuntimeHost,
  type RuntimeInitializeParams, type RuntimeInitializeResult, type RuntimePlugin,
  type ServiceInvokeParams, type ToolExecuteParams, type ToolExecuteResult,
  type JSONSchemaObject,
} from "@wuu/plugin-sdk";
import { frontmatter, INDEX_FILE, MAX_MEMORY_BYTES, missing, promptSection, readNotebookFile, readNotebookSnapshot, readSafeIndex, writeAtomicFile } from "./notebook.js";
import { runSessionMemory, sessionMemorySchema } from "./workspace-memory.js";

const CAPABILITY_PROMPT = "agent.system_prompt.section";
const CAPABILITY_CLIENT = "plugin.client.request";
const CAPABILITY_LIFECYCLE = "agent.turn.lifecycle";
const SESSION_MEMORY_SERVICE = "memory.session";
const NOTEBOOK_MEMORY_SERVICE = "memory.notebook";

interface Job {
  id: string;
  kind: string;
  request_id: string;
  session_id: string;
  state: string;
  output?: string;
  error?: string;
  changed_files?: { path: string; action: string }[];
  created_at: string;
}
interface FileInfo { name: string; type?: string; description?: string; content: string }
interface FileStamp { size: bigint; modified: bigint }

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
  private readonly jobs = new Map<string, Job>();
  private readonly jobSnapshots = new Map<string, Map<string, FileStamp>>();

  initialize(params: RuntimeInitializeParams, host: RuntimeHost): RuntimeInitializeResult {
    this.lifecycleHost = host;
    const home = params.wuu_home.trim();
    if (!home) throw new Error("memory plugin requires wuu_home");
    this.notebook = join(home, "memory");
    this.workspaceStateDir = params.workspace_state_dir?.trim() ?? "";
    return {
      tools: [
        { id: "memory_list", description: "List the user's durable memory notebook files. Use only when durable cross-project user preferences, feedback, references, or lessons are relevant.", input_schema: objectSchema(), activity: { read_only: true, concurrency_safe: true, risk: "low" } },
        { id: "memory_read", description: "Read one Markdown file from the user's durable memory notebook. MEMORY.md is the index; topic files contain the actual memories.", input_schema: objectSchema({ file: stringField("Notebook filename such as MEMORY.md or feedback_testing.md.") }, "file"), activity: { read_only: true, concurrency_safe: true, risk: "low" } },
        { id: "memory_write", description: "Create or replace one Markdown file in the user's durable memory notebook. Saving a memory requires updating both its topic file and the MEMORY.md index.", input_schema: objectSchema({ file: stringField("Notebook Markdown filename."), content: stringField("Complete replacement Markdown content.") }, "file", "content") },
        { id: "memory_delete", description: "Delete one topic file from the user's durable memory notebook. Also remove its pointer from MEMORY.md. MEMORY.md itself cannot be deleted.", input_schema: objectSchema({ file: stringField("Topic Markdown filename to delete.") }, "file") },
        { id: "session_memory", description: "Read or update workspace and current-session memory. Use project_memory only for stable workspace facts; summary for compact recoverable task state; checkpoint only for legacy compatibility; notes for session scratch notes.", input_schema: sessionMemorySchema() },
      ],
      capabilities: [
        { id: CAPABILITY_PROMPT, kind: "transform", version: 1 },
        { id: CAPABILITY_CLIENT, kind: "decision", version: 1 },
        { id: CAPABILITY_LIFECYCLE, kind: "observe", version: 1, error_policy: "isolate" },
      ],
      required_services: [requireKernelService("host.session.create"), requireKernelService("host.session.send"), { name: FILE_LOCK_SERVICE, major_version: 1, required: true }],
      provided_services: [{
        name: SESSION_MEMORY_SERVICE, version: "1.0.0",
        methods: ["status", "read", "append", "replace"].map((name) => ({ name, input_schema: `memory.session.${name}.request.v1`, output_schema: `memory.session.${name}.response.v1` })),
      }, {
        name: NOTEBOOK_MEMORY_SERVICE, version: "1.0.0",
        methods: ["index", "read"].map((name) => ({ name, input_schema: `memory.notebook.${name}.request.v1`, output_schema: "memory.notebook.snapshot.v1" })),
      }],
    };
  }

  async activate(): Promise<void> {
    if (!this.notebook.trim()) throw new Error("memory: notebook path is required");
    await mkdir(this.notebook, { recursive: true, mode: 0o755 });
  }

  async executeTool(call: ToolExecuteParams, _host: RuntimeHost, execution: RuntimeExecutionContext): Promise<ToolExecuteResult> {
    if (call.tool_id === "memory_list") return jsonResult({ files: await this.readNotebook() });
    const input = object(call.arguments);
    if (call.tool_id === "session_memory") {
      return jsonResult(await runSessionMemory(this.workspaceStateDir, {
        action: string(input.action), target: string(input.target), content: string(input.content), source: string(input.source),
      }, call.session_id?.trim() || call.thread_id?.trim() || "", this.lifecycleHost, execution));
    }
    const file = string(input.file);
    switch (call.tool_id) {
      case "memory_read": return jsonResult({ file, content: await this.readFile(file) });
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

  async invokeCapability(call: CapabilityInvokeParams, host: RuntimeHost): Promise<{ output: unknown }> {
    switch (call.capability) {
      case CAPABILITY_PROMPT: return { output: { text: promptSection(this.notebook, await readSafeIndex(this.notebook)) } };
      case CAPABILITY_CLIENT: {
        const input = object(call.input);
        return { output: { result: await this.clientRequest(string(input.method), input.input, host) } };
      }
      case CAPABILITY_LIFECYCLE: await this.settle(object(call.input)); return { output: {} };
      default: throw new Error(`unknown memory capability ${JSON.stringify(call.capability)}`);
    }
  }

  async invokeService(call: ServiceInvokeParams, _host: RuntimeHost, execution: RuntimeExecutionContext): Promise<unknown> {
    if (call.service === NOTEBOOK_MEMORY_SERVICE) return readNotebookSnapshot(this.notebook, call.method, object(call.params));
    if (call.service !== SESSION_MEMORY_SERVICE) throw new Error(`memory plugin does not provide service ${JSON.stringify(call.service)}`);
    const params = object(call.params);
    return runSessionMemory(this.workspaceStateDir, {
      action: call.method, target: string(params.target), content: string(params.content), source: string(params.source).trim() || call.caller.trim(),
    }, string(params.thread_id), this.lifecycleHost, execution);
  }

  private async clientRequest(method: string, raw: unknown, host: RuntimeHost): Promise<unknown> {
    switch (method.trim()) {
      case "memory.read": return { index_raw: await this.readFileOptional(INDEX_FILE), files: await this.readNotebook() };
      case "memory.job.get": {
        const id = string(object(raw).id);
        const job = this.jobs.get(id.trim());
        if (!job) throw new Error(`memory job ${JSON.stringify(id)} not found`);
        return { ...job };
      }
      case "memory.overview.start":
        return this.startJob("overview", "Read the user's memory notebook with memory_list and memory_read. Produce a concise Markdown overview of what is actually recorded, grouped by useful themes. Do not modify memory. Clearly say when the notebook is empty.", host);
      case "memory.chat.start": {
        const message = string(object(raw).message).trim();
        if (!message) throw new Error("memory chat message is required");
        return this.startJob("chat", "Manage the user's durable memory according to this request:\n\n" + message + "\n\nInspect existing files first. Use memory_write and memory_delete as needed, keeping MEMORY.md as a one-line index rather than a content file. Do not save secrets, raw transcripts, temporary task state, commit ids, or repository-derived facts. Finish with a concise explanation of exactly what changed.", host);
      }
      default: throw new Error(`unknown memory client method ${JSON.stringify(method)}`);
    }
  }

  private async startJob(kind: string, prompt: string, host: RuntimeHost): Promise<Job> {
    const id = "memory-" + randomBytes(10).toString("hex");
    const created = await host.call("host.session.create", { request_id: id + ":session", name: "Memory " + kind, visibility: "plugin", context_source: "fresh", workspace: "shared" });
    const entry: Job = { id, kind, request_id: id + ":turn", session_id: created.session_id, state: "starting", created_at: new Date().toISOString() };
    if (kind === "chat") this.jobSnapshots.set(id, await this.snapshotFiles());
    this.jobs.set(id, entry);
    try {
      const sent = await host.call("host.session.send", { request_id: entry.request_id, session_id: entry.session_id, input: { prompt }, cause: "memory." + kind });
      // A terminal lifecycle notification must not be undone by a late send acknowledgement.
      if (entry.state === "starting") entry.state = sent.state;
    } catch (error) {
      entry.state = "failed";
      entry.error = error instanceof Error ? error.message : String(error);
      throw error;
    }
    return { ...entry };
  }

  private async settle(input: Record<string, unknown>): Promise<void> {
    const state = string(input.state);
    if (!["completed", "failed", "interrupted", "discarded"].includes(state)) return;
    const requestId = string(input.request_id);
    for (const entry of this.jobs.values()) {
      if (entry.request_id !== requestId) continue;
      entry.state = state;
      const output = string(input.final_output).trim();
      const error = string(input.error).trim();
      if (output) entry.output = output; else delete entry.output;
      if (error) entry.error = error; else delete entry.error;
      if (entry.kind === "chat") {
        const before = this.jobSnapshots.get(entry.id) ?? new Map<string, FileStamp>();
        const after = await this.snapshotFiles();
        const changed: { path: string; action: string }[] = [];
        for (const [path, current] of after) {
          const previous = before.get(path);
          if (previous?.size === current.size && previous.modified === current.modified) continue;
          changed.push({ path, action: previous ? "modified" : "created" });
        }
        for (const path of before.keys()) if (!after.has(path)) changed.push({ path, action: "deleted" });
        changed.sort((a, b) => compareNames(a.path, b.path));
        if (changed.length) entry.changed_files = changed; else delete entry.changed_files;
      }
      return;
    }
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
      if (entry.isDirectory() || !entry.name.endsWith(".md") || entry.name.toLowerCase() === INDEX_FILE.toLowerCase()) continue;
      const content = await this.readFile(entry.name);
      files.push({ name: entry.name, ...frontmatter(content), content });
    }
    return files.sort((a, b) => compareNames(a.name, b.name));
  }

  private async snapshotFiles(): Promise<Map<string, FileStamp>> {
    const out = new Map<string, FileStamp>();
    const entries = await readdir(this.notebook, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (entry.isDirectory() || !entry.name.endsWith(".md")) continue;
      const info = await lstat(join(this.notebook, entry.name), { bigint: true }).catch(() => undefined);
      if (info) out.set(entry.name, { size: info.size, modified: info.mtimeNs });
    }
    return out;
  }
}

function compareNames(a: string, b: string): number { return Buffer.compare(Buffer.from(a), Buffer.from(b)); }

export default new MemoryPlugin();
