// Rendered check for the compacted process summary row.
//
// A compaction indicator shares the summary line with the activity text. A
// long summary must truncate before the indicator is pushed onto a second
// line: the row stays one line tall, the ring stays beside the text, and the
// disclosure chevron stays inside the row.
// Screenshots are written to out/e2e/process-compaction-summary.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "streaming-e2e-preload.cjs");
const evidenceDir = path.join(desktopRoot, "out", "e2e", "process-compaction-summary");

process.env.WUU_STREAM_E2E_CWD = repoRoot;
app.commandLine.appendSwitch("disable-gpu");
app.commandLine.appendSwitch("disable-software-rasterizer");

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Streaming E2E preload is missing.");
  fs.mkdirSync(evidenceDir, { recursive: true });

  const win = new BrowserWindow({
    width: 1080,
    height: 900,
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false,
      backgroundThrottling: false,
    },
  });
  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });
  win.webContents.on("console-message", (event) => {
    if ((event.level ?? 0) >= 2) {
      console.error(`renderer console: ${event.message ?? String(event)}`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")), 5000);
  const threadID = await startActiveThread(win);
  const turnID = "turn-process-compaction-summary-e2e";
  const userItem = {
    id: "user-process-compaction-summary-e2e",
    type: "user_message",
    status: "completed",
    text: "Process compaction summary layout e2e seed.",
  };
  const answerItem = {
    id: "answer-process-compaction-summary-e2e",
    type: "agent_message",
    phase: "final_answer",
    status: "in_progress",
    text: "",
  };

  emitNotification(win, "turn/started", {
    thread_id: threadID,
    turn: {
      id: turnID,
      items: [userItem],
      items_view: "full",
      status: "in_progress",
      started_at: new Date().toISOString(),
    },
  });
  emitNotification(win, "item/started", {
    thread_id: threadID,
    turn_id: turnID,
    item: answerItem,
  });
  emitNotification(win, "item/agentMessage/delta", {
    thread_id: threadID,
    turn_id: turnID,
    item_id: answerItem.id,
    delta: "A long activity summary keeps its compaction ring on the same line.",
  });

  // Five commands plus a long extension name make the summary wider than the
  // available room once the compaction ring and chevron are accounted for.
  const items = [];
  for (let index = 0; index < 5; index += 1) {
    items.push({
      id: `bash-process-compaction-summary-e2e-${index}`,
      type: "tool_call",
      status: "completed",
      name: "bash",
      capability: "command.bash",
      arguments: JSON.stringify({ command: `echo compaction-summary-${index}` }),
    });
  }
  items.push(
    {
      id: "mcp-process-compaction-summary-e2e",
      type: "tool_call",
      status: "completed",
      name: "mcp_plugin_cua_mac_computer_computer_e4eeb575",
      arguments: "{}",
    },
    {
      id: "history-process-compaction-summary-e2e",
      type: "tool_call",
      status: "completed",
      name: "read_session_history",
      arguments: "{}",
    },
    {
      id: "compact-process-compaction-summary-e2e",
      type: "context_compaction",
      status: "completed",
      reason: "proactive",
      text: "Compacted history: 162 -> 88 messages (~240k -> ~123k tokens)",
    },
  );
  for (const item of items) {
    emitNotification(win, "item/started", { thread_id: threadID, turn_id: turnID, item });
    emitNotification(win, "item/completed", { thread_id: threadID, turn_id: turnID, item });
  }

  const geometry = await waitFor(win, compactionSummaryGeometry, 5000);
  console.log(
    `row ${geometry.row.width.toFixed(1)}x${geometry.row.height.toFixed(1)}; ` +
      `text ${geometry.text.width.toFixed(1)}x${geometry.text.height.toFixed(1)}; ` +
      `ring top ${geometry.ring.top.toFixed(1)}; text top ${geometry.text.top.toFixed(1)}; ` +
      `chevron right ${geometry.chevron.right.toFixed(1)}; row right ${geometry.row.right.toFixed(1)}`,
  );

  assert.ok(
    geometry.row.height <= 32,
    `A long summary plus a compaction ring must stay one line tall. Row height=${geometry.row.height}`,
  );
  assert.ok(
    geometry.text.width > 0,
    "The summary text should keep visible width beside the compaction ring.",
  );
  assert.ok(
    Math.abs(geometry.ring.top - geometry.text.top) <= 2,
    `The compaction ring left the summary line. Ring top=${geometry.ring.top} text top=${geometry.text.top}`,
  );
  assert.ok(
    geometry.ring.bottom <= geometry.row.bottom + 1,
    `The compaction ring overflows the summary row. Ring bottom=${geometry.ring.bottom} row bottom=${geometry.row.bottom}`,
  );
  assert.ok(
    geometry.chevron.right <= geometry.row.right + 1,
    `The disclosure chevron is pushed outside the summary row. Chevron right=${geometry.chevron.right} row right=${geometry.row.right}`,
  );

  // The mock submission skips the placement cleanup that normally reveals
  // the new turn in the desktop shell. Drop its transient marker so the
  // evidence screenshot shows the row this check measures.
  await evaluate(win, () => {
    document.querySelector(".scroll-region-content")?.removeAttribute("data-submit-placing");
  });
  await delay(200);
  const image = await win.webContents.capturePage();
  fs.writeFileSync(path.join(evidenceDir, "compaction-summary.png"), image.toPNG());
  console.log("process compaction summary e2e passed");
  win.destroy();
  app.exit(0);
}

function compactionSummaryGeometry() {
  const row = document.querySelector(".process-surface-row");
  const line = row?.querySelector(".process-surface-summary-line");
  const text = line?.querySelector(".process-surface-summary-text");
  const ring = line?.querySelector(".context-compaction-indicator");
  const chevron = row?.querySelector(".process-surface-chevron");
  if (!(row instanceof HTMLElement) || !(line instanceof HTMLElement) || !(text instanceof HTMLElement) ||
      !(ring instanceof HTMLElement) || !(chevron instanceof SVGElement)) {
    return null;
  }
  if (!text.textContent?.includes("mcp_plugin_cua_mac_computer_computer_e4eeb575")) {
    return null;
  }
  const rect = (element) => {
    const box = element.getBoundingClientRect();
    return {
      left: box.left,
      top: box.top,
      right: box.right,
      bottom: box.bottom,
      width: box.width,
      height: box.height,
    };
  };
  return {
    row: rect(row),
    line: rect(line),
    text: rect(text),
    ring: rect(ring),
    chevron: rect(chevron),
  };
}

async function startActiveThread(win) {
  const started = await waitFor(
    win,
    () => {
      const textarea = document.querySelector(".composer textarea");
      if (!(textarea instanceof HTMLTextAreaElement)) return false;
      textarea.focus();
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      valueSetter?.call(textarea, "Process compaction summary layout e2e seed.");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      const enter = new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true });
      textarea.dispatchEvent(enter);
      return enter.defaultPrevented;
    },
    3000,
  );
  assert.equal(started, true, "Composer should create the e2e thread.");
  await waitFor(
    win,
    () => (document.querySelector(".conversation-width")?.textContent ?? "").includes("Process compaction summary layout e2e seed."),
    3000,
  );
  return "thread-immediate-title-e2e";
}

function emitNotification(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD || process.cwd(),
    kind: "notification",
    message: { method, params },
  });
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", () => resolve());
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate);
    if (lastValue) return lastValue;
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

async function evaluate(win, fn, ...args) {
  return win.webContents.executeJavaScript(`(${fn.toString()})(${args.map((value) => JSON.stringify(value)).join(",")})`, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
