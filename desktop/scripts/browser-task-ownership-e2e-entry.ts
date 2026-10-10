// Real Go threads and browser tools, production desktop reverse RPC/host, and
// Chromium pages. A local model supplies deterministic tool calls. The takeover
// case holds the result of an actual CDP geometry read to exercise its async
// boundary without replacing the browser or fabricating a command response.
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { createServer, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { app, BrowserWindow, WebContentsView } from "electron";
import { AppServerClientPool } from "../src/main/appServerClients";
import {
  BrowserHostCoordinator, defaultBrowserHostDeps,
  type BrowserHostWindowHandle, type BrowserViewHandle,
} from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";
import {
  APP_SERVER_PROTOCOL_VERSION, BROWSER_REVERSE_RPC_METHODS,
  type ActivitySession, type RuntimeContext, type ServerEvent, type Thread,
} from "../src/shared/protocol";

const desktop = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = process.env.WUU_BROWSER_OWNERSHIP_OUTPUT ?? join(desktop, "out", "browser-task-ownership");
const fixture = mkdtempSync(join(tmpdir(), "wuu-browser-ownership-"));
const home = join(fixture, "home");
const workdir = join(fixture, "project");
for (const directory of [output, home, workdir]) mkdirSync(directory, { recursive: true });
app.setPath("userData", join(fixture, "profile"));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_BROWSER_OWNERSHIP_CORE ?? join(desktop, "build", "bin", "wuu-core");
process.env.WUU_SAFE_MODE = "1";
process.env.WUU_ENABLE_BROWSER = "1";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(yes => { resolve = yes; });
  return { promise, resolve };
}
async function bounded<T>(label: string, promise: Promise<T>, timeout = 30_000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([promise, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`Timed out: ${label}`)), timeout);
    })]);
  } finally { clearTimeout(timer); }
}

const events: unknown[] = [];
const calls: { id: string; method: string; params: Record<string, unknown> }[] = [];
const providerRequests: unknown[] = [];
const failures: string[] = [];
const checks: { name: string; passed: boolean; detail?: string }[] = [];
function check(name: string, condition: unknown, detail?: string) {
  checks.push({ name, passed: Boolean(condition), ...(detail ? { detail } : {}) });
  if (!condition) failures.push(`${name}${detail ? `: ${detail}` : ""}`);
}
const completed = new Map<string, ReturnType<typeof deferred>>();
const activityByThread = new Map<string, ActivitySession>();
const revocations = new Map<string, ReturnType<typeof deferred>>();
const turns = new Map<string, number>();
const providerTabs = new Map<string, string>();
const cancellations = new Map<string, ReturnType<typeof deferred>>();
const hostCompletions = new Map<string, ReturnType<typeof deferred>>();
let heldControlThread: string | undefined;
const heldControls: { activity: ActivitySession; method: string }[] = [];
const aWaiting = deferred();
let heldA: ServerResponse | undefined;
const dWaiting = deferred();
let heldD: ServerResponse | undefined;
let popupWaiting = deferred();
let heldPopup: ServerResponse | undefined;
const popupRequests: { method: string; url: string; body: string }[] = [];
const popupAdoptions: { openerTabID: string; tabID: string; url: string }[] = [];
const popupLoads = new Map<string, ReturnType<typeof deferred>>();
const slowPopupStarted = deferred();
let slowPopupResponse: ServerResponse | undefined;
const popupNonce = "fixture-popup-nonce";
let baseURL = "";
let serial = 0;

function respond(res: ServerResponse, tool?: Record<string, unknown>) {
  res.writeHead(200, { "Content-Type": "text/event-stream" });
  const delta = tool ? { role: "assistant", tool_calls: [{ index: 0, id: `fixture-${++serial}`, type: "function", function: { name: "wuu_browser", arguments: JSON.stringify(tool) } }] }
    : { role: "assistant", content: "Browser fixture complete." };
  res.write(`data: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
  res.end(`data: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta: {}, finish_reason: tool ? "tool_calls" : "stop" }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } })}\n\ndata: [DONE]\n\n`);
}
const server = createServer((req, res) => {
  const pathname = new URL(req.url ?? "/", "http://127.0.0.1").pathname;
  if (pathname === "/popup-opener") {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(`<!doctype html><title>Popup opener</title>
      <style>button{position:absolute;left:40px;width:240px;height:45px}</style>
      <button style="top:40px" id="blank">Script-written blank popup</button>
      <button style="top:100px" id="message">Opener message popup</button>
      <form method="POST" action="/popup-form" target="_blank">
        <input type="hidden" name="nonce" value="${popupNonce}">
        <input type="hidden" name="payload" value="two words & a plus +">
        <button style="top:160px" id="post">Submit POST to new tab</button>
      </form>
      <button style="top:220px" id="named">Open named popup</button>
      <button style="top:280px" id="reuse">Reuse named popup</button>
      <button style="top:340px" id="slow">Open loading popup</button>
      <script>
        window.popupState={messages:[]};
        addEventListener('message', event => {
          if(event.origin===location.origin && event.data.nonce==='${popupNonce}') {
            popupState.messages.push({...event.data, sourceMatches:event.source===window.messageChild});
          }
        });
        document.querySelector('#blank').onclick=()=>{
          const child=window.open('about:blank','blank-proof');
          popupState.blankReturned=!!child;
          if(child){
            child.document.write('<!doctype html><title>Script-written popup</title><body data-nonce="${popupNonce}">Blank popup content</body>');
            child.document.close();
            popupState.blankHasOpener=child.opener===window;
          }
        };
        document.querySelector('#message').onclick=()=>{
          window.messageChild=window.open('/popup-message','message-proof');
          popupState.messageReturned=!!window.messageChild;
        };
        document.querySelector('#named').onclick=()=>{window.namedChild=window.open('/popup-named?step=1','reuse-proof');};
        document.querySelector('#reuse').onclick=()=>{
          const child=window.open('/popup-named?step=2','reuse-proof');
          popupState.namedReused=!!child && child===window.namedChild;
        };
        document.querySelector('#slow').onclick=()=>{window.slowChild=window.open('/popup-slow','slow-proof');};
      </script>`);
    return;
  }
  if (pathname === "/popup-slow") {
    popupRequests.push({ method: req.method ?? "", url: req.url ?? "", body: "" });
    slowPopupResponse = res;
    slowPopupStarted.resolve();
    return;
  }
  if (["/popup-message", "/popup-form", "/popup-named"].includes(pathname)) {
    let body = "";
    req.on("data", data => { body += data; });
    req.on("end", () => {
      popupRequests.push({ method: req.method ?? "", url: req.url ?? "", body });
      const proof = { kind: pathname.slice(1), nonce: popupNonce, method: req.method, body };
      res.writeHead(200, { "Content-Type": "text/html" });
      res.end(`<!doctype html><title>${pathname}${new URL(req.url ?? "/", "http://127.0.0.1").search}</title>
        <body><h1>Popup child</h1><button id="target" style="position:absolute;left:100px;top:100px;width:100px;height:50px">Target</button>
        <script>window.clicks=0;document.querySelector('#target').onclick=()=>window.clicks++;
        window.popupProof=${JSON.stringify(proof)};window.popupProof.hasOpener=!!window.opener;
        if(window.opener)window.opener.postMessage(window.popupProof,location.origin);
        fetch('/popup-ready?source='+encodeURIComponent(location.pathname+location.search));</script>`);
    });
    return;
  }
  if (pathname === "/popup-ready") {
    popupLoads.get(new URL(req.url ?? "/", "http://127.0.0.1").searchParams.get("source") ?? "")?.resolve();
    res.writeHead(204).end();
    return;
  }
  if (req.method === "GET") {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(`<!doctype html><title>${req.url}</title><body><h1>${req.url}</h1><button id="target" style="position:absolute;left:100px;top:100px;width:100px;height:50px">Target</button><input id="field" style="position:absolute;left:100px;top:200px"><script>window.clicks=0;document.querySelector('#target').onclick=()=>window.clicks++;</script>`);
    return;
  }
  let body = "";
  req.on("data", data => { body += data; });
  req.on("end", () => {
    const input = JSON.parse(body);
    providerRequests.push(input);
    if (!input.tools?.length) {
      if (input.stream) respond(res);
      else { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify({ choices: [{ message: { role: "assistant", content: "Browser fixture" }, finish_reason: "stop" }] })); }
      return;
    }
    const prompt = JSON.stringify(input.messages?.filter((message: { role: string }) => message.role === "user") ?? []);
    const marker = /OWNERSHIP_(?:POPUP|[ABCD])/.exec(prompt)?.[0];
    if (!marker) { respond(res); return; }
    const step = turns.get(marker) ?? 0;
    turns.set(marker, step + 1);
    if (step === 0) { respond(res, { action: "navigate", url: `${baseURL}/${marker === "OWNERSHIP_POPUP" ? "popup-opener" : marker}` }); return; }
    if (marker === "OWNERSHIP_POPUP") { heldPopup = res; popupWaiting.resolve(); return; }
    if (marker === "OWNERSHIP_A" && step === 1) { heldA = res; aWaiting.resolve(); return; }
    if (marker === "OWNERSHIP_A" && step === 2) { respond(res, { action: "finalize", keep: [] }); return; }
    if (marker === "OWNERSHIP_D") {
      const last = [...input.messages].reverse().find((message: { role: string }) => message.role === "tool");
      const text = typeof last?.content === "string" ? last.content : (last?.content ?? []).map((part: { text?: string }) => part.text ?? "").join("\n");
      if (step === 1) {
        const tabID = /"tab_id":"([^"]+)"/.exec(text)?.[1];
        if (!tabID) { res.writeHead(500).end(`Missing navigate tab: ${text}`); return; }
        providerTabs.set(marker, tabID);
        heldD = res;
        dWaiting.resolve();
        return;
      }
      if (step === 2) {
        const nodeID = /\[(\d+)\][^\n]*Target/.exec(text)?.[1];
        if (!nodeID) { res.writeHead(500).end(`Missing observed target: ${text}`); return; }
        respond(res, { action: "click", tab_id: providerTabs.get(marker), node_id: Number(nodeID) }); return;
      }
    }
    respond(res);
  });
});

let pool: AppServerClientPool | undefined;
let host: BrowserHostCoordinator | undefined;
const views: WebContentsView[] = [];
let geometryGate: { entered: ReturnType<typeof deferred>; resume: ReturnType<typeof deferred> } | undefined;
const injected = new Map<string, { resolve(value: unknown): void; reject(error: Error): void }>();
function notify(event: ServerEvent) {
  events.push(event);
  host?.handleServerEvent?.(event);
  if (event.kind === "server-request") {
    if (event.message.method.startsWith("browser/")) {
      const id = String(event.message.id);
      hostCompletions.set(id, deferred());
      calls.push({ id, method: event.message.method, params: event.message.params as Record<string, unknown> });
      void host!.handleServerRequest(event);
    } else pool!.rejectServerRequest(String(event.message.id), `Unexpected fixture request ${event.message.method}`);
  }
  if (event.kind !== "notification") return;
  if (event.message.method.startsWith("activity/")) {
    const activity = event.message.params as unknown as ActivitySession;
    activityByThread.set(activity.thread_id, activity);
    if (heldControlThread === activity.thread_id) heldControls.push({ activity, method: event.message.method });
    else host!.updateActivity(activity, event.message.method);
    if (activity.controller !== "agent" || activity.state === "stopped") revocations.get(activity.thread_id)?.resolve();
  }
  if (event.message.method === "browser/request_cancelled") {
    const params = event.message.params as unknown as { thread_id: string };
    cancellations.get(params.thread_id)?.resolve();
  }
  if (event.message.method === "turn/completed" || event.message.method === "turn/error") {
    const params = event.message.params as unknown as { thread_id: string };
    completed.get(params.thread_id)?.resolve();
  }
}
async function request(threadID: string, method: string, params: Record<string, unknown> = {}): Promise<unknown> {
  const id = `fixture-direct-${++serial}`;
  return bounded(method, new Promise((resolve, reject) => {
    injected.set(id, { resolve, reject });
    void host!.handleServerRequest({ workdir, kind: "server-request", message: {
      id, method, params: { workdir, thread_id: threadID, ...params },
    } } as Extract<ServerEvent, { kind: "server-request" }>);
  }));
}
async function readTab(tabID: string): Promise<WebContentsView | undefined> {
  const meta = host!.tabSurfaceMeta(workdir, tabID);
  return views.find(view => view.webContents && !view.webContents.isDestroyed() && view.webContents.getURL() === meta?.url);
}

app.whenReady().then(async () => {
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  baseURL = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
  writeFileSync(join(home, "config.json"), JSON.stringify({
    default_provider: "fixture",
    ptc: { enabled: false },
    agent: { tool_loading: "flat", max_steps: 20 },
    providers: { fixture: { type: "openai-compatible", base_url: `${baseURL}/v1`, api_key: "fixture-only", model: "fixture", models: { fixture: { tool_call: true, modalities: { input: ["text"], output: ["text"] }, limit: { context: 200000, output: 8000 } } } } },
    engines: Object.fromEntries(["codex", "claude", "cursor", "devin", "grok", "hermes", "pi", "opencode", "antigravity"].map(id => [id, { enabled: false }])),
  }));
  const main = new BrowserWindow({ show: false, width: 900, height: 650 });
  const context: RuntimeContext = { kind: "no_project", cwd: workdir };
  pool = new AppServerClientPool(() => context, () => workdir, notify, undefined, () => ({
    protocol_version: APP_SERVER_PROTOCOL_VERSION,
    client: { name: "browser-ownership-e2e", version: "fixture" },
    capabilities: { reverse_rpc: { methods: [...BROWSER_REVERSE_RPC_METHODS] } },
  }));
  host = new BrowserHostCoordinator({ mainWindow: () => main } as unknown as WindowRegistry, {
    respond(id, result) {
      hostCompletions.get(id)?.resolve();
      const direct = injected.get(id);
      if (direct) { injected.delete(id); direct.resolve(result); }
      else pool!.respondToServerRequest(id, result);
    },
    reject(id, message) {
      hostCompletions.get(id)?.resolve();
      const direct = injected.get(id);
      if (direct) { injected.delete(id); direct.reject(new Error(message)); }
      else pool!.rejectServerRequest(id, message);
    },
  }, defaultBrowserHostDeps(
    () => new BrowserWindow({ show: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
    () => {
      const view = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
      const send = view.webContents.debugger.sendCommand.bind(view.webContents.debugger);
      view.webContents.debugger.sendCommand = async (method, params, session) => {
        const result = await send(method, params, session);
        if (method === "DOM.getBoxModel" && geometryGate) {
          const gate = geometryGate;
          geometryGate = undefined;
          gate.entered.resolve();
          await gate.resume.promise;
        }
        return result;
      };
      views.push(view);
      return view as unknown as BrowserViewHandle;
    },
  ), () => undefined);
  host.setRendererSink({
    surface() {}, userInput() {}, presented() {},
    adopted(payload) { popupAdoptions.push(payload); },
  });
  pool.setClientTorndownHandler(cwd => host!.onClientTorndown(cwd));
  async function start(marker: string) {
    const { thread } = await pool!.request<{ thread: Thread }>("thread/start", { engine: "wuu", provider: "fixture", model: "fixture", permission_mode: "unconfined" });
    completed.set(thread.id, deferred());
    await pool!.request("turn/start", { thread_id: thread.id, prompt: marker });
    return thread.id;
  }
  // The fixture browser is real Chromium. The local provider drives every
  // opener click through the Go tool, reverse RPC, and native page input.
  const popup = await start("OWNERSHIP_POPUP");
  await bounded("popup opener is ready", popupWaiting.promise);
  const popupTab = String(calls.find(call => call.method === "browser/open_tab" && call.params.thread_id === popup)?.params.tab_id ?? "");
  const popupView = await readTab(popupTab);
  assert(popupView, "The real Go turn opens the popup fixture page");
  host.reportBounds(workdir, popupTab, main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 800, height: 600 }, 1, true);
  main.showInactive();
  await popupView.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  async function popupTool(tool: Record<string, unknown>) {
    const response = heldPopup;
    assert(response, "The provider is waiting for its next popup action");
    heldPopup = undefined;
    popupWaiting = deferred();
    respond(response, tool);
    await bounded(`popup action ${tool.action}`, popupWaiting.promise);
  }
  type ListedTabs = { tab_ids: string[]; tabs: { tab_id: string; url: string; title: string }[] };
  async function popupTabs() { return await request(popup, "browser/list_tabs") as ListedTabs; }
  async function clickPopup(y: number, path?: string) {
    if (path) popupLoads.set(path, deferred());
    await popupTool({ action: "click", tab_id: popupTab, x: 120, y });
    if (path) await bounded(`popup document ${path}`, popupLoads.get(path)!.promise);
  }
  await clickPopup(60);
  const blankState = await popupView.webContents.executeJavaScript("window.popupState");
  check("about:blank returns a live WindowProxy", blankState.blankReturned === true);
  check("script-written blank popup keeps its opener", blankState.blankHasOpener === true);
  const blank = (await popupTabs()).tabs.find(tab => tab.title === "Script-written popup");
  check("script-written about:blank is adopted into the owning task", Boolean(blank), JSON.stringify(await popupTabs()));
  if (blank) {
    const blankView = await readTab(blank.tab_id);
    check("adopted blank popup retains synchronously written content", await blankView?.webContents.executeJavaScript("document.body.dataset.nonce") === popupNonce);
  }
  await clickPopup(120, "/popup-message");
  const message = (await popupTabs()).tabs.find(tab => tab.url === `${baseURL}/popup-message`);
  check("navigated popup is adopted into the owning task", Boolean(message));
  const messageView = message ? await readTab(message.tab_id) : undefined;
  check("child sees the original opener", await messageView?.webContents.executeJavaScript("window.popupProof.hasOpener") === true);
  const messageProof = await popupView.webContents.executeJavaScript(`new Promise(resolve => {
    const proof = () => window.popupState.messages.find(message => message.kind === 'popup-message');
    if(proof()) return resolve(proof());
    const timer = setTimeout(() => { removeEventListener('message', receive); resolve(null); }, 2000);
    function receive() { if(proof()) { clearTimeout(timer); removeEventListener('message', receive); resolve(proof()); } }
    addEventListener('message', receive);
  })`);
  check("child postMessage reaches opener with original window identity", messageProof?.nonce === popupNonce && messageProof?.sourceMatches === true, JSON.stringify(messageProof));
  await clickPopup(180, "/popup-form");
  const submitted = popupRequests.filter(item => item.url === "/popup-form");
  const formBody = new URLSearchParams(submitted[0]?.body);
  check("target=_blank form preserves POST and encoded body", submitted.length === 1 && submitted[0]?.method === "POST" && formBody.get("nonce") === popupNonce && formBody.get("payload") === "two words & a plus +", JSON.stringify(submitted));
  await clickPopup(240, "/popup-named?step=1");
  const namedBefore = (await popupTabs()).tabs.filter(tab => tab.url.includes("/popup-named"));
  await clickPopup(300, "/popup-named?step=2");
  const namedAfter = (await popupTabs()).tabs.filter(tab => tab.url.includes("/popup-named"));
  check("named popup reuses its WindowProxy", await popupView.webContents.executeJavaScript("window.popupState.namedReused") === true);
  check("named popup reuses one owned tab", namedBefore.length === 1 && namedAfter.length === 1 && namedBefore[0].tab_id === namedAfter[0].tab_id && namedAfter[0].url.endsWith("?step=2"), JSON.stringify({ namedBefore, namedAfter }));
  await popupTool({ action: "tabs" });
  const sibling = await pool.request<{ thread: Thread }>("thread/start", { engine: "wuu", provider: "fixture", model: "fixture", permission_mode: "unconfined" });
  const foreign = await request(sibling.thread.id, "browser/list_tabs") as ListedTabs;
  check("a foreign thread cannot discover adopted popups", foreign.tab_ids.length === 0, JSON.stringify(foreign));
  if (message && messageView) {
    const rejectedClick = await request(sibling.thread.id, "browser/cdp", { tab_id: message.tab_id, method: "click", params: { x: 140, y: 120 } }).then(() => "delivered", error => String(error));
    check("a foreign thread cannot click an adopted popup", rejectedClick !== "delivered" && await messageView.webContents.executeJavaScript("window.clicks") === 0, rejectedClick);
    const rejectedClose = await request(sibling.thread.id, "browser/close_tab", { tab_id: message.tab_id }).then(() => "closed", error => String(error));
    check("a foreign thread cannot close an adopted popup", rejectedClose !== "closed" && host.tabSurfaceMeta(workdir, message.tab_id) !== undefined, rejectedClose);
  }
  const beforeSlow = new Set((await popupTabs()).tab_ids);
  await clickPopup(360);
  await bounded("slow popup starts its actual HTTP navigation", slowPopupStarted.promise);
  const slow = (await popupTabs()).tabs.find(tab => !beforeSlow.has(tab.tab_id));
  check("a loading popup is owned before its response finishes", Boolean(slow));
  if (slow) {
    const adoptionCountBeforeClose = popupAdoptions.length;
    await request(popup, "browser/close_tab", { tab_id: slow.tab_id });
    slowPopupResponse!.end("<!doctype html><title>Closed popup response</title>");
    await popupTool({ action: "tabs" });
    check("closing a loading popup cannot resurrect it", !(await popupTabs()).tab_ids.includes(slow.tab_id) && host.tabSurfaceMeta(workdir, slow.tab_id) === undefined);
    check("closed loading popup produces no stale adoption", !popupAdoptions.slice(adoptionCountBeforeClose).some(item => item.tabID === slow.tab_id));
  } else slowPopupResponse!.end("<!doctype html><title>Unowned popup response</title>");
  await request(popup, "browser/close_tab", { tab_id: popupTab });
  check("owned popup outlives its explicitly closed opener", Boolean(message && host.tabSurfaceMeta(workdir, message.tab_id) && messageView && !messageView.webContents.isDestroyed()));
  if (messageView && !messageView.webContents.isDestroyed()) {
    check("surviving popup remains usable after opener close", await messageView.webContents.executeJavaScript("document.querySelector('#target').click(); window.clicks") === 1);
  }
  await popupTool({ action: "finalize", keep: [] });
  check("owning task finalize closes all of its adopted popups", (await popupTabs()).tab_ids.length === 0);
  respond(heldPopup!);
  await bounded("popup task completes", completed.get(popup)!.promise);

  const a = await start("OWNERSHIP_A");
  await bounded("A waits with its tab", aWaiting.promise);
  const b = await start("OWNERSHIP_B");
  await bounded("B completes beside running A", completed.get(b)!.promise);
  const opened = (marker: string) => calls.find(call => call.method === "browser/open_tab" && String(call.params.initial_url).endsWith(`/${marker}`))?.params;
  const aTab = String(opened("OWNERSHIP_A")?.tab_id ?? "");
  const bTab = String(opened("OWNERSHIP_B")?.tab_id ?? "");
  assert(aTab && bTab && aTab !== bTab, "Both real threads must navigate their own tab");
  check("Go injects the creating thread into reverse RPC", opened("OWNERSHIP_A")?.thread_id === a && opened("OWNERSHIP_B")?.thread_id === b);
  const siblings = await request(a, "browser/list_tabs") as { tab_ids: string[] };
  check("A cannot list B's tab", siblings.tab_ids.includes(aTab) && !siblings.tab_ids.includes(bTab), JSON.stringify(siblings));
  respond(heldA!, { action: "tabs" });
  await bounded("A finalizes", completed.get(a)!.promise);
  check("A finalize preserves B's live page", host.tabSurfaceMeta(workdir, bTab)?.url === `${baseURL}/OWNERSHIP_B`);
  check("A finalize closes A's own tab", host.tabSurfaceMeta(workdir, aTab) === undefined);

  const c = await start("OWNERSHIP_C");
  await bounded("C opens a live input page", completed.get(c)!.promise);
  const cTab = String(opened("OWNERSHIP_C")?.tab_id ?? "");
  const cView = await readTab(cTab);
  assert(cView, "C page remains alive for input cancellation checks");
  const observation = await request(c, "browser/cdp", { tab_id: cTab, method: "observe", params: {} }) as { result: { nodes: { node_id: number; name: string }[] } };
  const target = observation.result.nodes.find(node => node.name === "Target");
  assert(target, "Actual Chromium DOM observation includes the target");
  host.reportBounds(workdir, cTab, main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 800, height: 600 }, 1, true);
  const gate = { entered: deferred(), resume: deferred() };
  geometryGate = gate;
  const pending = request(c, "browser/cdp", { tab_id: cTab, method: "click", params: { node_id: target.node_id } }).then(() => "delivered", error => String(error));
  await bounded("real geometry response barrier", gate.entered.promise);
  revocations.set(c, deferred());
  const activity = activityByThread.get(c)!;
  await pool.request("activity/takeover", { thread_id: c, activity_id: activity.id });
  await bounded("desktop receives takeover", revocations.get(c)!.promise);
  gate.resume.resolve();
  const interrupted = await pending;
  check("takeover cancels input awaiting geometry", interrupted !== "delivered", interrupted);
  check("revoked click never reaches the page", await cView.webContents.executeJavaScript("window.clicks") === 0);
  await request(c, "browser/set_visibility", { tab_id: cTab, visible: false });
  await cView.webContents.executeJavaScript("document.querySelector('#field').focus()");
  const staleType = await request(c, "browser/cdp", { tab_id: cTab, method: "type", params: { text: "stale" } }).then(() => "delivered", error => String(error));
  check("hidden input remains revoked", staleType !== "delivered" && await cView.webContents.executeJavaScript("document.querySelector('#field').value") === "", staleType);
  await pool.request("activity/release", { thread_id: c, activity_id: activity.id });
  await request(c, "browser/cdp", { tab_id: cTab, method: "type", params: { text: "resumed" } });
  check("explicit release renews input authority", await cView.webContents.executeJavaScript("document.querySelector('#field').value") === "resumed");
  const cross = await request(a, "browser/cdp", { tab_id: cTab, method: "type", params: { text: "sibling" } }).then(() => "delivered", error => String(error));
  check("a sibling cannot address a known foreign tab ID", cross !== "delivered", cross);
  const collision = await request(a, "browser/open_tab", { tab_id: cTab, initial_url: `${baseURL}/foreign` }).then(() => "delivered", error => String(error));
  check("a sibling cannot reopen another thread's tab ID", collision !== "delivered" && cView.webContents.getURL() === `${baseURL}/OWNERSHIP_C`, collision);
  // Delay a real older takeover/release pair, then generate fresh native page
  // input. Only the response to that input's own takeover can clear its latch.
  const localTab = "native-input-page";
  await request(c, "browser/open_tab", { tab_id: localTab, initial_url: `${baseURL}/local-input` });
  const localView = await readTab(localTab);
  assert(localView, "A second task-owned tab is available for native input");
  host.reportBounds(workdir, localTab, main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 800, height: 600 }, 1, true);
  main.showInactive();
  const localInput = deferred();
  let localGeneration: number | undefined;
  host.setRendererSink({
    surface() {}, adopted() {}, presented() {},
    userInput(payload) {
      if (payload.threadID === c) { localGeneration = payload.inputGeneration; localInput.resolve(); }
    },
  });
  heldControlThread = c;
  await pool.request("activity/takeover", { thread_id: c, activity_id: activity.id });
  await pool.request("activity/release", { thread_id: c, activity_id: activity.id });
  localView.webContents.sendInputEvent({ type: "mouseDown", x: 500, y: 400, button: "left", clickCount: 1 });
  localView.webContents.sendInputEvent({ type: "mouseUp", x: 500, y: 400, button: "left", clickCount: 1 });
  await bounded("native local input reaches browser host", localInput.promise);
  assert(localGeneration !== undefined, "Native input carries a takeover generation");
  heldControlThread = undefined;
  for (const held of heldControls.splice(0)) host.updateActivity(held.activity, held.method);
  const queuedGrant = await request(c, "browser/cdp", { tab_id: localTab, method: "type", params: { text: "old-grant" } }).then(() => "delivered", error => String(error));
  check("queued old takeover and grant cannot override newer native input", queuedGrant !== "delivered", queuedGrant);
  const acknowledged = await pool.request<{ activity: ActivitySession }>("activity/takeover", { thread_id: c, activity_id: activity.id });
  host.acknowledgeLocalTakeover(acknowledged.activity, localGeneration);
  await pool.request("activity/release", { thread_id: c, activity_id: activity.id });
  await localView.webContents.executeJavaScript("document.querySelector('#field').value='';document.querySelector('#field').focus()");
  await request(c, "browser/cdp", { tab_id: localTab, method: "type", params: { text: "local-resumed" } });
  check("matching local takeover acknowledgment permits a fresh grant", await localView.webContents.executeJavaScript("document.querySelector('#field').value") === "local-resumed");

  await request(c, "browser/close_tab", { tab_id: localTab });

  // Hold an actual model-generated Go browser call, interrupt its turn, and
  // wait for its matching cancellation notification before releasing geometry.
  const dGate = { entered: deferred(), resume: deferred() };
  geometryGate = dGate;
  const d = await start("OWNERSHIP_D");
  cancellations.set(d, deferred());
  await bounded("D navigates before its next model action", dWaiting.promise);
  const dTab = String(opened("OWNERSHIP_D")?.tab_id ?? "");
  const dView = await readTab(dTab);
  assert(dView, "D's live page exists");
  // This case tests cancellation of a visible page's real Go command. Mount
  // the page before observe requests its preview, so Chromium has a surface.
  host.reportBounds(workdir, dTab, main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 800, height: 600 }, 1, true);
  await dView.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  respond(heldD!, { action: "observe", tab_id: dTab });
  await bounded("D's real Go click waits on geometry", Promise.race([
    dGate.entered.promise,
    completed.get(d)!.promise.then(() => { throw new Error("D completed before click geometry; inspect its tool results and provider requests"); }),
  ]));
  const dCall = calls.find(call => call.method === "browser/cdp" && call.params.thread_id === d && call.params.method === "click");
  assert(dCall?.params.request_id, "Real Go click carries cancellation identity");
  await pool.request("turn/interrupt", { thread_id: d });
  await bounded("Go cancels its pending browser request", cancellations.get(d)!.promise);
  dGate.resume.resolve();
  await bounded("Cancelled host operation finishes", hostCompletions.get(dCall.id)!.promise);
  check("turn interruption cancels a real pending Go browser call", await dView.webContents.executeJavaScript("window.clicks") === 0);
  const cancellation = events.find(event => {
    const wire = event as ServerEvent;
    return wire.kind === "notification" && wire.message.method === "browser/request_cancelled" &&
      (wire.message.params as Record<string, unknown>).request_id === dCall.params.request_id;
  });
  check("cancellation targets the original request identity", Boolean(cancellation));

  // A main-frame navigation invalidates geometry even if it returns to the
  // same URL. This directly exercises the production host's reverse RPC path.
  const navObserve = await request(c, "browser/cdp", { tab_id: cTab, method: "observe", params: {} }) as { result: { nodes: { node_id: number; name: string }[] } };
  const navTarget = navObserve.result.nodes.find(node => node.name === "Target")!;
  const navGate = { entered: deferred(), resume: deferred() };
  geometryGate = navGate;
  const oldPageInput = request(c, "browser/cdp", { tab_id: cTab, method: "click", params: { node_id: navTarget.node_id } }).then(() => "delivered", error => String(error));
  await bounded("C geometry before same-URL navigation", navGate.entered.promise);
  await cView.webContents.loadURL(`${baseURL}/intermediate`);
  await cView.webContents.loadURL(`${baseURL}/OWNERSHIP_C`);
  navGate.resume.resolve();
  const navigationResult = await oldPageInput;
  check("same-URL return rejects old-page input", navigationResult !== "delivered" && await cView.webContents.executeJavaScript("window.clicks") === 0, navigationResult);

  const stopObserve = await request(c, "browser/cdp", { tab_id: cTab, method: "observe", params: {} }) as { result: { nodes: { node_id: number; name: string }[] } };
  const stopTarget = stopObserve.result.nodes.find(node => node.name === "Target")!;
  const stopGate = { entered: deferred(), resume: deferred() };
  geometryGate = stopGate;
  const stoppedInput = request(c, "browser/cdp", { tab_id: cTab, method: "click", params: { node_id: stopTarget.node_id } }).then(() => "delivered", error => String(error));
  await bounded("C geometry before stop", stopGate.entered.promise);
  await pool.request("activity/stop", { thread_id: c, activity_id: activity.id });
  stopGate.resume.resolve();
  const stopResult = await stoppedInput;
  check("activity stop cancels pending input", stopResult !== "delivered" && await cView.webContents.executeJavaScript("window.clicks") === 0, stopResult);
  host.reportBounds(workdir, cTab, main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 800, height: 600 }, 1, true);
  main.showInactive();
  await cView.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  writeFileSync(join(output, "page.png"), (await cView.webContents.capturePage(undefined, { stayHidden: true })).toPNG());

  const disposeObserve = await request(d, "browser/cdp", { tab_id: dTab, method: "observe", params: {} }) as { result: { nodes: { node_id: number; name: string }[] } };
  const disposeTarget = disposeObserve.result.nodes.find(node => node.name === "Target")!;
  const disposeGate = { entered: deferred(), resume: deferred() };
  geometryGate = disposeGate;
  const disposedInput = request(d, "browser/cdp", { tab_id: dTab, method: "click", params: { node_id: disposeTarget.node_id } }).then(() => "delivered", error => String(error));
  await bounded("D geometry before disposal", disposeGate.entered.promise);
  host.destroyAll();
  disposeGate.resume.resolve();
  const disposeResult = await disposedInput;
  check("disposal invalidates input before closing views", disposeResult !== "delivered" && host.tabSurfaceMeta(workdir, dTab) === undefined, disposeResult);
  writeFileSync(join(output, "results.json"), JSON.stringify({ checks, threads: { a, b, c, d }, tabs: { aTab, bTab, cTab, dTab }, calls, providerRequests, popupRequests, popupAdoptions, events }, null, 2));
  console.log(JSON.stringify(checks, null, 2));
  await pool.shutdown();
  host.destroyAll();
  main.destroy();
  server.close();
  rmSync(fixture, { recursive: true, force: true });
  assert.equal(failures.length, 0, failures.join("\n"));
  console.log(`PASS: browser task ownership and takeover; evidence ${output}`);
  app.exit(0);
}).catch(async error => {
  writeFileSync(join(output, "failure.json"), JSON.stringify({ error: String(error), checks, calls, providerRequests, popupRequests, popupAdoptions, events }, null, 2));
  console.error(error);
  await pool?.shutdown().catch(() => undefined);
  host?.destroyAll();
  server.close();
  app.exit(1);
});
