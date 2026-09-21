// Synthetic conversations only: never connects to an app-server or user profile.
// Run after `npm run build`: electron scripts/conversation-perf-e2e.cjs
// Timings are diagnostic, not hardware-dependent CI thresholds.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const root = path.resolve(__dirname, "..");
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-conversation-perf-"));
app.setPath("userData", profile);
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
  const win = new BrowserWindow({ width: 1380, height: 860, show: true, webPreferences: {
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  win.webContents.debugger.attach("1.3");
  if (process.env.WUU_PERF_URL) await win.loadURL(process.env.WUU_PERF_URL);
  else await win.loadFile(path.join(root, "out/renderer/index.html"));
  await waitFor(win, () => !!document.querySelector(".composer textarea"));
  await delay(500);
  await measure(win, "first-send", () => send(win));
  assert.ok(await evaluate(win, () => !!document.querySelector(".turn")), "Send must render a turn");
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
  emit(win, "turn/completed", { thread_id: thread.id, turn: { id: stream.turn_id, status: "completed", completed_at: now } });
  await delay(300);
  await measure(win, "settled-idle", async () => {});
  win.destroy();
}).then(() => app.quit()).catch(error => { console.error(error); app.exit(1); });
app.on("quit", () => fs.rmSync(profile, { recursive: true, force: true }));
