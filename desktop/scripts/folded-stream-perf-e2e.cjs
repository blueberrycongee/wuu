// Measures renderer work while reasoning streams inside a collapsed process
// fold, then checks that opening the fold shows the caught-up text and keeps
// following the live stream. Synthetic bridge only; no app-server or model.
//
//   WUU_FOLDED_STREAM_SCENARIO=group|reasoning npm run test:e2e:folded-stream
//
// Set WUU_FOLDED_STREAM_RENDERER_HTML to compare another renderer build.
// Writes out/folded-stream-perf/<scenario>-report.json plus collapsed/expanded
// screenshots. Timing is diagnostic; caught-up text and live-follow are asserted.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = process.env.WUU_FOLDED_STREAM_RENDERER_HTML || path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "streaming-e2e-preload.cjs");
const scenario = process.env.WUU_FOLDED_STREAM_SCENARIO || "group";
const output = path.resolve(process.env.WUU_FOLDED_STREAM_OUTPUT || path.join(desktopRoot, "out", "folded-stream-perf"));
const chunkCount = Number(process.env.WUU_FOLDED_STREAM_CHUNKS || 600);
const chunkIntervalMs = 16;
assert.ok(["group", "reasoning"].includes(scenario), `Unknown scenario ${scenario}`);

process.env.WUU_STREAM_E2E_CWD = repoRoot;
process.env.WUU_STREAM_E2E_LOCALE = "zh-CN";

const threadID = "thread-immediate-title-e2e";
const turnID = `turn-${threadID}`;
const reasoningID = "reasoning-folded-stream";
const foldSelector = scenario === "group" ? ".process-surface-fold.has-details" : ".turn-reasoning-fold";
const summarySelector = `${foldSelector} > summary`;
const textSelector = `${foldSelector} .reasoning-stream`;
const bodySelector = `${foldSelector} > ${scenario === "group" ? ".process-surface-body" : ".turn-reasoning-body"}`;

const timeout = setTimeout(() => fail(new Error("Folded stream probe timed out")), 180_000);
app.whenReady().then(run).catch(fail);

function chunk(index) {
  let text = `Step ${index}: checking **module-${index}** and \`fn${index}()\` against the plan. `;
  if (index % 8 === 7) text += "\n\n";
  if (index % 40 === 39) text += `- item ${index}\n- item ${index + 1}\n\n\`\`\`ts\nconst value${index} = ${index};\n\`\`\`\n\n`;
  return text;
}

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  const win = new BrowserWindow({
    width: 1100, height: 820, show: true,
    webPreferences: { contextIsolation: true, nodeIntegration: false, preload, sandbox: false, backgroundThrottling: false },
  });
  win.webContents.on("render-process-gone", (_event, details) => fail(new Error(`Renderer exited: ${details.reason}`)));
  await new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", resolve);
    win.loadFile(rendererHtml);
  });
  const cdp = win.webContents.debugger;
  cdp.attach("1.3");
  await cdp.sendCommand("Performance.enable", { timeDomain: "threadTicks" });

  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")), 8000);
  const sent = await waitFor(win, () => {
    const textarea = document.querySelector(".composer textarea");
    if (!(textarea instanceof HTMLTextAreaElement)) return false;
    textarea.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Investigate the folded stream.");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    const enter = new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true });
    textarea.dispatchEvent(enter);
    return enter.defaultPrevented;
  }, 3000);
  assert.equal(sent, true);

  const now = new Date().toISOString();
  const userItem = { id: `user-${threadID}`, type: "user_message", status: "completed", text: "Investigate the folded stream." };
  const toolItem = {
    id: "tool-folded-stream", type: "tool_call", status: "completed", name: "grep",
    arguments: '{"pattern":"stream","path":"desktop/src/renderer"}', result: "desktop/src/renderer/App.tsx: stream",
  };
  const reasoningItem = { id: reasoningID, type: "reasoning", status: "in_progress", text: "" };
  const turn = { id: turnID, items: [userItem], items_view: "full", status: "in_progress", started_at: now };
  emit(win, "thread/resumed", { thread: { id: threadID, preview: "Folded stream", model_provider: "e2e", model: "mock-stream",
    cwd: repoRoot, status: "in_progress", created_at: now, updated_at: now, turns: [] } });
  emit(win, "turn/started", { thread_id: threadID, turn });
  if (scenario === "group") emit(win, "item/completed", { thread_id: threadID, turn_id: turnID, item: toolItem });
  emit(win, "item/started", { thread_id: threadID, turn_id: turnID, item: reasoningItem });

  let streamed = "";
  let index = 0;
  const stream = async count => {
    for (let end = index + count; index < end; index++) {
      const delta = chunk(index);
      streamed += delta;
      emit(win, "item/reasoning/delta", { thread_id: threadID, turn_id: turnID, item_id: reasoningID, delta });
      await delay(chunkIntervalMs);
    }
  };
  await stream(20);
  await waitFor(win, new Function(`return Boolean(document.querySelector(${JSON.stringify(foldSelector)}))`), 5000);
  await delay(500);

  await evaluate(win, `(() => {
    const probe = window.__foldedProbe = { longTasks: 0, longTaskMs: 0, frames: [], mutations: 0, last: performance.now(), running: true };
    new PerformanceObserver(list => { for (const entry of list.getEntries()) { probe.longTasks++; probe.longTaskMs += entry.duration; } })
      .observe({ type: "longtask" });
    probe.targets = {};
    new MutationObserver(records => {
      if (!probe.running) return;
      probe.mutations += records.length;
      for (const record of records) {
        const node = record.target.nodeType === 1 ? record.target : record.target.parentElement;
        const key = (node?.className && typeof node.className === "string" ? node.className.split(" ")[0] : node?.tagName) + ":" + record.type;
        probe.targets[key] = (probe.targets[key] ?? 0) + 1;
      }
    })
      .observe(document.querySelector(${JSON.stringify(bodySelector)}), { subtree: true, childList: true, characterData: true, attributes: true });
    const tick = now => { if (!probe.running) return; probe.frames.push(now - probe.last); probe.last = now; requestAnimationFrame(tick); };
    requestAnimationFrame(tick);
  })()`);
  const before = await metrics(cdp);
  const started = Date.now();
  await stream(chunkCount);
  await delay(400);
  const wallMs = Date.now() - started;
  const after = await metrics(cdp);
  const probe = await evaluate(win, `(() => {
    const probe = window.__foldedProbe; probe.running = false;
    const frames = probe.frames.slice(1).sort((a, b) => a - b);
    const fold = document.querySelector(${JSON.stringify(foldSelector)});
    return { longTasks: probe.longTasks, longTaskMs: Math.round(probe.longTaskMs), mutations: probe.mutations,
      mutationTargets: Object.entries(probe.targets).sort((a, b) => b[1] - a[1]).slice(0, 6),
      frames: frames.length, p95FrameMs: Math.round(frames[Math.floor(frames.length * 0.95)] * 10) / 10,
      maxFrameMs: Math.round(frames.at(-1) * 10) / 10, slowFrames: frames.filter(value => value > 34).length,
      foldOpen: fold.open, foldHeight: Math.round(fold.getBoundingClientRect().height) };
  })()`);
  assert.equal(probe.foldOpen, false, "The process fold must stay collapsed while streaming.");
  fs.mkdirSync(output, { recursive: true });
  fs.writeFileSync(path.join(output, `${scenario}-collapsed.png`), (await win.webContents.capturePage()).toPNG());

  // Open the fold with a real click and record the first frames it paints.
  const lastMarker = `module-${index - 1}`;
  await evaluate(win, `(() => {
    const samples = window.__openSamples = [];
    let last = performance.now();
    const fold = document.querySelector(${JSON.stringify(foldSelector)});
    const sample = () => {
      const text = document.querySelector(${JSON.stringify(textSelector)})?.textContent ?? "";
      const now = performance.now();
      samples.push({ frameMs: Math.round(now - last), open: fold.open, hasLatest: text.includes(${JSON.stringify(lastMarker)}), textLength: text.length,
        height: Math.round(fold.getBoundingClientRect().height) });
      last = now;
      if (samples.length < 40) requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  })()`);
  const rect = await evaluate(win, `(() => { const r = document.querySelector(${JSON.stringify(summarySelector)}).getBoundingClientRect();
    return { x: Math.round(r.left + Math.min(80, r.width / 2)), y: Math.round(r.top + r.height / 2) }; })()`);
  win.webContents.sendInputEvent({ type: "mouseDown", x: rect.x, y: rect.y, button: "left", clickCount: 1 });
  win.webContents.sendInputEvent({ type: "mouseUp", x: rect.x, y: rect.y, button: "left", clickCount: 1 });
  await waitFor(win, () => window.__openSamples.length >= 40, 5000);
  const openSamples = await evaluate(win, "window.__openSamples");
  const firstOpen = openSamples.findIndex(sample => sample.open);
  assert.ok(firstOpen >= 0, "Clicking the summary should open the fold.");
  const openFrames = openSamples.slice(firstOpen);
  const staleOpenFrames = openFrames.filter(sample => !sample.hasLatest).length;
  assert.equal(staleOpenFrames, 0, "Every painted open frame must show the caught-up reasoning text.");

  await stream(30);
  const followMarker = `module-${index - 1}`;
  // Following means the latest text is rendered and its scroll container stays
  // pinned to the bottom.
  const followed = await waitFor(win, new Function(`const node = document.querySelector(${JSON.stringify(textSelector)});
    if (!node?.textContent?.includes(${JSON.stringify(followMarker)})) return false;
    for (let el = node.parentElement; el && !el.matches(${JSON.stringify(foldSelector)}); el = el.parentElement) {
      if (el.scrollHeight > el.clientHeight + 1 && /auto|scroll/.test(getComputedStyle(el).overflowY)) {
        return el.scrollHeight - el.clientHeight - el.scrollTop < 32;
      }
    }
    return true;`), 3000).catch(() => false);
  assert.equal(followed, true, "An open fold must keep following the live stream.");
  await delay(400);
  fs.writeFileSync(path.join(output, `${scenario}-expanded.png`), (await win.webContents.capturePage()).toPNG());

  const delta = key => Math.round(((after[key] ?? 0) - (before[key] ?? 0)) * (key.endsWith("Duration") ? 1000 : 1) * 10) / 10;
  const report = {
    scenario, rendererHtml, chunks: chunkCount, chunkIntervalMs, streamedCharacters: streamed.length, wallMs,
    collapsed: {
      taskMs: delta("TaskDuration"), scriptMs: delta("ScriptDuration"), layoutMs: delta("LayoutDuration"),
      recalcStyleMs: delta("RecalcStyleDuration"), layoutCount: delta("LayoutCount"), recalcStyleCount: delta("RecalcStyleCount"),
      ...probe,
    },
    open: { firstOpenFrame: firstOpen, staleOpenFrames, heights: openFrames.slice(0, 16).map(sample => sample.height),
      maxOpenFrameMs: Math.max(...openFrames.slice(0, 16).map(sample => sample.frameMs)), followed },
  };
  fs.writeFileSync(path.join(output, `${scenario}-report.json`), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report));
  clearTimeout(timeout);
  app.exit(0);
}

async function metrics(cdp) {
  const { metrics: list } = await cdp.sendCommand("Performance.getMetrics");
  return Object.fromEntries(list.map(metric => [metric.name, metric.value]));
}

function emit(win, method, params) {
  win.webContents.send("test:server-event", { workdir: repoRoot, kind: "notification", message: { method, params } });
}

async function waitFor(win, predicate, timeoutMs) {
  const started = Date.now();
  let last;
  while (Date.now() - started < timeoutMs) {
    last = await evaluate(win, `(${predicate.toString()})()`);
    if (last) return last;
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(last)}`);
}

function evaluate(win, source) {
  return win.webContents.executeJavaScript(source, true);
}

function delay(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
