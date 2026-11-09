// Synthetic long session only: never connects to an app-server or user profile.
// Run after `npm run build`: electron scripts/long-session-stream-perf-e2e.cjs
// Streams reasoning, tool output and an answer into the open conversation for
// WUU_PERF_ROUNDS turns while WUU_PERF_BACKGROUND other sessions stream, and
// records per-turn renderer cost so growth over a session is visible.
// Timings are diagnostic, not hardware-dependent CI thresholds. Results and the
// first/last turn CPU profiles are written to WUU_PERF_OUTPUT.
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const root = path.resolve(__dirname, "..");
const rounds = Number(process.env.WUU_PERF_ROUNDS || 30);
const backgroundCount = Number(process.env.WUU_PERF_BACKGROUND || 2);
const output = process.env.WUU_PERF_OUTPUT || path.join(os.tmpdir(), "wuu-long-session-perf");
process.env.WUU_STREAM_E2E_SIDEBAR_THREADS ||= "140";
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-long-session-perf-"));
app.setPath("userData", profile);
setTimeout(() => { console.error("Long-session profiling exceeded its time budget"); app.exit(1); }, 30000 + rounds * 6000).unref();
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

async function metrics(cdp) {
  const { metrics: values } = await cdp.sendCommand("Performance.getMetrics");
  return Object.fromEntries(values.map(metric => [metric.name, metric.value]));
}

function hottest(cpu) {
  const nodes = new Map(cpu.nodes.map(node => [node.id, node]));
  const self = new Map();
  cpu.samples.forEach((id, index) => {
    const frame = nodes.get(id).callFrame;
    const key = `${frame.functionName || "(anonymous)"} ${path.basename(frame.url)}:${frame.lineNumber + 1}`;
    self.set(key, (self.get(key) ?? 0) + (cpu.timeDeltas[index] ?? 0) / 1000);
  });
  return [...self].filter(([key]) => !key.startsWith("(idle)") && !key.startsWith("(program)"))
    .sort((a, b) => b[1] - a[1]).slice(0, 12).map(([key, ms]) => [key, Math.round(ms)]);
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1380, height: 860, show: true, webPreferences: {
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  const cdp = win.webContents.debugger;
  cdp.attach("1.3");
  await win.loadFile(path.join(root, "out/renderer/index.html"));
  await cdp.sendCommand("Performance.enable");
  await cdp.sendCommand("Profiler.enable");
  await waitFor(win, () => !!document.querySelector(".composer textarea"));
  await delay(500);
  await evaluate(win, () => {
    const textarea = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Start a long session.");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true }));
  });
  await waitFor(win, () => !!document.querySelector(".turn"));

  const threadID = "thread-immediate-title-e2e";
  const now = new Date().toISOString();
  emit(win, "turn/completed", { thread_id: threadID, turn: { id: `turn-${threadID}`, status: "completed", items_view: "full", items: [], completed_at: now } });
  const answer = ("## Response\n\nA paragraph with **formatting** and enough text to wrap in the conversation viewport.\n\n```ts\nconst answer = 42;\n```\n\n").repeat(5);
  const thread = { id: threadID, preview: "Long session", model_provider: "e2e", model: "mock-stream", cwd: process.cwd(), status: "idle", created_at: now, updated_at: now,
    turns: Array.from({ length: 120 }, (_, i) => ({ id: `history-${i}`, status: "completed", items_view: "full", started_at: now, completed_at: now, items: [
      { id: `history-user-${i}`, type: "user_message", status: "completed", text: `Question ${i}` },
      { id: `history-reasoning-${i}`, type: "reasoning", status: "completed", text: "Thinking about the request. ".repeat(20) },
      { id: `history-tool-${i}`, type: "tool_call", status: "completed", name: "bash", arguments: '{"command":"go test ./..."}', result: "ok  package\n".repeat(40) },
      { id: `history-answer-${i}`, type: "agent_message", status: "completed", text: answer },
    ] })) };
  const backgroundIDs = Array.from({ length: backgroundCount }, (_, i) => `sidebar-fade-${i}`);
  for (const id of backgroundIDs) {
    emit(win, "thread/resumed", { thread: { id, preview: id, model_provider: "e2e", model: "mock-stream", cwd: process.cwd(), status: "idle", created_at: now, updated_at: now, turns: [] } });
  }
  emit(win, "thread/resumed", { thread });
  await waitFor(win, () => document.querySelectorAll(".turn").length > 3);
  await delay(1000);

  fs.mkdirSync(output, { recursive: true });
  const results = [];
  for (let round = 0; round < rounds; round++) {
    const turnID = `long-turn-${round}`;
    const user = { id: `long-user-${round}`, type: "user_message", status: "completed", text: `Round ${round}` };
    const reasoning = { id: `long-reasoning-${round}`, type: "reasoning", status: "in_progress", text: "" };
    const tool = { id: `long-tool-${round}`, type: "tool_call", status: "in_progress", name: "bash", arguments: '{"command":"go test ./..."}', result: "" };
    const reply = { id: `long-answer-${round}`, type: "agent_message", status: "in_progress", text: "" };
    for (const id of backgroundIDs) {
      emit(win, "turn/started", { thread_id: id, turn: { id: `${id}-turn-${round}`, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [] } });
      emit(win, "item/started", { thread_id: id, turn_id: `${id}-turn-${round}`, item: { id: `${id}-answer-${round}`, type: "agent_message", status: "in_progress", text: "" } });
    }
    const profiled = round === 1 || round === rounds - 1;
    if (profiled) await cdp.sendCommand("Profiler.start");
    const before = await metrics(cdp);
    const started = Date.now();
    await evaluate(win, () => {
      const probe = window.__longSessionProbe = { frames: [], last: performance.now(), running: true };
      const frame = time => { if (!probe.running) return; probe.frames.push(time - probe.last); probe.last = time; requestAnimationFrame(frame); };
      requestAnimationFrame(frame);
    });
    emit(win, "turn/started", { thread_id: threadID, turn: { id: turnID, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [user] } });
    const stream = async (item, method, count, text) => {
      emit(win, "item/started", { thread_id: threadID, turn_id: turnID, item });
      let value = "";
      for (let i = 0; i < count; i++) {
        const delta = text(i);
        value += delta;
        emit(win, method, { thread_id: threadID, turn_id: turnID, item_id: item.id, delta });
        for (const id of backgroundIDs) emit(win, "item/agentMessage/delta", { thread_id: id, turn_id: `${id}-turn-${round}`, item_id: `${id}-answer-${round}`, delta: `background ${i}. ` });
        await delay(16);
      }
      return value;
    };
    const reasoningText = await stream(reasoning, "item/reasoning/delta", 30, i => `Reasoning step ${i} about round ${round}. `);
    emit(win, "item/completed", { thread_id: threadID, turn_id: turnID, item: { ...reasoning, status: "completed", text: reasoningText } });
    const toolText = await stream(tool, "item/toolCall/outputDelta", 30, i => `ok  github.com/example/pkg${i}  0.${i}s\n`);
    emit(win, "item/completed", { thread_id: threadID, turn_id: turnID, item: { ...tool, status: "completed", result: toolText } });
    const replyText = await stream(reply, "item/agentMessage/delta", 60, i => `Streaming paragraph ${i} with **formatted text** and a little more content.\n\n`);
    emit(win, "turn/completed", { thread_id: threadID, turn: { id: turnID, status: "completed", items_view: "full", completed_at: new Date().toISOString(), items: [
      user, { ...reasoning, status: "completed", text: reasoningText }, { ...tool, status: "completed", result: toolText }, { ...reply, status: "completed", text: replyText },
    ] } });
    for (const id of backgroundIDs) emit(win, "turn/completed", { thread_id: id, turn: { id: `${id}-turn-${round}`, status: "completed", items_view: "full", completed_at: new Date().toISOString(), items: [
      { id: `${id}-answer-${round}`, type: "agent_message", status: "completed", text: "background done" },
    ] } });
    await delay(300);
    const probe = await evaluate(win, () => {
      const probe = window.__longSessionProbe;
      probe.running = false;
      const frames = probe.frames.slice(1).sort((a, b) => a - b);
      return { frames: frames.length, p95FrameMs: Math.round(frames[Math.floor(frames.length * 0.95)] ?? 0), slowFrames: frames.filter(value => value > 34).length,
        elements: document.getElementsByTagName("*").length };
    });
    const after = await metrics(cdp);
    const delta = name => +(after[name] - before[name]).toFixed(3);
    const row = { round, wallMs: Date.now() - started, scriptS: delta("ScriptDuration"), taskS: delta("TaskDuration"), styleS: delta("RecalcStyleDuration"),
      layoutS: delta("LayoutDuration"), heapMB: +(after.JSHeapUsedSize / 1048576).toFixed(1), ...probe };
    if (profiled) {
      const { profile: cpu } = await cdp.sendCommand("Profiler.stop");
      fs.writeFileSync(path.join(output, `round-${round}.cpuprofile`), JSON.stringify(cpu));
      row.hot = hottest(cpu);
    }
    results.push(row);
    console.log(JSON.stringify(row));
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log(JSON.stringify({ name: "long-session-stream", output }));
  win.destroy();
}).then(() => app.quit()).catch(error => { console.error(error); app.exit(1); });
app.on("quit", () => fs.rmSync(profile, { recursive: true, force: true }));
