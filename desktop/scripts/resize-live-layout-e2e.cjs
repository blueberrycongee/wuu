// Real production-renderer layout with synthetic thread notifications. No Go core,
// model inference, or native embedded browser is launched by this fixture.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { fileURLToPath } = require("node:url");
const { app, BrowserWindow, ipcMain, screen } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const renderer = process.env.WUU_E2E_RENDERER || path.join(desktopRoot, "out/renderer/index.html");
const output = process.env.WUU_RESIZE_OUTPUT || path.join(desktopRoot, "out/e2e/resize-live-layout.json");
const publicTokens = [...new Set([...fs.readFileSync(path.join(desktopRoot, "src/shared/themeContract.generated.ts"), "utf8")
  .matchAll(/"(--(?:wuu|hljs)-[a-z0-9-]+)"/g)].map(match => match[1]))];
assert.ok(publicTokens.length > 0 && publicTokens.includes("--wuu-color-link") && publicTokens.some(name => name.startsWith("--hljs-")), "generated public theme and syntax token lists are loaded");
const tokenNames = publicTokens.concat(["--font-ui", "--font-body", "--font-code", "--space-1", "--space-2", "--space-4", "--conversation-message-font-size", "--conversation-reading-line-height"]);
const result = { renderer, scriptSha256: crypto.createHash("sha256").update(fs.readFileSync(__filename)).digest("hex"), publicTokenCount: publicTokens.length, cases: [], motions: [], checks: [], errors: [] };
const userData = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-resize-live-"));
app.setPath("userData", userData);
process.env.WUU_RESIZE_E2E_CWD = repoRoot;
process.env.WUU_RESIZE_LIVE_E2E = "1";
let win;
let phase = "initial history";
let browserBounds = [];
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const save = () => { fs.mkdirSync(path.dirname(output), { recursive: true }); fs.writeFileSync(output, JSON.stringify(result, null, 2)); };
async function evaluate(fn, arg) {
  const value = await win.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${JSON.stringify(arg)})}}catch(error){return {error:String(error.stack||error)}}})()`);
  if (value.error) throw new Error(value.error);
  return value.value;
}
async function until(fn, label = phase) {
  for (let i = 0; i < 240; i++) {
    const value = await evaluate(fn);
    if (value) return value;
    await delay(25);
  }
  throw new Error(`Timed out: ${label}`);
}
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function settle() {
  await frames();
  await until(() => !document.documentElement.matches(".window-resizing,.layout-motion-active") && !document.getAnimations().some(animation =>
    animation.playState === "running" && animation.effect?.target instanceof Element &&
    animation.effect.target.matches(".app-shell,.scroll-region,.dock-composer-wrap,.composer-stack,.environment-side-stack,.environment-panel,.workspace-right-panel,.sidebar")), "layout settles");
  await frames();
}
async function click(selector) {
  await evaluate(selector => {
    const node = document.querySelector(selector);
    if (!node) throw new Error(`Missing ${selector}`);
    node.click();
  }, selector);
}
async function snapshot(name) {
  phase = name;
  await settle();
  const snapshot = await evaluate(names => {
    const boxes = {};
    for (const selector of [".sidebar", ".conversation-pane", ".scroll-region", ".conversation-width", ".composer-stack", ".workspace-right-panel", ".environment-side-stack", ".side-thread-panel", ".conversation-split-pane"]) {
      boxes[selector] = [...document.querySelectorAll(selector)].map(node => ({ box: node.getBoundingClientRect().toJSON(), font: getComputedStyle(node).font }));
    }
    const root = getComputedStyle(document.documentElement);
    const flows = [...document.querySelectorAll(".conversation-width")].map(node => {
      const style = getComputedStyle(node);
      const tokens = Object.fromEntries(names.map(name => [name, style.getPropertyValue(name)]));
      return { tokens, inheritedLinkColor: style.getPropertyValue("--wuu-color-link") === root.getPropertyValue("--wuu-color-link") };
    });
    return { boxes, flows };
  }, tokenNames);
  assert.ok(snapshot.flows.length > 0, `${name}: message flow exists`);
  assert.ok(snapshot.flows.every(flow => flow.inheritedLinkColor), `${name}: public theme token still inherits`);
  result.cases.push({ name, ...snapshot });
  save();
}
async function drag(selector, delta) {
  await evaluate(async ({ selector, delta }) => {
    const handle = document.querySelector(selector);
    if (!handle) throw new Error(`Missing ${selector}`);
    const box = handle.getBoundingClientRect();
    const x = box.left + box.width / 2;
    handle.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, clientX: x, pointerId: 13 }));
    await new Promise(requestAnimationFrame);
    for (let i = 0; i <= 40; i++) {
      const fraction = i <= 20 ? i / 20 : (40 - i) / 20;
      window.dispatchEvent(new PointerEvent("pointermove", { bubbles: true, button: 0, clientX: x + delta * fraction, pointerId: 13 }));
      await new Promise(resolve => setTimeout(resolve, 16));
    }
    await new Promise(requestAnimationFrame);
    window.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, button: 0, clientX: x, pointerId: 13 }));
  }, { selector, delta });
}
async function motion(name, action, requireDragging = true) {
  phase = name;
  await settle();
  browserBounds = [];
  await evaluate(() => {
    const viewport = document.querySelector(".conversation-pane>.scroll-region");
    const flow = viewport.querySelector('.cached-conversation-pane[data-active="true"] .conversation-width');
    const rich = [...flow.querySelectorAll(".rich-content")].at(-1);
    const ranges = [...rich.querySelectorAll(".rich-paragraph,td,th")].map(node => {
      const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
      const group = [];
      let text;
      while ((text = walker.nextNode())) {
        if (!text.textContent.trim()) continue;
        const range = document.createRange();
        range.selectNodeContents(text);
        group.push(range);
      }
      return group;
    });
    const geometry = () => ({ top: viewport.scrollTop, width: flow.getBoundingClientRect().width, follow: viewport.style.overflowAnchor });
    const data = { initial: geometry(), samples: [] };
    const sample = now => {
      const lines = ranges.reduce((sum, group) => sum + new Set(group.flatMap(range => [...range.getClientRects()].map(rect => Math.round(rect.top)))).size, 0);
      data.samples.push({ time: now, width: rich.getBoundingClientRect().width, lines, dragging: document.documentElement.classList.contains("window-resizing"), top: viewport.scrollTop, follow: viewport.style.overflowAnchor });
      data.raf = requestAnimationFrame(sample);
    };
    data.raf = requestAnimationFrame(sample);
    window.__resizeLive = { data, geometry };
  });
  await action();
  await settle();
  const data = await evaluate(() => {
    const { data, geometry } = window.__resizeLive;
    cancelAnimationFrame(data.raf);
    data.final = geometry();
    delete window.__resizeLive;
    return data;
  });
  const active = requireDragging ? data.samples.filter(sample => sample.dragging) : data.samples;
  result.motions.push({ name, ...data, browserBounds });
  save();
  assert.ok(new Set(active.map(sample => sample.width)).size >= (requireDragging ? 8 : 3), `${name}: content changes width during motion`);
  assert.ok(new Set(active.map(sample => sample.lines)).size >= 2, `${name}: text wraps during motion`);
  assert.ok(Math.abs(data.initial.width - data.final.width) < 1, `${name}: final width returns`);
  assert.ok(Math.abs(data.initial.top - data.final.top) <= 2, `${name}: final scroll position returns`);
  assert.equal(data.final.follow, data.initial.follow, `${name}: follow ownership remains stable`);
}
const emit = (method, params) => win.webContents.send("test:server-event", { workdir: repoRoot, kind: "notification", message: { jsonrpc: "2.0", method, params } });
async function geometry() {
  return evaluate(() => {
    const node = document.querySelector(".conversation-pane>.scroll-region");
    return { top: node.scrollTop, height: node.scrollHeight, client: node.clientHeight, distance: node.scrollHeight - node.clientHeight - node.scrollTop, owner: node.style.overflowAnchor };
  });
}
async function readingPoint() {
  return evaluate(() => {
    const viewport = document.querySelector(".conversation-pane>.scroll-region");
    const box = viewport.getBoundingClientRect();
    const flow = viewport.querySelector('.cached-conversation-pane[data-active="true"] .conversation-width').getBoundingClientRect();
    const x = Math.max(box.left + 10, Math.min(box.right - 10, flow.left + flow.width / 2));
    for (const offset of [40, 80, 120, 160, 200]) {
      const node = document.elementFromPoint(x, box.top + offset)?.closest(".rich-paragraph,td,pre,[data-user-message-id]");
      if (node?.closest(".turn")) return { text: node.textContent, turn: node.closest(".turn").dataset.turnId, offset: node.getBoundingClientRect().top - box.top };
    }
    throw new Error("No visible reading paragraph");
  });
}
async function pointOffset(point) {
  return evaluate(point => {
    const viewport = document.querySelector(".conversation-pane>.scroll-region");
    const turn = viewport.querySelector(`.turn[data-turn-id="${point.turn}"]`);
    const block = [...turn.querySelectorAll(".rich-paragraph,td,pre,[data-user-message-id]")].find(node => node.textContent === point.text);
    if (!block) throw new Error("Reading paragraph removed");
    return block.getBoundingClientRect().top - viewport.getBoundingClientRect().top;
  }, point);
}
function check(name, data) { result.checks.push({ name, ...data }); save(); console.log(`PASS ${name}`); }
async function streamAndSubmission() {
  const identity = { thread_id: "resize-thread", turn_id: "resize-live-turn", item_id: "resize-live-answer" };
  const user = { id: "resize-live-user", type: "user_message", status: "completed", text: "Resize while output keeps streaming." };
  let text = "A stable first paragraph wraps during a browser panel drag. ".repeat(10) + "\n\n";
  const live = { id: identity.turn_id, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [user, { id: identity.item_id, type: "agent_message", status: "in_progress", text }] };
  emit("turn/started", { thread_id: identity.thread_id, turn: live });
  await until(() => document.querySelector('[data-turn-id="resize-live-turn"]'), "live turn");
  await settle();
  async function append(from, count) {
    for (let index = from; index < from + count; index++) {
      const delta = `Live paragraph ${index}: preserve real-time wrapping and the reader's chosen scroll ownership while panel width changes.\n\n`;
      text += delta;
      emit("item/agentMessage/delta", { ...identity, delta });
      await delay(16);
    }
  }
  phase = "stream while resizing";
  const before = await geometry();
  await Promise.all([append(0, 64), drag(".workspace-right-panel-resizer", -140)]);
  await until(() => document.querySelector('[data-turn-id="resize-live-turn"]')?.textContent.includes("Live paragraph 63"), "final delta");
  await settle();
  const after = await geometry();
  assert.ok(after.height > before.height);
  assert.equal(after.owner, "none");
  assert.ok(after.distance <= 2, "follower catches streamed end");
  check(phase, { before, after });

  phase = "paused stream and panel resize";
  await evaluate(() => {
    const node = document.querySelector(".conversation-pane>.scroll-region");
    node.dispatchEvent(new WheelEvent("wheel", { deltaY: -600, bubbles: true }));
    node.scrollTop -= 600;
    node.dispatchEvent(new Event("scroll"));
  });
  await until(() => document.querySelector(".conversation-pane>.scroll-region").style.overflowAnchor === "auto", "paused reader");
  await frames();
  const point = await readingPoint();
  await Promise.all([append(64, 32), drag(".workspace-right-panel-resizer", -140)]);
  await until(() => document.querySelector('[data-turn-id="resize-live-turn"]')?.textContent.includes("Live paragraph 95"), "paused delta");
  await settle();
  const offset = await pointOffset(point);
  assert.equal((await geometry()).owner, "auto");
  assert.ok(Math.abs(offset - point.offset) <= 2, "paused reading endpoint remains anchored");
  check(phase, { point, offset });

  phase = "paused window resize";
  const windowPoint = await readingPoint();
  for (let index = 0; index <= 24; index++) {
    const fraction = index <= 12 ? index / 12 : (24 - index) / 12;
    win.setContentSize(Math.round(1380 - fraction * 220), 860);
    await delay(16);
  }
  await settle();
  const windowOffset = await pointOffset(windowPoint);
  assert.equal((await geometry()).owner, "auto");
  assert.ok(Math.abs(windowOffset - windowPoint.offset) <= 2);
  check(phase, { point: windowPoint, offset: windowOffset });
  emit("turn/completed", { thread_id: identity.thread_id, turn: { ...live, status: "completed", completed_at: new Date().toISOString(), items: [user, { id: identity.item_id, type: "agent_message", status: "completed", text }] } });
  await settle();

  phase = "submitted query placement during resize";
  await evaluate(() => {
    const input = document.querySelector("[data-main-conversation-composer] textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Keep this submitted question visible while resizing the panel.");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await frames();
  await click("[data-main-conversation-composer] .composer-send-button");
  await until(() => document.querySelector('[data-user-message-id="resize-submitted-user-1"]'), "accepted query");
  result.submissionBeforeResize = await evaluate(() => {
    const viewport = document.querySelector(".conversation-pane>.scroll-region");
    const message = document.querySelector('[data-user-message-id="resize-submitted-user-1"]');
    return {
      turnOrder: [...viewport.querySelectorAll(".turn[data-turn-id]")].slice(-4).map(node => node.dataset.turnId),
      submittedMessageCount: viewport.querySelectorAll('[data-user-message-id="resize-submitted-user-1"]').length,
      offset: message.getBoundingClientRect().top - viewport.getBoundingClientRect().top,
      scrollTop: viewport.scrollTop, scrollHeight: viewport.scrollHeight,
      owner: viewport.style.overflowAnchor,
      placing: !!viewport.querySelector(".scroll-region-content[data-submit-placing]")
    };
  });
  save();
  assert.equal(result.submissionBeforeResize.submittedMessageCount, 1, "one accepted submission is rendered");
  assert.equal(result.submissionBeforeResize.turnOrder.at(-1), "resize-submitted-turn-1", "accepted submission is the latest turn");
  await drag(".workspace-right-panel-resizer", -140);
  await until(() => !document.querySelector(".scroll-region-content[data-submit-placing]"), "placement completes");
  await settle();
  async function submittedGeometry() {
    return evaluate(() => {
      const viewport = document.querySelector(".conversation-pane>.scroll-region");
      const message = document.querySelector('[data-user-message-id="resize-submitted-user-1"]');
      const box = viewport.getBoundingClientRect(), rect = message.getBoundingClientRect();
      return { offset: rect.top - box.top, bottom: rect.bottom - box.top, height: box.height, owner: viewport.style.overflowAnchor };
    });
  }
  const held = await submittedGeometry();
  result.placementAttempt = held;
  save();
  assert.ok(held.offset >= -1 && held.bottom <= held.height + 1, "submitted query remains visible");
  check(phase, { held });
  phase = "held query during short reply and resize";
  emit("item/started", { thread_id: identity.thread_id, turn_id: "resize-submitted-turn-1", item: { id: "resize-submitted-answer", type: "agent_message", status: "in_progress", text: "A short answer preserves the submitted question's reading position." } });
  await until(() => document.querySelector('[data-turn-id="resize-submitted-turn-1"]')?.textContent.includes("A short answer"), "short reply");
  await drag(".workspace-right-panel-resizer", -100);
  await settle();
  const heldAfter = await submittedGeometry();
  assert.ok(Math.abs(heldAfter.offset - held.offset) <= 2, "held query keeps its endpoint");
  check(phase, { before: held, after: heldAfter });
  emit("turn/completed", { thread_id: identity.thread_id, turn: {
    id: "resize-submitted-turn-1", status: "completed", items_view: "full", started_at: live.started_at, completed_at: new Date().toISOString(),
    items: [
      { id: "resize-submitted-user-1", type: "user_message", status: "completed", text: "Keep this submitted question visible while resizing the panel." },
      { id: "resize-submitted-answer", type: "agent_message", status: "completed", text: "A short answer preserves the submitted question's reading position." }
    ]
  } });
  await settle();
}

app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1380, height: 860, frame: false, show: true, webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false, preload: path.join(__dirname, "resize-e2e-preload.cjs") } });
  win.webContents.on("console-message", ({ level, message }) => { if (level >= 2) result.errors.push({ phase, message }); });
  win.webContents.on("render-process-gone", (_event, details) => fail(new Error(`Renderer exited: ${details.reason}`)));
  ipcMain.on("test:browser-bounds", (_event, payload) => browserBounds.push(payload.rect));
  ipcMain.on("test:pip-host", () => {});
  await win.loadFile(renderer);
  await until(() => document.querySelector(".turn"));
  await settle();
  const resources = await evaluate(() => performance.getEntriesByType("resource").map(entry => entry.name).filter(name => name.startsWith("file:") && /\.(?:js|css)$/.test(name)));
  result.artifacts = [renderer, path.join(__dirname, "resize-e2e-preload.cjs"), ...new Set(resources.map(resource => fileURLToPath(resource)))].map(file => ({
    path: file, sha256: crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex")
  }));
  result.runtime = { display: screen.getPrimaryDisplay(), gpu: app.getGPUFeatureStatus(), zoom: win.webContents.getZoomFactor(), viewport: await evaluate(() => ({ width: innerWidth, height: innerHeight, ratio: devicePixelRatio, visibility: document.visibilityState })) };
  assert.ok(result.runtime.viewport.width >= 800 && result.runtime.viewport.height >= 600, "valid desktop viewport");
  for (const [width, font, theme] of [[1380, 14, "light"], [1100, 20, "dark"], [820, 20, "light"], [1380, 14, "dark"]]) {
    win.setContentSize(width, 860);
    await evaluate(({ font, theme }) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--wuu-color-link", "#8656c2");
      window.dispatchEvent(new Event("wuu-content-size-change"));
    }, { font, theme });
    await snapshot(`window-${width}-${font}-${theme}`);
  }
  await evaluate(() => { if (document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) document.querySelector(".sidebar-toggle-button").click(); });
  await settle();
  await motion("window live reflow", async () => {
    for (let index = 0; index <= 40; index++) {
      const fraction = index <= 20 ? index / 20 : (40 - index) / 20;
      win.setContentSize(Math.round(1380 - fraction * 260), 860);
      await delay(16);
    }
  }, false);
  await motion("left live reflow", () => drag(".sidebar-resizer", 190));
  await motion("left collapse and expand", async () => { await click(".sidebar-toggle-button"); await settle(); await click(".sidebar-toggle-button"); }, false);
  await click(".title-actions .side-panel-toggle-button");
  await until(() => document.querySelector(".workspace-right-panel-resizer"), "right panel");
  await settle();
  await motion("right live reflow", () => drag(".workspace-right-panel-resizer", -140));
  await click('[data-wuu-tool="browser"]');
  await until(() => document.querySelector(".workspace-browser-host"), "browser host");
  await motion("browser live reflow", () => drag(".workspace-right-panel-resizer", -140));
  await snapshot("browser panel");
  await motion("right collapse and expand", async () => { await click(".title-actions .side-panel-toggle-button"); await settle(); await click(".title-actions .side-panel-toggle-button"); }, false);
  await click(".title-actions .side-panel-toggle-button");
  await settle();
  await evaluate(() => {
    const input = document.querySelector("[data-main-conversation-composer] textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "/side");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await until(() => document.querySelector('.slash-command-item[data-command-name="side"]'), "side command");
  await click('.slash-command-item[data-command-name="side"]');
  await until(() => document.querySelector(".side-thread-panel"), "side thread");
  await snapshot("side thread");
  await click(".side-thread-panel__close");
  await settle();
  await evaluate(() => {
    const message = document.querySelector('[data-user-message-id="resize-user-35"]');
    const button = [...message.querySelectorAll("button")].find(node => node.querySelector("svg.lucide-info") || ["打开关联会话", "Open related session"].includes(node.getAttribute("aria-label")));
    if (!button) throw new Error("Related-session control missing");
    button.click();
  });
  await until(() => document.querySelectorAll(".conversation-split-pane").length === 2, "split panes");
  await snapshot("split flow");
  assert.deepEqual(result.errors, [], "no renderer errors");
  if (process.env.WUU_RESIZE_COMPARE) {
    const baseline = JSON.parse(fs.readFileSync(process.env.WUU_RESIZE_COMPARE, "utf8"));
    assert.equal(result.publicTokenCount, baseline.publicTokenCount);
    assert.deepEqual(result.cases, baseline.cases, "source-built variants preserve public tokens and final host geometry");
    result.comparedWith = process.env.WUU_RESIZE_COMPARE;
  }
  save();
  await click(".conversation-split-close");
  await until(() => !document.querySelector(".conversation-split-pane"), "close split");
  await click(".title-actions .side-panel-toggle-button");
  await until(() => document.querySelector(".workspace-right-panel-resizer"), "restore browser panel");
  await click('[data-wuu-tool="browser"]');
  await until(() => document.querySelector(".workspace-browser-host"), "restore browser host");
  await settle();
  await streamAndSubmission();
  assert.deepEqual(result.errors, [], "no renderer errors after notifications");
  result.passed = true;
  save();
  console.log(`PASS ${result.cases.length} geometry/token cases, ${result.motions.length} live motions, ${result.checks.length} scroll cases; ${publicTokens.length} public tokens`);
  win.destroy();
  app.quit();
}).catch(fail);
function fail(error) {
  result.failure = { phase, error: String(error), stack: error.stack };
  save();
  console.error(error);
  if (win && !win.isDestroyed()) win.destroy();
  app.exit(1);
}
setTimeout(() => fail(new Error(`Timed out during ${phase}`)), 90000).unref();
