// Synthetic conversations only: never connects to an app-server or user profile.
// Run after `npm run build`: electron scripts/conversation-perf-e2e.cjs
// Timings are diagnostic, not hardware-dependent CI thresholds.
// WUU_PERF_VARIANT=large-narrow exercises dark mode and 20px UI text at 820px.
// WUU_PERF_VARIANT=reduced exercises the system reduced-motion preference.
// WUU_PERF_PENDING=steer exercises Enter during a running turn instead of Tab.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow, nativeTheme, ipcMain } = require("electron");
const { once } = require("node:events");

const root = path.resolve(__dirname, "..");
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-conversation-perf-"));
app.setPath("userData", profile);
setTimeout(() => { console.error("Conversation profiling exceeded 45 seconds"); app.exit(1); }, 45000).unref();
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn})()`);
const emit = (win, method, params) => win.webContents.send("test:server-event", {
  workdir: process.cwd(), kind: "notification", message: { jsonrpc: "2.0", method, params },
});

async function waitFor(win, fn) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(win, fn)) return;
    await delay(40);
  }
  throw new Error(`Timed out: ${fn}`);
}

async function measure(win, name, action) {
  const debug = win.webContents.debugger;
  await debug.sendCommand("Performance.enable");
  await debug.sendCommand("Profiler.enable");
  await debug.sendCommand("Profiler.start");
  const before = await debug.sendCommand("Performance.getMetrics");
  await evaluate(win, () => {
    window.__perf = { frames: [], rects: 0, scans: 0, properties: {} };
    const rect = Element.prototype.getBoundingClientRect;
    const query = Element.prototype.querySelectorAll;
    const set = CSSStyleDeclaration.prototype.setProperty;
    CSSStyleDeclaration.prototype.setProperty = function (name, ...args) {
      if (name.startsWith("--")) window.__perf.properties[name] = (window.__perf.properties[name] ?? 0) + 1;
      return set.call(this, name, ...args);
    };
    Element.prototype.getBoundingClientRect = function () { window.__perf.rects++; return rect.call(this); };
    Element.prototype.querySelectorAll = function (selector) {
      if (selector === ".turn[data-turn-id]") window.__perf.scans++;
      return query.call(this, selector);
    };
    let last;
    const frame = time => {
      if (last !== undefined) window.__perf.frames.push(time - last);
      last = time;
      window.__perf.frame = requestAnimationFrame(frame);
    };
    window.__perf.restore = () => {
      cancelAnimationFrame(window.__perf.frame);
      Element.prototype.getBoundingClientRect = rect;
      Element.prototype.querySelectorAll = query;
      CSSStyleDeclaration.prototype.setProperty = set;
    };
    window.__perf.frame = requestAnimationFrame(frame);
  });
  await action();
  await delay(1400);
  const result = await evaluate(win, () => {
    window.__perf.restore();
    const { frames, rects, scans, properties } = window.__perf;
    frames.sort((a, b) => a - b);
    return { frames: frames.length, p95: frames[Math.floor(frames.length * .95)], max: frames.at(-1), over25ms: frames.filter(x => x > 25).length, rects, scans, properties };
  });
  const after = await debug.sendCommand("Performance.getMetrics");
  const cpu = await debug.sendCommand("Profiler.stop");
  const prior = Object.fromEntries(before.metrics.map(m => [m.name, m.value]));
  for (const m of after.metrics) {
    if (["LayoutCount", "RecalcStyleCount", "LayoutDuration", "RecalcStyleDuration", "ScriptDuration", "TaskDuration"].includes(m.name)) result[m.name] = +(m.value - prior[m.name]).toFixed(4);
  }
  const hits = new Map();
  for (const n of cpu.profile.nodes) {
    if (!n.hitCount) continue;
    const f = n.callFrame;
    const key = `${f.functionName} ${f.url}:${f.lineNumber + 1}`;
    hits.set(key, (hits.get(key) ?? 0) + n.hitCount);
  }
  result.hot = [...hits].sort((a, b) => b[1] - a[1]).slice(0, 15);
  console.log(JSON.stringify({ name, ...result }));
}

async function send(win) {
  await evaluate(win, () => {
    const textarea = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Measure submitted message motion.");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await evaluate(win, () => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true })));
}

app.whenReady().then(async () => {
  const largeNarrow = process.env.WUU_PERF_VARIANT === "large-narrow";
  nativeTheme.themeSource = largeNarrow ? "dark" : "light";
  const win = new BrowserWindow({ width: largeNarrow ? 820 : 1380, height: 860, show: true, webPreferences: {
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  win.webContents.debugger.attach("1.3");
  if (process.env.WUU_PERF_URL) await win.loadURL(process.env.WUU_PERF_URL);
  else await win.loadFile(path.join(root, "out/renderer/index.html"));
  if (process.env.WUU_PERF_VARIANT === "reduced") {
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  }
  await waitFor(win, () => !!document.querySelector(".composer textarea"));
  if (largeNarrow) await evaluate(win, () => {
    document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
    document.documentElement.style.setProperty("--appearance-scale", String(20 / 14));
  });
  await delay(500);
  await measure(win, "first-send", () => send(win));
  assert.ok(await evaluate(win, () => !!document.querySelector(".turn")), "Send must render a turn");
  assert.ok(await evaluate(win, () => {
    const viewport = document.querySelector(".conversation-pane > .scroll-region").getBoundingClientRect();
    const query = document.querySelector("[data-user-message-id]").getBoundingClientRect();
    return query.top >= viewport.top - 1 && query.bottom <= viewport.bottom + 1;
  }), "The submitted first message must remain inside the reading viewport");
  const now = new Date().toISOString();
  emit(win, "turn/completed", { thread_id: "thread-immediate-title-e2e", turn: { id: "turn-thread-immediate-title-e2e", status: "completed", items_view: "full", items: [], completed_at: now } });
  const thread = { id: "thread-immediate-title-e2e", preview: "Performance fixture", model_provider: "e2e", model: "mock-stream", cwd: process.cwd(), status: "idle", created_at: now, updated_at: now,
    turns: Array.from({ length: 120 }, (_, i) => ({ id: `history-${i}`, status: "completed", items_view: "full", started_at: now, completed_at: now,
      items: [{ id: `user-${i}`, type: "user_message", status: "completed", text: `Question ${i}` }, { id: `answer-${i}`, type: "agent_message", status: "completed", text: ("## Response\n\nA paragraph with **formatting** and enough text to wrap in the conversation viewport.\n\n```ts\nconst answer = 42;\n```\n\n").repeat(5) }],
    })),
  };
  emit(win, "thread/resumed", { thread });
  await waitFor(win, () => !!document.querySelector('[data-turn-id="history-119"]'));
  for (let i = 0; i < 3; i++) {
    await evaluate(win, () => document.querySelector(".conversation-turn-history-loader")?.click());
    await delay(100);
  }
  await waitFor(win, () => document.querySelectorAll(".turn").length >= 120);
  await delay(700);
  await measure(win, "history-scroll", () => evaluate(win, () => {
    const node = document.querySelector(".conversation-pane > .scroll-region");
    node.dispatchEvent(new WheelEvent("wheel", { deltaY: -80, bubbles: true }));
    node.scrollTop = node.scrollHeight * .8;
    const start = performance.now();
    const frame = now => { node.scrollTop -= 5; if (now - start < 800) requestAnimationFrame(frame); };
    requestAnimationFrame(frame);
  }));
  await measure(win, "history-send", () => send(win));
  assert.ok(await evaluate(win, () => document.querySelector('[data-user-message-id="user-thread-immediate-title-e2e"]')), "History send must render the acknowledged message");
  const stream = { thread_id: thread.id, turn_id: `turn-${thread.id}`, item_id: "perf-answer" };
  emit(win, "item/started", { ...stream, item: { id: stream.item_id, type: "agent_message", status: "in_progress", text: "" } });
  await measure(win, "history-stream", async () => {
    for (let i = 0; i < 60; i++) {
      emit(win, "item/agentMessage/delta", { ...stream, delta: `Streaming paragraph ${i} with **formatted text** and a little more content.\n\n` });
      await delay(16);
    }
  });
  await waitFor(win, () => [...document.querySelectorAll(".streaming-markdown")].some(node => node.textContent.includes("Streaming paragraph 59")));
  const completedAnswer = { id: stream.item_id, type: "agent_message", status: "completed", text: Array.from({ length: 60 }, (_, i) => `Streaming paragraph ${i} with **formatted text** and a little more content.\n\n`).join("") };
  const steer = process.env.WUU_PERF_PENDING === "steer";
  // Pause in history before enqueueing. Receiving the queued turn must not
  // create a submission reservation or move the reader to the new bubble.
  await evaluate(win, () => {
    const node = document.querySelector(".conversation-pane > .scroll-region");
    node.dispatchEvent(new WheelEvent("wheel", { deltaY: -1200, bubbles: true }));
    node.scrollTop = Math.max(0, node.scrollTop - 1200);
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  const queuedEvent = once(ipcMain, "test:queued-input");
  await evaluate(win, () => {
    const textarea = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Queued follow-up.");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  if (steer) {
    await evaluate(win, () => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true })));
  } else {
    await evaluate(win, () => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", code: "Tab", bubbles: true, cancelable: true })));
  }
  const [, queued] = await queuedEvent;
  await waitFor(win, () => !!document.querySelector(".composer-pending-preview"));
  await delay(250);
  if (!steer) emit(win, "turn/completed", { thread_id: thread.id, turn: { id: stream.turn_id, status: "completed", items_view: "full", items: [
    { id: `user-${thread.id}`, type: "user_message", status: "completed", text: "Measure submitted message motion." }, completedAnswer,
  ], completed_at: now } });
  await delay(500);
  const readingTop = await evaluate(win, () => document.querySelector(".conversation-pane > .scroll-region").scrollTop);
  const readingAnchor = await evaluate(win, () => {
    const viewport = document.querySelector(".conversation-pane > .scroll-region").getBoundingClientRect();
    // Do not measure every offscreen paragraph: that forces content-visibility
    // subtrees to lay out and changes the very scroll extent being observed.
    let anchor;
    for (let y = viewport.top + 40; y < viewport.bottom - 40 && !anchor; y += 20) {
      for (let x = viewport.left + 80; x < viewport.right - 80 && !anchor; x += 60) {
        anchor = document.elementFromPoint(x, y)?.closest(".cached-conversation-pane[data-active=true] p");
      }
    }
    if (!anchor) throw new Error("No visible reading anchor");
    window.__queueReadingAnchor = anchor;
    return anchor.getBoundingClientRect().top;
  });
  const acceptedInput = { id: "dequeued-user", type: "user_message", status: "completed", text: queued.text, source_id: queued.id };
  if (steer) {
    emit(win, "item/completed", { thread_id: thread.id, turn_id: stream.turn_id, item: acceptedInput });
  } else {
    emit(win, "turn/started", { thread_id: thread.id, turn: { id: "dequeued-turn", status: "in_progress", items_view: "full", started_at: now, items: [acceptedInput] } });
  }
  await waitFor(win, () => !!document.querySelector('[data-user-message-id="dequeued-user"]') && !document.querySelector(".composer-pending-preview"));
  await delay(750);
  const afterDequeue = await evaluate(win, () => ({
    top: document.querySelector(".conversation-pane > .scroll-region").scrollTop,
    tail: parseFloat(document.querySelector(".conversation-pane").style.getPropertyValue("--session-tail-space")) || 0,
    anchor: window.__queueReadingAnchor.getBoundingClientRect().top,
    connected: window.__queueReadingAnchor.isConnected,
  }));
  assert.ok(afterDequeue.connected && Math.abs(afterDequeue.anchor - readingAnchor) <= 1, `Dequeue must preserve the visible reading anchor: ${readingAnchor} -> ${afterDequeue.anchor}, scroll ${readingTop} -> ${afterDequeue.top}`);
  assert.equal(afterDequeue.tail, 0, "Dequeue must not reserve blank response space");
  console.log(JSON.stringify({ name: steer ? "steer-reading-position" : "queued-reading-position", before: { top: readingTop, anchor: readingAnchor }, after: afterDequeue }));
  emit(win, "turn/completed", { thread_id: thread.id, turn: { id: steer ? stream.turn_id : "dequeued-turn", status: "completed", completed_at: now } });
  await delay(300);
  await measure(win, "settled-idle", async () => {});
  win.destroy();
}).then(() => app.quit()).catch(error => { console.error(error); app.exit(1); });
app.on("quit", () => fs.rmSync(profile, { recursive: true, force: true }));
