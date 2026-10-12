// Run after electron-vite build:
// electron scripts/composer-inline-e2e.cjs
// Real Chromium input, production renderer/PluginHost, synthetic backend and
// third-party provider. No model, credentials, or user workspace is used.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow, protocol, ipcMain, clipboard } = require("electron");

const root = path.resolve(__dirname, "..");
const renderer = process.env.WUU_E2E_RENDERER || path.join(root, "out/renderer/index.html");
const output = process.env.WUU_INLINE_E2E_OUTPUT || path.join(root, "out/e2e/composer-inline");
const inputSelector = '[data-main-conversation-composer] [data-wuu-component="composer-input"]';
const ghostSelector = '[data-wuu-component="composer-inline-completion-text"]';
const state = { renderer, fixture: "production renderer with synthetic backend/provider", checks: [], screenshots: [], contexts: [], errors: [] };
const digest = (file) => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
state.runnerSha256 = digest(__filename);
state.providerSha256 = digest(path.join(__dirname, "composer-inline-e2e-plugin.mjs"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(os.tmpdir(), "wuu-inline-e2e-")));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(root);
process.env.WUU_RESIZE_LIVE_E2E = "1";
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "composer-inline-e2e-preload.cjs");
protocol.registerSchemesAsPrivileged([{ scheme: "wuu-plugin", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
let win;
let phase = "startup";
let watchdog;
let failing = false;
const runtimeRequests = [];
const submissions = [];
const runtimePending = new Map();
async function runtimeRequest(text) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const found = runtimeRequests.findLast((request) => request.params.input.draftText === text);
    if (found) return found;
    await delay(20);
  }
  throw new Error(`Runtime request not received for ${text}`);
}
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(state, null, 2));
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function bounded(promise, label) {
  let timeout;
  return Promise.race([
    promise,
    new Promise((_, reject) => { timeout = setTimeout(() => reject(new Error(`${label} timed out during ${phase}`)), 12000); }),
  ]).finally(() => clearTimeout(timeout));
}
async function evaluate(fn, argument) {
  const response = await bounded(win.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${JSON.stringify(argument)})}}catch(error){return {error:String(error.stack||error)}}})()`, true), "Renderer evaluation");
  if (response.error) throw new Error(response.error);
  return response.value;
}
async function until(fn, argument, label = phase) {
  const deadline = Date.now() + 12000;
  while (Date.now() < deadline) {
    const value = await evaluate(fn, argument);
    if (value) return value;
    await delay(20);
  }
  throw new Error(`Timed out: ${label}`);
}
const frames = () => evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function key(keyCode, modifiers = [], repeat = false) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers: repeat ? [...modifiers, "isautorepeat"] : modifiers });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
  await frames();
}
async function focus() {
  await evaluate((selector) => {
    const input = document.querySelector(selector);
    if (!input) throw new Error("Composer is missing");
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
    input.dispatchEvent(new Event("select", { bubbles: true }));
  }, inputSelector);
  await frames();
}
async function draft(text) {
  await focus();
  await key("A", [process.platform === "darwin" ? "meta" : "control"]);
  if (text) await win.webContents.insertText(text);
  else await key("Backspace");
  await until(({ selector, text }) => document.querySelector(selector)?.value === text, { selector: inputSelector, text }, `draft ${JSON.stringify(text)}`);
  await frames();
}
async function request(text) {
  return until((text) => window.__inlineCompletionE2E?.active.requests.findLast((request) => request.context.draftText === text && !request.aborted), text, `provider request ${JSON.stringify(text)}`);
}
async function resolve(id, suffix) {
  await evaluate(({ id, suffix }) => window.__inlineCompletionE2E.active.resolve(id, suffix), { id, suffix });
  await frames();
}
async function suggestion(text, suffix) {
  await draft(text);
  const pending = await request(text);
  await resolve(pending.id, suffix);
  await until(({ selector, suffix }) => document.querySelector(selector)?.textContent === suffix, { selector: ghostSelector, suffix }, "host renders offered suffix");
  return pending;
}
async function value() { return evaluate((selector) => document.querySelector(selector)?.value, inputSelector); }
async function noGhost(label) {
  await frames();
  assert.equal(await evaluate((selector) => document.querySelector(selector)?.textContent || "", ghostSelector), "", label);
}
async function check(name, body) {
  phase = name;
  state.phase = name;
  const started = Date.now();
  console.log(`${new Date().toISOString()} START ${name}`);
  save();
  await body();
  state.checks.push({ name, passed: true, durationMs: Date.now() - started });
  save();
  console.log(`PASS ${name}`);
}
async function screenshot(name) {
  await frames();
  const file = `${name}.png`;
  const layout = await evaluate((selector) => {
    const suffix = document.querySelector(selector);
    if (!suffix) return null;
    const mirror = suffix.parentElement;
    const viewport = suffix.closest('[data-wuu-component="composer-inline-completion"]');
    const range = document.createRange();
    range.selectNodeContents(suffix);
    const style = getComputedStyle(mirror);
    return { mirror: { clientWidth: mirror.clientWidth, scrollWidth: mirror.scrollWidth, scrollHeight: mirror.scrollHeight,
      whiteSpace: style.whiteSpace, overflowWrap: style.overflowWrap, suffixWordBreak: getComputedStyle(suffix).wordBreak },
      viewport: { width: viewport.clientWidth, height: viewport.clientHeight },
      suffixLineRects: Array.from(range.getClientRects(), (rect) => rect.toJSON()) };
  }, ghostSelector);
  (state.screenshotLayouts ??= {})[file] = layout;
  fs.writeFileSync(path.join(output, file), (await bounded(win.webContents.capturePage(), "Screenshot capture")).toPNG());
  state.screenshots.push(file);
  save();
  return layout;
}
async function fail(error) {
  if (failing) return;
  failing = true;
  clearTimeout(watchdog);
  state.passed = false;
  state.failure = { phase, message: String(error.stack || error) };
  save();
  console.error(error);
  if (win && !win.isDestroyed()) {
    try { await screenshot("failure"); } catch { /* Keep the original failure. */ }
  }
  app.exit(1);
}

state.phase = "waiting for Electron startup";
save();
app.whenReady().then(async () => {
  // Leave time to save the last frame and diagnostics before CI's step timeout.
  watchdog = setTimeout(() => { void fail(new Error(`Composer E2E exceeded 150 seconds during ${phase}`)); }, 150000);
  ipcMain.on("test:inline-submitted", (_event, payload) => submissions.push(payload));
  ipcMain.handle("test:inline-runtime", (_event, params) => {
    const entry = { params, cancelled: false };
    runtimeRequests.push(entry);
    return new Promise((resolve) => runtimePending.set(params.request_id, resolve));
  });
  ipcMain.handle("test:inline-runtime-cancel", (_event, identity) => {
    const entry = runtimeRequests.find((request) => request.params.request_id === identity.request_id);
    const owns = entry && entry.params.id === identity.id && entry.params.fingerprint === identity.fingerprint;
    const resolve = owns && runtimePending.get(identity.request_id);
    if (resolve) {
      entry.cancelled = true;
      runtimePending.delete(identity.request_id);
      resolve({ result: null });
    }
    return { cancelled: Boolean(resolve) };
  });
  protocol.handle("wuu-plugin", (request) => {
    const url = new URL(request.url);
    if (url.hostname !== "inline-completion" || !url.pathname.endsWith("/desktop.js")) return new Response("Not found", { status: 404 });
    return new Response(fs.readFileSync(path.join(__dirname, "composer-inline-e2e-plugin.mjs")), { headers: { "Content-Type": "text/javascript" } });
  });
  win = new BrowserWindow({ width: 1200, height: 850, show: process.env.WUU_E2E_VISIBLE === "true", webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error" && !message.includes("Expected fixture provider failure")) state.errors.push({ phase, message });
  });
  win.webContents.on("render-process-gone", (_event, details) => { state.errors.push({ phase, message: `renderer exited: ${details.reason}` }); save(); });
  await win.loadFile(renderer);
  // Hidden Chromium windows throttle compositor frames even with background
  // throttling disabled. Match the workspace fixture's real Xvfb render path.
  win.showInactive();
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await until((selector) => Boolean(document.querySelector(selector)), inputSelector);
  await check("no provider preserves native Tab and draft", async () => {
    await draft("Baseline native input");
    await noGhost("no plugin means no decoration");
    await key("Tab");
    assert.equal(await value(), "Baseline native input");
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), false);
    assert.equal(await evaluate(() => Boolean(window.__inlineCompletionE2E)), false);
  });
  await check("slash-like follow-up preserves folded content without providers", async () => {
    await draft("");
    const pasted = Array.from({ length: 20 }, (_, index) => `Pasted evidence line ${index + 1}.`).join("\n");
    clipboard.writeText(pasted);
    win.webContents.paste();
    await until(() => Boolean(document.querySelector('[data-main-conversation-composer] .composer-collapsed-prompt-card')));
    await draft("/rev");
    assert.equal(await evaluate(() => Boolean(document.querySelector('[data-floating-menu-owner="composer-slash"]'))), false);
    await key("Tab");
    assert.equal(await value(), "/rev", "a suffix inside a folded draft is not a slash command");
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), false);
    await evaluate(() => document.querySelector('[data-main-conversation-composer] .composer-collapsed-prompt-card .composer-document-card-main').click());
    await until(({ selector, expected }) => document.querySelector(selector)?.value === expected, { selector: inputSelector, expected: `/rev${pasted}` }, "folded content remains intact after Tab");
    await draft("");
  });
  win.webContents.send("test:inline-inventory", { enabled: true, generation: 1 });
  await until(() => Boolean(window.__inlineCompletionE2E?.active));

  await check("host rendering and native Tab acceptance", async () => {
    const pending = await suggestion("Plan", " a focused check");
    assert.equal(pending.context.kind, "main");
    assert.equal(await value(), "Plan", "a visible suggestion never changes the draft before acceptance");
    state.contexts.push(pending.context);
    await screenshot("wide-light-suggestion");
    await key("Tab");
    assert.equal(await value(), "Plan a focused check");
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), true);
    await key("Z", [process.platform === "darwin" ? "meta" : "control"]);
    assert.equal(await value(), "Plan", "accepted suffix can be undone through the native edit history");
  });

  await check("accept then immediate submit uses live text and refreshes after turn", async () => {
    await suggestion("Send live", " accepted text");
    const previousSubmissions = submissions.length;
    for (const keyCode of ["Tab", "Enter"]) {
      win.webContents.sendInputEvent({ type: "keyDown", keyCode });
      win.webContents.sendInputEvent({ type: "keyUp", keyCode });
    }
    const deadline = Date.now() + 10000;
    while (submissions.length === previousSubmissions && Date.now() < deadline) await delay(20);
    assert.equal(submissions.length, previousSubmissions + 1);
    const submitted = submissions.at(-1);
    assert.equal(submitted.text, "Send live accepted text", "submission cannot use the pre-acceptance React draft");
    assert.ok(submitted.response.turn?.id, "synthetic backend created the turn");
    win.webContents.send("test:server-event", {
      kind: "notification", workdir: path.dirname(root),
      message: { method: "turn/completed", params: { thread_id: submitted.threadId, turn: { ...submitted.response.turn, status: "completed", completed_at: new Date().toISOString() } } },
    });
    await until(() => window.__inlineCompletionE2E.active.requests.some((request) => request.context.draftText === "" && request.context.running === false && !request.aborted));
    state.submissions = submissions.map(({ threadId, text, response }) => ({ threadId, text, turnId: response.turn.id }));
  });

  await check("typing invalidates old async results", async () => {
    await draft("Older");
    const old = await request("Older");
    await win.webContents.insertText(" newer");
    const current = await request("Older newer");
    assert.ok(current.context.revision > old.context.revision);
    assert.equal(await evaluate((id) => window.__inlineCompletionE2E.active.requests.find((r) => r.id === id).aborted, old.id), true);
    await resolve(old.id, " STALE RESPONSE");
    await noGhost("late reply must not revive an obsolete draft");
    await resolve(current.id, " valid reply");
    await until((selector) => document.querySelector(selector)?.textContent === " valid reply", ghostSelector);
  });

  await check("caret and selection invalidate completion", async () => {
    await key("Left");
    await noGhost("middle caret must not display append-only completion");
    const count = await evaluate(() => window.__inlineCompletionE2E.active.requests.length);
    await key("Home");
    await key("End", ["shift"]);
    await frames();
    assert.equal(await evaluate(() => window.__inlineCompletionE2E.active.requests.length), count);
    assert.equal(await value(), "Older newer");
  });

  await check("native IME composition suppresses suggestion and Tab acceptance", async () => {
    await suggestion("Input ", " should not be accepted");
    await evaluate((selector) => {
      window.__inlineIMEEvents = [];
      for (const type of ["compositionstart", "compositionupdate", "compositionend"]) document.querySelector(selector).addEventListener(type, (event) => window.__inlineIMEEvents.push({ type, data: event.data }));
    }, inputSelector);
    await win.webContents.debugger.sendCommand("Input.imeSetComposition", { text: "拼", selectionStart: 1, selectionEnd: 1 });
    await until(() => window.__inlineIMEEvents.some((event) => event.type === "compositionstart"));
    await noGhost("composition hides pending text immediately");
    const count = await evaluate(() => window.__inlineCompletionE2E.active.requests.length);
    await win.webContents.debugger.sendCommand("Input.imeSetComposition", { text: "拼音", selectionStart: 2, selectionEnd: 2 });
    await frames();
    assert.equal(await evaluate(() => window.__inlineCompletionE2E.active.requests.length), count, "composition updates do not invoke providers");
    await key("Tab");
    assert.ok(!(await value()).includes("should not be accepted"));
    await win.webContents.debugger.sendCommand("Input.insertText", { text: "拼音" });
    await until(() => window.__inlineIMEEvents.some((event) => event.type === "compositionend"));
    state.ime = await evaluate(() => window.__inlineIMEEvents);
    assert.ok(state.ime.length >= 2);
    state.imeRequestCountDuringComposition = count;
    await focus();
    await request(await value());
  });

  await check("Escape dismisses one revision and preserves native Tab", async () => {
    await suggestion("Dismiss", " this suffix");
    const count = await evaluate(() => window.__inlineCompletionE2E.active.requests.length);
    await key("Escape");
    await noGhost("Escape clears the current completion");
    assert.equal(await evaluate(() => window.__inlineCompletionE2E.active.requests.length), count, "dismissal must not immediately re-request the same revision");
    await key("Tab");
    assert.equal(await value(), "Dismiss");
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), false);
    await focus();
    await win.webContents.insertText(" again");
    const next = await request("Dismiss again");
    await resolve(next.id, " works");
    await until((selector) => document.querySelector(selector)?.textContent === " works", ghostSelector);
  });

  await check("slash command has Tab priority", async () => {
    await draft("/rev");
    await until(() => Boolean(document.querySelector('[data-floating-menu-owner="composer-slash"] .slash-command-item:not(:disabled)')));
    await noGhost("slash drafts do not compete with inline providers");
    const requestsBeforeTab = await evaluate(() => window.__inlineCompletionE2E.active.requests.length);
    await screenshot("slash-menu-priority");
    await key("Tab");
    // Prompt commands remain in the menu for Enter submission after completion.
    await until((selector) => document.querySelector(selector)?.value.trimEnd() === "/review", inputSelector);
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), true);
    assert.equal(await evaluate(() => window.__inlineCompletionE2E.active.requests.length), requestsBeforeTab, "slash Tab must not invoke inline providers");
    await noGhost("completed slash command still owns the composer");
  });

  await check("provider failure and empty result fall through deterministically", async () => {
    await evaluate(() => { const fixture = window.__inlineCompletionE2E.active; fixture.failFirst = true; fixture.fallbackText = " fallback suffix"; });
    await draft("Fallback");
    const pending = await request("Fallback");
    await resolve(pending.id, null);
    await until((selector) => document.querySelector(selector)?.textContent === " fallback suffix", ghostSelector);
    const order = await evaluate(() => window.__inlineCompletionE2E.active.order);
    assert.ok(order.lastIndexOf("fallback") > order.lastIndexOf(`controlled:${pending.id}`));
    await evaluate(() => { const fixture = window.__inlineCompletionE2E.active; fixture.failFirst = false; fixture.fallbackText = ""; });
  });

  await check("Shift and repeated Tab never accept inline text", async () => {
    await suggestion("Modified", " forbidden acceptance");
    await key("Tab", ["shift"]);
    assert.equal(await value(), "Modified");
    await focus();
    const requestAfterFocus = await request("Modified");
    await resolve(requestAfterFocus.id, " repeated acceptance");
    await until((selector) => Boolean(document.querySelector(selector)), ghostSelector);
    await key("Tab", [], true);
    assert.equal(await value(), "Modified");
  });

  await check("provider signal reaches generation-bound runtime cancellation", async () => {
    await evaluate(() => { window.__inlineCompletionE2E.active.runtimeMode = true; });
    await draft("Runtime old");
    const older = await runtimeRequest("Runtime old");
    await win.webContents.insertText(" newer");
    const current = await runtimeRequest("Runtime old newer");
    const deadline = Date.now() + 5000;
    while (!older.cancelled && Date.now() < deadline) await delay(20);
    assert.equal(older.cancelled, true, "draft invalidation cancels the exact backend request");
    assert.notEqual(older.params.request_id, current.params.request_id);
    assert.equal(older.params.fingerprint, current.params.fingerprint);
    runtimePending.get(current.params.request_id)({ result: { text: " runtime suffix" } });
    runtimePending.delete(current.params.request_id);
    await until((selector) => document.querySelector(selector)?.textContent === " runtime suffix", ghostSelector);
    state.runtimeRequests = runtimeRequests;
    await evaluate(() => { window.__inlineCompletionE2E.active.runtimeMode = false; });
  });

  await check("provider disposal aborts work and cannot resurrect a result", async () => {
    await draft("Dispose pending");
    const pending = await request("Dispose pending");
    await evaluate(() => window.__inlineCompletionE2E.active.provider.dispose());
    await until((id) => window.__inlineCompletionE2E.active.requests.find((request) => request.id === id).aborted, pending.id);
    await resolve(pending.id, " stale disposed suffix");
    await noGhost("disposed provider response is ignored");
    win.webContents.send("test:inline-inventory", { enabled: true, generation: 2 });
    await until(() => window.__inlineCompletionE2E.generations.length === 2 && !window.__inlineCompletionE2E.active.disposed);
    await suggestion("Registered again", " fresh suffix");
  });

  await check("thread context replacement invalidates the previous composer", async () => {
    await draft("Previous context");
    const previous = await request("Previous context");
    await evaluate(() => document.querySelector(".session-tab-new").click());
    // The welcome content and the persistent main composer are siblings.
    await until(({ id, selector }) => Boolean(document.querySelector('[data-wuu-component="empty-session"]'))
      && document.querySelector(selector)?.value === ""
      && window.__inlineCompletionE2E.active.requests.find((request) => request.id === id)?.aborted,
    { id: previous.id, selector: inputSelector });
    await resolve(previous.id, " stale thread suffix");
    await noGhost("another conversation never receives the previous suffix");
    await screenshot("new-context-ignores-previous-reply");
    await draft("Next context");
    const next = await request("Next context");
    assert.notEqual(next.context.composerId, previous.context.composerId);
    assert.notEqual(next.context.threadId, previous.context.threadId);
    state.contexts.push(previous.context, next.context);
  });

  await check("generation removal aborts and disables product behavior", async () => {
    const generationCount = await evaluate(() => window.__inlineCompletionE2E.generations.length);
    const pending = await request("Next context");
    win.webContents.send("test:inline-inventory", { enabled: false, generation: 3 });
    await until(() => window.__inlineCompletionE2E.active.disposed);
    await resolve(pending.id, " stale generation suffix");
    await noGhost("unloaded generation response is ignored");
    const count = await evaluate(() => window.__inlineCompletionE2E.active.requests.length);
    await draft("Disabled");
    assert.equal(await evaluate(() => window.__inlineCompletionE2E.active.requests.length), count);
    await key("Tab");
    assert.equal(await value(), "Disabled");
    assert.equal(await evaluate((selector) => document.activeElement === document.querySelector(selector), inputSelector), false);
    win.webContents.send("test:inline-inventory", { enabled: true, generation: 4 });
    await until((count) => window.__inlineCompletionE2E.generations.length === count + 1 && !window.__inlineCompletionE2E.active.disposed, generationCount);
  });

  await check("host text remains literal across theme, font and narrow layout", async () => {
    for (const [width, font, theme, collapseSidebar] of [[1200, 14, "light", false], [480, 20, "dark", false], [480, 20, "dark", true]]) {
      const scenario = `${width}-${font}-${theme}${collapseSidebar ? "-sidebar-collapsed" : ""}`;
      state.visual = { width, font, theme, collapseSidebar, stage: "resize", before: win.getContentBounds() };
      save();
      console.log(`${new Date().toISOString()} VISUAL ${JSON.stringify(state.visual)}`);
      win.setContentSize(width, 850);
      await until((width) => innerWidth === width, width, "visual scenario viewport resize");
      state.visual.actual = win.getContentBounds();
      if (collapseSidebar) {
        await evaluate(() => document.querySelector('[data-wuu-component="sidebar-toggle"]').click());
        await until(() => Boolean(document.querySelector('[data-wuu-sidebar-mode="collapsed"]')), null, "sidebar collapses for narrow composer");
      }
      state.visual.stage = "configure typography";
      save();
      await evaluate(({ font, theme }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--wuu-font-size-ui", `${font}px`);
        document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      }, { font, theme });
      const text = "Document a focused implementation plan.\n保留输入中的中文和换行。";
      const suffix = " <b>literal suffix</b>，然后验证结果。";
      state.visual.stage = "offer multiline completion";
      save();
      await suggestion(text, suffix);
      state.visual.stage = "measure completion";
      save();
      const geometry = await evaluate(({ inputSelector, ghostSelector }) => {
        const input = document.querySelector(inputSelector);
        const suffix = document.querySelector(ghostSelector);
        const overlay = suffix.closest('[data-wuu-component="composer-inline-completion"]');
        const inputBox = input.getBoundingClientRect();
        const overlayBox = overlay.getBoundingClientRect();
        const typography = (element) => {
          const style = getComputedStyle(element);
          return { family: style.fontFamily, size: style.fontSize, lineHeight: style.lineHeight };
        };
        return { input: inputBox.toJSON(), viewport: { left: inputBox.left + input.clientLeft, top: inputBox.top + input.clientTop, width: input.clientWidth, height: input.clientHeight },
          overlay: overlayBox.toJSON(), ariaHidden: overlay.getAttribute("aria-hidden"), nestedMarkup: Boolean(suffix.querySelector("b")), text: suffix.textContent,
          inputFont: typography(input), overlayFont: typography(suffix.parentElement) };
      }, { inputSelector, ghostSelector });
      state.visual.geometry = geometry;
      save();
      assert.equal(geometry.ariaHidden, "true");
      assert.equal(geometry.nestedMarkup, false, "plugin completion is plain text, never injected markup");
      assert.ok(geometry.input.left >= 0 && geometry.input.right <= width + 1);
      // Classic scrollbars consume part of the border box. The overlay must
      // match the native client viewport, never paint over its scrollbar.
      assert.ok(["left", "top", "width", "height"].every((key) => Math.abs(geometry.viewport[key] - geometry.overlay[key]) <= 2), "overlay follows native input viewport");
      state.checks.push({ name: `geometry ${scenario}`, passed: true, geometry });
      state.visual.stage = "capture completion";
      await screenshot(`completion-${scenario}`);
      if (collapseSidebar) {
        state.visual.stage = "offer unbroken suffix";
        save();
        await suggestion("Keep the native input word at this narrow wrap boundary: word", "AContinuousUnbrokenCompletionMustWrapWithoutMovingTheNativeCaret");
        state.visual.stage = "capture unbroken suffix";
        const layout = await screenshot("narrow-unbroken-suffix-wrap");
        assert.ok(layout.suffixLineRects.length > 1, "long unbroken suffix wraps to multiple lines");
        assert.ok(layout.mirror.scrollWidth <= layout.mirror.clientWidth + 1, "wrapped suffix does not overflow horizontally");
        assert.ok(layout.mirror.scrollHeight > layout.viewport.height, "overflow remains clipped to the stable native input viewport");
      }
    }
  });

  assert.deepEqual(state.errors, [], "no unexpected renderer errors");
  state.passed = true;
  save();
  clearTimeout(watchdog);
  win.destroy();
  app.quit();
}).catch(fail);
