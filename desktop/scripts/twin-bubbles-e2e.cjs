// Real production App/PluginConversation renderer with the explicitly synthetic
// personal fixture transport. No account, provider, or user computer is used.
// Usage: node desktop/scripts/twin-bubbles-e2e.cjs
// DOM-only fallback: node desktop/scripts/twin-bubbles-e2e.cjs --dom-only
// Focused shell regression: node desktop/scripts/twin-bubbles-e2e.cjs --layout-only
// Output: desktop/out/twin-bubbles-e2e/{result.json,*.png,chromium.log}
const assert = require("node:assert/strict");
const { execFileSync, spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const { once } = require("node:events");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const { setTimeout: delay } = require("node:timers/promises");

const desktop = path.resolve(__dirname, "..");
const domOnly = process.argv.includes("--dom-only");
const wheelRetentionMs = 8000;
const output = path.resolve(process.env.WUU_TWIN_BUBBLES_OUTPUT || path.join(desktop, "out/twin-bubbles-e2e"));
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-twin-bubbles-"));
const report = {
  boundary: "Chromium renders the production desktop App, personal contribution and shared conversation components with disclosed synthetic fixture data. This does not test a production account, Electron IPC, Go host, provider, or physical trackpad.",
  cases: [], screenshots: [], measurements: [], runtimeErrors: [], passed: false,
  command: `node desktop/scripts/twin-bubbles-e2e.cjs${domOnly ? " --dom-only" : process.argv.includes("--layout-only") ? " --layout-only" : ""}`,
};
fs.mkdirSync(output, { recursive: true });
let server, browser, socket, sessionId, chromiumLog = "", requestId = 0;
const pending = new Map();

async function send(method, params = {}, targetSession = sessionId) {
  return new Promise((resolve, reject) => {
    const id = ++requestId;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 15_000);
    pending.set(id, {
      resolve: value => { clearTimeout(timer); resolve(value); },
      reject: error => { clearTimeout(timer); reject(error); },
    });
    socket.send(JSON.stringify({ id, method, params, ...(targetSession ? { sessionId: targetSession } : {}) }));
  });
}

async function evaluate(fn, ...args) {
  const response = await send("Runtime.evaluate", {
    expression: `(${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})`,
    awaitPromise: true, returnByValue: true,
  });
  assert.ok(!response.exceptionDetails, JSON.stringify(response.exceptionDetails));
  return response.result.value;
}

async function until(fn, label, ...args) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const result = await evaluate(fn, ...args);
    if (result) return result;
    await delay(30);
  }
  throw new Error(`Timed out: ${label}`);
}

async function settle() {
  await evaluate(() => Promise.race([
    Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {}))),
    new Promise(resolve => setTimeout(resolve, 1200)),
  ]).then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))));
}

async function click(selector) {
  const point = await evaluate(selector => {
    const element = document.querySelector(selector);
    if (!element) throw new Error(`Missing ${selector}`);
    element.scrollIntoView({ block: "nearest", behavior: "instant" });
    const rect = element.getBoundingClientRect();
    return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
  }, selector);
  await clickPoint(point);
}

async function clickPoint(point) {
  await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...point });
  await send("Input.dispatchMouseEvent", { type: "mousePressed", ...point, button: "left", buttons: 1, clickCount: 1 });
  await send("Input.dispatchMouseEvent", { type: "mouseReleased", ...point, button: "left", buttons: 0, clickCount: 1 });
}

async function drag(from, to, pointerType = "mouse", afterMove, cancel = false) {
  if (pointerType === "touch") {
    await send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ ...from, id: 1 }] });
    for (let step = 1; step <= 12; step++) {
      await send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: from.x + (to.x - from.x) * step / 12, y: from.y + (to.y - from.y) * step / 12, id: 1 }] });
    }
    await afterMove?.();
    await send("Input.dispatchTouchEvent", { type: cancel ? "touchCancel" : "touchEnd", touchPoints: [] });
    return;
  }
  await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...from });
  await send("Input.dispatchMouseEvent", { type: "mousePressed", ...from, button: "left", buttons: 1, clickCount: 1 });
  for (let step = 1; step <= 12; step++) {
    await send("Input.dispatchMouseEvent", { type: "mouseMoved", x: from.x + (to.x - from.x) * step / 12, y: from.y + (to.y - from.y) * step / 12, button: "left", buttons: 1 });
  }
  await afterMove?.();
  await send("Input.dispatchMouseEvent", { type: "mouseReleased", ...to, button: "left", buttons: 0, clickCount: 1 });
}

async function key(key, modifiers = 0) {
  const codes = { ArrowLeft: 37, ArrowRight: 39, Escape: 27, Tab: 9 };
  await send("Input.dispatchKeyEvent", { type: "keyDown", key, code: key, modifiers, windowsVirtualKeyCode: codes[key], nativeVirtualKeyCode: codes[key] });
  await send("Input.dispatchKeyEvent", { type: "keyUp", key, code: key, modifiers, windowsVirtualKeyCode: codes[key], nativeVirtualKeyCode: codes[key] });
}

async function screenshot(name) {
  await settle();
  const image = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const file = path.join(output, `${name}.png`);
  fs.writeFileSync(file, Buffer.from(image.data, "base64"));
  report.screenshots.push(file);
  report.measurements.push({ name, ...await geometry() });
}

async function geometry() {
  return evaluate(() => {
    const rect = node => {
      const box = node.getBoundingClientRect(), css = getComputedStyle(node);
      return { left: box.left, top: box.top, right: box.right, bottom: box.bottom, width: box.width, height: box.height,
        fontSize: css.fontSize, background: css.backgroundColor, borderRadius: css.borderRadius,
        scrollWidth: node.scrollWidth, clientWidth: node.clientWidth };
    };
    return { viewport: { width: innerWidth, height: innerHeight }, theme: document.documentElement.dataset.theme,
      documentOverflow: document.documentElement.scrollWidth - innerWidth,
      conversation: document.querySelector(".plugin-conversation") && rect(document.querySelector(".plugin-conversation")),
      composer: document.querySelector(".plugin-conversation-composer") && rect(document.querySelector(".plugin-conversation-composer")),
      messages: [...document.querySelectorAll(".plugin-conversation-message")].map(node => ({ role: node.dataset.role, ...rect(node),
        content: node.querySelector(".bubble-message-content") && rect(node.querySelector(".bubble-message-content")),
        time: node.querySelector(".bubble-message-time") && { ...rect(node.querySelector(".bubble-message-time")), opacity: getComputedStyle(node.querySelector(".bubble-message-time")).opacity },
      })),
      attachments: [...document.querySelectorAll(".plugin-conversation-attachment")].map(rect),
    };
  });
}

async function load({ width = 1440, height = 1000, theme = "light", font = 15, query = "" } = {}) {
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });
  await send("Page.navigate", { url: `http://127.0.0.1:${server.address().port}/?theme=${theme}&font=${font}&attachments=all${query}` });
  if (!query.includes("presentation=standard")) {
    await until(() => !!window.mosaicFixture && !!document.querySelector('[data-wuu-plugin="personal"]'), "production shell fixture");
    await click('[data-wuu-plugin="personal"]');
  }
  await until(() => document.querySelectorAll(".plugin-conversation-message").length >= 4, "synthetic main conversation");
  await settle();
}

async function check(name, run) {
  try {
    const details = await run();
    report.cases.push({ name, passed: true, ...(details === undefined ? {} : { details }) });
    console.log(`PASS: ${name}`);
  } catch (error) {
    report.cases.push({ name, passed: false, error: String(error.stack || error) });
    throw error;
  }
}

async function revealed(expected) {
  await until(expected => document.querySelector(".plugin-conversation-scroll")?.dataset.timesRevealed === String(expected), `timestamps ${expected ? "revealed" : "hidden"}`, expected);
  await settle();
  const times = await evaluate(() => [...document.querySelectorAll(".bubble-message-time")].map(node => ({ opacity: Number(getComputedStyle(node).opacity), hidden: node.getAttribute("aria-hidden"), value: node.textContent, dateTime: node.dateTime })));
  assert.ok(times.length > 0);
  assert.ok(times.every(time => time.opacity === Number(expected) && time.hidden === String(!expected)), JSON.stringify(times));
  return times;
}

async function setReveal(show) {
  await evaluate(() => document.querySelector(".bubble-time-toggle").focus({ preventScroll: true }));
  await key(show ? "ArrowLeft" : "ArrowRight", 1);
  await revealed(show);
  await evaluate(() => document.activeElement.blur());
}

async function backgroundPoint(blankOnly = false) {
  return evaluate(blankOnly => {
    const rows = [...document.querySelectorAll('.plugin-conversation-message[data-role="user"]')];
    const row = rows[rows.length - 1];
    row.scrollIntoView({ block: "center", behavior: "instant" });
    const rect = row.getBoundingClientRect();
    for (let y = rect.top + 3; y < rect.bottom - 2; y += 5) {
      for (let x = rect.right - (blankOnly ? 3 : 110); x > rect.left + (blankOnly ? 2 : 100); x -= 5) {
        const target = document.elementFromPoint(x, y);
        if (target === row || (!blankOnly && target?.classList.contains("bubble-message-content"))) return { x, y };
      }
    }
    throw new Error("No visible blank message-row drag target");
  }, blankOnly);
}

async function textPoint() {
  return evaluate(() => {
    const element = document.querySelector('.plugin-conversation-message[data-role="assistant"] .agent-text p');
    element.scrollIntoView({ block: "center", behavior: "instant" });
    const rect = element.getBoundingClientRect();
    return { x: rect.right - 10, y: rect.top + Math.min(rect.height / 2, 14) };
  });
}

async function assertContained() {
  const layout = await geometry();
  assert.ok(layout.documentOverflow <= 1, `Page overflows horizontally: ${JSON.stringify(layout)}`);
  for (const message of layout.messages) {
    assert.ok(message.left >= layout.conversation.left - 1 && message.right <= layout.conversation.right + 1, `Message leaves conversation: ${JSON.stringify(message)}`);
    assert.ok(message.content.left >= message.left - 1 && message.content.right <= message.right + 1, `Bubble leaves row: ${JSON.stringify(message)}`);
    assert.ok(message.content.scrollWidth <= message.content.clientWidth + 1, `Bubble content overflows: ${JSON.stringify(message)}`);
    if (message.time.opacity === "1") assert.ok(message.content.right <= message.time.left + 1, `Timestamp overlaps bubble: ${JSON.stringify(message)}`);
  }
  for (const attachment of layout.attachments) assert.ok(attachment.right <= layout.conversation.right + 1 && attachment.scrollWidth <= attachment.clientWidth + 1, `Attachment overflows: ${JSON.stringify(attachment)}`);
  return layout;
}

function assertRevealGeometry(before, after) {
  assert.equal(after.messages.length, before.messages.length);
  const changes = after.messages.map((message, index) => {
    const previous = before.messages[index];
    assert.equal(message.role, previous.role);
    const shift = message.content.left - previous.content.left;
    const expectedShift = message.role === "user" ? -64 : 0;
    assert.ok(Math.abs(shift - expectedShift) <= 1, `Unexpected ${message.role} translation: ${JSON.stringify({ shift, expectedShift, previous, message })}`);
    for (const dimension of ["width", "height"]) {
      assert.ok(Math.abs(message.content[dimension] - previous.content[dimension]) <= 1, `Timestamp reveal reflows ${message.role} bubble ${dimension}: ${JSON.stringify({ previous, message })}`);
      assert.ok(Math.abs(message[dimension] - previous[dimension]) <= 1, `Timestamp reveal changes ${message.role} row ${dimension}: ${JSON.stringify({ previous, message })}`);
    }
    return { role: message.role, shift, widthBefore: previous.content.width, widthAfter: message.content.width, heightBefore: previous.content.height, heightAfter: message.content.height };
  });
  assert.ok(changes.some(change => change.role === "user") && changes.some(change => change.role === "assistant"));
  return changes;
}

function assertMessageGrouping(layout, requireBoth = false) {
  const gaps = layout.messages.slice(1).map((message, index) => {
    const previous = layout.messages[index], sameRole = previous.role === message.role;
    const gap = message.top - previous.bottom, expected = sameRole ? 4 : 16;
    assert.ok(Math.abs(gap - expected) <= 1, `Unexpected rendered message gap: ${JSON.stringify({ previousRole: previous.role, role: message.role, gap, expected })}`);
    return { previousRole: previous.role, role: message.role, sameRole, gap };
  });
  assert.ok(gaps.length > 0);
  if (requireBoth) assert.ok(gaps.some(gap => gap.sameRole) && gaps.some(gap => !gap.sameRole), "Grouping fixture must exercise both same-role and different-role neighbors");
  return gaps;
}

async function submit(text) {
  const before = await evaluate(() => document.querySelectorAll(".plugin-conversation-message").length);
  await click(".plugin-conversation-composer textarea");
  await send("Input.insertText", { text });
  await until(() => !!document.querySelector(".plugin-conversation-composer .composer-send-button:not(:disabled)"), "send enabled");
  await click(".plugin-conversation-composer .composer-send-button");
  await until(count => document.querySelectorAll(".plugin-conversation-message").length === count + 2, "synthetic reply after native composer submission", before);
  await settle();
}

async function start() {
  execFileSync(process.execPath, [path.join(__dirname, "build-personal-fixture.cjs")], { cwd: desktop, stdio: "inherit" });
  const html = fs.readFileSync(path.join(desktop, "out/personal-fixture/index.html"));
  report.fixtureSha256 = createHash("sha256").update(html).digest("hex");
  server = http.createServer((request, response) => {
    if (new URL(request.url, "http://localhost").pathname !== "/") { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }); response.end(html);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const chromium = process.env.WUU_CHROMIUM || "/usr/bin/chromium";
  browser = spawn(chromium, ["--headless=new", "--disable-dev-shm-usage", "--disable-background-networking", "--disable-component-update", "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: ["ignore", "pipe", "pipe"] });
  browser.stdout.on("data", data => { chromiumLog += data; });
  browser.stderr.on("data", data => { chromiumLog += data; });
  let debuggerPort;
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    assert.equal(browser.exitCode, null, `Chromium exited before ready: ${chromiumLog}`);
    try { debuggerPort = Number(fs.readFileSync(path.join(profile, "DevToolsActivePort"), "utf8").split("\n")[0]); break; }
    catch { await delay(50); }
  }
  assert.ok(debuggerPort, `Chromium debugging socket missing: ${chromiumLog}`);
  const version = await fetch(`http://127.0.0.1:${debuggerPort}/json/version`).then(response => response.json());
  report.browser = version.Browser;
  socket = new WebSocket(version.webSocketDebuggerUrl);
  await once(socket, "open");
  socket.addEventListener("message", ({ data }) => {
    const response = JSON.parse(data);
    if (response.method === "Runtime.exceptionThrown") report.runtimeErrors.push(response.params.exceptionDetails);
    const request = pending.get(response.id);
    if (!request) return;
    pending.delete(response.id);
    if (response.error) request.reject(new Error(JSON.stringify(response.error))); else request.resolve(response.result);
  });
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  ({ sessionId } = await send("Target.attachToTarget", { targetId, flatten: true }));
  await send("Runtime.enable"); await send("Page.enable");
  await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
}

async function run() {
  await start();
  for (const configuration of [
    { name: "shell-light", theme: "light", width: 1440, height: 900, font: 15 },
    { name: "shell-dark", theme: "dark", width: 1440, height: 900, font: 15 },
    { name: "shell-light-large", theme: "light", width: 760, height: 800, font: 19 },
    { name: "shell-dark-large", theme: "dark", width: 760, height: 800, font: 19 },
  ]) {
    await load(configuration);
    await check(`${configuration.name} keeps Personal above retained Coding content`, async () => {
      // Model a retained active Coding pane, including its explicit visibility.
      await evaluate(() => {
        const retained = document.createElement("div");
        retained.id = "retained-coding-probe";
        retained.className = "cached-conversation-pane";
        retained.dataset.active = "true";
        retained.textContent = "Retained Coding conversation";
        retained.style.height = "1600px";
        document.querySelector(".conversation-pane > .scroll-region").append(retained);
      });
      for (let visit = 0; visit < 2; visit++) {
        if (visit) {
          await evaluate(() => window.mosaicFixture.controller.deactivateRegion("primary"));
          await settle();
          assert.equal(await evaluate(() => document.querySelector("#retained-coding-probe").checkVisibility({ visibilityProperty: true })), true);
          await click('[data-wuu-plugin="personal"]');
          await settle();
        }
        const bounds = await evaluate(() => {
          const pane = document.querySelector(".conversation-pane").getBoundingClientRect();
          const view = document.querySelector(".plugin-workbench-view-primary").getBoundingClientRect();
          const composer = document.querySelector(".plugin-conversation-composer").getBoundingClientRect();
          const old = document.querySelector("#retained-coding-probe");
          return { paneHeight: pane.height, viewHeight: view.height, composerHeight: composer.height, composerTop: composer.top,
            composerBottom: composer.bottom, paneBottom: pane.bottom,
            codingHidden: !old.checkVisibility({ visibilityProperty: true }) };
        });
        assert.ok(bounds.viewHeight >= bounds.paneHeight - 2, JSON.stringify(bounds));
        assert.ok(bounds.composerHeight > 0 && bounds.composerTop >= 0 && bounds.composerBottom <= bounds.paneBottom + 1, JSON.stringify(bounds));
        assert.ok(bounds.codingHidden, JSON.stringify(bounds));
      }
      await screenshot(configuration.name);
      await evaluate(() => document.querySelector("#retained-coding-probe").remove());
    });
  }
  if (process.argv.includes("--layout-only")) { report.passed = true; return; }
  await load();
  await check("twin mascot occupies the navigation icon column without growing the row", async () => {
    const alignment = await evaluate(() => {
      const nav = document.querySelector('[data-wuu-plugin="personal"]');
      const other = document.querySelector('[data-wuu-plugin="automations"]');
      const rect = node => { const r = node.getBoundingClientRect(); return { x:r.x, y:r.y, width:r.width, height:r.height, centerY:r.y+r.height/2 }; };
      return { row:rect(nav), other:rect(other), icon:rect(nav.querySelector('[data-plugin-avatar]')), label:rect(nav.lastElementChild) };
    });
    assert.ok(Math.abs(alignment.row.height-alignment.other.height) <= 1, JSON.stringify(alignment));
    assert.ok(Math.abs(alignment.icon.centerY-alignment.label.centerY) <= 1, JSON.stringify(alignment));
    assert.ok(alignment.icon.x+alignment.icon.width <= alignment.label.x, JSON.stringify(alignment));
    return alignment;
  });
  await check("twin renders distinct bubbles without the old action toolbar", async () => {
    assert.equal(await evaluate(() => window.mosaicFixture.synthetic), true);
    assert.equal(await evaluate(() => document.querySelector(".plugin-conversation").dataset.presentation), "bubbles");
    const styles = await evaluate(() => [...document.querySelectorAll(".plugin-conversation-message")].map(row => {
      const bubble = row.querySelector(row.dataset.role === "user" ? ".user-message" : ".bubble-message-content");
      const css = getComputedStyle(bubble);
      return { background: css.backgroundColor, radius: parseFloat(css.borderTopLeftRadius), actions: row.querySelectorAll(".message-actions").length, bubble: row.hasAttribute("data-bubble") };
    }));
    assert.ok(styles.every(style => style.bubble && style.actions === 0 && style.radius > 0 && style.background !== "rgba(0, 0, 0, 0)"), JSON.stringify(styles));
    const layout = await assertContained();
    assert.equal(layout.messages.length, 4);
    assert.ok(layout.messages.every(message => message.width > 0 && message.height > 0));
    const gaps = assertMessageGrouping(layout);
    await revealed(false);
    await screenshot("twin-light");
    return { styles, gaps };
  });
  await check("native blank-space left drag reveals timestamps and right drag hides them", async () => {
    let from = await backgroundPoint();
    const before = await geometry();
    await drag(from, { x: from.x - 95, y: from.y + 1 });
    await revealed(true);
    const changes = assertRevealGeometry(before, await assertContained());
    await screenshot("twin-timestamps");
    from = await backgroundPoint();
    await drag(from, { x: from.x + 95, y: from.y });
    await revealed(false);
    return changes;
  });
  await check("horizontal wheel reveals timestamps persistently without reflow or hijacking vertical wheel", async () => {
    const point = await textPoint();
    const before = await geometry();
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", ...point, deltaX: 60, deltaY: 0 });
    await revealed(true);
    const changes = assertRevealGeometry(before, await assertContained());
    // A retention window verifies visible behavior, not an implementation timer.
    await delay(wheelRetentionMs);
    await revealed(true);
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", ...point, deltaX: -60, deltaY: 0 });
    await revealed(false);
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", ...point, deltaX: 60, deltaY: 0 });
    await revealed(true);
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", ...point, deltaX: 2, deltaY: 90 });
    await revealed(false);
    return { retainedForMs: wheelRetentionMs, changes };
  });
  await check("native blank-column click hides time while clicking bubble prose preserves it", async () => {
    await setReveal(true);
    await clickPoint(await textPoint());
    await revealed(true);
    await clickPoint(await backgroundPoint(true));
    await revealed(false);
  });
  await check("keyboard Alt+Left, Alt+Right and Escape control timestamp visibility", async () => {
    await setReveal(true); await setReveal(false);
    await evaluate(() => document.querySelector(".bubble-time-toggle").focus({ preventScroll: true }));
    await key("ArrowLeft", 1); await revealed(true);
    await key("Escape"); await revealed(false);
    await evaluate(() => document.activeElement.blur());
  });
  for (const role of ["assistant", "user"]) await check(`native ${role} prose left drag preserves text selection instead of revealing time`, async () => {
    const points = await evaluate(role => {
      const element = document.querySelector(role === "user" ? '.plugin-conversation-message[data-role="user"] .user-message' : '.plugin-conversation-message[data-role="assistant"] .agent-text p');
      element.scrollIntoView({ block: "center", behavior: "instant" });
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
      let node; while ((node = walker.nextNode()) && node.length < 8) {}
      if (!node) throw new Error("No selectable prose in fixture");
      const first = document.createRange(), last = document.createRange();
      first.setStart(node, 0); first.collapse(true); last.setStart(node, 8); last.collapse(true);
      const a = first.getBoundingClientRect(), b = last.getBoundingClientRect();
      window.getSelection().removeAllRanges();
      return { text: node.textContent.slice(0, 8), from: { x: b.left, y: b.top + b.height / 2 }, to: { x: a.left, y: a.top + a.height / 2 } };
    }, role);
    await drag(points.from, points.to);
    const selection = await until(() => window.getSelection()?.toString(), "native selected text");
    assert.equal(selection, points.text);
    await revealed(false);
    await evaluate(() => window.getSelection().removeAllRanges());
    return { selected: selection };
  });
  await check("attachment card opens verified bytes in a tab without a chat download control", async () => {
    await click('[data-action="open-attachment"]');
    const file = await until(() => {
      const link = document.querySelector('.conversation-attachment-preview a[download]');
      return link && { name: link.download, href: link.href };
    }, "verified attachment ready");
    assert.ok(file.href.startsWith("blob:"));
    const text = await evaluate(async url => fetch(url).then(response => response.text()), file.href);
    assert.equal(text, "hello");
    await click('.workspace-tool-tab-close');
    await until(() => !document.querySelector('.conversation-attachment-preview'), "file tab closed");
    await revealed(false);
    return { name: file.name, verifiedText: text };
  });
  await check("native touch left swipe on prose reveals time", async () => {
    const from = await textPoint();
    await drag(from, { x: from.x - 100, y: from.y + 1 }, "touch");
    await revealed(true); await assertContained(); await setReveal(false);
  });
  await check("native touch cancellation restores timestamp visibility", async () => {
    const from = await textPoint();
    await drag(from, { x: from.x - 100, y: from.y + 1 }, "touch", () => revealed(true), true);
    await revealed(false);
  });
  await check("long content and native vertical touch scrolling remain usable", async () => {
    for (let i = 0; i < 4; i++) await submit(`合成滚动测试 ${i + 1}。\n\n${"这是一段用于检查滚动和气泡换行的合成文字。".repeat(7)}`);
    const from = await evaluate(() => {
      const scroll = document.querySelector(".plugin-conversation-scroll");
      if (scroll.scrollHeight <= scroll.clientHeight + 100) throw new Error("Fixture did not produce scrollable content");
      scroll.scrollTop = 20;
      const rect = scroll.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height * 0.7, before: scroll.scrollTop };
    });
    await drag({ x: from.x, y: from.y }, { x: from.x + 2, y: from.y - 160 }, "touch");
    const after = await until(before => {
      const top = document.querySelector(".plugin-conversation-scroll").scrollTop;
      return top > before + 10 && top;
    }, "native vertical touch scrolled", from.before);
    await revealed(false); await assertContained();
    return { before: from.before, after };
  });
  for (const configuration of [
    { name: "twin-dark", theme: "dark", width: 1440, height: 1000, font: 15 },
    { name: "twin-narrow-large", theme: "light", width: 760, height: 1000, font: 19 },
    { name: "twin-narrow-dark-large", theme: "dark", width: 760, height: 1000, font: 19 },
  ]) {
    await check(`${configuration.name} fits without horizontal overflow or timestamp overlap`, async () => {
      await load(configuration); const before = await assertContained(); await screenshot(configuration.name);
      await setReveal(true); const layout = await assertContained(); await screenshot(`${configuration.name}-timestamps`);
      return { viewport: layout.viewport, theme: layout.theme, changes: assertRevealGeometry(before, layout), gaps: assertMessageGrouping(layout) };
    });
  }
  await check("consecutive messages have compact grouping without changing role boundaries", async () => {
    await load({ query: "&grouping=1" });
    await until(() => document.querySelectorAll(".plugin-conversation-message").length === 7, "grouped messages");
    const before = await assertContained(), gaps = assertMessageGrouping(before, true);
    await screenshot("twin-grouped");
    await setReveal(true);
    const after = await assertContained();
    const changes = assertRevealGeometry(before, after);
    assertMessageGrouping(after, true);
    await screenshot("twin-grouped-timestamps");
    return { gaps, changes };
  });
  await check("standard shared conversations retain original action controls", async () => {
    await load({ query: "&presentation=standard" });
    const state = await evaluate(() => ({
      presentation: document.querySelector(".plugin-conversation").dataset.presentation,
      bubbleRows: document.querySelectorAll(".plugin-conversation-message[data-bubble]").length,
      times: document.querySelectorAll(".bubble-message-time").length,
      actions: document.querySelectorAll(".plugin-conversation-message .message-actions").length,
      buttons: document.querySelectorAll(".plugin-conversation-message .message-action-button").length,
    }));
    assert.equal(state.presentation, "standard"); assert.equal(state.bubbleRows, 0); assert.equal(state.times, 0);
    assert.equal(state.actions, 4); assert.ok(state.buttons >= 4);
    await screenshot("standard-conversation-control");
    return state;
  });
  assert.equal(report.runtimeErrors.length, 0, JSON.stringify(report.runtimeErrors));
  report.passed = true;
}

async function runDomOnly() {
  const { webcrypto } = require("node:crypto");
  const { JSDOM, VirtualConsole } = require("jsdom");
  report.boundary = "DOM-only execution of the actual production App and conversation components with synthetic fixture transport. Pointer, wheel and keyboard events are synthetic. No layout, pixels, native selection, native scrolling, Electron, host or provider behavior is proved.";
  report.visualAcceptance = false;
  report.nativeInputs = false;
  report.rendererLimitations = [];
  execFileSync(process.execPath, [path.join(__dirname, "build-personal-fixture.cjs")], { cwd: desktop, stdio: "inherit" });
  const html = fs.readFileSync(path.join(desktop, "out/personal-fixture/index.html"), "utf8");
  report.fixtureSha256 = createHash("sha256").update(html).digest("hex");
  function createDom(query = "") {
    const virtualConsole = new VirtualConsole();
    virtualConsole.on("jsdomError", error => {
      if (String(error).includes("HTMLCanvasElement.prototype.getContext")) report.rendererLimitations.push(String(error));
      else if (error.type !== "css parsing") report.runtimeErrors.push(String(error));
    });
    virtualConsole.on("error", error => report.runtimeErrors.push(String(error)));
    return new JSDOM(html, {
      url: `https://synthetic-fixture.invalid/${query}`, runScripts: "dangerously", pretendToBeVisual: true, virtualConsole,
      beforeParse(window) {
        window.matchMedia = query => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} });
        window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
        window.structuredClone = structuredClone; window.TextEncoder = TextEncoder; window.TextDecoder = TextDecoder;
        Object.defineProperty(window.crypto, "subtle", { value: { digest: (algorithm, bytes) => webcrypto.subtle.digest(algorithm, new Uint8Array(bytes)) } });
        const blobs = new Map(); let blobIndex = 0;
        window.Blob = require("node:buffer").Blob;
        window.URL.createObjectURL = blob => { const url = `blob:synthetic-dom-only${blobIndex++ ? `-${blobIndex}` : ""}`; blobs.set(url, blob); return url; };
        window.URL.revokeObjectURL = url => blobs.delete(url);
        window.fetch = async url => ({ ok: blobs.has(url), blob: async () => blobs.get(url) });
        window.document.addEventListener("click", event => { if (event.target.closest?.("a[download]")) event.preventDefault(); });
        window.PointerEvent = class extends window.MouseEvent {
          constructor(type, init = {}) { super(type, init); Object.defineProperties(this, { pointerId: { value: init.pointerId ?? 1 }, pointerType: { value: init.pointerType ?? "mouse" }, isPrimary: { value: init.isPrimary ?? true } }); }
        };
        const captures = new WeakMap();
        window.Element.prototype.setPointerCapture = function(id) { captures.set(this, id); };
        window.Element.prototype.hasPointerCapture = function(id) { return captures.get(this) === id; };
        window.Element.prototype.releasePointerCapture = function() { captures.delete(this); };
      },
    });
  }
  async function wait(window, predicate, label) {
    if (predicate()) return;
    await new Promise((resolve, reject) => {
      const observer = new window.MutationObserver(() => { if (predicate()) { clearTimeout(timer); observer.disconnect(); resolve(); } });
      const timer = setTimeout(() => { observer.disconnect(); reject(new Error(`DOM condition timed out: ${label}`)); }, 6000);
      observer.observe(window.document, { childList: true, subtree: true, attributes: true });
    });
  }
  const dom = createDom("?attachments=all");
  try {
    const { window } = dom, document = window.document;
    await wait(window, () => !!document.querySelector('[data-wuu-plugin="personal"]'), "twin navigation");
    document.querySelector('[data-wuu-plugin="personal"]').click();
    await wait(window, () => document.querySelectorAll(".plugin-conversation-message").length === 4, "main messages");
    const scroll = document.querySelector(".plugin-conversation-scroll"), toggle = document.querySelector(".bubble-time-toggle");
    const state = () => scroll.dataset.timesRevealed === "true";
    const nextFrame = () => new Promise(resolve => window.requestAnimationFrame(resolve));
    async function expectedReveal(value) {
      await wait(window, () => state() === value, `timestamps ${value}`);
      await nextFrame();
      assert.equal(state(), value);
      assert.equal(toggle.getAttribute("aria-pressed"), String(value));
      const times = [...document.querySelectorAll(".bubble-message-time")];
      assert.equal(times.length, 4);
      assert.ok(times.every(time => time.getAttribute("aria-hidden") === String(!value) && time.dateTime && time.textContent));
    }
    const keyboard = (target, key, altKey = false) => target.dispatchEvent(new window.KeyboardEvent("keydown", { key, altKey, bubbles: true, cancelable: true }));
    const pointer = (target, type, x, y, pointerType = "mouse") => {
      const event = new window.PointerEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: type === "pointerup" || type === "pointercancel" ? 0 : 1, clientX: x, clientY: y, pointerType });
      target.dispatchEvent(event);
      return event;
    };
    async function swipe(target, dx, dy = 0, pointerType = "mouse", cancel = false, afterMove) {
      pointer(target, "pointerdown", 200, 100, pointerType);
      const move = pointer(target, "pointermove", 200 + dx, 100 + dy, pointerType);
      await nextFrame();
      await afterMove?.();
      pointer(target, cancel ? "pointercancel" : "pointerup", 200 + dx, 100 + dy, pointerType);
      await nextFrame();
      return move.defaultPrevented;
    }
    await check("DOM: twin identity shares the spherical mascot across navigation and profile", async () => {
      const nav = document.querySelector('[data-wuu-plugin="personal"]');
      const trigger = document.querySelector('[data-action="toggle-personal-panel"]');
      const navIcon = nav.querySelector('[data-plugin-avatar]');
      assert.equal(navIcon.parentElement, nav);
      assert.ok(navIcon.classList.contains('plugin-ui-avatar'));
      assert.equal(nav.lastElementChild.textContent, 'twin');
      assert.equal(nav.lastElementChild.classList.contains('plugin-ui-avatar'), false);
      // JSDOM cannot resolve the full CSS cascade; actual grid alignment is a browser QA assertion.

      for (const surface of [nav, trigger, document.querySelector('.mosaic-profile-head')]) {
        assert.ok(surface.querySelector('[data-plugin-avatar]'));
        assert.equal(surface.querySelector('[data-agent-avatar-id]').dataset.agentAvatarId, 'wuu-mascot');
        assert.equal(surface.querySelector('[data-wuu-mascot-accessory]').dataset.wuuMascotAccessory, 'headset');
        assert.equal(surface.querySelector('[data-icon="sparkles"]'), null);
      }
      assert.equal(trigger.querySelector('[data-agent-avatar-motion]').dataset.agentAvatarMotion, 'static');
      trigger.click();
      await nextFrame();
      assert.equal(document.querySelector('.mosaic-profile-head [data-agent-avatar-motion]').dataset.agentAvatarMotion, 'expressive');
      keyboard(document.querySelector('[data-personal-panel]'), 'Escape');
      await nextFrame();
      assert.equal(trigger.getAttribute('aria-expanded'), 'false');
      for (const status of ['thinking', 'responding', 'idle']) {
        window.mosaicFixture.setActivity(status);
        await wait(window, () => trigger.querySelector('[data-agent-avatar-state]')?.dataset.agentAvatarState === status, status);
        assert.equal(trigger.querySelector('[data-wuu-mascot-morph]').dataset.wuuMascotMorph, 'idle');
        assert.ok(trigger.getAttribute('aria-label').endsWith({ thinking: '正在思考', responding: '正在回复', idle: '已就绪' }[status]));
        assert.equal(trigger.textContent, '');
      }
    });
    await check("DOM: only twin main conversation opts into bubbles and removes action toolbar", async () => {
      assert.equal(document.querySelector(".plugin-conversation").dataset.presentation, "bubbles");
      assert.equal(document.querySelectorAll(".plugin-conversation-message[data-bubble]").length, 4);
      assert.equal(document.querySelectorAll(".plugin-conversation-message .message-actions").length, 0);
      await expectedReveal(false);
    });
    await check("DOM: identity opens an accessible floating panel, details return and Escape restores focus", async () => {
      const trigger = document.querySelector('[data-action="toggle-personal-panel"]');
      const panel = document.querySelector('[data-personal-panel]');
      await wait(window, () => panel.hidden, "profile exit finished");
      assert.equal(trigger.getAttribute("aria-haspopup"), "dialog");
      trigger.focus(); trigger.click(); await nextFrame();
      assert.equal(panel.hidden, false);
      assert.equal(panel.getAttribute("role"), "dialog");
      assert.ok(panel.contains(document.activeElement));
      const first = panel.querySelector('summary,button:not(:disabled)');
      assert.equal(document.activeElement, panel);
      assert.equal(trigger.getAttribute('aria-hidden'), 'true');
      const last = [...panel.querySelectorAll('button:not(:disabled),a[href],input:not(:disabled),textarea:not(:disabled),select:not(:disabled),summary')].filter(node => !node.closest('details:not([open])') || node.tagName === 'SUMMARY').at(-1);
      last.focus(); keyboard(last, "Tab"); await nextFrame(); assert.equal(document.activeElement, first);
      const showOutputs = panel.querySelector('[data-action="show-more-outputs"]');
      assert.equal(panel.querySelectorAll('[data-output-sha256]').length, 3);
      showOutputs.click(); await nextFrame();
      assert.equal(panel.querySelectorAll('[data-output-sha256]').length, 5);
      assert.ok(panel.querySelector('[data-output-task-id="reading"]'));
      assert.ok(panel.querySelector('[data-routine-id="weekly"]'));
      const task = panel.querySelector('[data-task-id="trip"]');
      task.click(); await nextFrame();
      assert.ok(panel.querySelector('[data-task-detail="trip"]'));
      keyboard(document.activeElement, "Escape"); await nextFrame();
      assert.equal(panel.querySelector('[data-task-detail]'), null);
      assert.equal(document.activeElement.dataset.taskId, "trip");
      keyboard(document.activeElement, "Escape"); await nextFrame();
      await wait(window, () => panel.hidden, "profile exit finished");
      assert.equal(document.activeElement, trigger);
      trigger.click(); await nextFrame();
      const prose = document.querySelector(".agent-text");
      const stopPointer = event => event.stopPropagation();
      prose.addEventListener("pointerdown", stopPointer);
      pointer(prose, "pointerdown", 20, 20);
      await wait(window, () => panel.hidden, "outside exit finished");
      prose.removeEventListener("pointerdown", stopPointer);
    });
    await check("DOM: accessible toggle, Alt+Left/Right and Escape update timestamps", async () => {
      toggle.click(); await expectedReveal(true); toggle.click(); await expectedReveal(false);
      keyboard(toggle, "ArrowLeft", true); await expectedReveal(true);
      keyboard(toggle, "ArrowRight", true); await expectedReveal(false);
      keyboard(toggle, "ArrowLeft", true); await expectedReveal(true);
      keyboard(toggle, "Escape"); await expectedReveal(false);
      keyboard(document.querySelector(".plugin-conversation-composer textarea"), "ArrowLeft", true); await expectedReveal(false);
    });
    await check("DOM: horizontal background pointer drag reveals, right drag closes, cancellation resets", async () => {
      assert.equal(await swipe(scroll, -70, 0, "mouse", false, () => expectedReveal(true)), true); await expectedReveal(true);
      scroll.click(); await expectedReveal(true);
      await swipe(scroll, 70); await expectedReveal(false);
      await swipe(scroll, -70, 0, "mouse", true, () => expectedReveal(true)); await expectedReveal(false);
      await swipe(scroll, -15, 45, "touch"); await expectedReveal(false);
    });
    await check("DOM: prose mouse drag and interactive control drag do not trigger reveal", async () => {
      assert.equal(await swipe(document.querySelector(".agent-text"), -70), false); await expectedReveal(false);
      assert.equal(await swipe(document.querySelector(".user-message"), -70), false); await expectedReveal(false);
      assert.equal(await swipe(document.querySelector('[data-action="open-attachment"]'), -70, 0, "touch"), false); await expectedReveal(false);
      return { defaultPrevented: false, nativeSelectionVerified: false };
    });
    await check("DOM: touch prose horizontal intent reveals and vertical intent stays untouched", async () => {
      assert.equal(await swipe(document.querySelector(".agent-text"), -70, 1, "touch", false, () => expectedReveal(true)), true); await expectedReveal(true);
      keyboard(toggle, "ArrowRight", true); await expectedReveal(false);
      assert.equal(await swipe(document.querySelector(".agent-text"), -10, 60, "touch"), false); await expectedReveal(false);
    });
    await check("DOM: horizontal wheel reveal persists; vertical/modified/interactive wheel is not prevented", async () => {
      const wheel = (target, deltaX, deltaY, extra = {}) => {
        const event = new window.WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX, deltaY, ...extra });
        target.dispatchEvent(event); return event.defaultPrevented;
      };
      assert.equal(wheel(scroll, 60, 0), true); await expectedReveal(true);
      await delay(wheelRetentionMs); await expectedReveal(true);
      assert.equal(wheel(scroll, -60, 0), true); await expectedReveal(false);
      assert.equal(wheel(scroll, 60, 0), true); await expectedReveal(true);
      assert.equal(wheel(scroll, 2, 90), false); await expectedReveal(false);
      assert.equal(wheel(scroll, 60, 0, { ctrlKey: true }), false); await expectedReveal(false);
      assert.equal(wheel(scroll, 60, 0, { shiftKey: true }), false); await expectedReveal(false);
      assert.equal(wheel(document.querySelector('[data-action="open-attachment"]'), 60, 0), false); await expectedReveal(false);
      return { retainedForMs: wheelRetentionMs, layoutVerified: false };
    });
    await check("DOM: blank-column click dismisses time but bubble prose click does not", async () => {
      keyboard(toggle, "ArrowLeft", true); await expectedReveal(true);
      document.querySelector(".agent-text").click(); await expectedReveal(true);
      document.querySelector(".user-message").click(); await expectedReveal(true);
      pointer(scroll, "pointerdown", 200, 100);
      pointer(scroll, "pointerup", 200, 100);
      scroll.click(); await expectedReveal(false);
    });
    await check("DOM: chat file has only an open action; download remains inside the viewer", async () => {
      const chip = document.querySelector('.conversation-file-card');
      assert.equal(chip.querySelectorAll('button').length, 1);
      assert.equal(chip.querySelector('a[download],.conversation-file-download'), null);
      chip.querySelector('button').focus();
      assert.equal(chip.querySelector('.conversation-file-download'), null);
      chip.querySelector('button').click();
      await wait(window, () => !!document.querySelector('.conversation-attachment-preview a[download]'), "viewer download");
      assert.equal(document.querySelector('.conversation-attachment-preview a[download]').href, "blob:synthetic-dom-only");
      await expectedReveal(false);
    });
    await check("DOM: attachment body opens a real shell tab, deduplicates and returns to chat on close", async () => {
      const card = [...document.querySelectorAll('[data-action="open-attachment"]')].find(node => node.textContent.includes("合成示例.txt"));
      card.click();
      await wait(window, () => document.querySelector(".conversation-attachment-preview pre")?.textContent === "hello", "internal text preview");
      const tabs = () => document.querySelectorAll('[data-wuu-tab-kind="conversation-attachment"]');
      assert.equal(tabs().length, 1);
      assert.equal(document.querySelector('[data-action="toggle-personal-panel"]').getAttribute("aria-expanded"), "false");
      await wait(window, () => document.querySelector("[data-personal-panel]").hidden, "file open exits profile");
      assert.ok(tabs()[0].textContent.includes("合成示例.txt"));
      card.click(); await nextFrame(); assert.equal(tabs().length, 1);
      tabs()[0].querySelector(".workspace-tool-tab-close").click();
      await wait(window, () => !document.querySelector(".conversation-attachment-preview"), "closed file returns to chat");
      assert.ok(document.querySelector(".plugin-conversation-scroll"));
    });
    await check("DOM: output-detail preview keeps its verified reader after the card unmounts", async () => {
      document.querySelector('[data-action="toggle-personal-panel"]').click(); await nextFrame();
      document.querySelector('[data-output-sha256]').click(); await nextFrame();
      const panel = document.querySelector('[data-personal-panel]');
      assert.ok(panel.querySelector('[data-artifact-detail]'));
      panel.querySelector('[data-action="open-attachment"]').click();
      await wait(window, () => document.querySelector(".conversation-attachment-preview pre")?.textContent === "hello", "output detail internal file preview");
      await wait(window, () => panel.hidden, "profile exit finished");
      document.querySelector('[data-wuu-tab-kind="conversation-attachment"] .workspace-tool-tab-close').click();
      await wait(window, () => !document.querySelector(".conversation-attachment-preview"), "output preview closes");
    });
    await check("DOM: HTML cannot execute, unsupported files offer download, failed reads show recovery", async () => {
      const open = name => [...document.querySelectorAll('[data-action="open-attachment"]')].find(node => node.textContent.includes(name)).click();
      open("合成网页.html");
      await wait(window, () => document.querySelector(".conversation-attachment-preview pre")?.textContent.includes("<script>"), "inert HTML source");
      assert.equal(window.__unsafeAttachmentExecuted, undefined);
      assert.equal(document.querySelector(".conversation-attachment-preview iframe"), null);
      open("合成归档.zip");
      await wait(window, () => document.querySelector(".conversation-attachment-preview a[download]")?.download === "合成归档.zip", "unsupported download fallback");
      assert.equal(document.querySelector(".conversation-attachment-preview pre"), null);
      open("合成读取失败.txt");
      await wait(window, () => !!document.querySelector('.conversation-attachment-preview [role="alert"] button'), "retryable error");
      assert.equal(document.querySelectorAll('[data-wuu-tab-kind="conversation-attachment"]').length, 3);
    });
    await check("DOM: persistent synthetic rename updates navigation without changing product settings", async () => {
      assert.equal(document.querySelector('.mosaic [data-action="search-history"]'), null);
      assert.equal(window.getComputedStyle(document.querySelector('.mosaic .plugin-conversation-titlebar')).overflow, "visible");
      const identityCard = document.querySelector(".mosaic-identity-hover");
      assert.equal(window.getComputedStyle(identityCard).minWidth, "0");
      assert.equal(window.getComputedStyle(identityCard).top, "64px");
      assert.equal(window.getComputedStyle(identityCard.querySelector("strong")).fontWeight, "400");
      assert.equal(window.getComputedStyle(identityCard.querySelector("strong")).fontSize, "15px");
      assert.equal(window.getComputedStyle(identityCard.querySelector("strong")).lineHeight, "20px");
      assert.equal(window.getComputedStyle(identityCard.querySelector("small")).fontSize, "12px");
      assert.equal(window.getComputedStyle(identityCard.querySelector("small")).lineHeight, "16px");
      const rename = async value => {
        document.querySelector('.mosaic-identity-hover').click();
        await wait(window, () => !!document.querySelector('.mosaic-name-input'), 'rename dialog');
        const input = document.querySelector('.mosaic-name-input');
        Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(input, value);
        input.dispatchEvent(new window.Event('input', { bubbles:true })); await nextFrame();
        document.querySelector('.mosaic-name-save').click();
        await wait(window, () => !document.querySelector('.mosaic-name-dialog'), 'rename saved');
      };
      await rename('<Cloud twin>');
      assert.equal(document.querySelector('[data-wuu-plugin="personal"] > span:last-child').textContent, '<Cloud twin>');
      assert.equal(window.localStorage.getItem('wuu.fixture.personal-name'), '<Cloud twin>');
      assert.ok(document.querySelector('[data-action="toggle-personal-panel"]').getAttribute('aria-label').includes('<Cloud twin>'));
      assert.equal(document.querySelector('cloud'), null);
      await rename('twin');
    });
    await check("DOM: Settings remains reachable and uses the same twin identity", async () => {
      document.querySelector('.sidebar-account-trigger').click();
      await wait(window, () => !!document.querySelector('[data-settings-page="providers"]'), 'account settings action');
      document.querySelector('[data-settings-page="providers"]').click();
      await wait(window, () => !!document.querySelector('[data-wuu-component="plugin-settings-navigation"] [data-plugin-avatar]'), 'twin Settings identity');
      const mark = document.querySelector('[data-wuu-component="plugin-settings-navigation"] [data-agent-avatar-id]');
      assert.equal(mark.dataset.agentAvatarId, 'wuu-mascot');
    });
  } finally { dom.window.close(); }
  const regular = createDom();
  try {
    const { window } = regular, document = window.document;
    await wait(window, () => !!document.querySelector('[data-wuu-plugin="personal"]'), "regular attachment fixture");
    document.querySelector('[data-wuu-plugin="personal"]').click();
    await wait(window, () => document.querySelectorAll('.conversation-file-card').length === 2, "two representative attachments");
    await check("DOM: default files are siblings before prose, not nested grey bubbles", async () => {
      const row = document.querySelector('.plugin-conversation-message > .plugin-conversation-attachments');
      assert.ok(row);
      assert.ok(row.nextElementSibling.matches('.bubble-message-content'));
      assert.equal(row.querySelectorAll('.plugin-conversation-attachment').length, 2);
      assert.equal(row.querySelector('.mosaic-attachments'), null);
      assert.equal(document.querySelector('.bubble-message-content .conversation-file-card'), null);
    });
  } finally { regular.window.close(); }
  const layout = createDom("?attachments=layout");
  try {
    const { window } = layout, document = window.document;
    await wait(window, () => !!document.querySelector('[data-wuu-plugin="personal"]'), "attachment layout fixture");
    document.querySelector('[data-wuu-plugin="personal"]').click();
    await wait(window, () => document.querySelectorAll('.plugin-conversation-message').length === 6, "layout messages");
    await check("DOM: user files and attachment-only messages preserve separate layout", async () => {
      const messages = document.querySelectorAll('.plugin-conversation-message');
      assert.equal(messages[4].dataset.role, 'user');
      assert.equal(messages[4].querySelectorAll('.conversation-file-card').length, 2);
      assert.equal(messages[5].querySelector('.bubble-message-content'), null);
      assert.equal(messages[5].querySelectorAll('.conversation-file-card').length, 1);
    });
  } finally { layout.window.close(); }
  const grouped = createDom("?grouping=1");
  try {
    const { window } = grouped, document = window.document;
    await wait(window, () => !!document.querySelector('[data-wuu-plugin="personal"]'), "grouped twin navigation");
    document.querySelector('[data-wuu-plugin="personal"]').click();
    await wait(window, () => document.querySelectorAll(".plugin-conversation-message").length === 7, "grouped messages");
    await check("DOM: grouping fixture reaches real renderer with both same-role and different-role neighbors", async () => {
      const roles = [...document.querySelectorAll(".plugin-conversation-message")].map(message => message.dataset.role);
      assert.ok(roles.some((role, index) => index > 0 && role === roles[index - 1]));
      assert.ok(roles.some((role, index) => index > 0 && role !== roles[index - 1]));
      assert.equal(document.querySelectorAll(".bubble-message-time").length, roles.length);
      return { roles, spacingVerified: false };
    });
  } finally { grouped.window.close(); }
  const standard = createDom("?presentation=standard");
  try {
    const { window } = standard, document = window.document;
    await wait(window, () => document.querySelectorAll(".plugin-conversation-message").length === 4, "standard messages");
    await check("DOM: standard shared renderer keeps original action buttons with no bubbles or timestamp gesture", async () => {
      assert.equal(document.querySelector(".plugin-conversation").dataset.presentation, "standard");
      assert.ok(document.querySelector('[data-action="search-history"]'));
      assert.equal(document.querySelectorAll(".plugin-conversation-message[data-bubble], .bubble-message-time, .bubble-time-toggle").length, 0);
      assert.equal(document.querySelectorAll(".plugin-conversation-message .message-actions").length, 4);
      assert.ok(document.querySelectorAll(".plugin-conversation-message .message-action-button").length >= 4);
      const wheel = new window.WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX: 60, deltaY: 0 });
      document.querySelector(".plugin-conversation-scroll").dispatchEvent(wheel);
      assert.equal(wheel.defaultPrevented, false);
    });
  } finally { standard.window.close(); }
  assert.equal(report.runtimeErrors.length, 0, JSON.stringify(report.runtimeErrors));
  report.passed = true;
}

(domOnly ? runDomOnly() : run()).catch(error => {
  report.error = String(error.stack || error); process.exitCode = 1; console.error(report.error);
}).finally(async () => {
  fs.writeFileSync(path.join(output, domOnly ? "dom-result.json" : "result.json"), JSON.stringify(report, null, 2) + "\n");
  socket?.close();
  browser?.kill("SIGTERM");
  if (browser && browser.exitCode === null) await Promise.race([once(browser, "exit"), delay(3000).then(() => browser.kill("SIGKILL"))]);
  server?.close();
  if (!domOnly) fs.writeFileSync(path.join(output, "chromium.log"), chromiumLog);
  fs.rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  console.log(`Evidence: ${output}`);
});
