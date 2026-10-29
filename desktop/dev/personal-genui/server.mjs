import { createServer } from "node:http";
import { readFile, writeFile, rename } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
const root = fileURLToPath(new URL("../../../", import.meta.url));
const output = resolve(process.env.GENUI_OUTPUT ?? `${root}/.tmp/personal-genui`);
const { PersonalGenUI, toolSource } = await import(pathToFileURL(`${output}/service.mjs`));
const { fixtures } = await import(pathToFileURL(`${output}/fixtures.mjs`));
const path = `${output}/fixture-state.json`;
let data = { storage: {}, deliveries: {}, writes: 0 };
try { data = JSON.parse(await readFile(path, "utf8")); } catch (error) { if (error.code !== "ENOENT") throw error; }
async function persist() { await writeFile(`${path}.tmp`, JSON.stringify(data)); await rename(`${path}.tmp`, path); }
const binding = { owner: "synthetic-owner", session: "synthetic-main", agent: "synthetic-twin" };
const owner = { ...binding, domain: "personal", source: "owner" }, model = { ...owner, source: "model" };
const host = { async call(method, p) {
  if (method === "host.storage.get") return { value: data.storage[p.key] ?? null };
  if (method === "host.storage.compare_exchange") {
    if ((data.storage[p.key] ?? null) !== p.expected) return { swapped: false, value: data.storage[p.key] ?? null };
    data.storage[p.key] = p.value; data.writes++; await persist(); return { swapped: true, value: p.value };
  }
  if (method === "host.session.inspect") {
    const sent = data.deliveries[p.request_id];
    return { receipt: sent ? { request_id: sent.user_input_request_id, host_request_id: p.request_id, session_id: sent.session_id, user_space_id: binding.owner, product_domain: "personal", agent_id: binding.agent, plugin_id: "personal", receipt_id: `fixture-${sent.user_input_request_id}`, receipt_state: "accepted", execution_state: "queued", revision: 1 } : undefined };
  }
  if (method === "host.session.send") {
    if (!data.deliveries[p.request_id]) { data.deliveries[p.request_id] = p; await persist(); }
    return { state: "queued", session_id: p.session_id, queue_id: p.request_id };
  }
  throw new Error(`fixture host does not implement ${method}`);
} };
const service = new PersonalGenUI(host, binding), ids = {};
for (const [name, spec] of Object.entries(fixtures)) ids[name] = (await service.publish({ ...toolSource(name, `turn-${name}`, "fixture-ui-tool"), spec }, model)).surfaceId;
let queue = Promise.resolve();
const server = createServer((request, response) => {
  // The fixture serializes file writes; production concurrency uses host CAS.
  queue = queue.then(async () => {
    try {
      const base = `http://127.0.0.1:${server.address().port}`, url = new URL(request.url, base);
      if (request.headers.host !== new URL(base).host || (request.headers.origin && request.headers.origin !== base)) { response.writeHead(403); response.end(); return; }
      if (url.pathname === "/") { response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }); response.end(await readFile(`${output}/preview.html`)); return; }
      let body = ""; for await (const chunk of request) { body += chunk; if (Buffer.byteLength(body) > 65536) throw new Error("fixture request too large"); }
      const input = body ? JSON.parse(body) : undefined;
      let result;
      switch (`${request.method} ${url.pathname}`) {
        case "GET /fixture/bootstrap": result = Object.fromEntries(await Promise.all(Object.entries(ids).map(async ([name, id]) => [name, await service.inspect(id, owner)]))); break;
        case "POST /fixture/action": {
          result = await service.act(input, owner);
          if (result.submission) result = await service.retrySubmission(result.surface.surfaceId, owner, host);
          break;
        }
        case "GET /fixture/read": result = await service.inspect(ids[url.searchParams.get("id")], owner); break;
        case "POST /fixture/retry": result = await service.retrySubmission(ids[input.id], owner, host); break;
        case "GET /fixture/status": result = { fixture: true, modelCalls: 0, writes: data.writes, recordedSubmissions: Object.keys(data.deliveries).length }; break;
        default: response.writeHead(404); response.end(); return;
      }
      response.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" }); response.end(JSON.stringify(result));
    } catch (error) { response.writeHead(400, { "Content-Type": "application/json" }); response.end(JSON.stringify({ error: String(error) })); }
  });
});
server.listen(0, "127.0.0.1", () => console.log(JSON.stringify({ fixture: true, realModel: false, url: `http://127.0.0.1:${server.address().port}` })));
