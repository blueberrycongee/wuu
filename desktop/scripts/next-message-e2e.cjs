// Run after electron-vite build:
// xvfb-run -a node_modules/.bin/electron --no-sandbox scripts/next-message-e2e.cjs
// Production App, DesktopPluginRuntime, PluginHost, bundled desktop plugin,
// next-message runtime source and ComposerView. Backend turns, storage and the
// model service are synthetic; no paid model runs or user files are accessed.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow, protocol, ipcMain } = require("electron");
const { attachComposerFiles } = require("./composer-attachment-e2e.cjs");

const desktop = path.resolve(__dirname, "..");
const repo = path.dirname(desktop);
const pluginPath = path.join(repo, "internal/plugin/bundled/next-message/desktop.js");
const manifestPath = path.join(repo, "internal/plugin/bundled/next-message/plugin.json");
const output = process.env.WUU_NEXT_MESSAGE_E2E_OUTPUT || path.join(desktop, "out/e2e/next-message");
const renderer = process.env.WUU_E2E_RENDERER || path.join(desktop, "out/renderer/index.html");
const inputSelector = '[data-main-conversation-composer] [data-wuu-component="composer-input"]';
const ghostSelector = '[data-wuu-component="composer-inline-completion-text"]';
const hash = file => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const now = new Date().toISOString();
const workdir = path.join(repo, "fixture-workspace-a");
const otherWorkdir = path.join(repo, "fixture-workspace-b");
const threadId = "next-message-thread";
const suggestion = "Run the focused tests and summarize any failures.";
const report = {
  startedAt: now, fixture: "Production App, desktop plugin, next-message runtime source and ComposerView; synthetic turns, in-memory storage and mocked host.model.request; no paid model or user data",
  limits: ["The UI fixture calls the production runtime source in-process. Go/JSON-RPC process transport and real provider quality are not established by this UI run.", "Linux Xvfb rendering does not establish native macOS/Windows compositor behavior."],
  command: process.argv.join(" "),
  checks: [], screenshots: [], requests: [], submissions: [], errors: [], sourceHashes: { runner: hash(__filename) },
};
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-next-message-e2e-"));
app.setPath("userData", profile);
process.env.XDG_CACHE_HOME ||= path.join(profile, "cache");
process.env.WUU_RESIZE_E2E_CWD = workdir;
process.env.WUU_RESIZE_LIVE_E2E = "1";
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "next-message-e2e-preload.cjs");
protocol.registerSchemesAsPrivileged([{ scheme: "wuu-plugin", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
let win, phase = "startup", watchdog, turnCounter = 0, failing = false;
const pending = new Map();
const runtimeInstances = new Map(), runtimeControllers = new Map(), storageByWorkspace = new Map([
  ["workspace-a", '{"provider":"synthetic","model":"mock-next-message"}'],
  ["workspace-b", '{"provider":"synthetic","model":"mock-next-message"}'],
]);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
function thread(id, cwd, extra = {}) {
  return { id, title: id === threadId ? "Next-message acceptance · synthetic model" : id, preview: "Synthetic model fixture",
    model_provider: "synthetic", model: "mock-next-message", cwd, workspace_id: cwd === workdir ? "workspace-a" : "workspace-b",
    workspace_kind: "project", status: "idle", created_at: now, updated_at: now,
    turns: [{ id: "historical-turn", kind: "user", status: "completed", items_view: "full", started_at: now, completed_at: now,
      items: [{ id: "historical-user", type: "user_message", status: "completed", text: "Prepare a small, reviewable change.",
        ...(id === threadId && cwd === workdir ? { related_session_id: "workspace-b-split", input_text: "A related workspace B conversation is available." } : {}) },
        { id: "historical-assistant", type: "agent_message", status: "completed", terminal: true, text: "Synthetic model fixture: no paid model calls. The patch is prepared; the focused tests are ready to run." }] }], ...extra };
}
const threads = [thread(threadId, workdir), thread("Other conversation", workdir), thread("Read-only conversation", workdir, { read_only: true }),
  thread("workspace-b-thread", otherWorkdir, { title: "Workspace B conversation" }), thread("workspace-b-split", otherWorkdir, { title: "Workspace B split" })];
async function evaluate(fn, arg) {
  const response = await Promise.race([
    win.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${JSON.stringify(arg)})}}catch(error){return {error:String(error.stack||error)}}})()`, true),
    delay(12000).then(() => { throw new Error(`Renderer evaluation timeout: ${phase}`); }),
  ]);
  if (response.error) throw new Error(response.error);
  return response.value;
}
async function until(fn, arg, label = phase) {
  const deadline = Date.now() + 12000;
  do { const value = await evaluate(fn, arg); if (value) return value; await delay(20); } while (Date.now() < deadline);
  throw new Error(`Timed out: ${label}`);
}
async function waitMain(fn, label) {
  const deadline = Date.now() + 12000;
  do { const value = fn(); if (value) return value; await delay(20); } while (Date.now() < deadline);
  throw new Error(`Timed out: ${label}`);
}
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function key(keyCode, modifiers = []) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
  await frames();
}
async function focus() {
  await evaluate(selector => { const input = document.querySelector(selector); input.focus(); input.setSelectionRange(input.value.length, input.value.length); input.dispatchEvent(new Event("select", { bubbles: true })); }, inputSelector);
  await frames();
}
async function draft(text) {
  await focus();
  await evaluate(({ selector, text }) => {
    const input = document.querySelector(selector);
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, text);
    input.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText", data: text }));
  }, { selector: inputSelector, text });
  await until(({ selector, text }) => document.querySelector(selector)?.value === text, { selector: inputSelector, text });
  await frames();
}
const value = () => evaluate(selector => document.querySelector(selector)?.value, inputSelector);
async function noGhost(label) { await frames(); assert.equal(await evaluate(selector => document.querySelector(selector)?.textContent || "", ghostSelector), "", label); }
function event(method, params, cwd = workdir) {
  win.webContents.send("test:server-event", { kind: "notification", workdir: cwd, message: { method, params } });
}
async function startTurn({ id = `live-turn-${++turnCounter}`, status = "in_progress", kind = "user", cwd = workdir, target = threadId } = {}) {
  const turn = { id, kind, status, items_view: "full", started_at: new Date().toISOString(),
    items: [{ id: `${id}-user`, type: "user_message", status: "completed", text: "Prepare a small, reviewable change." }] };
  event("turn/started", { thread_id: target, turn }, cwd);
  await frames();
  return { turn, cwd, target };
}
async function completeTurn(live, status = "completed", streamFinal = true) {
  const turn = { ...live.turn, status, completed_at: new Date().toISOString(),
    items: [...live.turn.items, { id: `${live.turn.id}-assistant`, type: "agent_message", status: "completed", terminal: true, text: "The patch is prepared. The focused tests are ready to run." }] };
  if (streamFinal && status === "completed") {
    const answer = turn.items.at(-1);
    event("item/started", { thread_id: live.target, turn_id: live.turn.id, item: { ...answer, status: "in_progress", text: "" } }, live.cwd);
    event("item/agentMessage/delta", { thread_id: live.target, turn_id: live.turn.id, item_id: answer.id, delta: answer.text }, live.cwd);
    event("item/completed", { thread_id: live.target, turn_id: live.turn.id, item: answer }, live.cwd);
    await frames();
    assert.equal(predictions().filter(item => item.params.input?.turnId === live.turn.id).length, 0, "a finished answer cannot infer before successful turn completion");
  }
  event("turn/completed", { thread_id: live.target, turn }, live.cwd);
  await frames();
  return turn;
}
function predictions() { return report.requests.filter(item => item.params.method === "next-message.predict"); }
async function requestFor(live) { return waitMain(() => predictions().findLast(item => item.params.input?.turnId === live.turn.id && pending.has(item.params.request_id)), `prediction for ${live.turn.id}`); }
async function resolve(request, text = suggestion) {
  const finish = pending.get(request.params.request_id);
  if (finish) { pending.delete(request.params.request_id); finish({ text: JSON.stringify({ suggestion: text }), provider: "synthetic", model: "mock-next-message", finish_reason: "stop", truncated: false }); }
  await frames();
}
async function offer(text = suggestion) {
  await draft("");
  const live = await startTurn();
  await completeTurn(live);
  const request = await requestFor(live);
  await resolve(request, text);
  await until(({ selector, text }) => document.querySelector(selector)?.textContent === text, { selector: ghostSelector, text }, "gray ghost appears after successful live turn");
  return { live, request };
}
async function hoverContextMeter() {
  const meter = await until(() => {
    const node = document.querySelector('[data-main-conversation-composer] .composer-context-meter');
    if (!node) return null;
    const rect = node.getBoundingClientRect();
    return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
  });
  win.webContents.sendInputEvent({ type: "mouseMove", ...meter });
  await until(() => Boolean(document.querySelector('.composer-context-meter-tooltip')));
  await frames();
}
async function check(name, fn) {
  phase = name; report.phase = name; save();
  console.log(`START ${name}`);
  const start = Date.now(); await fn();
  report.checks.push({ name, passed: true, durationMs: Date.now() - start }); save(); console.log(`PASS ${name}`);
}
async function screenshot(name) {
  await frames();
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const filename = `${name}.png`;
  fs.writeFileSync(path.join(output, filename), (await win.webContents.capturePage()).toPNG());
  const geometry = await evaluate(({ inputSelector, ghostSelector }) => {
    const input = document.querySelector(inputSelector), ghost = document.querySelector(ghostSelector);
    const overlay = ghost?.closest('[data-wuu-component="composer-inline-completion"]');
    const toolbar = input?.closest('[data-wuu-component="composer"]')?.querySelector('[data-wuu-component="composer-toolbar"]');
    const style = element => { if (!element) return null; const css = getComputedStyle(element); return { color: css.color, fontSize: css.fontSize, lineHeight: css.lineHeight, fontFamily: css.fontFamily }; };
    return { viewport: { width: innerWidth, height: innerHeight }, input: input?.getBoundingClientRect().toJSON(), overlay: overlay?.getBoundingClientRect().toJSON(),
      toolbar: toolbar?.getBoundingClientRect().toJSON(), inputStyle: style(input), ghostStyle: style(ghost), ghostText: ghost?.textContent, value: input?.value,
      ariaHidden: overlay?.getAttribute("aria-hidden"), focused: input === document.activeElement,
      mirror: ghost ? { clientWidth: ghost.parentElement.clientWidth, scrollWidth: ghost.parentElement.scrollWidth,
        clientHeight: ghost.parentElement.clientHeight, scrollHeight: ghost.parentElement.scrollHeight } : null };
  }, { inputSelector, ghostSelector });
  report.screenshots.push({ filename, sha256: hash(path.join(output, filename)), capturedAt: new Date().toISOString(), geometry }); save();
  return geometry;
}
async function fail(error) {
  if (failing) return; failing = true; clearTimeout(watchdog);
  report.passed = false; report.failure = { phase, message: String(error.stack || error) }; save(); console.error(error);
  if (win && !win.isDestroyed()) { try { await screenshot("failure"); } catch {} }
  app.exit(1);
}

app.whenReady().then(async () => {
  watchdog = setTimeout(() => void fail(new Error(`E2E exceeded 240 seconds: ${phase}`)), 240000);
  assert.ok(fs.existsSync(pluginPath), "bundled desktop plugin must exist before acceptance runs");
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  const pluginSource = fs.readFileSync(pluginPath);
  report.sourceHashes.plugin = crypto.createHash("sha256").update(pluginSource).digest("hex"); report.sourceHashes.preload = hash(path.join(__dirname, "next-message-e2e-preload.cjs"));
  report.sourceHashes.renderer = hash(renderer);
  const runtimeSource = path.join(repo, "plugins/next-message/src/index.ts");
  const runtimeBundle = path.join(desktop, "out-e2e/next-message-runtime.cjs");
  require("esbuild").buildSync({ entryPoints: [runtimeSource], outfile: runtimeBundle, bundle: true, platform: "node", format: "cjs", target: "node24",
    alias: { "@wuu/plugin-sdk": path.join(repo, "packages/plugin-sdk/src/index.ts") } });
  const { createNextMessagePlugin } = require(runtimeBundle);
  report.sourceHashes.runtimeSource = hash(runtimeSource); report.sourceHashes.runtimeBundle = hash(runtimeBundle);
  report.rendererAssets = [...fs.readFileSync(renderer, "utf8").matchAll(/(?:src|href)="([^\"]+\.(?:js|css))"/g)]
    .map((match) => ({ path: match[1], sha256: hash(path.resolve(path.dirname(renderer), match[1])) }));
  report.environment = { electron: process.versions.electron, chromium: process.versions.chrome, node: process.versions.node, platform: process.platform, arch: process.arch, display: process.env.DISPLAY || "headless" };
  report.phase = "create production BrowserWindow"; save();
  ipcMain.on("test:next-message-fixture", event => { event.returnValue = { context: { kind: "project", project_id: "workspace-a", cwd: workdir },
    projects: [{ id: "workspace-a", name: "Synthetic fixture A", path: workdir, created_at: now, updated_at: now }, { id: "workspace-b", name: "Synthetic fixture B", path: otherWorkdir, created_at: now, updated_at: now }],
    threads, plugin: { contributions: { settings_pages: manifest.contributes?.settingsPages?.map(entry => ({ ...entry,
      ...(typeof entry.icon === "string" ? { icon: { name: entry.icon } } : {}) })) } }, pluginDigest: report.sourceHashes.plugin }; });
  ipcMain.on("test:next-message-submitted", (_event, payload) => { report.submissions.push(payload); });
  ipcMain.handle("test:next-message-runtime", async (_event, params) => {
    const request = { params, receivedAt: new Date().toISOString(), cancelled: false }; report.requests.push(request); save();
    assert.ok(params.workspace_id, "runtime calls must target an explicit registered workspace");
    const controller = new AbortController(); runtimeControllers.set(params.request_id, controller);
    const host = { async call(method, input) {
      if (method === "host.storage.get") return { value: storageByWorkspace.get(params.workspace_id) ?? null };
      if (method === "host.storage.set") { storageByWorkspace.set(params.workspace_id, input.value); return {}; }
      assert.equal(method, "host.service.call"); assert.equal(input.service, "host.model.request"); assert.equal(input.method, "call");
      request.modelRequest = input.params; save();
      return new Promise(resolve => pending.set(params.request_id, resolve));
    } };
    try {
      const key = JSON.stringify([params.fingerprint, params.workspace_id]);
      if (!runtimeInstances.has(key)) {
        const instance = createNextMessagePlugin();
        runtimeInstances.set(key, (async () => { await instance.initialize({}, host); return instance; })());
      }
      const instance = await runtimeInstances.get(key);
      const result = await instance.invokeCapability({ capability: "plugin.client.request", input: { method: params.method, input: params.input } }, host, { signal: controller.signal });
      request.runtimeCompleted = true; return result.output;
    } catch (error) {
      if (!controller.signal.aborted) throw error;
      request.runtimeCancelled = true; return { result: null };
    } finally { runtimeControllers.delete(params.request_id); save(); }
  });
  ipcMain.handle("test:next-message-cancel", (_event, identity) => {
    const request = report.requests.find(item => item.params.request_id === identity.request_id && item.params.id === identity.id && item.params.fingerprint === identity.fingerprint);
    if (request) { request.cancelled = true; runtimeControllers.get(identity.request_id)?.abort(); }
    // Keep the mock promise unsettled so the fixture can deliberately deliver a
    // late answer and prove that production cancellation suppresses it.
    return { cancelled: Boolean(request) };
  });
  protocol.handle("wuu-plugin", request => {
    const url = new URL(request.url);
    return url.hostname === "next-message" && url.pathname.endsWith("/desktop.js")
      ? new Response(pluginSource, { headers: { "Content-Type": "text/javascript" } }) : new Response("Not found", { status: 404 });
  });
  win = new BrowserWindow({ width: 1200, height: 850, show: false, webPreferences: { preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  win.webContents.on("console-message", ({ level, message }) => { if (level === "error") { report.errors.push({ phase, message }); console.error(message); } });
  await win.loadFile(renderer); win.showInactive();
  win.webContents.debugger.attach("1.3");
  win.webContents.debugger.on("message", (_event, method, params) => { if (method === "Runtime.exceptionThrown") { report.exceptions ??= []; report.exceptions.push(params.exceptionDetails); save(); } });
  await win.webContents.debugger.sendCommand("Runtime.enable");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await until(selector => Boolean(document.querySelector(selector)), inputSelector);
  await focus();

  await check("historical completed snapshot never triggers inference", async () => {
    await delay(400); assert.equal(predictions().length, 0); await noGhost("history is not a new successful turn");
    await screenshot("historical-no-suggestion");
  });
  await check("successful live turn shows gray host ghost; Tab inserts and never sends", async () => {
    const { request } = await offer();
    assert.equal(request.modelRequest.max_output_tokens, 128); assert.equal(request.modelRequest.allow_unbounded_output, true);
    assert.equal(request.modelRequest.timeout_ms, 30000);
    assert.equal(request.modelRequest.messages.length, 2); assert.equal(request.modelRequest.tools, undefined);
    const sent = JSON.parse(request.modelRequest.messages[1].content);
    assert.equal(sent.turns.at(-1).user, request.params.input.turns.at(-1).userText);
    assert.equal(sent.turns.at(-1).assistant, request.params.input.turns.at(-1).assistantText);
    assert.equal(sent.turns.length, 2, "recent visible history should precede the live turn");
    assert.ok(request.modelRequest.messages.reduce((bytes, message) => bytes + Buffer.byteLength(message.content), 0) <= 8192);
    const geometry = await screenshot("wide-light-default-ghost");
    assert.equal(await value(), ""); assert.equal(geometry.ariaHidden, "true");
    assert.notEqual(geometry.inputStyle.color, geometry.ghostStyle.color);
    const before = report.submissions.length;
    const focusState = () => evaluate(selector => {
      const input = document.querySelector(selector);
      return { focused: document.activeElement === input,
        modality: document.documentElement.getAttribute("data-focus-modality"),
        ringOutline: getComputedStyle(input.closest(".composer-frame"), "::after").outline };
    }, inputSelector);
    await evaluate(selector => document.querySelector(selector).dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })), inputSelector);
    const beforeFocus = await focusState();
    assert.equal(beforeFocus.focused, true);
    assert.equal(beforeFocus.modality, "pointer");
    await key("Tab"); assert.equal(await value(), suggestion); assert.equal(report.submissions.length, before);
    assert.deepEqual(await focusState(), beforeFocus, "accepting a completion preserves focus and its visible appearance");
    await noGhost("accepted suggestion is ordinary input"); await screenshot("tab-inserted-not-sent");
    await key("Tab");
    assert.equal((await focusState()).focused, false, "Tab without a suggestion still moves focus");
    assert.equal((await focusState()).modality, "keyboard");
    await focus();
  });
  await check("clearing or undoing acceptance restores the suggestion without another prediction", async () => {
    const count = predictions().length;
    const before = report.submissions.length;
    for (const action of ["delete", "undo"]) {
      if (action === "delete") {
        await evaluate(selector => document.querySelector(selector).select(), inputSelector);
        await key("Backspace");
      } else {
        await evaluate(() => document.execCommand("undo"));
      }
      await until(selector => document.querySelector(selector)?.value === "", inputSelector);
      await until(({ selector, suggestion }) => document.querySelector(selector)?.textContent === suggestion,
        { selector: ghostSelector, suggestion }, `suggestion returns after ${action}`);
      await screenshot(`suggestion-restored-after-${action}`);
      await key("Tab");
      assert.equal(await value(), suggestion);
      await noGhost("reaccepted suggestion is ordinary input");
      assert.equal(predictions().length, count);
      assert.equal(report.submissions.length, before);
    }
    await draft("");
    await until(selector => Boolean(document.querySelector(selector)), ghostSelector);
    await key("Escape");
    await draft("temporary draft"); await draft("");
    await noGhost("explicit dismissal still survives clearing the draft");
    assert.equal(predictions().length, count);
  });
  await check("direct running-to-completed transition also produces one suggestion", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live, "completed", false);
    const request = await requestFor(live); await resolve(request);
    await until(({ selector, suggestion }) => document.querySelector(selector)?.textContent === suggestion, { selector: ghostSelector, suggestion });
    assert.equal(predictions().filter(item => item.params.input.turnId === live.turn.id).length, 1); await key("Escape");
  });
  await check("a live turn survives a matching thread resume snapshot", async () => {
    await draft(""); const live = await startTurn();
    event("thread/resumed", { thread: { ...threads[0], status: "running", turns: [...threads[0].turns, live.turn] } });
    await frames(); await completeTurn(live);
    const request = await requestFor(live); await resolve(request);
    await until(({ selector, suggestion }) => document.querySelector(selector)?.textContent === suggestion, { selector: ghostSelector, suggestion });
    await key("Escape");
  });
  await check("an unfocused empty composer still predicts and accepts after focus returns", async () => {
    await draft("");
    await evaluate(() => document.querySelector('[data-main-conversation-composer] .composer-plus-button').focus());
    const live = await startTurn(); await completeTurn(live);
    const request = await requestFor(live);
    assert.equal(await evaluate(selector => document.activeElement === document.querySelector(selector), inputSelector), false);
    await resolve(request);
    await until(({ selector, suggestion }) => document.querySelector(selector)?.textContent === suggestion,
      { selector: ghostSelector, suggestion });
    await focus(); await key("Tab");
    assert.equal(await value(), suggestion);
  });
  await check("Escape dismisses and focus changes do not retry the turn", async () => {
    await offer(); const count = predictions().length;
    await key("Escape"); await noGhost("dismissed");
    await key("Tab"); await focus(); await delay(400);
    assert.equal(predictions().length, count); await noGhost("focus cannot regenerate dismissal");
  });
  await check("slash menu keeps native Tab priority over next-message predictions", async () => {
    await offer(); await draft("/si");
    await until(() => Boolean(document.querySelector('.slash-command-item[data-command-name="side"]')));
    await noGhost("slash completion owns this draft"); const count = predictions().length;
    const selected = await until(() => document.querySelector('.slash-command-item[aria-selected="true"]')?.getAttribute("data-command-name"));
    await screenshot("slash-menu-tab-priority"); await key("Tab");
    assert.equal((await value()).trimEnd(), `/${selected}`); assert.equal(predictions().length, count);
    assert.equal(report.submissions.length, 0); await key("Escape"); await draft("");
  });
  await check("native IME composition suppresses the ghost and Tab acceptance", async () => {
    await offer(); const count = predictions().length;
    await evaluate(selector => { window.__nextMessageIME = []; for (const type of ["compositionstart", "compositionupdate", "compositionend"]) document.querySelector(selector).addEventListener(type, event => window.__nextMessageIME.push({ type, data: event.data })); }, inputSelector);
    await win.webContents.debugger.sendCommand("Input.imeSetComposition", { text: "拼", selectionStart: 1, selectionEnd: 1 });
    await until(() => window.__nextMessageIME.some(event => event.type === "compositionstart")); await noGhost("IME immediately hides the ghost");
    await win.webContents.debugger.sendCommand("Input.imeSetComposition", { text: "拼音", selectionStart: 2, selectionEnd: 2 }); await frames();
    await key("Tab"); assert.equal((await value()).includes(suggestion), false);
    await win.webContents.debugger.sendCommand("Input.insertText", { text: "拼音" });
    await until(() => window.__nextMessageIME.some(event => event.type === "compositionend"));
    assert.equal(predictions().length, count); report.ime = await evaluate(() => window.__nextMessageIME);
    await focus(); await draft("");
  });
  await check("hovering retained-history usage preserves the ghost and Tab acceptance", async () => {
    await offer();
    const count = predictions().length;
    const submissions = report.submissions.length;
    await hoverContextMeter();
    assert.equal(await evaluate(selector => document.querySelector(selector)?.textContent, ghostSelector), suggestion,
      "a read-only usage tooltip must not hide the suggestion");
    assert.equal(await evaluate(selector => document.activeElement === document.querySelector(selector), inputSelector), true);
    await screenshot("context-tooltip-keeps-suggestion");
    await key("Tab");
    assert.equal(await value(), suggestion, "Tab accepts while the usage tooltip is open");
    assert.equal(report.submissions.length, submissions);
    assert.equal(predictions().length, count, "hover must not start another model request");
    win.webContents.sendInputEvent({ type: "mouseMove", x: win.getContentSize()[0] - 20, y: 100 });
    await until(() => !document.querySelector('.composer-context-meter-tooltip'));
  });
  await check("toolbar menu temporarily hides cached ghost without another model attempt", async () => {
    await offer(); const count = predictions().length;
    await evaluate(() => document.querySelector('[data-main-conversation-composer] .composer-plus-button').click());
    await until(() => Boolean(document.querySelector('[data-floating-menu-owner="composer-plus"]')));
    await noGhost("toolbar menu owns keyboard interaction"); await key("Tab"); assert.equal(await value(), "");
    await key("Escape"); await focus(); await delay(200); assert.equal(predictions().length, count);
    await until(({ selector, text }) => document.querySelector(selector)?.textContent === text,
      { selector: ghostSelector, text: suggestion });
    await key("Escape");
  });
  await check("typing hides an in-flight suggestion and clearing the draft reveals it", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
    await win.webContents.insertText("My own next step");
    await frames(); assert.equal(request.cancelled, false, "draft changes must not cancel background inference");
    await resolve(request, "Continue after clearing the draft."); await noGhost("draft owns the composer");
    await draft(""); await until(selector => document.querySelector(selector)?.textContent === "Continue after clearing the draft.", ghostSelector);
    assert.equal(predictions().filter(item => item.params.input.turnId === live.turn.id).length, 1); await key("Escape");
  });
  await check("new turn cancels an older pending prediction", async () => {
    await draft(""); const older = await startTurn(); await completeTurn(older); const request = await requestFor(older);
    const newer = await startTurn(); await waitMain(() => request.cancelled, "new turn cancellation");
    await resolve(request, "STALE PREVIOUS TURN"); await noGhost("new turn suppresses previous result");
    await completeTurn(newer, "failed");
  });
  await check("failed interrupted background and unmatched completion never infer", async () => {
    await draft(""); const before = predictions().length;
    for (const status of ["failed", "interrupted"]) { const live = await startTurn(); await completeTurn(live, status); }
    const background = await startTurn({ kind: "background" }); await completeTurn(background);
    await completeTurn({ turn: { id: "unobserved-historical-turn", kind: "user", items: [] }, cwd: workdir, target: threadId });
    await delay(400); assert.equal(predictions().length, before); await noGhost("only observed successful user turns qualify");
  });
  await check("runtime abstention is terminal across focus changes", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
    await resolve(request, null); const count = predictions().length; await noGhost("null is quiet abstention");
    await key("Tab"); await focus(); await delay(400); assert.equal(predictions().length, count);
  });
  await check("duplicate completion cannot produce a second request", async () => {
    await draft(""); const live = await startTurn(); const completed = await completeTurn(live); const request = await requestFor(live);
    event("turn/completed", { thread_id: threadId, turn: completed }); await frames();
    await resolve(request); await until(selector => Boolean(document.querySelector(selector)), ghostSelector);
    await delay(400); assert.equal(predictions().filter(item => item.params.input.turnId === live.turn.id).length, 1);
    await key("Escape");
  });
  await check("attachments and queued messages hide but do not cancel predictions", async () => {
    await draft("");
    await attachComposerFiles(win, "[data-main-conversation-composer]", [{ name: "synthetic-fixture.txt", type: "text/plain", contents: "Only synthetic attachment content." }]);
    await until(() => Boolean(document.querySelector(".composer-attachment-tray-item")));
    await focus(); let live = await startTurn(); await completeTurn(live);
    let request = await requestFor(live); await resolve(request, "Continue after removing the attachment.");
    await noGhost("attachment-bearing draft is not empty");
    await evaluate(() => document.querySelector("[data-main-conversation-composer] .composer-attachment-card-remove").click());
    await until(() => !document.querySelector(".composer-attachment-tray-item"));
    await until(selector => document.querySelector(selector)?.textContent === "Continue after removing the attachment.", ghostSelector);
    await focus(); await key("Escape");
    event("turn/held", { thread_id: threadId, messages: [{ id: "held-next-message", thread_id: threadId, origin: "queue", prompt: "An existing queued instruction", state: "held" }] });
    await until(() => document.body.textContent.includes("An existing queued instruction"));
    await focus(); live = await startTurn(); await completeTurn(live);
    request = await requestFor(live); await resolve(request, "Continue after the queue clears.");
    await noGhost("queued instructions suppress suggestions");
    event("turn/held", { thread_id: threadId, messages: [] });
    await until(selector => document.querySelector(selector)?.textContent === "Continue after the queue clears.", ghostSelector);
    await focus(); await key("Escape"); await noGhost("queue result dismissed");
  });
  await check("main-turn notifications do not arm the side composer", async () => {
    await draft("/side");
    await until(() => Boolean(document.querySelector('.slash-command-item[data-command-name="side"]')));
    await evaluate(() => document.querySelector('.slash-command-item[data-command-name="side"]').click());
    await until(() => Boolean(document.querySelector('.side-thread-panel [data-wuu-component="composer-input"]')));
    await evaluate(() => document.querySelector('.side-thread-panel [data-wuu-component="composer-input"]').focus());
    const live = await startTurn({ target: "next-message-side" }); await completeTurn(live);
    const request = await requestFor(live); await resolve(request);
    await noGhost("unrelated main conversation result stays out of the side composer");
    await screenshot("side-conversation-no-suggestion");
    await evaluate(() => document.querySelector('[data-wuu-tab-kind="side-thread"] .workspace-tool-tab-close').click());
    await until(() => !document.querySelector(".side-thread-panel")); await focus();
  });
  await check("thread navigation keeps prediction running and shows it on return", async () => {
    await evaluate(() => { const header = document.querySelector('[data-section-id="workspace-a"] .sidebar-section-header-group button'); if (header?.getAttribute("aria-expanded") === "false") header.click(); });
    await until(() => [...document.querySelectorAll(".thread-row-main")].some(node => node.textContent.includes("Other conversation")));
    await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(node => node.textContent.includes("Other conversation")).click());
    await frames(); assert.equal(request.cancelled, false, "thread navigation must not cancel prediction");
    await resolve(request, "Return to this conversation."); await noGhost("reply cannot cross threads");
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(node => node.textContent.includes("Next-message acceptance")).click());
    await focus(); await until(selector => document.querySelector(selector)?.textContent === "Return to this conversation.", ghostSelector);
    assert.equal(predictions().filter(item => item.params.input.turnId === live.turn.id).length, 1);
    await key("Escape");
  });
  await check("reply completed while another conversation is open still predicts", async () => {
    await draft(""); const live = await startTurn();
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(node => node.textContent.includes("Other conversation")).click());
    await completeTurn(live); const request = await requestFor(live);
    await resolve(request, "Follow up after returning."); await noGhost("background result stays with its conversation");
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(node => node.textContent.includes("Next-message acceptance")).click());
    await focus(); await until(selector => document.querySelector(selector)?.textContent === "Follow up after returning.", ghostSelector);
    await key("Escape");
  });
  await check("read-only composer never displays a background prediction", async () => {
    const snapshot = await evaluate(async id => (await window.wuu.resumeThread(id)).thread, threadId);
    event("thread/resumed", { thread_id: threadId, thread: { ...snapshot, read_only: true } });
    await until(selector => document.querySelector(selector)?.disabled, inputSelector);
    const live = await startTurn(); await completeTurn(live);
    const request = await requestFor(live); await resolve(request);
    await noGhost("read-only input never offers a suggestion");
    await screenshot("read-only-no-suggestion");
    event("thread/resumed", { thread_id: threadId, thread: { ...snapshot, read_only: false } });
    await until(selector => document.querySelector(selector)?.disabled === false, inputSelector); await focus();
  });
  await check("workspace-scoped events isolate identical thread IDs", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
    const unrelated = await startTurn({ cwd: otherWorkdir }); await completeTurn(unrelated);
    const otherRequest = await requestFor(unrelated);
    assert.equal(otherRequest.params.workspace_id, "workspace-b");
    assert.equal(request.cancelled, false, "foreign workspace cannot cancel A inference");
    await resolve(otherRequest, "Workspace B only."); await noGhost("B suggestion stays out of A");
    await resolve(request, "Workspace A only."); await frames();
    const shown = await evaluate(selector => document.querySelector(selector)?.textContent || "", ghostSelector);
    assert.notEqual(shown, "Workspace B only.");
    if (shown) { await focus(); await key("Escape"); }
    const snapshot = await evaluate(async id => (await window.wuu.resumeThread(id)).thread, threadId);
    event("thread/resumed", { thread_id: threadId, thread: snapshot }); await frames(); await focus();
  });
  await check("workspace navigation preserves each workspace's prediction", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live); const old = await requestFor(live);
    async function selectWorkspace(id) {
      await evaluate(id => { const group = document.querySelector(`[data-section-id="${id}"]`); const header = group.querySelector('.sidebar-section-header-group button'); if (header.getAttribute("aria-expanded") === "false") header.click(); }, id);
      await until(id => Boolean(document.querySelector(`[data-section-id="${id}"] .thread-row-main`)), id);
      await evaluate(id => [...document.querySelectorAll(`[data-section-id="${id}"] .thread-row-main`)].find(node => node.textContent.includes(id === "workspace-b" ? "Workspace B conversation" : "Next-message acceptance")).click(), id);
      await until(async id => (await window.wuu.listProjects()).active_project_id === id, id);
      await until(id => document.querySelector('.conversation-title-heading h1')?.textContent.includes(id === "workspace-b" ? "Workspace B conversation" : "Next-message acceptance"), id);
      await until(selector => document.querySelector(selector)?.disabled === false, inputSelector);
      await focus();
    }
    await selectWorkspace("workspace-b"); assert.equal(old.cancelled, false, "workspace switch must not cancel A prediction");
    await resolve(old, "Return to workspace A."); await noGhost("A result stays out of workspace B");
    const next = await startTurn({ cwd: otherWorkdir, target: "workspace-b-thread" }); await completeTurn(next); const current = await requestFor(next);
    assert.equal(current.params.workspace_id, "workspace-b"); await resolve(current, "Check only workspace B.");
    await until(selector => document.querySelector(selector)?.textContent === "Check only workspace B.", ghostSelector);
    await screenshot("workspace-b-isolated-suggestion");
    await selectWorkspace("workspace-a");
    await until(selector => document.querySelector(selector)?.textContent === "Return to workspace A.", ghostSelector);
    await key("Escape");
  });
  await check("cross-workspace split keeps its prediction across focus transfer", async () => {
    await draft("");
    const linkTurn = await startTurn();
    linkTurn.turn.items[0].related_session_id = "workspace-b-split";
    linkTurn.turn.items[0].input_text = "Open the synthetic workspace B split conversation.";
    linkTurn.turn.items[0].origin = "host";
    linkTurn.turn.items[0].presentation_kind = "session_message";
    linkTurn.turn.items[0].name = "Workspace B split";
    await completeTurn(linkTurn);
    await until(() => Boolean(document.querySelector('.session-message-source')));
    await evaluate(() => [...document.querySelectorAll('.session-message-source')].at(-1).click());
    const splitSelector = '.conversation-split-pane[data-thread-id="workspace-b-split"] [data-wuu-component="composer-input"]';
    await until(selector => Boolean(document.querySelector(selector)), splitSelector);
    await evaluate(selector => document.querySelector(selector).focus(), splitSelector); await frames();
    const live = await startTurn({ cwd: otherWorkdir, target: "workspace-b-split" }); await completeTurn(live);
    const request = await requestFor(live); assert.equal(request.params.workspace_id, "workspace-b"); assert.equal(request.params.input.threadId, "workspace-b-split");
    await resolve(request, "Check the workspace B split changes.");
    await until(selector => document.querySelector(selector)?.closest('.conversation-split-pane').querySelector('[data-wuu-component="composer-inline-completion-text"]')?.textContent === "Check the workspace B split changes.", splitSelector);
    await screenshot("cross-workspace-split-ghost"); await key("Tab");
    assert.equal(await evaluate(selector => document.querySelector(selector).value, splitSelector), "Check the workspace B split changes.");
    assert.equal(report.submissions.length, 0);
    await evaluate(selector => {
      const input = document.querySelector(selector);
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "");
      input.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "deleteContentBackward" }));
    }, splitSelector);
    const pendingTurn = await startTurn({ cwd: otherWorkdir, target: "workspace-b-split" }); await completeTurn(pendingTurn); const old = await requestFor(pendingTurn);
    await evaluate(() => document.querySelector('.conversation-split-pane:first-child [data-wuu-component="composer-input"]').focus()); await frames();
    assert.equal(old.cancelled, false, "focus transfer keeps the split prediction running");
    await resolve(old, "Check the split after focus moves.");
    await until(selector => document.querySelector(selector)?.closest('.conversation-split-pane').querySelector('[data-wuu-component="composer-inline-completion-text"]')?.textContent === "Check the split after focus moves.", splitSelector);
    await evaluate(() => document.querySelector('.conversation-split-close').click()); await until(() => !document.querySelector('.conversation-split-pane')); await focus();
  });
  await check("history restoration and server errors invalidate pending predictions", async () => {
    for (const kind of ["thread/resumed", "server-error"]) {
      await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
      if (kind === "thread/resumed") event(kind, { thread_id: threadId });
      else win.webContents.send("test:server-event", { kind, workdir, message: "Synthetic connection failure" });
      await waitMain(() => request.cancelled, `${kind} cancels prediction`);
      await resolve(request, `STALE ${kind}`); await noGhost(`${kind} result suppressed`);
      await key("Tab"); await focus(); await delay(200);
      assert.equal(predictions().filter(item => item.params.input.turnId === live.turn.id).length, 1);
    }
    await evaluate(() => document.querySelector('.archive-tip-dismiss')?.click());
    await until(() => !document.querySelector('.archive-tip-dismiss'));
  });
  await check("theme typography and width matrix uses host rendering", async () => {
    for (const width of [1200, 720, 480]) for (const theme of ["light", "dark"]) for (const font of [15, 20]) {
      win.setContentSize(width, 850);
      await until(width => innerWidth === width, width);
      await evaluate(({ theme, font }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--wuu-font-size-ui", `${font}px`);
        document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
        const toggle = document.querySelector('[data-wuu-component="sidebar-toggle"]');
        if (!document.querySelector('[data-wuu-sidebar-mode="collapsed"]')) toggle?.click();
      }, { theme, font });
      await offer("Run the focused tests, then explain any remaining failures without changing unrelated files.");
      const geometry = await screenshot(`ghost-${width}-${theme}-${font}`);
      assert.ok(geometry.input.left >= 0 && geometry.input.right <= width + 1);
      assert.ok(geometry.overlay.left >= geometry.input.left - 2 && geometry.overlay.right <= geometry.input.right + 2);
      assert.equal(geometry.inputStyle.fontSize, geometry.ghostStyle.fontSize);
      assert.ok(geometry.mirror.scrollWidth <= geometry.mirror.clientWidth + 1);
      assert.ok(geometry.overlay.bottom <= geometry.toolbar.top + 1, "the ghost viewport must stop before the toolbar");
      // The existing phone-width layout hides usage controls entirely.
      if (width > 480) {
        await hoverContextMeter();
        assert.equal(await evaluate(selector => document.querySelector(selector)?.textContent, ghostSelector), geometry.ghostText);
        await screenshot(`context-tooltip-${width}-${theme}-${font}`);
      }
      await key("Tab");
      assert.equal(await value(), geometry.ghostText);
      win.webContents.sendInputEvent({ type: "mouseMove", x: win.getContentSize()[0] - 20, y: 100 });
      await until(() => !document.querySelector('.composer-context-meter-tooltip'));
    }
  });
  await check("plugin settings save and reload the selected model in the workspace", async () => {
    win.setContentSize(1200, 850); await until(() => innerWidth === 1200);
    await evaluate(() => {
      document.documentElement.dataset.theme = "light";
      document.documentElement.style.setProperty("--conversation-message-font-size", "15px");
      document.documentElement.style.setProperty("--wuu-font-size-ui", "15px");
      if (document.querySelector('[data-wuu-sidebar-mode="collapsed"]')) document.querySelector('[data-wuu-component="sidebar-toggle"]').click();
    });
    await until(() => Boolean(document.querySelector(".sidebar-account-trigger")));
    await evaluate(() => document.querySelector(".sidebar-account-trigger").click());
    await until(() => Boolean(document.querySelector('[data-settings-page="providers"]')));
    await evaluate(() => document.querySelector('[data-settings-page="providers"]').click());
    const settingsButton = () => [...document.querySelectorAll(".settings-nav-item")].find(node => node.textContent.trim() === "Next message");
    await until(settingsButton); await evaluate(() => [...document.querySelectorAll(".settings-nav-item")].find(node => node.textContent.trim() === "Next message").click());
    await until(() => document.querySelector(".plugin-ui-input")?.disabled === false);
    assert.equal(await evaluate(() => document.querySelectorAll(".plugin-ui-input")[1]?.value), "low", "legacy model selection should display its effective low tier");
    await evaluate(() => { document.querySelector(".plugin-ui-input").value = JSON.stringify(["synthetic", "mock-next-message"]); document.querySelector(".plugin-ui-input").dispatchEvent(new Event("change", { bubbles: true })); });
    await evaluate(() => [...document.querySelectorAll("button")].find(node => node.textContent.trim() === "Save changes").click());
    const request = await waitMain(() => report.requests.findLast(item => item.params.method === "next-message.update"), "model selection save");
    assert.equal(request.params.workspace_id, "workspace-a"); assert.deepEqual(request.params.input, { provider: "synthetic", model: "mock-next-message", variant: "low" });
    await until(() => [...document.querySelectorAll('[role="status"]')].some(node => node.textContent === "Saved"));
    await screenshot("settings-light-default");
    await evaluate(() => [...document.querySelectorAll(".settings-nav-item")].find(node => node.textContent.trim() === "General").click());
    await until(() => !document.querySelector(".plugin-ui-input"));
    await evaluate(() => [...document.querySelectorAll(".settings-nav-item")].find(node => node.textContent.trim() === "Next message").click());
    await until(() => document.querySelector(".plugin-ui-input")?.value === JSON.stringify(["synthetic", "mock-next-message"]));
    win.setContentSize(640, 850); await until(() => innerWidth === 640);
    await evaluate(() => { document.documentElement.dataset.theme = "dark"; document.documentElement.style.setProperty("--conversation-message-font-size", "20px"); document.documentElement.style.setProperty("--wuu-font-size-ui", "20px"); });
    await evaluate(() => document.querySelector('.plugin-ui-input').scrollIntoView({ block: "center" }));
    await screenshot("settings-dark-large-narrow");
    await evaluate(() => document.querySelector(".settings-back-button").click()); await until(selector => Boolean(document.querySelector(selector)), inputSelector);
  });
  await check("real UI recording captures completion acceptance typing and dismissal", async () => {
    win.setContentSize(1000, 760); await until(() => innerWidth === 1000);
    await evaluate(() => { document.documentElement.dataset.theme = "light"; document.documentElement.style.setProperty("--wuu-font-size-ui", "15px"); document.documentElement.style.setProperty("--conversation-message-font-size", "15px"); });
    await draft("");
    const recording = { method: "Electron webContents.capturePage at 10 fps; actual unmodified UI captures", startedAt: new Date().toISOString(), frames: [] };
    const framesDir = path.join(output, "recording-frames"); fs.rmSync(framesDir, { recursive: true, force: true }); fs.mkdirSync(framesDir, { recursive: true });
    let stop = false;
    const capture = (async () => { while (!stop) {
      const started = Date.now(); const filename = `${String(recording.frames.length).padStart(5, "0")}.png`;
      fs.writeFileSync(path.join(framesDir, filename), (await win.webContents.capturePage()).toPNG());
      recording.frames.push({ filename, sha256: hash(path.join(framesDir, filename)), capturedAt: new Date().toISOString(), elapsedMs: Date.now() - Date.parse(recording.startedAt) });
      await delay(Math.max(0, 100 - (Date.now() - started)));
    } })();
    await delay(600); const live = await startTurn(); await delay(650); await completeTurn(live); const request = await requestFor(live);
    await delay(450); await resolve(request); await until(selector => Boolean(document.querySelector(selector)), ghostSelector);
    await delay(1000); await key("Tab"); await delay(800);
    await win.webContents.insertText(" Start with the desktop suite."); await delay(1000);
    await draft(""); await offer("Review the changed files before continuing."); await delay(850); await key("Escape"); await delay(700);
    stop = true; await capture; recording.endedAt = new Date().toISOString(); recording.frameCount = recording.frames.length;
    fs.writeFileSync(path.join(output, "recording.json"), JSON.stringify(recording, null, 2)); report.recording = recording;
    assert.equal(report.submissions.length, 0, "recording never sends any drafted text");
  });
  await check("disabling the plugin cancels pending work and restores native Tab", async () => {
    await draft(""); const live = await startTurn(); await completeTurn(live); const request = await requestFor(live);
    win.webContents.send("test:next-message-inventory", { enabled: false });
    await waitMain(() => request.cancelled, "plugin disposal cancels request");
    await resolve(request, "STALE DISABLED PLUGIN"); await noGhost("disabled plugin cannot render late result");
    const count = predictions().length; const next = await startTurn(); await completeTurn(next);
    await delay(300); assert.equal(predictions().length, count);
    await key("Tab"); assert.equal(await value(), "");
    assert.equal(await evaluate(selector => document.activeElement === document.querySelector(selector), inputSelector), false);
  });
  assert.deepEqual(report.errors, [], "no unexpected renderer errors");
  report.passed = true; report.finishedAt = new Date().toISOString(); save(); clearTimeout(watchdog);
  win.destroy(); app.quit();
}).catch(fail);
