// Production Twin and GenUI desktop renderers and real Personal app-server.
// Conversation history and model are synthetic. Build the bundled plugins and
// desktop core, then build-personal-fixture.cjs before running this script.
// WUU_TEST_CORE overrides desktop/build/bin/wuu-core.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, ipcMain } = require("electron");
const { spawn, execFileSync } = require("node:child_process");
const readline = require("node:readline");

const output = path.resolve(process.env.WUU_PERSONAL_GENUI_OUTPUT || path.join(__dirname, "../out/personal-shared-genui-e2e"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const report = { scope: "Real Personal host, scoped renderer loading and state storage; synthetic desktop admission bridge, conversation history and chat transport", cases: [], calls: [] };
let win;
let core;
let initialized;
let nextId = 0;
const waiting = new Map();
function rpc(method, params = {}) {
  const id = ++nextId;
  return new Promise((resolve, reject) => { waiting.set(id, { resolve, reject }); core.stdin.write(`${JSON.stringify({id,method,params})}\n`); });
}
async function initializeHost() {
  const fixture = fs.mkdtempSync(path.join(require("node:os").tmpdir(), "wuu-personal-scoped-e2e-"));
  report.fixtureDir = fixture;
  fs.mkdirSync(path.join(fixture, "home"));
  const config = path.join(fixture, "config.json");
  fs.writeFileSync(config, JSON.stringify({composition:{version:4,profile:"desktop"},default_provider:"fixture",providers:{fixture:{type:"openai-compatible",base_url:"https://model.invalid/v1",model:"fixture"}}}));
  const binary = process.env.WUU_TEST_CORE || path.join(__dirname, "../build/bin/wuu-core");
  core = spawn(binary, ["app-server", "--desktop-owner-pipe", "--user-space", "local", "--product-domain", "personal", "--composition-profile", "personal", "--workdir", fixture, "--config", config], {
    stdio:["pipe","pipe","pipe","pipe"], env:{...process.env,WUU_HOME:path.join(fixture,"home"),WUU_NODE_EXECUTABLE:execFileSync("which",["node"],{encoding:"utf8"}).trim()}
  });
  let stderr = "";
  core.stderr.on("data", chunk => { stderr += chunk; });
  core.on("exit", code => { for (const pending of waiting.values()) pending.reject(new Error(`Personal host exited ${code}: ${stderr}`)); waiting.clear(); });
  readline.createInterface({input:core.stdout}).on("line", line => { let frame; try { frame=JSON.parse(line); } catch { return; } const pending=waiting.get(frame.id); if (!pending) return; waiting.delete(frame.id); frame.error ? pending.reject(new Error(JSON.stringify(frame.error))) : pending.resolve(frame.result); });
  core.stdio[3].end(JSON.stringify({version:1,user_space_id:"local",product_domain:"personal"}));
  initialized = await rpc("initialize");
  const personal = initialized.extension_inventory.find(item => item.provenance?.plugin_id === "personal");
  const genui = initialized.extension_inventory.find(item => item.provenance?.plugin_id === "generative-ui");
  assert(personal?.enabled && genui?.enabled && genui.desktop?.content_renderers?.length, "Personal scoped inventory lacks active GenUI renderer");
  ipcMain.handle("fixture:scope", async (_event, method, params) => {
    report.calls.push({method, operation:params?.method});
    if (method === "personal-source") return personal;
    if (method === "initialize-scope") { assert.equal(params.id, personal.id); assert.equal(params.graph_generation, personal.graph_generation); return initialized.extension_inventory; }
    if (method === "plugin/client/request") { const { composition_profile, workspace_id, workspace_root, ...wire } = params; return rpc(method, wire); }
    return rpc(method, params);
  });
  report.cases.push("Real Personal host advertises the enabled scoped GenUI renderer");
}
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function click(selector) {
  const point = await win.webContents.executeJavaScript(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return {x:Math.floor(r.x+r.width/2),y:Math.floor(r.y+r.height/2)}; })()`);
  win.webContents.sendInputEvent({ type: "mouseMove", ...point });
  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
}

app.whenReady().then(async () => {
  await initializeHost();
  win = new BrowserWindow({ width: 1180, height: 800, show: true, webPreferences: { contextIsolation: true, preload:path.join(__dirname,"personal-scoped-e2e-preload.cjs") } });
  win.webContents.on("console-message", (_event, _level, message) => console.log("renderer:", message));
  await win.loadFile(path.resolve(__dirname, "../out/personal-fixture/index.html"), { query: { genui: "1" } });
  await until(() => document.querySelector('[data-wuu-component="generated-ui"] input'));
  await until(() => document.querySelector('[data-wuu-component="generated-ui"]').getAttribute("aria-busy") === "false");
  assert.match(await evaluate(() => document.querySelector('[data-wuu-component="generated-ui"]').textContent), /旅行预算/);
  const bounds = await evaluate(() => { const r = document.querySelector('[data-wuu-component="generated-ui"]').getBoundingClientRect(); return { width:r.width, left:r.left, right:r.right, viewport:innerWidth }; });
  assert(bounds.width >= 240 && bounds.left >= 0 && bounds.right <= bounds.viewport, JSON.stringify(bounds));
  await click('[data-wuu-component="generated-ui"] input');
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Backspace" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Backspace" });
  win.webContents.sendInputEvent({ type: "char", keyCode: "4" });
  await until(() => document.querySelector('[data-wuu-component="generated-ui"] input').value === "4");
  await until(() => document.querySelector('[data-genui-node="total"]').textContent.includes("400"));
  fs.writeFileSync(path.join(output, "wide-light.png"), (await win.webContents.capturePage()).toPNG());
  report.cases.push("Shared renderer displays a Personal history resource and recalculates edited values");
  await evaluate(() => {
    const node = document.querySelector('.mosaic-main textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(node, '按照这个安排');
    node.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await click('[data-genui-node="send"] button');
  await until(() => window.__sendRecoveryFixture.requests.length === 1);
  const submission = await evaluate(() => window.__sendRecoveryFixture.requests[0]);
  assert.equal(submission.renderer_context.values.people, 4);
  assert.equal(submission.renderer_context.tool_call_id, "synthetic-call");
  assert.equal(await evaluate(() => document.querySelector('.mosaic-main textarea').value), '按照这个安排');
  report.cases.push("Trusted GenUI button sends the source-bound state and leaves the composer draft intact");
  await click('.mosaic-main .composer-send-button');
  await until(() => window.__sendRecoveryFixture.requests.length === 2);
  const request = await evaluate(() => window.__sendRecoveryFixture.requests[1]);
  assert.equal(request.renderer_contexts[0].values.people, 4);
  assert.equal(request.renderer_contexts[0].tool_call_id, "synthetic-call");
  report.cases.push("Ordinary Twin message carries the edited source-bound value");
  await evaluate(() => {
    const f=window.__streamFixture, params={thread_id:"synthetic-main",turn_id:"stream-turn",item:{id:"stream-item",source_id:"stream-call",type:"tool_call",status:"in_progress",display:{capability:"generative-ui"}}};
    f.emit("item/started",params,{user_space_id:"foreign",product_domain:"personal",composition_profile:"personal"});
  });
  assert.equal(await evaluate(() => document.querySelectorAll('[data-genui-preview="true"]').length),0);
  await evaluate(() => {
    const f=window.__streamFixture, common={thread_id:"synthetic-main",turn_id:"stream-turn"};
    f.emit("item/started",{...common,item:{id:"stream-item",source_id:"stream-call",type:"tool_call",status:"in_progress",display:{capability:"generative-ui"}}});
    const raw=JSON.stringify({spec:f.spec});
    const split=raw.indexOf(',{"id":"streamInput"');
    f.emit("item/toolCall/delta",{...common,item_id:"stream-item",delta:raw.slice(0,split+1)});
  });
  await until(() => [...document.querySelectorAll('[data-genui-preview="true"]')].some(node=>node.textContent.includes("第一段已生成")));
  const writesBeforePreview = report.calls.filter(call => call.operation === "view-state.set").length;
  await evaluate(() => { const preview=document.querySelector('[data-genui-preview="true"]'); const viewport=preview.closest('.plugin-conversation-scroll'); if (viewport) viewport.scrollTop=viewport.scrollHeight; });
  await until(() => { const r=document.querySelector('[data-genui-preview="true"]').getBoundingClientRect(); const viewport=document.querySelector('.plugin-conversation-scroll').getBoundingClientRect(); return r.top >= viewport.top && r.bottom <= viewport.bottom; });
  fs.writeFileSync(path.join(output,"stream-preview.png"),(await win.webContents.capturePage()).toPNG());
  await evaluate(() => {
    const f=window.__streamFixture, raw=JSON.stringify({spec:f.spec}), split=raw.indexOf(',{"id":"streamInput"');
    f.emit("item/toolCall/delta",{thread_id:"synthetic-main",turn_id:"stream-turn",item_id:"stream-item",delta:raw.slice(split+1)});
  });
  await until(() => [...document.querySelectorAll('[data-genui-preview="true"] input')].some(node=>node.disabled));
  assert.equal(report.calls.filter(call => call.operation === "view-state.set").length,writesBeforePreview);
  await evaluate(() => { const f=window.__streamFixture; f.finish(); f.emit("turn/completed",{thread_id:"synthetic-main",turn:{id:"stream-turn",status:"completed",items_view:"full",items:[]}}); });
  await until(() => [...document.querySelectorAll('[data-genui-preview="false"]')].some(node=>node.textContent.includes("第一段已生成")));
  assert.equal(await evaluate(() => document.querySelectorAll('[data-genui-preview="true"]').length),0);
  report.cases.push("Scoped tool arguments stream into a read-only preview and converge to one durable view");
  await win.loadFile(path.resolve(__dirname, "../out/personal-fixture/index.html"), { query: { genui: "1", theme: "dark", font: "20" } });
  win.setSize(620, 740);
  await until(() => document.querySelector('[data-wuu-component="generated-ui"] input'));
  await until(() => document.querySelector('[data-wuu-component="generated-ui"]').getAttribute("aria-busy") === "false");
  assert.equal(await evaluate(() => document.querySelector('[data-wuu-component="generated-ui"] input').value), "4");
  assert(report.calls.some(call => call.method === "plugin/desktop-module/read"));
  assert(report.calls.some(call => call.method === "plugin/client/request" && call.operation === "view-state.set"));
  assert(report.calls.filter(call => call.method === "plugin/client/request" && call.operation === "view-state.get").length >= 2);
  fs.writeFileSync(path.join(output, "narrow-dark-large.png"), (await win.webContents.capturePage()).toPNG());
  report.cases.push("Saved input restores on reload; narrow dark view with large UI text rendered for visual inspection");
  report.status = "passed";
}).catch(async error => { report.status = "failed"; report.error = error.stack; console.error(error); if (win && !win.isDestroyed()) { report.page = (await evaluate(() => document.body.innerText)).slice(0,4000); fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG()); } }).finally(() => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
  core?.kill();
  app.exit(report.status === "passed" ? 0 : 1);
});
