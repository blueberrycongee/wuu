// Rendered check for the compacted process summary row.
//
// A compaction indicator shares the summary line with the activity text. A
// long summary must truncate before the indicator is pushed onto a second
// line: the row stays one line tall, the ring stays beside the text, and the
// disclosure chevron stays inside the row.
// The composer hint and aggregated activity summary must also share the same
// quiet text color before and after the turn finishes, across both themes.
// Rendered measurements and screenshots go to out/e2e/process-compaction-summary.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "streaming-e2e-preload.cjs");
const evidenceDir = path.resolve(process.env.WUU_PROCESS_SUMMARY_OUTPUT || path.join(desktopRoot, "out", "e2e", "process-compaction-summary"));
app.setPath("userData", fs.mkdtempSync(path.join(desktopRoot, "out", "process-summary-profile-")));

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
  const colors = [];
  await checkQuietText(win, "welcome", colors);
  win.setContentSize(1080, 900);
  await evaluate(win, () => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.style.setProperty("--conversation-message-font-size", "15px");
  });
  const threadID = await startActiveThread(win);
  // The synthetic turn below bypasses normal submission cleanup.
  await evaluate(win, () => {
    const input = document.querySelector("[data-main-conversation-composer] textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
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

  // Keep a long command active after a mixed history so both the live call
  // and the settled aggregate exercise truncation beside the compaction ring.
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
      id: "latest-process-compaction-summary-e2e",
      type: "tool_call",
      status: "in_progress",
      name: "bash",
      arguments: JSON.stringify({ command: "pnpm typecheck --filter desktop --filter plugin-sdk --filter protocol --filter remote-control --filter native-clients" }),
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
    if (item.status === "completed") emitNotification(win, "item/completed", { thread_id: threadID, turn_id: turnID, item });
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
  await checkQuietText(win, "running", colors);
  emitNotification(win, "turn/completed", {
    thread_id: threadID,
    turn: {
      id: turnID,
      items: [userItem, ...items.map(item => ({ ...item, status: "completed" })), {
        ...answerItem, status: "completed", terminal: true,
        text: "The activity summary and composer hint keep the same quiet color.",
      }],
      items_view: "full",
      status: "completed",
      started_at: new Date().toISOString(),
      completed_at: new Date().toISOString(),
    },
  });
  await waitFor(win, () => !!document.querySelector('.turn-answer-body [data-stream-state="settled"]'), 10000);
  // Completion folds process details. Reopen them so the comparison and its
  // evidence exercise visible summary text rather than a hidden DOM subtree.
  await evaluate(win, () => {
    const toggle = document.querySelector('[data-turn-id="turn-process-compaction-summary-e2e"] .turn-process-toggle');
    if (toggle.getAttribute("aria-expanded") !== "true") toggle.click();
  });
  await checkQuietText(win, "completed", colors);
  console.log("process compaction summary e2e passed");
  win.destroy();
  app.exit(0);
}

async function checkQuietText(win, state, report) {
  if (state !== "welcome") {
    await waitFor(win, () => document.querySelector('.process-surface-summary-text')
      ?.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }), 5000);
    await waitFor(win, () => !document.querySelector('.process-surface [data-transitioning="true"]'), 5000);
  }
  for (const [width, fontSize] of [[1080, 15], [600, 20]]) {
    win.setContentSize(width, 900);
    for (const theme of ["light", "dark"]) {
      const colors = await evaluate(win, async (theme, fontSize) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${fontSize}px`);
        await document.fonts.ready;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        const input = document.querySelector('[data-main-conversation-composer] textarea');
        const summary = document.querySelector('.process-surface-summary-text');
        const placeholder = getComputedStyle(input, "::placeholder");
        return {
          placeholder: placeholder.color,
          placeholderOpacity: placeholder.opacity,
          enteredText: getComputedStyle(input).color,
          summary: summary ? getComputedStyle(summary).color : null,
          summaryOpacity: summary ? getComputedStyle(summary).opacity : null,
          empty: input.value === "" && input.placeholder.length > 0,
        };
      }, theme, fontSize);
      const label = `${state}/${theme}/${width}/${fontSize}`;
      report.push({ state, theme, width, fontSize, ...colors });
      fs.writeFileSync(path.join(evidenceDir, "quiet-text.json"), JSON.stringify(report, null, 2));
      assert.ok(colors.empty, `${label}: the composer must show its placeholder`);
      assert.notEqual(colors.placeholder, colors.enteredText, `${label}: hints must remain quieter than entered text`);
      if (state !== "welcome") {
        assert.ok(colors.summary, `${label}: the aggregated tool summary must be rendered`);
        assert.equal(colors.placeholder, colors.summary, `${label}: composer hints and aggregated tools must share the same gray`);
        assert.equal(colors.placeholderOpacity, colors.summaryOpacity, `${label}: opacity must not make the matching colors diverge`);
        const welcome = report.find(entry => entry.state === "welcome" && entry.theme === theme && entry.width === width);
        assert.equal(colors.placeholder, welcome.placeholder, `${label}: welcome and conversation hints must match`);
        fs.writeFileSync(path.join(evidenceDir, `${state}-${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
      console.log(`PASS quiet text ${label}`);
    }
  }
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
  if (!text.textContent?.includes("pnpm typecheck")) {
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
