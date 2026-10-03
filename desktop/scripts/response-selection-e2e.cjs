const fs = require("node:fs");
const path = require("node:path");
const browserPreview = process.argv.includes("--browser-preview");
const streamingOnly = process.argv.includes("--streaming-only");
const { app, BrowserWindow, ipcMain } = browserPreview ? {} : require("electron");

// npx electron-vite build && npx electron scripts/response-selection-e2e.cjs
const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_SELECTION_E2E_OUTPUT || path.join(desktop, "out/response-selection-e2e");
fs.mkdirSync(output, { recursive: true });
if (app) app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
if (app) process.env.WUU_SELECTION_E2E_STREAMING = "true";
process.env.WUU_SELECTION_E2E_CWD = path.dirname(desktop);
const report = { scene: "response-selection-v2", boundary: "Real Electron renderer; synthetic preload transport. No Go/provider execution.", cases: [], calls: [], screenshots: [], measurements: [], errors: [] };
if (ipcMain) ipcMain.on("selection:bridge-call", (_event, call) => report.calls.push(call));
let win;
const surface = '[data-thread-id="selection-main"] article[data-response-item-id="selection-main-answer"] .agent-text';
const card = '[data-main-conversation-composer] .composer-response-selection-card .composer-document-card-main';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function evaluate(fn, ...args) {
  const result = await win.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})}}catch(e){return {error:String(e.stack||e)}}})()`, true);
  if (result.error) throw new Error(result.error);
  return result.value;
}
async function until(fn, label, ...args) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const result = await evaluate(fn, ...args);
    if (result) return result;
    await sleep(25);
  }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector) {
  await until(selector => !!document.querySelector(selector), selector, selector);
  await evaluate(selector => document.querySelector(selector).click(), selector);
}
async function input(selector, value) {
  await evaluate((selector, value) => {
    const node = document.querySelector(selector);
    window.dispatchEvent(new CustomEvent("selection-fixture-step", { detail: { step: "before-input-focus", selector, active: document.activeElement?.className } }));
    node.focus();
    window.dispatchEvent(new CustomEvent("selection-fixture-step", { detail: { step: "after-input-focus", selector, active: document.activeElement?.className } }));
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(node, value);
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }, selector, value);
  // The embedded-browser adapter invokes functions in one JS task, unlike
  // separate Electron IPC evaluations. Let React commit the controlled draft
  // before a subsequent submit click reads it.
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
// Entrances and tray lifts run on the compositor; capture the settled UI, not
// a frame from the middle of a fade.
async function settle() {
  await evaluate(() => Promise.race([
    Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => undefined))),
    new Promise(resolve => setTimeout(resolve, 1500)),
  ]).then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))));
}
async function screenshot(name) {
  await settle();
  const file = path.join(output, `${name}.png`);
  fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(file);
  report.measurements.push({ name, ...await evaluate(measureGeometry) });
}
function measureGeometry() {
  const selectors = {
    toolbar: ".response-selection-toolbar", card: "[data-main-conversation-composer] .composer-response-selection-card .composer-document-card-main",
    frame: "[data-main-conversation-composer] .composer-frame",
    popover: ".composer-response-selection-popover", sourceComment: ".response-selection-toolbar .selection-action-comment-input",
  };
  const regions = {};
  for (const [name, selector] of Object.entries(selectors)) {
    regions[name] = [...document.querySelectorAll(selector)].filter(node => node.getBoundingClientRect().width).map(node => {
      const rect = node.getBoundingClientRect(), css = getComputedStyle(node);
      return { selector, left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height,
        fontSize: css.fontSize, fontFamily: css.fontFamily, lineHeight: css.lineHeight,
        scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight,
        insideComposer: name === "card" ? !!node.closest(".composer-frame-shell, .composer-stack") : null,
        contained: rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth + 1 && rect.bottom <= innerHeight + 1 };
    });
  }
  const frame = window.frameElement?.getBoundingClientRect();
  return { unit: "CSS px", boundary: "DOM layout boxes, not glyph/ink or aesthetic measurements", viewport: { width: innerWidth, height: innerHeight, devicePixelRatio, visualScale: visualViewport?.scale },
    parentFrame: frame ? { left: frame.left, top: frame.top, width: frame.width, height: frame.height } : null,
    theme: document.documentElement.dataset.theme, userAgent: navigator.userAgent, regions };
}
async function select(text, last = false, physical = false, backward = false, align = "center") {
  await evaluate((selector, align) => {
    const root = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().width);
    document.activeElement?.blur();
    root.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerId: 1 }));
    root.scrollIntoView({ block: align, behavior: "instant" });
  }, surface, align);
  // Source navigation and centering deliver scroll asynchronously. A user starts
  // the next drag after that movement; don't create a toolbar destined to be
  // dismissed by the previous operation's queued scroll event.
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const selected = await evaluate((selector, text, last, backward) => {
    const root = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().width);
    const source = root.textContent;
    const start = last ? source.lastIndexOf(text) : source.indexOf(text);
    if (start < 0) throw new Error(`Missing ${text} in ${source}`);
    const end = start + text.length;
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let offset = 0, node, startNode, endNode, startOffset, endOffset;
    while ((node = walker.nextNode())) {
      if (!startNode && start < offset + node.length) { startNode = node; startOffset = start - offset; }
      if (!endNode && end <= offset + node.length) { endNode = node; endOffset = end - offset; }
      offset += node.length;
    }
    const range = document.createRange();
    range.setStart(startNode, startOffset); range.setEnd(endNode, endOffset);
    const first = range.cloneRange(); first.collapse(true);
    const final = range.cloneRange(); final.collapse(false);
    const a = first.getBoundingClientRect(), b = final.getBoundingClientRect();
    const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
    if (backward) selection.setBaseAndExtent(endNode, endOffset, startNode, startOffset);
    document.dispatchEvent(new Event("selectionchange"));
    root.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerId: 1 }));
    return { text, renderedText: selection.toString(), start, end, source, from: { x: Math.round(a.x), y: Math.round(a.y + a.height / 2) }, to: { x: Math.round(b.x), y: Math.round(b.y + b.height / 2) } };
  }, surface, text, last, backward);
  if (physical) {
    await evaluate(() => window.getSelection().removeAllRanges());
    win.focus();
    win.webContents.focus();
    const from = backward ? selected.to : selected.from;
    const to = backward ? selected.from : selected.to;
    const hit = await evaluate((selector, point) => {
      const root = document.querySelector(selector);
      const target = document.elementFromPoint(point.x, point.y);
      return { inSource: root?.contains(target), target: target?.className };
    }, surface, from);
    if (!hit.inSource) throw new Error(`Native drag start missed its source: ${JSON.stringify({ from, to, hit })}`);
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", ...from });
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mousePressed", ...from, button: "left", buttons: 1, clickCount: 1 });
    for (let step = 1; step <= 10; step++) {
      await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", x: Math.round(from.x + (to.x - from.x) * step / 10), y: Math.round(from.y + (to.y - from.y) * step / 10), button: "left", buttons: 1 });
    }
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseReleased", ...to, button: "left", buttons: 0, clickCount: 1 });
    try {
      await until(text => window.getSelection()?.toString() === text, "physical drag selected exact text", text);
    } catch (error) {
      const actual = await evaluate(() => ({ text: window.getSelection()?.toString(), focused: document.hasFocus(), active: document.activeElement?.className }));
      throw new Error(`${error.message}: ${JSON.stringify({ expected: text, actual, from, to })}`);
    }
  }
  await until(() => !!document.querySelector(".response-selection-toolbar button"), "native selection toolbar");
  return selected;
}
async function add(text, last = false, physical = false, comment = "") {
  const result = await select(text, last, physical);
  if (comment) {
    await click(".response-selection-toolbar .selection-action-comment-toggle");
    await input(".response-selection-toolbar .selection-action-comment-input", comment);
  }
  await click(comment ? ".response-selection-toolbar .selection-action-comment-submit" : ".response-selection-toolbar .selection-action-menu-controls > button:first-child");
  await until(selector => !!document.querySelector(selector), "quote card", card);
  return result;
}
async function closeQuotePanel() {
  await evaluate(() => document.querySelector(".composer-response-selection-popover").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  await until(() => !document.querySelector(".composer-response-selection-popover"), "quote panel closed");
}
async function checkSource(expected) {
  await settle();
  await click(card);
  await click(".composer-response-selection-source");
  const actual = await until(() => {
    const highlight = CSS.highlights.get("wuu-response-source");
    const range = highlight && [...highlight][0];
    if (!range) return null;
    const root = range.startContainer.parentElement.closest(".agent-text");
    const prefix = document.createRange(); prefix.selectNodeContents(root); prefix.setEnd(range.startContainer, range.startOffset);
    return { text: range.toString(), start: prefix.toString().length };
  }, "exact source highlight");
  if (actual.text !== expected.text || actual.start !== expected.start) {
    throw new Error(`Source highlight mismatch: ${JSON.stringify({ actual, expected })}`);
  }
}
async function send(expected, comment, prompt, method = "startTurn") {
  const before = report.calls.length;
  await input('[data-main-conversation-composer] .composer textarea', prompt);
  await click('[data-main-conversation-composer] .composer-send-button');
  await until(selector => !document.querySelector(selector), "submitted quote clears", card);
  for (let retry = 0; report.calls.length === before && retry < 100; retry++) await sleep(20);
  if (report.calls.length !== before + 1) throw new Error("Expected exactly one bridge call");
  const call = report.calls.at(-1);
  validatePayload(call, expected, comment, prompt, method);
}
function validatePayload(call, expected, comment, prompt, method = "startTurn") {
  if (call.method !== method || call.args[0] !== "selection-main") throw new Error("Wrong send route: " + call.method);
  const parts = call.args[method === "steerTurn" ? 7 : 6];
  const quote = `Quoted assistant response (JSON):\n${JSON.stringify({ text: expected.text, comment })}\n`;
  if (parts[0].type !== "response_selection" || parts[0].text !== quote || parts[0].selection.text !== expected.text || (parts[0].selection.comment || "") !== comment) throw new Error("Quote/comment content parts mismatch");
  const source = parts[0].selection.source;
  if (source.thread_id !== "selection-main" || source.turn_id !== "selection-main-turn" || source.item_id !== "selection-main-answer" || source.start_offset !== expected.start || source.end_offset !== expected.end || (source.range_text ?? expected.text) !== expected.source.slice(expected.start, expected.end) || !parts[0].selection.id) throw new Error("Rich selection source metadata mismatch");
  if (prompt && (parts.at(-1).type !== "text" || parts.at(-1).text !== prompt)) throw new Error("Prompt part mismatch");
  const flattened = call.args[method === "steerTurn" ? 2 : 1];
  if (!flattened.includes(quote.trimEnd()) || (prompt && !flattened.includes(prompt))) throw new Error("Flattened prompt lost quote/comment or prompt");
}
async function checkAnnotationPanel(name) {
  await settle();
  const geometry = await evaluate(selector => {
    const anchor = document.querySelector(selector);
    const owner = anchor?.closest(".composer-frame, .composer-frame-shell, .composer-stack");
    const panel = document.querySelector(".composer-response-selection-popover");
    if (!owner || !panel) throw new Error("Missing annotation panel or owning composer");
    const input = panel.querySelector("textarea");
    return { owner: owner.getBoundingClientRect().toJSON(), panel: panel.getBoundingClientRect().toJSON(),
      viewport: { width: innerWidth, height: innerHeight }, scrollWidth: panel.scrollWidth, clientWidth: panel.clientWidth,
      input: input ? { scrollWidth: input.scrollWidth, clientWidth: input.clientWidth, value: input.value } : null };
  }, card);
  report.measurements.push({ name, annotation: geometry });
  if (geometry.panel.left < Math.max(0, geometry.owner.left) - 1 ||
    geometry.panel.right > Math.min(geometry.viewport.width, geometry.owner.right) + 1)
    throw new Error(`${name}: annotation escaped its owning composer column: ${JSON.stringify(geometry)}`);
  if (geometry.panel.top < -1 || geometry.panel.bottom > geometry.viewport.height + 1 ||
    geometry.scrollWidth > geometry.clientWidth + 1 ||
    (geometry.input && geometry.input.scrollWidth > geometry.input.clientWidth + 1))
    throw new Error(`${name}: annotation or editor overflow: ${JSON.stringify(geometry)}`);
}

async function checkPlacement(name, expected, expectedSide) {
  await settle();
  const geometry = await evaluate(({ selector, start, end }) => {
    const root = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().width);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const range = document.createRange();
    let node, offset = 0, started = false;
    while ((node = walker.nextNode())) {
      if (!started && start < offset + node.length) { range.setStart(node, start - offset); started = true; }
      if (end <= offset + node.length) { range.setEnd(node, end - offset); break; }
      offset += node.length;
    }
    const toolbar = document.querySelector(".response-selection-toolbar");
    const popup = toolbar.getBoundingClientRect();
    const bounds = range.getBoundingClientRect();
    return { viewport: { width: innerWidth, height: innerHeight }, source: bounds.toJSON(),
      sourceRects: [...range.getClientRects()].filter(rect => rect.width && rect.height).map(rect => rect.toJSON()),
      popup: popup.toJSON(),
      complete: { left: popup.left, right: popup.right, top: popup.top, bottom: popup.bottom } };
  }, { selector: surface, start: expected.start, end: expected.end });
  report.measurements.push({ name: `${name}-source-placement`, ...geometry });
  const { source, popup, complete, viewport } = geometry;
  const expectedLeft = Math.max(8, Math.min(source.left, viewport.width - 8 - popup.width));
  if (Math.abs(popup.left - expectedLeft) > 2) throw new Error(`${name}: popup does not align to the source start with viewport clamping: ${JSON.stringify(geometry)}`);
  if (complete.left < -1 || complete.top < -1 || complete.right > viewport.width + 1 || complete.bottom > viewport.height + 1) throw new Error(`${name}: popup left the viewport`);
  const height = complete.bottom - complete.top;
  const aboveFits = source.top >= height + 16;
  const belowFits = viewport.height - source.bottom >= height + 16;
  const above = complete.bottom <= source.top + 1;
  const below = complete.top >= source.bottom - 1;
  if (aboveFits && !above) throw new Error(`${name}: popup should be above the source: ${JSON.stringify(geometry)}`);
  if (!aboveFits && belowFits && !below) throw new Error(`${name}: top-edge fallback overlaps the source: ${JSON.stringify(geometry)}`);
  if (expectedSide && !(expectedSide === "above" ? above : below)) throw new Error(`${name}: expected ${expectedSide} placement: ${JSON.stringify(geometry)}`);
  if ((aboveFits || belowFits) && geometry.sourceRects.some(rect => complete.left < rect.right && complete.right > rect.left && complete.top < rect.bottom && complete.bottom > rect.top)) throw new Error(`${name}: comment popup obscures selected text`);
  await screenshot(name);
  return geometry;
}
async function placementCoverage() {
  await click(card);
  await click(".composer-response-selection-remove");
  win.setContentSize(1200, 900);
  await evaluate(() => { document.documentElement.dataset.theme = "light"; document.documentElement.style.setProperty("--ui-font-size", "14px"); document.documentElement.style.setProperty("--conversation-message-font-size", "14px"); });
  const forward = await select("Native drag selection", false, true);
  const first = await checkPlacement("placement-forward-native", forward, "above");
  const reverse = await select("Native drag selection", false, true, true);
  const reversed = await checkPlacement("placement-reverse-native", reverse, "above");
  if (Math.abs(first.popup.left - reversed.popup.left) > 2 || Math.abs(first.popup.top - reversed.popup.top) > 2) throw new Error("Reversing the same selection changed popup placement");
  const allText = await evaluate(selector => document.querySelector(selector).textContent, surface);
  const multiline = await select(allText);
  await checkPlacement("placement-multiline", multiline, "above");
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await win.webContents.insertText("First line 第二行 😀\nA longer instruction that expands the comment box while keeping the selected passage unobscured.\nOne more line for growth.");
  await checkPlacement("placement-multiline-comment-growth", multiline);
  win.setContentSize(390, 820);
  const edge = await select("Repeated 😀 café 中文 target.", true);
  await checkPlacement("placement-narrow-edge", edge);
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await win.webContents.insertText("Keep this narrow comment readable. 第二行 😀\nThe source and its annotation must stay separate.");
  await checkPlacement("placement-narrow-comment", edge);
  await evaluate(() => document.querySelector(".response-selection-toolbar textarea")
    .dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
  await until(() => !document.querySelector(".response-selection-toolbar textarea"), "cancel annotation input");
  const cancelFocus = await evaluate(() => document.activeElement?.classList.contains("selection-action-comment-toggle"));
  if (!cancelFocus) throw new Error("Escape from annotation did not return focus to its comment action");
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await until(() => document.querySelector(".response-selection-toolbar textarea")?.value === "",
    "cancelled source comment reopens without discarded draft");
  report.cases.push("source annotation Escape restores action focus and clears its unsaved draft on reopen");
  win.setContentSize(760, 420);
  const top = await select("Native drag selection", false, false, false, "start");
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await win.webContents.insertText("Top-edge fallback 第二行 😀");
  await checkPlacement("placement-top-edge-comment", top, "below");
  const moved = await evaluate(() => { const scroll = document.querySelector(".conversation-pane > .scroll-region"); const before = scroll.scrollTop; scroll.scrollBy({ top: -20, behavior: "instant" }); return scroll.scrollTop !== before; });
  if (!moved) throw new Error("Visible-source scroll scenario did not move the source viewport");
  await checkPlacement("placement-visible-source-scroll", top);
  const preserved = await evaluate(() => { const input = document.querySelector(".response-selection-commenting textarea, .response-selection-toolbar textarea"); return { value: input?.value, focused: document.activeElement === input }; });
  if (preserved.value !== "Top-edge fallback 第二行 😀" || !preserved.focused) throw new Error("Moving the source lost the comment or input focus");
  await evaluate(() => { const scroll = document.querySelector(".conversation-pane > .scroll-region"); scroll.scrollTo({ top: scroll.scrollHeight, behavior: "instant" }); });
  await until(() => !document.querySelector(".response-selection-toolbar"), "fully offscreen source dismisses the popup");
  report.cases.push("source-left full-range anchors; forward/reverse native drag; multiline comment growth; narrow edge clamp; top fallback; visible-source scroll preserves comment/focus; offscreen dismissal");
}
async function streamingCoverage() {
  const { thread } = await evaluate(() => window.wuu.resumeThread("selection-main"));
  const turn = thread.turns[0];
  const answer = turn.items.find(item => item.id === "selection-main-answer");
  const original = answer.text;
  const appended = original + "\n\nAppended stream output.";
  const ids = { thread_id: "selection-main", turn_id: "selection-main-turn", item_id: "selection-main-answer" };
  const notify = (method, params) => win.webContents.send("selection:server-event", {
    workdir: path.dirname(desktop), kind: "notification", message: { method, params },
  });
  await until(selector => !!document.querySelector(selector)?.querySelector('[data-stream-state="streaming"]'),
    "live response", surface);
  const expected = await select("Native drag selection", false, true);
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  const comment = "Explain the live passage 😀 中文";
  await win.webContents.insertText(comment);
  notify("item/agentMessage/delta", { ...ids, delta: "\n\nAppended stream output." });
  await until(selector => document.querySelector(selector)?.textContent.includes("Appended stream output."), "stream append rendered", surface);
  const preserved = await evaluate(() => {
    const input = document.querySelector(".response-selection-toolbar .selection-action-comment-input");
    return { value: input?.value, focused: document.activeElement === input };
  });
  if (preserved.value !== comment || !preserved.focused) throw new Error("Stream append lost the comment or its focus");
  await click(".response-selection-toolbar .selection-action-comment-submit");
  await until(selector => !!document.querySelector(selector), "live quote card", card);
  await checkSource(expected);
  await send(expected, comment, "Explain this while continuing.", "steerTurn");
  report.cases.push({ name: "live native drag, comment survives append, exact live source and steer payload", preserved });

  await select("Repeated 😀 café 中文 target.", true);
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await win.webContents.insertText("Discard this if the passage changes");
  notify("item/agentMessage/replace", { ...ids, text: appended.replaceAll("Repeated", "Rewritten") });
  await until(selector => document.querySelector(selector)?.textContent.includes("Rewritten"), "replacement rendered", surface);
  await until(() => !document.querySelector(".response-selection-toolbar"), "changed source dismisses annotation");
  if (await evaluate(selector => !!document.querySelector(selector), card)) throw new Error("Invalidated passage was added to the composer");
  validatePayload(report.calls.at(-1), expected, comment, "Explain this while continuing.", "steerTurn");
  report.cases.push("live replacement dismisses a stale annotation; already submitted quote remains an immutable snapshot");

  notify("item/agentMessage/replace", { ...ids, text: appended });
  await until(selector => document.querySelector(selector)?.textContent.includes("Repeated"), "original source restored", surface);
  const finalSelection = await select("Repeated 😀 café 中文 target.", true);
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  const finalComment = "Keep this through turn completion";
  await win.webContents.insertText(finalComment);
  const item = { ...answer, status: "completed", text: appended };
  notify("item/completed", { ...ids, item });
  notify("turn/completed", { thread_id: ids.thread_id, turn: {
    ...turn, status: "completed", terminal: true, completed_at: new Date().toISOString(),
    items: turn.items.map(current => current.id === item.id ? item : current),
  } });
  await until(selector => !!document.querySelector(selector)?.querySelector('[data-stream-state="settled"]'), "turn settled", surface);
  const retained = await evaluate(() => document.querySelector(".response-selection-toolbar .selection-action-comment-input")?.value);
  if (retained !== finalComment) throw new Error("Turn completion discarded the live comment");
  await click(".response-selection-toolbar .selection-action-comment-submit");
  await until(selector => !!document.querySelector(selector), "completed annotation card", card);
  await checkSource(finalSelection);
  await click(card);
  await click(".composer-response-selection-remove");
  report.cases.push("turn completion preserves the active Unicode selection and comment with exact source offsets");
}

async function run() {
  win = new BrowserWindow({ width: 1200, height: 820, show: process.env.WUU_E2E_VISIBLE === "true", webPreferences: { preload: path.join(__dirname, "response-selection-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  win.setContentSize(1200, 820);
  win.webContents.debugger.attach("1.3");
  win.webContents.on("console-message", ({ level, message }) => { if (level >= 3) report.errors.push(message); });
  // The synthetic bridge never needs remote resources or a live provider.
  win.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
    report.errors.push(`Unexpected network request: ${details.url}`);
    callback({ cancel: true });
  });
  await win.loadFile(process.env.WUU_E2E_RENDERER || path.join(desktop, "out/renderer/index.html"));
  await until(selector => !!document.querySelector(selector), "rendered response", surface);
  await evaluate(() => {
    for (const toggle of document.querySelectorAll('.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]')) toggle.click();
    if (!document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) document.querySelector(".sidebar-toggle-button").click();
  });
  await settle();
  await streamingCoverage();
  if (streamingOnly) {
    if (report.errors.length) throw new Error(JSON.stringify(report.errors));
    report.status = "passed";
    return;
  }
  const drag = await add("Native drag selection", false, true);
  await checkSource(drag);
  await screenshot("physical-drag-source");
  await click(card);
  await click(".composer-response-selection-remove");
  report.cases.push("physical mouse drag -> toolbar -> card -> exact source -> remove");

  const comment = 'Explain "this" 😀\n第二行';
  const repeated = await select("Repeated 😀 café 中文 target.", true);
  await screenshot("toolbar-actions");
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  await screenshot("comment-empty-single-line");
  // Real Chromium input insertion, not a React setter, protects the restored-Range
  // focus contract: typing must enter the comment rather than replace the quote.
  const firstLine = comment.split("\n")[0];
  await win.webContents.insertText(firstLine);
  await screenshot("comment-single-line");
  await win.webContents.insertText(comment.slice(firstLine.length));
  await until(value => document.querySelector('.response-selection-toolbar .selection-action-comment-input')?.value === value, "native source comment typing", comment);
  await screenshot("comment-expanded");
  await click(".response-selection-toolbar .selection-action-comment-submit");
  await until(selector => !!document.querySelector(selector), "source comment added", card);
  await checkSource(repeated);
  await send(repeated, comment, "Please explain the selected passage.");
  report.cases.push("repeated Unicode last occurrence, UTF16 offsets, exact highlight, escaped comment + prompt payload");

  const quote = await add("Repeated 😀 café 中文 target.");
  await send(quote, "", "");
  report.cases.push("quote-only submission retains rich metadata and flattened quote");

  const multilineQuote = await evaluate(selector => document.querySelector(selector).textContent, surface);
  const multilineSelection = await add(multilineQuote);
  const longComment = `Long annotation with English and 中文.\n${"中文需要保持完整并正确换行。".repeat(16)}\n${"unbroken_annotation_".repeat(32)}`;
  await add("Repeated 😀 café 中文 target.", true, false, "Second annotation 第二条批注");
  win.setContentSize(390, 820);
  await evaluate(() => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
    document.documentElement.style.setProperty("--ui-font-size", "20px");
  });
  await settle();
  await screenshot("multiple-quotes-collapsed-light-390-20");
  await until(selector => document.querySelectorAll(selector).length === 2, "two quote attachment cards", card);
  await evaluate(selector => document.querySelectorAll(selector)[1].click(), card);
  await until(() => document.querySelector(".composer-response-selection-comment")?.value === "Second annotation 第二条批注",
    "second quote details");
  await checkAnnotationPanel("multiple-quotes-light-390-20");
  await screenshot("multiple-quotes-expanded-light-390-20");
  await click(".composer-response-selection-remove");
  await until(selector => document.querySelectorAll(selector).length === 1, "remove only second quote", card);
  await click(card);
  const retained = await evaluate(() => document.querySelector(".composer-response-selection-quote")?.textContent);
  if (retained !== multilineSelection.renderedText) throw new Error("Removing the second quote changed the first quote");
  await closeQuotePanel();
  report.cases.push("two quotes collapsed and expanded at 390px/20px, removing second retains complete first selection");
  for (const [width, height, font, theme] of [[1200, 820, 14, "light"], [1200, 820, 20, "dark"], [390, 820, 14, "dark"], [390, 820, 20, "light"]]) {
    win.setContentSize(width, height);
    await evaluate((font, theme) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--ui-font-size", `${font}px`);
    }, font, theme);
    await settle();
    await click(card);
    await until(() => {
      const node = document.querySelector(".composer-response-selection-popover");
      if (!node) return false;
      const rect = node.getBoundingClientRect();
      return rect.width > 0 && rect.left >= 0 && rect.right <= innerWidth + 1 && rect.top >= 0 && rect.bottom <= innerHeight + 1;
    }, "quote editor within viewport");
    await checkAnnotationPanel(`${theme}-${width}-${font}-quote`);
    const shownQuote = await evaluate(() => document.querySelector(".composer-response-selection-quote")?.textContent);
    // Chromium Selection text inserts rendered paragraph breaks that DOM
    // textContent intentionally omits. Compare against the native selection,
    // not concatenated DOM text; the source offsets still refer to textContent.
    if (shownQuote !== multilineSelection.renderedText) throw new Error(`Annotation quote lost original whitespace or text: ${JSON.stringify({ expected: multilineSelection.renderedText, actual: shownQuote })}`);
    await input(".composer-response-selection-comment", longComment);
    await checkAnnotationPanel(`${theme}-${width}-${font}-long-comment`);
    await screenshot(`${theme}-${width}-${font}`);
    await closeQuotePanel();
    await click(card);
    await until(expected => document.querySelector(".composer-response-selection-comment")?.value === expected,
      "long comment survives dismissal and reopening", longComment);
    await checkAnnotationPanel(`${theme}-${width}-${font}-reopened`);
    await closeQuotePanel();
  }
  report.cases.push("light/dark, default/20px font, wide/narrow owning-column bounds, exact multiline quote, long Chinese/English/unbroken comment wrapping and dismiss/reopen retention");
  await placementCoverage();
  if (report.errors.length) throw new Error(`Unexpected renderer or network errors: ${JSON.stringify(report.errors)}`);
  report.status = "passed";
}
if (browserPreview) servePreview();
else app.whenReady().then(run).catch(async error => {
  report.status = "failed"; report.failure = String(error.stack || error);
  console.error(error);
  if (!streamingOnly && win && !win.isDestroyed()) await screenshot("failure").catch(() => {});
}).finally(() => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  app.exit(report.status === "passed" ? 0 : 1);
});

// Explicit alternate runtime when Electron cannot launch under host restrictions.
// This serves the same built renderer and mock bridge, never the production IPC.
function servePreview() {
  const http = require("node:http");
  const root = path.join(desktop, "out/renderer");
  const preload = fs.readFileSync(path.join(__dirname, "response-selection-e2e-preload.cjs"), "utf8").replace('const { contextBridge, ipcRenderer } = require("electron");', "");
  const shim = `const process={env:{WUU_SELECTION_E2E_CWD:${JSON.stringify(path.dirname(desktop))}}};
    window.selectionCalls=[];
    const contextBridge={exposeInMainWorld:(name,api)=>window[name]=api};
    const ipcRenderer={on:()=>{},send:(_channel,call)=>window.selectionCalls.push(call)};\n${preload}`;
  const harness = `const report={scene:'response-selection-v2',calls:window.selectionCalls,cases:[],measurements:[],boundary:"Built React renderer in embedded browser; mock transport; NOT Electron E2E"};
    const surface=${JSON.stringify(surface)},card=${JSON.stringify(card)};
    const evaluate=async(fn,...args)=>fn(...args);
    ${[sleep, until, click, input, select, add, settle, closeQuotePanel, checkSource, send, validatePayload, measureGeometry].map(fn => `const ${fn.name || "sleep"}=${fn.toString()};`).join("\n")}
    const params=new URLSearchParams(location.search);
    report.nativeInput=[];
    report.eventTrace=[];
    function trace(event){const node=event.target;report.eventTrace.push({time:performance.now(),type:event.type,target:node?.className||node?.nodeName||'window',active:document.activeElement?.className,toolbar:!!document.querySelector('.response-selection-toolbar'),commenting:!!document.querySelector('.response-selection-commenting'),selection:window.getSelection()?.toString(),scrollTop:node?.scrollTop,visibility:document.visibilityState,viewport:{width:innerWidth,height:innerHeight,dpr:devicePixelRatio},detail:event.detail&&typeof event.detail==='object'?event.detail:undefined});if(report.eventTrace.length>500)report.eventTrace.shift()}
    for(const type of ['scroll','blur','focusin','focusout','pointerdown','pointerup','input','selectionchange','visibilitychange'])document.addEventListener(type,trace,true);
    for(const type of ['blur','resize','selection-fixture-step'])window.addEventListener(type,trace);
    document.addEventListener('input',event=>{if(event.target.matches('.response-selection-toolbar .selection-action-comment-input'))report.nativeInput.push({type:event.type,isTrusted:event.isTrusted,value:event.target.value,active:document.activeElement?.className,selection:window.getSelection()?.toString()})});
    document.addEventListener('keyup',event=>{if(event.key==='Escape')requestAnimationFrame(()=>{report.escapeFocus=document.activeElement?.className})});
    const style=document.createElement('style'); style.textContent='#selection-fixture-controls{position:fixed;top:36px;right:8px;max-width:calc(100% - 16px);z-index:2147483647;display:flex;flex-wrap:wrap;gap:4px;font:11px sans-serif;background:#eee;color:#111;padding:4px}';document.head.append(style);
    const controls=document.createElement('div');controls.id='selection-fixture-controls';document.body.append(controls);
    function control(label,action){const button=document.createElement('button');button.textContent=label;button.onclick=()=>Promise.resolve(action()).catch(error=>{report.failure=String(error);save()});controls.append(button)}
    control('Select repeated',()=>select('Repeated 😀 café 中文 target.',true));
    control('Select long',async()=>{const text=document.querySelector(surface).textContent;await select(text)});
    control('Toggle theme',()=>document.documentElement.dataset.theme=document.documentElement.dataset.theme==='dark'?'light':'dark');
    control('Toggle font',()=>{const root=document.documentElement;const size=root.dataset.fixtureLarge?'14':'20';root.dataset.fixtureLarge=size==='20'?'1':'';root.style.setProperty('--conversation-message-font-size',size+'px');root.style.setProperty('--ui-font-size',size+'px')});
    control('Hide fixture controls',()=>controls.remove());
    if(params.get('controls')==='0')controls.remove();
    control('Measure geometry',async()=>{report.measurements.push({name:'manual',...measureGeometry()});await save()});
    async function save(){report.geometry=measureGeometry();await fetch('/report'+location.search,{method:'POST',body:JSON.stringify(report,null,2)})}
    (async()=>{try{
      await until(selector=>!!document.querySelector(selector),'settled response',surface);
      const sourceRoot=document.querySelector(surface);
      const observer=new MutationObserver(records=>{for(const record of records)trace({type:'source-observer-mutation',target:record.target,detail:{kind:record.type,attribute:record.attributeName,oldValue:record.oldValue,newValue:record.attributeName?record.target.getAttribute(record.attributeName):undefined}})});
      observer.observe(sourceRoot,{subtree:true,childList:true,characterData:true});
      for(let node=sourceRoot.closest('article');node;node=node.parentElement)observer.observe(node,{attributes:true,attributeOldValue:true,attributeFilter:['hidden','inert','aria-hidden','style','class','data-thread-id','data-response-settled']});
      new MutationObserver(records=>{for(const record of records)for(const node of [...record.addedNodes,...record.removedNodes])if(node.nodeType===1&&node.matches('.response-selection-toolbar'))trace({type:'toolbar-lifecycle',target:node,detail:{connected:node.isConnected}})}).observe(document.body,{childList:true});
      for(const toggle of document.querySelectorAll('.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]'))toggle.click();
      if(!document.querySelector('.app-shell').classList.contains('sidebar-collapsed'))document.querySelector('.sidebar-toggle-button').click();
      if(params.has('theme'))document.documentElement.dataset.theme=params.get('theme');
      if(params.has('font')){document.documentElement.style.setProperty('--conversation-message-font-size',params.get('font')+'px');document.documentElement.style.setProperty('--ui-font-size',params.get('font')+'px')}
      // The embedded host initially loads pages hidden, then makes them visible
      // on observation. The product intentionally dismisses selection UI on
      // visibilitychange. Start user gestures only after that host transition;
      // never suppress the product's lifecycle handler to make a test pass.
      if(document.visibilityState!=='visible'){
        report.status='waiting-for-visible-host';await save();
        await new Promise(resolve=>{const visible=()=>{if(document.visibilityState==='visible'){document.removeEventListener('visibilitychange',visible);resolve()}};document.addEventListener('visibilitychange',visible);visible()});
      }
      await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
      const scene=params.get('state')||'manager';
      const text=params.get('long')==='1'?document.querySelector(surface).textContent:'Repeated 😀 café 中文 target.';
      if(scene==='send'){
        const comment='Explain "this" 😀\\n第二行';
        const selected=await add(text,true,false,comment);await checkSource(selected);await send(selected,comment,'Please explain.');
        report.cases.push('source-local Comment + Add + send wire payload');
        const quote=await add(text,true);await send(quote,'','');report.cases.push('quote-only wire payload');
      }else if(scene==='toolbar'||scene==='comment'||scene==='typing'){
        await select(text,true);
        if(scene==='comment'||scene==='typing'){await click('.response-selection-toolbar .selection-action-comment-toggle');if(scene==='comment')await input('.response-selection-toolbar .selection-action-comment-input','Explain "this" 😀\\n第二行')}
      }else{
        const expected=await add(text,true,false,'Explain "this" 😀\\n第二行');
        if(params.get('long')!=='1'){await checkSource(expected);report.cases.push({name:'repeated Unicode exact source highlight',expected})}
        const count=Math.max(1,Math.min(12,Number(params.get('count'))||(scene==='aggregate'?2:1)));
        for(let index=1;index<count;index++)await add('Native drag selection',false,false,'Comment '+(index+1));
        if(scene==='manager'||scene==='aggregate')await click(card);
        if(scene==='aggregate'){
          const entries=()=>[...document.querySelectorAll(card)];
          await until(()=>entries().length===2,'two selection attachment cards');
          await closeQuotePanel();
          entries()[1].click();
          await until(()=>!!document.querySelector('.composer-response-selection-remove'),'second quote details');
          document.querySelector('.composer-response-selection-remove').click();
          await until(()=>entries().length===1,'remove only the second selection');
          await checkSource(expected);await click(card);report.cases.push('two selection cards; removing the second preserves the first exact source');
        }
      }
      report.measurements.push({name:scene,...measureGeometry()});
      report.status='ready-for-visual-review';
    }catch(error){report.status='failed';report.failure=String(error.stack||error)}finally{await save()}})();
    setInterval(save,1000);`;
  http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");
    if (url.pathname === "/report" && request.method === "POST") {
      let body = ""; request.on("data", chunk => body += chunk); request.on("end", () => {
        const scene = [...url.searchParams].map(([key, value]) => `${key}-${value}`).join("-").replace(/[^a-z0-9-]/gi, "").slice(0, 160) || "default";
        fs.writeFileSync(path.join(output, "browser-report.json"), body);
        fs.writeFileSync(path.join(output, `browser-report-${scene}.json`), body); response.end("ok");
      }); return;
    }
    if (url.pathname === "/" && url.searchParams.has("width")) {
      const width = Math.max(320, Math.min(1800, Number(url.searchParams.get("width")) || 390));
      const height = Math.max(400, Math.min(1400, Number(url.searchParams.get("height")) || 820));
      const source = `/index.html?${url.searchParams}`.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
      response.setHeader("Content-Type", "text/html");
      response.end(`<!doctype html><html><head><title>Selection viewport fixture</title></head><body style="margin:0;background:#777"><iframe title="Real renderer ${width} by ${height}" src="${source}" width="${width}" height="${height}" style="display:block;border:0"></iframe></body></html>`);
      return;
    }
    if (request.url.startsWith("/fixture.js")) { response.setHeader("Content-Type", "text/javascript"); response.end(shim); return; }
    if (request.url.startsWith("/harness.js")) { response.setHeader("Content-Type", "text/javascript"); response.end(harness); return; }
    const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
    const file = path.resolve(root, `.${pathname === "/" ? "/index.html" : pathname}`);
    if (!file.startsWith(`${root}${path.sep}`) || !fs.existsSync(file)) { response.writeHead(404); response.end(); return; }
    const ext = path.extname(file);
    response.setHeader("Content-Type", { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[ext] || "application/octet-stream");
    if (ext === ".html") {
      response.end(fs.readFileSync(file, "utf8").replace("<head>", '<head><script src="/fixture.js"></script>').replace("</body>", '<script src="/harness.js"></script></body>'));
    } else response.end(fs.readFileSync(file));
  }).listen(4179, "127.0.0.1", () => console.log("Selection browser preview: http://127.0.0.1:4179"));
}
