const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "streaming-e2e-preload.cjs");
const evidenceDir = path.join(desktopRoot, "out", "e2e");

process.env.WUU_STREAM_E2E_CWD = repoRoot;
app.commandLine.appendSwitch("disable-gpu");
app.commandLine.appendSwitch("disable-software-rasterizer");

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Streaming E2E preload is missing.");
  fs.mkdirSync(evidenceDir, { recursive: true });

  const win = new BrowserWindow({
    width: 1100,
    height: 820,
    show: process.env.WUU_PROCESS_E2E_SCENARIO === "browser",
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false
    }
  });

  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });
  win.webContents.on("console-message", (_event, level, message, line, sourceId) => {
    if (level >= 2) {
      console.error(`renderer console: ${message} (${sourceId}:${line})`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")), 5000);

  const threadID = await startActiveThread(win);
  if (process.env.WUU_PROCESS_E2E_SCENARIO === "browser") {
    await verifyBrowserActivity(win, threadID);
    console.log("browser activity e2e passed");
    app.exit(0);
    return;
  }
  const now = new Date().toISOString();
  const userItem = {
    id: "user-process-handoff-e2e",
    type: "user_message",
    status: "completed",
    text: "Alternate text, tools, then final text."
  };
  const commentaryItem = {
    id: "agent-process-handoff-commentary-e2e",
    type: "agent_message",
    terminal: false,
    status: "completed",
    text: "第一段说明：我会先查看文件，然后再给出最终回答。"
  };
  const toolItems = Array.from({ length: 8 }, (_value, index) => ({
    id: `tool-process-handoff-e2e-${index + 1}`,
    type: "tool_call",
    status: "completed",
    name: index % 2 === 0 ? "list_files" : "read_file",
    arguments:
      index % 2 === 0
        ? '{"path":"desktop/src/renderer"}'
        : `{"path":"desktop/src/renderer/${index + 1}.tsx"}`,
    result: "App.tsx\nStreamingMarkdown.tsx\nToolActivity.tsx"
  }));
  const finalItem = {
    id: "agent-process-handoff-final-e2e",
    type: "agent_message",
    terminal: true,
    status: "in_progress",
    text: ""
  };
  const turn = {
    id: "turn-process-handoff-e2e",
    items: [userItem],
    items_view: "full",
    status: "in_progress",
    started_at: now
  };
  const finalText = "第二段最终回答应该接替上一段过程文字，而不是跳到另一个视觉位置。";

  emitNotification(win, "turn/started", { thread_id: threadID, turn });
  emitNotification(win, "item/completed", {
    thread_id: threadID,
    turn_id: turn.id,
    item: commentaryItem
  });
  for (const item of toolItems) {
    emitNotification(win, "item/completed", {
      thread_id: threadID,
      turn_id: turn.id,
      item
    });
  }

  const processRect = await waitFor(win, processCheckpointRect, 3000);
  assert.match(processRect.text, /第一段说明/, "Process preview should be visible before final text starts.");
  const beforeProcessState = await waitFor(win, processGroupState, 3000);
  assert.equal(beforeProcessState.expanded, true, "Process details should stay open while the turn is still in flight.");
  assert.equal(beforeProcessState.shellHasProcess, true, "Assistant shell should expose a stable process lane.");
  assert.equal(beforeProcessState.shellHasAnswer, false, "Answer lane should stay empty before final text starts.");
  assert.equal(beforeProcessState.checkpointVisible, false, "Process preview is hidden while the process fold is open.");
  assert.ok(beforeProcessState.detailsHeight > 1, `Open process details should occupy space. Height=${beforeProcessState.detailsHeight}`);
  await delay(450);
  await capture(win, "process-handoff-before.png");

  emitNotification(win, "item/started", {
    thread_id: threadID,
    turn_id: turn.id,
    item: finalItem
  });
  emitNotification(win, "item/agentMessage/delta", {
    thread_id: threadID,
    turn_id: turn.id,
    item_id: finalItem.id,
    delta: finalText
  });

  const finalRect = await waitFor(win, finalAnswerRect, 3000);
  await waitFor(win, processFoldCollapsed, 3000);
  await delay(450);
  const afterProcessState = await waitFor(win, processGroupState, 3000);
  assert.match(finalRect.text, /第二段/, "Final text should stream after process work.");
  assert.equal(afterProcessState.shellHasProcess, true, "Process lane should remain stable after final text starts.");
  assert.equal(afterProcessState.shellHasAnswer, true, "Final text should appear in the stable answer lane.");
  assert.equal(afterProcessState.checkpointVisible, false, "Stale process preview should stay hidden after answer handoff.");
  assert.equal(afterProcessState.expanded, false, "Process details should collapse when final text starts.");
  assert.ok(
    afterProcessState.detailsHeight <= 24,
    `Process details should yield their space during handoff. Height=${afterProcessState.detailsHeight}`
  );
  assert.ok(
    Number.parseFloat(finalRect.fontSize) >= 14,
    `Final text should render as readable answer body copy. Font size=${finalRect.fontSize}`
  );
  assert.ok(
    afterProcessState.groupToAnswerGap >= 4 && afterProcessState.groupToAnswerGap <= 24,
    `Process group and answer lane should use a compact stable gap. Gap=${afterProcessState.groupToAnswerGap}`
  );
  await capture(win, "process-handoff-after.png");

  emitNotification(win, "item/completed", {
    thread_id: threadID,
    turn_id: turn.id,
    item: { ...finalItem, status: "completed", text: finalText }
  });
  emitNotification(win, "turn/completed", {
    thread_id: threadID,
    turn: {
      ...turn,
      status: "completed",
      completed_at: new Date().toISOString(),
      duration_ms: 180,
      items: [
        userItem,
        commentaryItem,
        ...toolItems,
        { ...finalItem, status: "completed", text: finalText }
      ]
    }
  });

  await delay(700);
  const completedActionSlot = await waitFor(
    win,
    completedAgentActionSlotRect,
    3000
  );
  const completedFinalRect = await waitFor(win, finalAnswerRect, 3000);
  const completedProcessState = await waitFor(win, processGroupState, 3000);
  const bodyToActionsGap = completedActionSlot.firstButtonTop - (completedFinalRect.top + completedFinalRect.height);
  assert.equal(completedProcessState.shellHasProcess, true, "Completed process summary should remain in the stable shell.");
  assert.equal(completedProcessState.shellHasAnswer, true, "Completed answer should remain in the answer lane.");
  assert.equal(completedProcessState.checkpointVisible, false, "Completed process summary should not keep the live commentary preview.");
  assert.equal(completedProcessState.expanded, false, "Completed process details should remain collapsed by default.");
  assert.ok(
    completedProcessState.detailsHeight <= 24,
    `Completed process details should not occupy space until opened. Height=${completedProcessState.detailsHeight}`
  );
  assert.ok(
    Math.abs(completedFinalRect.left - finalRect.left) <= 2,
    `Completing the answer should not move text horizontally. Before=${finalRect.left}, after=${completedFinalRect.left}`
  );
  assert.ok(
    completedProcessState.groupToAnswerGap >= 4 && completedProcessState.groupToAnswerGap <= 48,
    `Process group and answer should use a compact hierarchy gap. Gap=${completedProcessState.groupToAnswerGap}`
  );
  assert.ok(
    bodyToActionsGap >= 4 && bodyToActionsGap <= 16,
    `Answer and action buttons should keep a compact breathing gap. Gap=${bodyToActionsGap}`
  );
  await capture(win, "process-handoff-completed.png");

  await verifyArtifactHandoff(win, threadID);
  await verifyBrowserActivity(win, threadID);
  console.log("process handoff e2e passed");
  app.exit(0);
}

async function verifyBrowserActivity(win, threadID) {
  const browserItems = [
    { id: "browser-navigate", type: "tool_call", status: "completed", name: "wuu_browser",
      arguments: JSON.stringify({ action: "navigate", url: "https://example.com/browser-guide" }) },
    { id: "browser-observe", type: "tool_call", status: "completed", name: "wuu_browser",
      arguments: JSON.stringify({ action: "observe", tab_id: "tab-1" }),
      result_detail: { structured_content: { action: "observe", title: "Browser guide — navigation and page interaction", url: "https://example.com/browser-guide" } } },
    { id: "browser-type", type: "tool_call", status: "completed", name: "wuu_browser",
      arguments: JSON.stringify({ action: "type", text: "private-input-must-stay-private", node_id: 3 }) },
    { id: "browser-key", type: "tool_call", status: "completed", name: "wuu_browser",
      arguments: JSON.stringify({ action: "key", keys: ["Control", "Enter"] }) },
  ];
  const turn = { id: "browser-activity", status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [
    { id: "browser-user", type: "user_message", status: "completed", text: "Inspect the browser guide." },
    ...browserItems,
  ] };
  emitNotification(win, "turn/started", { thread_id: threadID, turn });
  await waitFor(win, () => Array.from(document.querySelectorAll('.turn')).at(-1)?.textContent.includes('Inspect the browser guide'), 3000);
  await evaluate(win, () => {
    const shell = Array.from(document.querySelectorAll('.assistant-turn-shell')).at(-1);
    const fold = shell.querySelector('.turn-process-fold');
    if (!fold.classList.contains('expanded')) fold.querySelector('.turn-process-toggle').click();
  });
  await evaluate(win, () => {
    const shell = Array.from(document.querySelectorAll('.assistant-turn-shell')).at(-1);
    for (const details of shell.querySelectorAll('details.process-surface-fold')) details.open = true;
  });
  await waitFor(win, () => {
    const rows = Array.from(document.querySelectorAll('.assistant-turn-shell')).at(-1).querySelectorAll('.activity-row');
    return rows.length === 4 && Array.from(rows).every(row => row.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }) && !row.closest('[inert]'));
  }, 3000);
  const text = await evaluate(win, () => Array.from(document.querySelectorAll('.assistant-turn-shell')).at(-1).querySelector('.activity-timeline').textContent);
  assert.ok(text.includes('example.com/browser-guide'), 'Navigation must expose its destination.');
  assert.ok(text.includes('Browser guide'), 'Observation must expose its structured page title.');
  assert.ok(!text.includes('private-input-must-stay-private'), 'Input values must not leak into summaries.');
  assert.ok(!text.includes('wuu_browser'), 'Browser activity must use semantic labels.');
  for (const [theme, width, fontSize] of [['light', 1100, 15], ['dark', 1100, 20], ['light', 480, 20], ['dark', 480, 15]]) {
    win.setSize(width, 820);
    await win.webContents.executeJavaScript(`
      document.documentElement.dataset.theme = ${JSON.stringify(theme)};
      document.documentElement.style.setProperty('--font-ui', '${fontSize}px');
      document.documentElement.style.setProperty('--conversation-message-font-size', '${fontSize}px');
      if (${width < 600} && !document.querySelector('.app-shell').classList.contains('sidebar-collapsed')) {
        document.querySelector('[data-wuu-component="sidebar-toggle"]').click();
      }
      window.dispatchEvent(new Event('wuu-content-size-change'));
      Array.from(document.querySelectorAll('.activity-timeline')).at(-1).scrollIntoView({ block: 'center' });
      new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    `);
    // Allow theme, sidebar and native window resize transitions to settle for capture.
    await delay(600);
    await capture(win, `browser-activity-${theme}-${width}-${fontSize}.png`);
  }
}

async function verifyArtifactHandoff(win, threadID) {
  const results = [];
  const imageData = fs.readFileSync(path.join(desktopRoot, "dev/message-flow-reading/media-portrait.svg")).toString("base64");
  for (const [theme, width, fontSize, earlyTerminal] of [
    ["light", 1100, 15, true], ["dark", 1100, 20, false],
    ["light", 480, 20, false], ["dark", 480, 15, true],
  ]) {
    win.setSize(width, 900);
    await win.webContents.executeJavaScript(`
      document.documentElement.dataset.theme = ${JSON.stringify(theme)};
      document.documentElement.style.setProperty('--font-ui', '${fontSize}px');
      document.documentElement.style.setProperty('--conversation-message-font-size', '${fontSize}px');
      window.dispatchEvent(new Event('wuu-content-size-change'));
      if (${width < 600} && !document.querySelector('.app-shell').classList.contains('sidebar-collapsed')) {
        document.querySelector('[data-wuu-component="sidebar-toggle"]').click();
      }
    `);
    const id = `artifact-handoff-${results.length}`;
    const processText = "Published the image; verification is still in progress.";
    const items = [
      { id: `${id}-user`, type: "user_message", status: "completed", text: "Publish the image, verify it, then explain the result." },
      { id: `${id}-image`, type: "tool_call", name: "present_artifact", status: "completed", result_detail: { content: [
        { type: "image", mime_type: "image/svg+xml", data: imageData, artifact: { placement: "inline" } },
      ] } },
      { id: `${id}-commentary`, type: "agent_message", terminal: false, status: "completed", text: processText },
      { id: `${id}-tool`, type: "tool_call", name: "read_file", arguments: '{"path":"verification.json"}', result: "Verified", status: "completed" },
      { id: `${id}-reasoning`, type: "reasoning", status: "completed", text: "Compare the verification results." },
      { id: `${id}-commentary-2`, type: "agent_message", terminal: false, status: "completed", text: "Verification complete; preparing the final answer." },
      { id: `${id}-tool-2`, type: "tool_call", name: "run_shell", arguments: '{"command":"check"}', result: "Passed", status: "completed" },
      { id: `${id}-reasoning-2`, type: "reasoning", status: "completed", text: "Ready to report." },
    ];
    const turn = { id, items, status: "in_progress", items_view: "full", started_at: new Date().toISOString() };
    emitNotification(win, "turn/started", { thread_id: threadID, turn });
    await waitFor(win, () => {
      const shell = Array.from(document.querySelectorAll(".assistant-turn-shell")).at(-1);
      const image = shell?.querySelector(".turn-answer-body img");
      if (!image?.complete || !image.naturalWidth || !shell?.querySelector(".turn-answer-body")?.textContent.includes("Published the image")) return false;
      window.handoffImage = image;
      return true;
    }, 5000);
    const before = await evaluate(win, artifactHandoffState);
    assert.equal(before.expanded, true);
    assert.equal(before.outputHasProcess, true, "Work following a publication must stay visible before the answer");
    const answer = { id: `${id}-answer`, type: "agent_message", terminal: earlyTerminal, status: "in_progress", text: "" };
    emitNotification(win, "item/started", { thread_id: threadID, turn_id: id, item: answer });
    await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal((await evaluate(win, artifactHandoffState)).expanded, true, "An empty final-answer placeholder must not collapse work");
    const finalText = "The image and verification are ready.";
    emitNotification(win, "item/agentMessage/delta", { thread_id: threadID, turn_id: id, item_id: answer.id, delta: finalText });
    await waitFor(win, () => Array.from(document.querySelectorAll(".assistant-turn-shell")).at(-1)?.textContent.includes("The image and verification are ready."), 5000);
    if (!earlyTerminal) {
      assert.equal((await evaluate(win, artifactHandoffState)).outputHasProcess, true);
      answer.terminal = true;
      answer.text = finalText;
      emitNotification(win, "item/completed", { thread_id: threadID, turn_id: id, item: { ...answer, status: "completed" } });
    }
    await waitFor(win, processFoldCollapsed, 5000);
    const handoff = await evaluate(win, artifactHandoffState);
    assert.equal(handoff.outputHasProcess, false, "All commentary and grouped tool/reasoning records must leave the answer body at handoff");
    assert.equal(handoff.foldHasProcess, true, "Process content must remain available in the fold");
    assert.equal(handoff.imagePreserved, true, "Handoff must preserve the published image node");
    assert.equal(handoff.answerVisible, true);
    answer.text = finalText;
    answer.terminal = true;
    answer.status = "completed";
    turn.items = [...items, answer];
    turn.status = "completed";
    emitNotification(win, "turn/completed", { thread_id: threadID, turn: { ...turn, completed_at: new Date().toISOString(), duration_ms: 1000 } });
    await waitFor(win, () => {
      const shell = Array.from(document.querySelectorAll(".assistant-turn-shell")).at(-1);
      return shell?.querySelector(".turn-process-fold .collapsible-details")?.getBoundingClientRect().height < 1;
    }, 5000);
    const completed = await evaluate(win, artifactHandoffState);
    assert.equal(completed.outputHasProcess, false);
    assert.equal(completed.imagePreserved, true);
    await capture(win, `artifact-handoff-${theme}-${width}-${fontSize}.png`);
    await evaluate(win, () => Array.from(document.querySelectorAll(".turn-process-toggle")).at(-1).click());
    const reopened = await evaluate(win, artifactHandoffState);
    assert.equal(reopened.expanded, true, "Readers must be able to reopen the complete process");
    assert.equal(reopened.foldHasProcess, true);
    assert.equal(reopened.outputHasProcess, false);
    results.push({ theme, width, fontSize, earlyTerminal, before, handoff, completed, reopened });
  }
  fs.writeFileSync(path.join(evidenceDir, "artifact-handoff-results.json"), JSON.stringify(results, null, 2));
}

function artifactHandoffState() {
  const shell = Array.from(document.querySelectorAll(".assistant-turn-shell")).at(-1);
  const fold = shell.querySelector(".turn-process-fold");
  const output = shell.querySelector(".turn-answer-body");
  return {
    expanded: fold.classList.contains("expanded"),
    outputHasProcess: output.textContent.includes("Published the image") || output.textContent.includes("Verification complete") || Boolean(output.querySelector(".process-surface")),
    foldHasProcess: fold.textContent.includes("Published the image") && fold.textContent.includes("Verification complete") && Boolean(fold.querySelector(".process-surface")),
    imagePreserved: output.querySelector("img") === window.handoffImage,
    answerVisible: output.textContent.includes("The image and verification are ready."),
  };
}

async function startActiveThread(win) {
  const started = await waitFor(
    win,
    () => {
      const textarea = document.querySelector(".composer textarea");
      if (!(textarea instanceof HTMLTextAreaElement)) {
        return false;
      }
      textarea.focus();
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      valueSetter?.call(textarea, "Process handoff e2e seed.");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      const enter = new KeyboardEvent("keydown", {
        key: "Enter",
        code: "Enter",
        bubbles: true,
        cancelable: true
      });
      textarea.dispatchEvent(enter);
      return enter.defaultPrevented;
    },
    3000
  );
  assert.equal(started, true, "Composer should create an active e2e thread.");
  await waitFor(
    win,
    () => {
      const text = document.querySelector(".conversation-width")?.textContent ?? "";
      return text.includes("Process handoff e2e seed.");
    },
    3000
  );
  return "thread-immediate-title-e2e";
}

function processCheckpointRect() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const checkpoint =
    turn?.querySelector(".turn-process-preview-text") ??
    turn?.querySelector(".turn-process-fold-body") ??
    turn?.querySelector(".turn-process-fold");
  if (!(checkpoint instanceof HTMLElement)) {
    return null;
  }
  const rect = checkpoint.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) {
    return null;
  }
  const text = checkpoint.textContent ?? "";
  if (!text.includes("第一段说明")) {
    return null;
  }
  const style = getComputedStyle(checkpoint);
  return {
    text,
    top: rect.top,
    left: rect.left,
    width: rect.width,
    height: rect.height,
    fontSize: style.fontSize,
    lineHeight: style.lineHeight
  };
}

function finalAnswerRect() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const finalLane = turn?.querySelector(".turn-answer-body");
  const markdown = finalLane?.querySelector(".agent-text .streaming-markdown");
  const finalText =
    finalLane?.querySelector(".agent-text .streaming-plain-text") ??
    markdown?.querySelector(".rich-paragraph, .rich-heading, .streaming-plain-text") ??
    markdown;
  if (!(finalText instanceof HTMLElement) || !finalText.textContent?.trim()) {
    return null;
  }
  const text = finalText.textContent ?? "";
  if (!text.includes("第二段")) {
    return null;
  }
  const rect = finalText.getBoundingClientRect();
  const style = getComputedStyle(finalText);
  return {
    text,
    top: rect.top,
    left: rect.left,
    width: rect.width,
    height: rect.height,
    fontSize: style.fontSize,
    lineHeight: style.lineHeight
  };
}

function processGroupState() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const shell = turn?.querySelector(".assistant-turn-shell");
  const group = shell?.querySelector(".turn-process-fold");
  if (!(group instanceof HTMLElement)) {
    return null;
  }
  const processLane = shell?.querySelector(".turn-process-fold");
  const answerLane = shell?.querySelector(".turn-answer-body");
  const answer = answerLane?.querySelector(".agent-block");
  const checkpoint = group.querySelector(".turn-process-preview");
  const details = group.querySelector(".collapsible-details");
  if (!(processLane instanceof HTMLElement)) {
    return null;
  }
  const answerRect =
    answer instanceof HTMLElement
      ? answer.getBoundingClientRect()
      : { top: 0, bottom: 0, height: 0 };
  const detailsRect =
    details instanceof HTMLElement
      ? details.getBoundingClientRect()
      : { top: 0, bottom: 0, height: 0 };
  const groupRect = group.getBoundingClientRect();
  const visibleActivityRows = Array.from(group.querySelectorAll(".activity-row")).filter((row) => {
    if (!(row instanceof HTMLElement)) {
      return false;
    }
    const rect = row.getBoundingClientRect();
    return (
      detailsRect.height > 1 &&
      rect.width > 0 &&
      rect.height > 0 &&
      rect.bottom > detailsRect.top &&
      rect.top < detailsRect.bottom
    );
  }).length;
  return {
    expanded: group.classList.contains("expanded"),
    shellHasProcess: shell instanceof HTMLElement && shell.classList.contains("has-process"),
    shellHasAnswer: shell instanceof HTMLElement && shell.classList.contains("has-answer"),
    checkpointVisible: checkpoint instanceof HTMLElement && checkpoint.getBoundingClientRect().height > 0,
    detailsHeight: detailsRect.height,
    visibleActivityRows,
    groupToAnswerGap:
      answer instanceof HTMLElement && answerRect.height > 0
        ? answerRect.top - groupRect.bottom
        : null
  };
}

function processFoldCollapsed() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const group = turn?.querySelector(".turn-process-fold");
  return group instanceof HTMLElement && group.classList.contains("collapsed");
}

function agentActionSlotRect() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const slot = turn?.querySelector(".agent-message-actions");
  if (!(slot instanceof HTMLElement)) {
    return null;
  }
  const rect = slot.getBoundingClientRect();
  if (rect.height <= 0) {
    return null;
  }
  return {
    top: rect.top,
    left: rect.left,
    width: rect.width,
    height: rect.height,
    hasButtons: slot.querySelectorAll(".message-action-button").length > 0
  };
}

function completedAgentActionSlotRect() {
  const turns = Array.from(document.querySelectorAll(".turn"));
  const turn = turns.at(-1);
  const slot = turn?.querySelector(".agent-message-actions");
  if (!(slot instanceof HTMLElement)) {
    return null;
  }
  const rect = slot.getBoundingClientRect();
  if (rect.height <= 0) {
    return null;
  }
  const hasButtons = slot.querySelectorAll(".message-action-button").length > 0;
  return hasButtons
    ? {
        top: rect.top,
        left: rect.left,
        width: rect.width,
        height: rect.height,
        hasButtons,
        firstButtonTop: slot.querySelector(".message-action-button").getBoundingClientRect().top
      }
    : null;
}

function emitNotification(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD || process.cwd(),
    kind: "notification",
    message: { method, params }
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
    if (lastValue) {
      return lastValue;
    }
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

async function evaluate(win, fn) {
  return win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
}

async function capture(win, name) {
  const image = await win.webContents.capturePage();
  fs.writeFileSync(path.join(evidenceDir, name), image.toPNG());
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
