const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

// Run after `npm run build`. Drives the real renderer with an isolated mock
// bridge and native input: wheel, scrollbar-free programmatic jumps, clicks
// and sidebar toggles. Checks what a reader sees — the reading position, the
// following state and painted pixels — while output streams.
// WUU_SCROLL_E2E_ONLY=inspection or history-jump selects one native regression.
const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_SCROLL_E2E_OUTPUT || path.join(desktop, "out/conversation-scroll");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_STREAM_E2E_CWD = path.dirname(desktop);

const THREAD_ID = "thread-immediate-title-e2e";
const SECOND_THREAD_ID = "thread-streaming-e2e";
const VIEWPORT = ".conversation-pane > .scroll-region";
// Paragraph and turn gaps stay well below this; a longer run of background
// rows inside a long conversation is an unpainted (blank) band.
const BLANK_RUN_LIMIT_PX = 160;
const now = new Date().toISOString();

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function evaluate(win, fn, ...args) {
  // Report the page-side error instead of Electron's generic script failure.
  const result = await win.webContents.executeJavaScript(`(async () => {
    try { return { value: await (${fn})(${args.map(value => JSON.stringify(value)).join(",")}) }; }
    catch (error) { return { error: String(error?.stack ?? error) }; }
  })()`, true);
  if ("error" in result) throw new Error(`${result.error}\nin: ${String(fn).slice(0, 200)}`);
  return result.value;
}
async function until(win, fn, message, timeout = 8000, ...args) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = await evaluate(win, fn, ...args);
    if (result) return result;
    await sleep(20);
  }
  throw new Error(`Timed out: ${message}`);
}
const frames = (win, count = 20) => evaluate(win, count => new Promise(resolve => {
  let remaining = count;
  const tick = () => --remaining ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}), count);
function emit(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD,
    kind: "notification",
    message: { method, params },
  });
}

const geometry = win => evaluate(win, selector => {
  const node = document.querySelector(selector);
  const max = node.scrollHeight - node.clientHeight;
  return { top: node.scrollTop, max, distance: max - node.scrollTop, height: node.scrollHeight, client: node.clientHeight };
}, VIEWPORT);

function paragraph(turn, index) {
  return `Paragraph ${index + 1} of turn ${turn}: the reading column wraps this sentence differently at every width, so a sidebar toggle changes the height of every rendered turn.`;
}
function answer(turn, count = 5 + (turn * 7) % 11) {
  const parts = [];
  for (let index = 0; index < count; index += 1) {
    parts.push(paragraph(turn, index));
    if (index % 5 === 2) {
      parts.push("```ts\n" + Array.from({ length: 6 }, (_, line) => `export const value${turn}_${line} = ${line} * ${turn};`).join("\n") + "\n```");
    }
  }
  return parts.join("\n\n");
}
function completedTurn(prefix, index) {
  return {
    id: `${prefix}-turn-${index}`, status: "completed", items_view: "full", started_at: now, completed_at: now, duration_ms: 1000,
    items: [
      { id: `${prefix}-user-${index}`, seq: index * 2 + 1, type: "user_message", status: "completed", text: `Question ${index + 1}: explain part ${index + 1} of the system.` },
      { id: `${prefix}-agent-${index}`, seq: index * 2 + 2, type: "agent_message", status: "completed", text: answer(index) },
    ],
  };
}
function liveTurn(id, text = "Starting the answer.") {
  return {
    id, status: "in_progress", items_view: "full", started_at: now,
    items: [
      { id: `${id}-user`, type: "user_message", status: "completed", text: "Keep answering while I read." },
      { id: `${id}-agent`, type: "agent_message", status: "in_progress", text },
    ],
  };
}
function thread(id, turns, running) {
  return {
    id, preview: "Conversation scroll", model_provider: "e2e", model: "mock-stream",
    cwd: path.dirname(desktop), status: running ? "running" : "idle", created_at: now, updated_at: now, turns,
  };
}

/** Streams a steady answer; returns the stop function. */
function startStream(win, threadID, turnID, itemID, interval = 30) {
  let index = 0;
  let stopped = false;
  (async () => {
    while (!stopped) {
      index += 1;
      const delta = index % 10 === 0 ? `\n\nStreamed paragraph ${index}: ` : `word${index} alpha beta gamma delta `;
      emit(win, "item/agentMessage/delta", { thread_id: threadID, turn_id: turnID, item_id: itemID, delta });
      await sleep(interval);
    }
  })();
  return () => { stopped = true; };
}

async function wheel(win, deltaY, count, gap = 16) {
  const bounds = await evaluate(win, selector => {
    const rect = document.querySelector(selector).getBoundingClientRect();
    return { x: Math.round(rect.left + rect.width * 0.6), y: Math.round(rect.top + rect.height * 0.4) };
  }, VIEWPORT);
  for (let index = 0; index < count; index += 1) {
    // Electron's positive deltaY scrolls toward the top, like a wheel moved up.
    win.webContents.sendInputEvent({ type: "mouseWheel", ...bounds, deltaX: 0, deltaY, canScroll: true });
    await sleep(gap);
  }
}

/**
 * Samples the distance from latest after each painted frame while output
 * streams. A chunk can land between a paint and the next follow correction,
 * so a followed viewport is judged over time rather than by one read.
 */
const followSamples = (win, duration) => evaluate(win, (selector, duration) => new Promise(resolve => {
  const node = document.querySelector(selector);
  const started = performance.now();
  const initialHeight = node.scrollHeight;
  const samples = [];
  const sample = () => requestAnimationFrame(() => setTimeout(() => {
    samples.push(node.scrollHeight - node.clientHeight - node.scrollTop);
    if (performance.now() - started < duration) sample();
    else resolve({
      atLatest: samples.filter(distance => distance <= 2).length / samples.length,
      worst: Math.round(Math.max(...samples)),
      grew: node.scrollHeight - initialHeight,
    });
  }));
  sample();
}), VIEWPORT, duration);
function assertFollowing(follow, message) {
  assert.ok(follow.grew > 200 && follow.atLatest >= 0.75 && follow.worst <= 120, `${message}: ${JSON.stringify(follow)}`);
}

/** Waits until wheel or glide motion has come to rest. */
async function settleScroll(win) {
  let previous;
  for (let attempt = 0; attempt < 60; attempt += 1) {
    await sleep(60);
    const { top } = await geometry(win);
    if (previous !== undefined && Math.abs(top - previous) < 0.5) return;
    previous = top;
  }
  throw new Error("Scrolling did not settle");
}

async function toggleSidebar(win) {
  const toggled = await evaluate(win, () => {
    const button = document.querySelector('[data-wuu-component="sidebar-toggle"], .sidebar-collapse-toggle');
    button?.click();
    return Boolean(button);
  });
  assert.ok(toggled, "A sidebar toggle must be available");
  await until(win, () => !document.querySelector(".app-shell").classList.contains("sidebar-animating") &&
    !document.documentElement.classList.contains("layout-motion-active"), "sidebar motion to finish");
  await frames(win, 6);
}

/**
 * Captures the viewport repeatedly while `action` runs and reports the longest
 * vertical run of background-only rows, in CSS pixels.
 */
async function longestBlankRun(win, action, duration) {
  const rect = await evaluate(win, selector => {
    const node = document.querySelector(selector);
    const bounds = node.getBoundingClientRect();
    return { x: Math.round(bounds.left), y: Math.round(bounds.top), width: node.clientWidth, height: node.clientHeight };
  }, VIEWPORT);
  const started = Date.now();
  const pending = action();
  let worst = 0;
  let worstImage;
  const heights = new Set();
  while (Date.now() - started < duration) {
    const image = await win.webContents.capturePage(rect);
    heights.add((await geometry(win)).height);
    const { width, height } = image.getSize();
    const bitmap = image.toBitmap();
    const background = [bitmap[0], bitmap[1], bitmap[2]];
    let run = 0;
    let longest = 0;
    for (let y = 0; y < height; y += 1) {
      let blank = true;
      for (let x = 0; x < width; x += 2) {
        const offset = (y * width + x) * 4;
        if (Math.abs(bitmap[offset] - background[0]) + Math.abs(bitmap[offset + 1] - background[1]) +
          Math.abs(bitmap[offset + 2] - background[2]) > 24) {
          blank = false;
          break;
        }
      }
      run = blank ? run + 1 : 0;
      longest = Math.max(longest, run);
    }
    const css = longest * rect.height / height;
    if (css > worst) {
      worst = css;
      worstImage = image;
    }
  }
  await pending;
  return { worst: Math.round(worst), worstImage, heights: [...heights] };
}

function record(results, name, details) {
  results.push({ name, ...details });
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(results, null, 2));
  console.log(`PASS ${name}`);
}

async function typeAndSend(win, text) {
  await evaluate(win, text => {
    const input = document.querySelector(".composer textarea");
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, text);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }, text);
  await frames(win, 3);
  await evaluate(win, () => document.querySelector(".composer-send-button").click());
}

/**
 * Identifies the text block under a point near the top of the viewport by its
 * (unique, fixture-generated) text; a revealed pane may re-render that node.
 */
const readingPoint = win => evaluate(win, selector => {
  const node = document.querySelector(selector);
  const bounds = node.getBoundingClientRect();
  // Below the top edge, the first probe that lands in a text block: the spacing between
  // blocks and a fold's header are not text.
  let probe;
  let block = null;
  for (const offset of [40, 80, 120, 160, 200]) {
    probe = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + offset);
    block = probe?.closest("p, pre, [data-user-message-id]");
    if (block) break;
  }
  if (!block) throw new Error(`No text block at the reading point: ${probe?.outerHTML.slice(0, 120)}`);
  return { text: block.textContent, top: block.getBoundingClientRect().top - bounds.top };
}, VIEWPORT);
const readingPointTop = (win, point) => evaluate(win, (selector, text) => {
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const block = [...pane.querySelectorAll("p, pre, [data-user-message-id]")].find(candidate => candidate.textContent === text);
  if (!block) throw new Error(`Reading point is gone: ${text.slice(0, 60)}`);
  return block.getBoundingClientRect().top - document.querySelector(selector).getBoundingClientRect().top;
}, VIEWPORT, point.text);

/** Samples a message's top edge relative to the viewport after every frame. */
function trackMessage(win, messageID) {
  return evaluate(win, (selector, messageID) => {
    const node = document.querySelector(selector);
    window.__messageTrack = [];
    window.__messageTrackOn = true;
    const loop = () => requestAnimationFrame(() => setTimeout(() => {
      if (!window.__messageTrackOn) return;
      const message = document.querySelector(`[data-user-message-id="${messageID}"]`);
      if (message) {
        window.__messageTrack.push(message.getBoundingClientRect().top - node.getBoundingClientRect().top);
      }
      loop();
    }));
    loop();
  }, VIEWPORT, messageID);
}
const stopTracking = win => evaluate(win, () => {
  window.__messageTrackOn = false;
  return window.__messageTrack;
});

async function inspectProcess(win, results, turnID) {
  // --- Inspection preserves its own reading position, including native details layout.
  const processItems = [];
  for (let index = 0; index < 24; index++) {
    processItems.push({
      id: `inspection-tool-${index}`, type: "tool_call", status: "completed", name: "read_file",
      arguments: JSON.stringify({ path: `inspection-file-${index}.ts` }),
    });
    if (index === 10) processItems.push({
      id: "inspection-reason-middle", type: "reasoning", status: "completed",
      text: "Middle inspection reasoning.\n\n" + "An earlier reasoning paragraph. ".repeat(80),
    });
  }
  const liveReasoning = {
    id: "inspection-reason-live", type: "reasoning", status: "in_progress",
    text: "Current inspection reasoning.\n\n" + "Latest reasoning remains readable while inspecting earlier tools. ".repeat(60),
  };
  processItems.push(liveReasoning);
  for (const item of processItems) emit(win, "item/started", { thread_id: THREAD_ID, turn_id: turnID, item });
  const foldSelector = `[data-turn-id="${turnID}"] details.process-surface-fold`;
  await until(win, selector => document.querySelector(selector)?.textContent.includes("Current inspection reasoning."), "inspection records", 8000, foldSelector);
  await frames(win, 12);
  const processGeometry = () => evaluate(win, (selector, viewportSelector) => {
    const fold = document.querySelector(selector);
    const body = fold.querySelector(".process-surface-body");
    const bounds = fold.querySelector("summary").getBoundingClientRect();
    const viewport = document.querySelector(viewportSelector).getBoundingClientRect();
    return { open: fold.open, top: body.scrollTop, max: body.scrollHeight - body.clientHeight,
      summaryTop: bounds.top - viewport.top, anchor: body.style.overflowAnchor,
      width: body.clientWidth, height: body.clientHeight,
      fontSize: getComputedStyle(body.querySelector(".activity-row")).fontSize };
  }, foldSelector, VIEWPORT);
  const toggleProcess = async () => {
    const bounds = await evaluate(win, selector => {
      const rect = document.querySelector(selector).querySelector("summary").getBoundingClientRect();
      return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
    }, foldSelector);
    win.webContents.sendInputEvent({ type: "mouseDown", ...bounds, button: "left", clickCount: 1 });
    win.webContents.sendInputEvent({ type: "mouseUp", ...bounds, button: "left", clickCount: 1 });
    await frames(win, 20);
  };
  const order = await evaluate(win, selector => {
    const text = document.querySelector(selector).querySelector(".process-surface-body").textContent;
    return [text.indexOf("inspection-file-10.ts"), text.indexOf("Middle inspection reasoning."), text.indexOf("inspection-file-11.ts")];
  }, foldSelector);
  assert.ok(order[0] >= 0 && order[0] < order[1] && order[1] < order[2], `Inspection order differs from events: ${order}`);

  const inspectionCases = [
    { theme: "light", size: 14, width: 1240, reduced: false },
    { theme: "dark", size: 14, width: 1240, reduced: false },
    { theme: "light", size: 20, width: 720, reduced: false },
    { theme: "dark", size: 20, width: 720, reduced: true },
  ];
  for (const [index, appearance] of inspectionCases.entries()) {
    win.setSize(appearance.width, 920);
    await evaluate(win, appearance => {
      document.querySelector(".environment-panel-close-row button")?.click();
      const shell = document.querySelector(".app-shell");
      if (shell.classList.contains("right-panel-open")) document.querySelector(".workspace-panel-close")?.click();
      if (!shell.classList.contains("sidebar-collapsed") || shell.classList.contains("sidebar-drawer-open")) {
        document.querySelector('[data-wuu-component="sidebar-toggle"], .sidebar-collapse-toggle')?.click();
      }
      document.documentElement.dataset.theme = appearance.theme;
      document.documentElement.dataset.appearanceMotion = appearance.reduced ? "reduce" : "system";
      document.documentElement.style.setProperty("--conversation-message-font-size", `${appearance.size}px`);
      window.dispatchEvent(new Event("wuu-content-size-change"));
    }, appearance);
    await until(win, () => !document.querySelector(".app-shell").classList.contains("sidebar-animating") &&
      !document.documentElement.classList.contains("layout-motion-active"), "inspection layout to settle");
    await frames(win, 20);
    const beforeOpen = await processGeometry();
    await toggleProcess();
    const opened = await processGeometry();
    assert.ok(opened.open && opened.max > 200, `The inspection fixture must overflow: ${JSON.stringify(opened)}`);
    if (index === 0) assert.ok(opened.max - opened.top <= 1, `First live inspection must start at latest: ${JSON.stringify(opened)}`);
    assert.ok(Math.abs(opened.summaryTop - beforeOpen.summaryTop) <= 2, `Opening moved the inspection summary: ${JSON.stringify({ beforeOpen, opened })}`);
    if (index === 0) {
      emit(win, "item/reasoning/delta", { thread_id: THREAD_ID, turn_id: turnID, item_id: liveReasoning.id, delta: "\n\nNew reasoning while following latest. ".repeat(30) });
      await frames(win, 20);
      const followed = await processGeometry();
      assert.ok(followed.max > opened.max && followed.max - followed.top <= 1, `Live inspection must follow appended output: ${JSON.stringify({ opened, followed })}`);
    }
    const readingTop = await evaluate(win, selector => {
      const body = document.querySelector(selector).querySelector(".process-surface-body");
      body.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, bubbles: true }));
      body.scrollTop = Math.min(180, (body.scrollHeight - body.clientHeight) / 2);
      return body.scrollTop;
    }, foldSelector);
    await frames(win, 3);
    emit(win, "item/reasoning/delta", { thread_id: THREAD_ID, turn_id: turnID, item_id: liveReasoning.id, delta: "\n\nNew reasoning below the reading point. ".repeat(30) });
    await frames(win, 20);
    const grown = await processGeometry();
    assert.ok(Math.abs(grown.top - readingTop) <= 1 && grown.anchor === "auto", `Streaming stole inspection reading: ${JSON.stringify({ readingTop, grown })}`);
    await toggleProcess();
    const closed = await processGeometry();
    assert.ok(!closed.open, "Inspection must close through a native click");
    emit(win, "item/reasoning/delta", { thread_id: THREAD_ID, turn_id: turnID, item_id: liveReasoning.id, delta: "\n\nHidden reasoning growth. ".repeat(30) });
    await frames(win, 12);
    await toggleProcess();
    const reopened = await processGeometry();
    assert.ok(Math.abs(reopened.top - readingTop) <= 1 && reopened.anchor === "auto", `Reopening lost inspection reading: ${JSON.stringify({ readingTop, reopened })}`);
    assert.ok(Math.abs(reopened.summaryTop - closed.summaryTop) <= 2, `Reopening moved the inspection summary: ${JSON.stringify({ closed, reopened })}`);
    fs.writeFileSync(path.join(output, `inspection-${index}.png`), (await win.webContents.capturePage()).toPNG());
    record(results, "native inspection preserves event order and reading position", { appearance, order, readingTop, opened, grown, closed, reopened });
    await toggleProcess();
  }
  win.setSize(1240, 820);
  await evaluate(win, () => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.dataset.appearanceMotion = "system";
    document.documentElement.style.removeProperty("--conversation-message-font-size");
    window.dispatchEvent(new Event("wuu-content-size-change"));
  });
  await frames(win, 12);
  emit(win, "item/started", { thread_id: THREAD_ID, turn_id: turnID, item: {
    id: "inspection-history-boundary", type: "agent_message", phase: "commentary", status: "completed", text: "Completed inspection history follows.",
  } });
  for (const item of processItems) emit(win, "item/started", { thread_id: THREAD_ID, turn_id: turnID, item: {
    ...item, id: `history-${item.id}`, status: "completed",
  } });
  await until(win, selector => document.querySelectorAll(selector).length === 2, "completed inspection history", 8000, foldSelector);
  await evaluate(win, selector => document.querySelectorAll(selector)[1].querySelector("summary").click(), foldSelector);
  await frames(win, 20);
  const history = await evaluate(win, selector => {
    const fold = document.querySelectorAll(selector)[1];
    const body = fold.querySelector(".process-surface-body");
    return { open: fold.open, top: body.scrollTop, max: body.scrollHeight - body.clientHeight, anchor: body.style.overflowAnchor };
  }, foldSelector);
  assert.ok(history.open && history.max > 200 && history.top === 0 && history.anchor === "auto", `Completed inspection must start from the beginning: ${JSON.stringify(history)}`);
  record(results, "completed inspection starts from the beginning", { history });
}

async function verifyHistoryJump(win, results) {
  // Search remains a visible, keyboard-accessible entry point after removing
  // the message navigation rails. Keep the paint and exact landing contract.
  await wheel(win, -400, 30);
  await settleScroll(win);
  const modifiers = [process.platform === "darwin" ? "meta" : "control"];
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "P", modifiers });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "P", modifiers });
  await until(win, () => Boolean(document.querySelector(".conversation-search-dialog input")), "conversation search");
  await win.webContents.insertText("Question 3:");
  await until(win, () => {
    const result = document.querySelector(".conversation-search-result");
    return result?.textContent.includes("Question 3:") &&
      document.querySelector('.conversation-search-results[aria-busy="false"]');
  }, "the addressed history search result");
  const historyJump = await longestBlankRun(win, () => {
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Enter" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Enter" });
  }, 900);
  await settleScroll(win);
  const landed = await evaluate(win, selector => {
    const node = document.querySelector(selector);
    const message = document.querySelector('[data-user-message-id="history-user-2"]');
    if (!message?.textContent.includes("Question 3:")) throw new Error("Addressed history search target is missing");
    return message.getBoundingClientRect().top - node.getBoundingClientRect().top;
  }, VIEWPORT);
  assert.ok(historyJump.worst <= BLANK_RUN_LIMIT_PX, `Jumping to Question 3 painted a ${historyJump.worst}px blank band`);
  assert.ok(landed >= 0 && landed <= 120, `The jump must land on the selected message: ${landed}`);
  record(results, "jumping to a past query paints every frame and lands on it", { blank: historyJump.worst, landed });
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width: 1240, height: 820, show: process.env.WUU_E2E_HIDDEN !== "true",
    webPreferences: {
      preload: path.join(__dirname, "conversation-scroll-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  // Keyboard-driven UI (conversation search) must not depend on whether this
  // window is the one the OS focused while the suite runs.
  win.webContents.debugger.attach("1.3");
  win.webContents.on("did-finish-load", () => void win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true }));
  const errors = [];
  win.webContents.on("console-message", event => {
    if (event.level !== "error") return;
    errors.push(event.message);
    console.error(`renderer error: ${event.message}`);
  });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(win, () => Boolean(document.querySelector(".composer textarea")), "composer");
  await typeAndSend(win, "Boot");
  // Let the first submission be accepted and placed before history arrives.
  await until(win, () => Boolean(document.querySelector('[data-user-message-id="sent-user-1"]')) &&
    !document.querySelector(".scroll-region-content[data-submit-placing]"), "the first submission to be placed");
  await frames(win, 10);
  const bootTurn = {
    id: "sent-turn-1", status: "completed", items_view: "full", started_at: now, completed_at: now,
    items: [
      { id: "sent-user-1", type: "user_message", status: "completed", text: "Boot" },
      { id: "sent-agent-1", type: "agent_message", status: "completed", text: "Ready." },
    ],
  };
  emit(win, "turn/completed", { thread_id: THREAD_ID, turn: bootTurn });
  const history = Array.from({ length: 36 }, (_, index) => completedTurn("history", index));
  const results = [];
  if (process.env.WUU_SCROLL_E2E_ONLY === "inspection") {
    await typeAndSend(win, "Inspect this process without losing my reading position.");
    await until(win, () => Boolean(document.querySelector('[data-user-message-id="sent-user-2"]')) &&
      !document.querySelector(".scroll-region-content[data-submit-placing]"), "inspection submission placement");
    await frames(win, 12);
    await inspectProcess(win, results, "sent-turn-2");
    assert.deepEqual(errors, [], `Renderer errors: ${errors.join("\n")}`);
    win.destroy();
    app.exit(0);
    return;
  }

  // --- Output streams while the reader takes over and hands back control.
  const live = liveTurn("live-a");
  const liveAgent = live.items[1];
  emit(win, "thread/resumed", { thread: thread(THREAD_ID, [bootTurn, ...history, live], true) });
  await until(win, () => document.querySelectorAll(".turn").length >= 38, "history to render");
  if (process.env.WUU_SCROLL_E2E_ONLY === "history-jump") {
    await verifyHistoryJump(win, results);
    assert.deepEqual(errors, [], `Renderer errors: ${errors.join("\n")}`);
    win.destroy();
    app.exit(0);
    return;
  }
  // The boot message finishes its placement, then its reply is followed.
  await until(win, selector => {
    const node = document.querySelector(selector);
    return node.scrollHeight - node.clientHeight - node.scrollTop <= 2;
  }, "the conversation to follow latest content", 8000, VIEWPORT);
  let stop = startStream(win, THREAD_ID, live.id, liveAgent.id);
  assertFollowing(await followSamples(win, 1000), "Streaming must follow at latest");

  await wheel(win, 30, 8);
  await settleScroll(win);
  const reading = await geometry(win);
  await sleep(1200);
  const afterOutput = await geometry(win);
  assert.ok(afterOutput.height > reading.height, "The fixture must keep streaming");
  assert.ok(Math.abs(afterOutput.top - reading.top) <= 1, `Streaming moved a reader who scrolled up: ${JSON.stringify({ reading, afterOutput })}`);
  record(results, "streaming yields to the reader", { reading, afterOutput });

  await wheel(win, -150, 25);
  await sleep(400);
  const returned = await followSamples(win, 1200);
  assertFollowing(returned, "Scrolling back to latest must resume following");
  record(results, "returning to latest resumes following", { returned });

  await wheel(win, 300, 20);
  await settleScroll(win);
  const away = await geometry(win);
  assert.ok(away.distance > away.client, "The reader must be well away from latest");
  const jump = await longestBlankRun(win, () => evaluate(win, () => document.querySelector(".jump-to-latest-pill").click()), 900);
  const jumped = await followSamples(win, 1200);
  assertFollowing(jumped, "Jump to latest must keep following streamed output");
  assert.ok(jump.worst <= BLANK_RUN_LIMIT_PX, `Jump to latest painted a ${jump.worst}px blank band`);
  record(results, "jump to latest follows output that arrives during the jump", { away, jumped, blank: jump.worst });
  stop();
  emit(win, "turn/completed", { thread_id: THREAD_ID, turn: { ...live, status: "completed", completed_at: now, items: [live.items[0], { ...liveAgent, status: "completed", text: answer(99, 12) }] } });
  await frames(win, 20);

  // --- A paused reader keeps their place while output completes and the sidebar moves.
  await wheel(win, 200, 25);
  await settleScroll(win);
  const beforeCompletion = await geometry(win);
  const live2 = liveTurn("live-b", answer(98, 4));
  emit(win, "turn/started", { thread_id: THREAD_ID, turn: live2 });
  await frames(win, 10);
  stop = startStream(win, THREAD_ID, live2.id, live2.items[1].id);
  await sleep(700);
  stop();
  emit(win, "turn/completed", { thread_id: THREAD_ID, turn: { ...live2, status: "completed", completed_at: now, items: [live2.items[0], { ...live2.items[1], status: "completed", text: answer(98, 9) }] } });
  await frames(win, 20);
  const afterCompletion = await geometry(win);
  assert.ok(Math.abs(afterCompletion.top - beforeCompletion.top) <= 1, `Completion moved a paused reader: ${JSON.stringify({ beforeCompletion, afterCompletion })}`);
  record(results, "completion keeps a paused reader still", { beforeCompletion, afterCompletion });

  const anchorBefore = await readingPoint(win);
  await toggleSidebar(win);
  const anchorAfter = await readingPointTop(win, anchorBefore);
  const pausedAfterToggle = await evaluate(win, () => Boolean(document.querySelector(".jump-to-latest-pill")));
  assert.ok(Math.abs(anchorAfter - anchorBefore.top) <= 2, `A sidebar toggle moved the text being read: ${anchorBefore.top} -> ${anchorAfter}`);
  assert.ok(pausedAfterToggle, "A sidebar toggle must not resume following");
  record(results, "a sidebar toggle keeps a paused reader in place", { anchorBefore, anchorAfter });

  // --- The reported white screen: large scrolls after the reading column rewraps.
  await toggleSidebar(win);
  const flingUp = await longestBlankRun(win, () => wheel(win, 120, 70), 1500);
  const flingDown = await longestBlankRun(win, () => wheel(win, -120, 70), 1500);
  for (const [label, scan] of [["up", flingUp], ["down", flingDown]]) {
    if (scan.worst > BLANK_RUN_LIMIT_PX) fs.writeFileSync(path.join(output, `fling-${label}.png`), scan.worstImage.toPNG());
    assert.ok(scan.worst <= BLANK_RUN_LIMIT_PX, `Flinging ${label} after a sidebar toggle painted a ${scan.worst}px blank band`);
    // Skipped turns keep their real height, so a long scroll never changes the range under the reader.
    assert.ok(Math.max(...scan.heights) - Math.min(...scan.heights) <= 1, `Scroll height changed while flinging ${label}: ${scan.heights}`);
  }
  record(results, "large scrolls after a sidebar toggle paint every frame", { up: flingUp.worst, down: flingDown.worst });

  await verifyHistoryJump(win, results);

  // --- Sending: the message rises into its reading position and stays through a sidebar toggle.
  await evaluate(win, () => document.querySelector(".jump-to-latest-pill")?.click());
  await settleScroll(win);
  await trackMessage(win, "sent-user-2");
  await typeAndSend(win, "A new question that should settle in the reading band.");
  await until(win, () => Boolean(document.querySelector('[data-user-message-id="sent-user-2"]')), "accepted submission");
  await sleep(900);
  const placement = await stopTracking(win);
  const client = (await geometry(win)).client;
  const reversals = placement.slice(1).filter((top, index) => top > placement[index] + 0.5);
  assert.ok(placement.length > 5, "The submitted message must be tracked while it is placed");
  assert.equal(reversals.length, 0, `The submitted message moved back down while being placed: ${placement.map(Math.round)}`);
  const placedTop = placement.at(-1);
  assert.ok(placedTop >= 0 && placedTop <= client * 0.4, `The submitted message must settle in the upper reading band: ${placedTop}/${client}`);
  const replyItem = { id: "sent-agent-2", type: "agent_message", status: "in_progress", text: "" };
  emit(win, "item/started", { thread_id: THREAD_ID, turn_id: "sent-turn-2", item: replyItem });
  emit(win, "item/agentMessage/delta", { thread_id: THREAD_ID, turn_id: "sent-turn-2", item_id: replyItem.id, delta: "A short first line." });
  await frames(win, 10);
  const heldBefore = await evaluate(win, selector => document.querySelector('[data-user-message-id="sent-user-2"]').getBoundingClientRect().top - document.querySelector(selector).getBoundingClientRect().top, VIEWPORT);
  await toggleSidebar(win);
  const heldAfter = await evaluate(win, selector => document.querySelector('[data-user-message-id="sent-user-2"]').getBoundingClientRect().top - document.querySelector(selector).getBoundingClientRect().top, VIEWPORT);
  assert.ok(Math.abs(heldAfter - heldBefore) <= 1, `A sidebar toggle moved the held submission: ${heldBefore} -> ${heldAfter}`);
  record(results, "a submission rises into place and holds through a sidebar toggle", { placedTop, heldBefore, heldAfter });

  // --- The reply fills the reserved space, then output is followed without moving the message down.
  await trackMessage(win, "sent-user-2");
  stop = startStream(win, THREAD_ID, "sent-turn-2", replyItem.id, 25);
  await until(win, selector => {
    const node = document.querySelector(selector);
    return node.scrollHeight - node.clientHeight - node.scrollTop <= 2 &&
      document.querySelector('[data-user-message-id="sent-user-2"]').getBoundingClientRect().bottom < node.getBoundingClientRect().top;
  }, "the reply to fill the viewport and be followed", 20000, VIEWPORT);
  const followed = await followSamples(win, 1000);
  stop();
  const handoff = await stopTracking(win);
  const downward = handoff.slice(1).filter((top, index) => top > handoff[index] + 0.5);
  assertFollowing(followed, "The reply must be followed once it fills the viewport");
  assert.equal(downward.length, 0, `The submitted message moved down while its reply streamed: ${handoff.map(Math.round).slice(0, 80)}`);
  record(results, "the reply takes over following without moving the message back", { followed });

  // --- Each session keeps its own reading position.
  emit(win, "turn/completed", { thread_id: THREAD_ID, turn: { id: "sent-turn-2", status: "completed", items_view: "full", started_at: now, completed_at: now, items: [
    { id: "sent-user-2", type: "user_message", status: "completed", text: "A new question that should settle in the reading band." },
    { ...replyItem, status: "completed", text: answer(97, 10) },
  ] } });
  await frames(win, 10);
  await wheel(win, 300, 40);
  await settleScroll(win);
  const savedA = await readingPoint(win);
  // Switching away and back across a width change still restores the text, not an offset.
  await toggleSidebar(win);
  await evaluate(win, () => document.querySelector(".session-tab-new").click());
  await frames(win, 10);
  await typeAndSend(win, "Second session");
  await until(win, () => document.querySelectorAll(".sidebar-session-row").length === 2, "second session in the sidebar");
  const secondHistory = Array.from({ length: 12 }, (_, index) => completedTurn("second", index));
  emit(win, "turn/completed", { thread_id: SECOND_THREAD_ID, turn: { id: "sent-turn-3", status: "completed", items_view: "full", started_at: now, completed_at: now, items: [] } });
  emit(win, "thread/resumed", { thread: thread(SECOND_THREAD_ID, secondHistory, false) });
  await until(win, () => document.querySelector('.cached-conversation-pane[data-active="true"]')?.textContent.includes("Question 12:"), "second session history");
  await frames(win, 10);
  await wheel(win, 200, 10);
  await settleScroll(win);
  const savedB = await readingPoint(win);
  const switchSession = async () => {
    await evaluate(win, () => document.querySelector(".sidebar-session-row:not(.active) .thread-row-main").click());
    await frames(win, 10);
  };
  await switchSession();
  await until(win, id => document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === id, "session A", 4000, THREAD_ID);
  const restoredA = await readingPointTop(win, savedA);
  const revealA = await longestBlankRun(win, async () => undefined, 200);
  await switchSession();
  await until(win, id => document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === id, "session B", 4000, SECOND_THREAD_ID);
  const restoredB = await readingPointTop(win, savedB);
  assert.ok(Math.abs(restoredA - savedA.top) <= 2, `Session A lost its reading position: ${savedA.top} -> ${restoredA}`);
  assert.ok(Math.abs(restoredB - savedB.top) <= 2, `Session B lost its reading position: ${savedB.top} -> ${restoredB}`);
  assert.ok(revealA.worst <= BLANK_RUN_LIMIT_PX, `Returning to session A painted a ${revealA.worst}px blank band`);
  record(results, "each session restores its own reading position", { savedA, restoredA, savedB, restoredB });

  assert.deepEqual(errors, [], `Renderer errors: ${errors.join("\n")}`);
  win.destroy();
  app.exit(0);
}).catch(error => {
  console.error(error);
  app.exit(1);
});
