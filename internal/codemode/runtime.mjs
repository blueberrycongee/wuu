// One program per process. The Go owner supplies authority and owns termination.
import net from "node:net";
import { stripTypeScriptTypes } from "node:module";
import { inspect } from "node:util";

let input = "";
for await (const chunk of process.stdin) input += chunk;
const boot = JSON.parse(input);
for (const key of Object.keys(process.env)) delete process.env[key];
const [host, port] = boot.address.split(":");
const channel = net.createConnection({ host, port: Number(port) });
await new Promise((resolve, reject) => { channel.once("connect", resolve); channel.once("error", reject); });
const maxFrame = 64 * 1024 * 1024;
function send(value) {
  const body = Buffer.from(JSON.stringify(value));
  if (body.length > maxFrame) throw new Error("PTC control message exceeds the byte limit");
  const header = Buffer.alloc(4);
  header.writeUInt32LE(body.length);
  channel.write(Buffer.concat([header, body]));
}
send({ secret: boot.secret });
const pending = new Map();
let buffer = Buffer.alloc(0);
channel.on("data", chunk => {
  buffer = Buffer.concat([buffer, chunk]);
  while (buffer.length >= 4) {
    const size = buffer.readUInt32LE();
    if (!size || size > maxFrame) { channel.destroy(); return; }
    if (buffer.length < size + 4) return;
    const reply = JSON.parse(buffer.subarray(4, size + 4));
    buffer = buffer.subarray(size + 4);
    const call = pending.get(reply.id);
    if (!call) { channel.destroy(); return; }
    pending.delete(reply.id);
    if (reply.error) call.reject(new ToolCallError(call.name, reply.error));
    else call.resolve(reply.value);
  }
});
// Only JSON values may cross the process boundary; JSON.stringify alone loses
// undefined, non-finite numbers, prototypes and cyclic references.
function jsonValue(value, seen = new Set()) {
  if (value === null || typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value !== "object" || seen.has(value)) throw new Error("Expected lossless JSON");
  const proto = Object.getPrototypeOf(value);
  if (!Array.isArray(value) && proto !== Object.prototype && proto !== null) throw new Error("Expected plain JSON");
  seen.add(value);
  const out = Array.isArray(value) ? Array.from(value, item => jsonValue(item, seen)) : Object.fromEntries(Object.entries(value).map(([key, item]) => [key, jsonValue(item, seen)]));
  seen.delete(value);
  return out;
}
class ToolCallError extends Error {
  constructor(toolName, message) { super(message); this.name = "ToolCallError"; this.toolName = toolName; }
}
let nextID = 1;
const tools = Object.create(null);
for (const tool of boot.tools ?? []) {
  Object.defineProperty(tools, tool.name, { value: async args => {
    const normalized = jsonValue(args);
    if (pending.size >= 128) throw new ToolCallError(tool.name, "Too many pending tool calls; process in bounded batches");
    return new Promise((resolve, reject) => {
      const id = nextID++;
      pending.set(id, { name: tool.name, resolve, reject });
      try { send({ type: "call", id, name: tool.name, args: normalized }); }
      catch (error) { pending.delete(id); nextID--; reject(error); }
    });
  } });
}
let printedBytes = 0;
function log(...values) {
  const text = values.map(v => typeof v === "string" ? v : inspect(v, { depth: 6, maxArrayLength: 100, maxStringLength: boot.maxOutputBytes })).join(" ");
  printedBytes += Buffer.byteLength(JSON.stringify(text)) + 1;
  if (printedBytes > boot.maxOutputBytes) throw new Error("PTC output exceeded the byte limit");
  send({ type: "log", text });
}
const consoleShim = Object.fromEntries(["log", "info", "warn", "error", "debug"].map(name => [name, log]));
try {
  const prefix = "async function __program__() {\n", suffix = "\n}";
  const stripped = stripTypeScriptTypes(prefix + boot.code + suffix).slice(prefix.length, -suffix.length);
  const AsyncFunction = (async () => {}).constructor;
  const value = await new AsyncFunction("tools", "console", "ToolCallError", "'use strict';\n" + stripped)(tools, consoleShim, ToolCallError);
  send({ type: "done", ...(value === undefined ? {} : { value: jsonValue(value) }) });
} catch (error) {
  send({ type: "done", error: String(error?.message ?? error).slice(0, 8192) });
}
// Keep the root process alive until its owner stops the complete process tree.
await new Promise(() => {});
