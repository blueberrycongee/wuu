// The process owns transport; user code runs only inside the embedded interpreter.
import net from "node:net";
import * as nodeModule from "node:module";

const [nodeMajor, nodeMinor] = process.versions.node.split(".").map(Number);
if (nodeMajor < 22 || (nodeMajor === 22 && nodeMinor < 19) || typeof nodeModule.stripTypeScriptTypes !== "function") {
  throw new Error("PTC requires Node.js 22.19+ or the desktop Node runtime");
}

let input = "";
for await (const chunk of process.stdin) input += chunk;
const boot = JSON.parse(input);
for (const key of Object.keys(process.env)) delete process.env[key];
const moduleURL = source => "data:text/javascript;base64," + Buffer.from(source).toString("base64");
let engineSource = boot.modules["index.js"];
for (const name of ["wasi-shim.js", "extensions.js", "version.js"]) {
  engineSource = engineSource.replaceAll("'./" + name + "'", JSON.stringify(moduleURL(boot.modules[name])));
}
const { QuickJS, MAX_STACK_SIZE } = await import(moduleURL(engineSource));
const vm = await QuickJS.create({ wasm: Buffer.from(boot.wasm, "base64"), memoryLimit: 128 * 1024 * 1024, maxStackSize: MAX_STACK_SIZE });
const [host, port] = boot.address.split(":");
const channel = net.createConnection({ host, port: Number(port) });
await new Promise((resolve, reject) => { channel.once("connect", resolve); channel.once("error", reject); });
const maxFrame = 64 * 1024 * 1024;
function send(value) {
  const body = Buffer.from(JSON.stringify(value));
  if (body.length > maxFrame || channel.writableLength + body.length > maxFrame) throw new Error("PTC control message exceeds the byte limit");
  const header = Buffer.alloc(4);
  header.writeUInt32LE(body.length);
  channel.write(Buffer.concat([header, body]));
}
send({ secret: boot.secret });
let finished = false;
function fail(error) {
  if (finished) return;
  finished = true;
  send({ type: "done", error: String(error?.message ?? error).slice(0, 8192) });
}
// This function is serialized into the guest. Its closures never contain a host object.
function guestSetup(bridge, namesJSON, stateJSON, stateEnabled, maxStateBytes, maxStateKeys) {
  const stringify = JSON.stringify, parse = JSON.parse, string = String;
  const uncurry = fn => Function.prototype.call.bind(fn);
  const setHas = uncurry(Set.prototype.has), setAdd = uncurry(Set.prototype.add), setDelete = uncurry(Set.prototype.delete);
  const mapGet = uncurry(Map.prototype.get), mapSet = uncurry(Map.prototype.set), mapDelete = uncurry(Map.prototype.delete);
  const mapForEach = uncurry(Map.prototype.forEach);
  const mapSize = uncurry(Object.getOwnPropertyDescriptor(Map.prototype, "size").get);
  const hasOwn = uncurry(Object.prototype.hasOwnProperty), slice = uncurry(String.prototype.slice);
  const charCodeAt = uncurry(String.prototype.charCodeAt);
  const promiseThen = uncurry(Promise.prototype.then), NativePromise = Promise;
  const NativeSet = Set, NativeError = Error;
  const ownKeys = Reflect.ownKeys, descriptor = Object.getOwnPropertyDescriptor;
  const getPrototype = Object.getPrototypeOf, plainPrototype = Object.prototype;
  const isArray = Array.isArray, finite = Number.isFinite, is = Object.is;
  const pending = new Map();
  const state = new Map();
  let stateBytes = 0;
  let nextID = 1;
  function utf8Bytes(value) {
    let bytes = 0;
    for (let index = 0; index < value.length; index++) {
      const code = charCodeAt(value, index);
      if (code < 0x80) bytes++;
      else if (code < 0x800) bytes += 2;
      else if (code >= 0xd800 && code <= 0xdbff && index + 1 < value.length && charCodeAt(value, index + 1) >= 0xdc00 && charCodeAt(value, index + 1) <= 0xdfff) { bytes += 4; index++; }
      else bytes += 3;
    }
    return bytes;
  }
  const initial = parse(stateJSON), stateKeys = ownKeys(initial);
  for (let index = 0; index < stateKeys.length; index++) {
    const key = stateKeys[index], value = initial[key];
    mapSet(state, key, value);
    stateBytes += utf8Bytes(key) + utf8Bytes(value);
  }
  function json(value, seen = new NativeSet()) {
    if (value === null || typeof value === "string" || typeof value === "boolean") return stringify(value);
    if (typeof value === "number" && finite(value) && !is(value, -0)) return stringify(value);
    if (typeof value !== "object" || setHas(seen, value)) throw new NativeError("Expected lossless JSON");
    const array = isArray(value), proto = getPrototype(value);
    if (!array && proto !== plainPrototype && proto !== null) throw new NativeError("Expected plain JSON");
    setAdd(seen, value);
    const keys = ownKeys(value);
    let encoded = array ? "[" : "{", count = 0;
    if (array && keys.length !== value.length + 1) throw new NativeError("Expected dense JSON array");
    for (let index = 0; index < keys.length; index++) {
      const key = keys[index];
      if (array && key === "length") continue;
      const property = descriptor(value, key);
      if (typeof key !== "string" || !property.enumerable || !hasOwn(property, "value")) throw new NativeError("Expected JSON data properties");
      if (array && (string(count) !== key)) throw new NativeError("Expected dense JSON array");
      if (count++) encoded += ",";
      encoded += (array ? "" : stringify(key) + ":") + json(property.value, seen);
    }
    setDelete(seen, value);
    return encoded + (array ? "]" : "}");
  }
  class ToolCallError extends NativeError {
    constructor(name, message, result) {
      super(message); this.name = "ToolCallError"; this.toolName = name;
      if (result !== undefined) this.result = result;
    }
  }
  function stateKey(key) {
    if (!stateEnabled) throw new NativeError("PTC store/load/remove require a caller state scope");
    if (typeof key !== "string") throw new NativeError("Expected a state key string");
    return stringify(key);
  }
  function store(key, value) {
    const encodedKey = stateKey(key), encodedValue = json(value);
    const previous = mapGet(state, encodedKey);
    if (previous === undefined && mapSize(state) >= maxStateKeys) throw new NativeError("PTC state key limit exceeded (" + maxStateKeys + ")");
    const bytes = stateBytes + utf8Bytes(encodedValue) + (previous === undefined ? utf8Bytes(encodedKey) : -utf8Bytes(previous));
    if (bytes > maxStateBytes) throw new NativeError("PTC state exceeds the byte limit (" + maxStateBytes + ")");
    mapSet(state, encodedKey, encodedValue);
    stateBytes = bytes;
  }
  function load(key) {
    const value = mapGet(state, stateKey(key));
    return value === undefined ? undefined : parse(value);
  }
  function remove(key) {
    const encodedKey = stateKey(key), value = mapGet(state, encodedKey);
    if (value === undefined) return false;
    stateBytes -= utf8Bytes(encodedKey) + utf8Bytes(value);
    return mapDelete(state, encodedKey);
  }
  function stateSnapshot() {
    let encoded = "{", count = 0;
    mapForEach(state, (value, key) => {
      if (count++) encoded += ",";
      encoded += stringify(key) + ":" + stringify(value);
    });
    return encoded + "}";
  }
  function request(type, name, args) {
    if (typeof name !== "string") throw new NativeError("Expected a tool name string");
    const encoded = json(args);
    return new NativePromise((resolve, reject) => {
      if (mapSize(pending) >= 128) { reject(new ToolCallError(name, "Too many pending calls; process in bounded batches")); return; }
      const id = nextID;
      const message = json({ type, id, name, args: parse(encoded) });
      nextID++;
      mapSet(pending, id, { resolve, reject, name, type });
      try { bridge(message); }
      catch (error) { mapDelete(pending, id); nextID--; reject(error); }
    });
  }
  const tools = Object.create(null);
  for (const name of parse(namesJSON)) Object.defineProperty(tools, name, { value: args => request("call", name, args) });
  const console = Object.create(null);
  function log(...values) {
    let text = "";
    for (let index = 0; index < values.length; index++) {
      const value = values[index];
      if (index) text += " ";
      if (typeof value === "string") text += value;
      else {
        try { text += json(value); } catch { text += string(value); }
      }
    }
    bridge(json({ type: "log", text }));
  }
  for (const name of ["log", "info", "warn", "error", "debug"]) console[name] = log;
  const searchTools = (query, options = {}) => request("search", "searchTools", { query, ...options });
  const describeTool = name => request("describe", name, {});
  return {
    hasPendingCalls() { return mapSize(pending) > 0; },
    run(program) {
      const failed = error => {
        let message;
        try { message = slice(string(error?.message ?? error), 0, 8192); }
        catch { message = "Program failed with an unprintable error"; }
        bridge(json({ type: "done", error: message }));
      };
      promiseThen(program(tools, console, ToolCallError, searchTools, describeTool, store, load, remove), value => {
        try {
          const encoded = value === undefined ? undefined : json(value);
          bridge('{"type":"done"' + (encoded === undefined ? "" : ',"value":' + encoded) + (stateEnabled ? ',"state":' + stateSnapshot() : "") + '}');
        } catch (error) { failed(error); }
      }, failed);
    },
    settle(encoded) {
      const reply = parse(encoded), call = mapGet(pending, reply.id);
      if (!call) throw new NativeError("Unknown PTC reply");
      mapDelete(pending, reply.id);
      if (reply.error) call.reject(new ToolCallError(call.name, reply.error, call.type === "call" && hasOwn(reply, "value") ? reply.value : undefined));
      else call.resolve(reply.value);
    }
  };
}
let printedBytes = 0;
const bridge = vm.newFunction("bridge", encoded => {
  if (finished) return vm.undefined;
  const frame = JSON.parse(encoded.toString());
  if (frame.type === "log") {
    printedBytes += Buffer.byteLength(JSON.stringify(frame.text)) + 1;
    if (printedBytes > boot.maxOutputBytes) { fail(new Error("PTC output exceeded the byte limit")); return vm.undefined; }
  }
  send(frame);
  if (frame.type === "done") finished = true;
  return vm.undefined;
});
const api = vm.withScope(scope => scope.escape(vm.callFunction(vm.evalCode("(" + guestSetup.toString() + ")"), vm.undefined, bridge, vm.newString(JSON.stringify(boot.tools ?? [])), vm.newString(JSON.stringify(boot.state ?? {})), boot.stateEnabled ? vm.true : vm.false, vm.newNumber(boot.maxStateBytes), vm.newNumber(boot.maxStateKeys))));
const settle = api.getProp("settle"), run = api.getProp("run"), hasPendingCalls = api.getProp("hasPendingCalls");
function drain() {
  vm.executePendingJobs();
  // Without timers or I/O, only a pending host call can resume an idle guest.
  if (!finished && !vm.withScope(() => vm.callFunction(hasPendingCalls, api).toBoolean())) {
    fail(new Error("The program is waiting on a promise that can never settle: no host call is pending and timers are unavailable."));
  }
}
let buffer = Buffer.alloc(0);
channel.on("data", chunk => {
  if (finished) return;
  buffer = Buffer.concat([buffer, chunk]);
  try {
    while (buffer.length >= 4) {
      const size = buffer.readUInt32LE();
      if (!size || size > maxFrame) throw new Error("Invalid PTC reply size");
      if (buffer.length < size + 4) return;
      const encoded = buffer.subarray(4, size + 4).toString();
      buffer = buffer.subarray(size + 4);
      vm.withScope(() => vm.callFunction(settle, api, vm.newString(encoded)));
      drain();
    }
  } catch (error) { fail(error); }
});
channel.on("error", () => process.exit(1));
try {
  const source = nodeModule.stripTypeScriptTypes("(async function(tools, console, ToolCallError, searchTools, describeTool, store, load, remove) {\n" + boot.code + "\n})");
  vm.withScope(() => vm.callFunction(run, api, vm.evalCode(source, "program.ts")));
  drain();
} catch (error) { fail(error); }
// The Go owner stops this process, including a busy interpreter, on every exit path.
await new Promise(() => {});
