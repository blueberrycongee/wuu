const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const evidence = path.resolve(desktop, "../artifacts/session-tail-space");
fs.mkdirSync(evidence, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(evidence, "profile-")));
process.env.WUU_STREAM_E2E_CWD = path.dirname(desktop);
const evaluate = (win, fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(x => JSON.stringify(x)).join(",")})`, true);
async function until(win, fn) {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    const result = await evaluate(win, fn);
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  console.error(await evaluate(win, () => document.body.innerText.slice(-5000)));
  throw new Error(`Timed out: ${fn}`);
}
const frames = win => evaluate(win, () => new Promise(resolve => {
  let remaining = 24;
  const tick = () => --remaining ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}));
function emit(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD,
    kind: "notification", message: { method, params },
  });
}
const geometry = win => evaluate(win, () => {
  const viewport = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
  const activePane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const tail = [...(activePane ?? viewport).querySelectorAll(".turn")].at(-1);
  const capsule = document.querySelector(".conversation-status-cluster");
  const composer = document.querySelector(".dock-composer-wrap");
  return {
    windowWidth: window.innerWidth,
    threadID: activePane?.dataset.threadId,
    viewportWidth: viewport.clientWidth,
    top: viewport.scrollTop,
    bottomDistance: viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop,
    tailBottom: tail.getBoundingClientRect().bottom,
    capsuleTop: capsule?.getBoundingClientRect().top ?? null,
    capsuleVisible: capsule ? getComputedStyle(capsule).visibility === "visible" : false,
    composerTop: composer.getBoundingClientRect().top,
    tailSpace: Number.parseFloat(viewport.querySelector(":scope > .scroll-region-content")?.style.paddingBottom || "0"),
    statusSpace: document.querySelector(".conversation-pane").style.getPropertyValue("--conversation-status-space"),
  };
});

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width: 1200, height: 820, show: process.env.WUU_E2E_VISIBLE === "true",
    webPreferences: { preload: path.join(__dirname, "streaming-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false },
  });
  win.webContents.on("console-message", (_event, level, message) => { if (level >= 2) console.error(message); });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(win, () => !!document.querySelector(".composer textarea"));
  await frames(win);
  await evaluate(win, () => {
    const input = document.querySelector(".composer textarea");
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Check the Session TODO clearance.");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await frames(win);
  await evaluate(win, () => document.querySelector(".composer-send-button").click());
  await until(win, () => !!document.querySelector(".turn"));
  const threadID = "thread-immediate-title-e2e";
  const now = new Date().toISOString();
  const todo = {
    id: "tail-todo", type: "tool_call", status: "completed", name: "update_todo",
    display: { capability: "todo" },
    arguments: JSON.stringify({ todos: [
      { content: "Check running progress", status: "in_progress" },
      { content: "Browse history", status: "pending" },
      { content: "Finish without a gap", status: "pending" },
    ] }),
  };
  const agent = { id: "tail-answer", type: "agent_message", status: "in_progress", text: "Working on the Session layout." };
  const turn = {
    id: "tail-turn", status: "in_progress", items_view: "full", started_at: now,
    items: [{ id: "tail-user", type: "user_message", status: "completed", text: "Check the Session TODO clearance." }, todo, agent],
  };
  const thread = { id: threadID, preview: "Session clearance check", model_provider: "e2e", model: "mock-stream", cwd: path.dirname(desktop), status: "running", created_at: now, updated_at: now, turns: [turn] };
  emit(win, "thread/resumed", { thread });
  await until(win, () => !!document.querySelector(".conversation-status-todo-trigger"));
  const results = [];
  for (const [width, theme, font] of [[1200, "light", 14], [600, "light", 14], [390, "dark", 20], [1200, "dark", 20]]) {
    win.setContentSize(width, 780);
    await evaluate(win, (theme, font) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--appearance-scale", String(font / 14));
    }, theme, font);
    for (const long of [false, true]) {
      turn.id = `tail-${width}-${font}-${long}`;
      agent.id = `answer-${width}-${font}-${long}`;
      agent.text = long ? Array.from({ length: 55 }, (_, i) => `Paragraph ${i + 1}: Streaming content remains readable while TODO is available.`).join("\n\n") : "Working on the Session layout.";
      emit(win, "thread/resumed", { thread });
      await frames(win);
      const current = await geometry(win);
      assert.ok(current.capsuleTop >= current.tailBottom, `Capsule overlaps tail: ${JSON.stringify(current)}`);
      assert.ok(current.bottomDistance <= 2, `Must follow while streaming: ${JSON.stringify(current)}`);
      results.push({ width, theme, font, long, phase: "running", ...current });
      await fs.promises.writeFile(path.join(evidence, `${width}-${theme}-${font}-${long ? "long" : "short"}.png`), (await win.webContents.capturePage()).toPNG());
      if (width === 1200 && font === 14 && long) {
        // Controlled reproduction of the old clearance formula, with the same
        // real Session DOM and status placement, not a separate mock layout.
        const control = await win.webContents.insertCSS(".scroll-region > .scroll-region-content { padding-bottom: 0 !important; } .conversation-pane { --conversation-status-space: 0px !important; }");
        await evaluate(win, () => { const node = document.querySelector(".scroll-region"); node.scrollTop = node.scrollHeight; });
        await frames(win);
        const oldClearance = await geometry(win);
        assert.ok(oldClearance.tailBottom > oldClearance.capsuleTop, "Old clearance must reproduce the overlap");
        results.push({ width, phase: "old-clearance-control", ...oldClearance });
        await fs.promises.writeFile(path.join(evidence, "old-clearance-control.png"), (await win.webContents.capturePage()).toPNG());
        await win.webContents.removeInsertedCSS(control);
        await evaluate(win, () => { const node = document.querySelector(".scroll-region"); node.scrollTop = node.scrollHeight; });
        await frames(win);
      }
    }
    // TODO retains keyboard access to its existing preview card.
    await evaluate(win, () => document.querySelector(".conversation-status-todo-trigger").focus());
    await until(win, () => getComputedStyle(document.querySelector(".conversation-status-todo-card")).visibility === "visible");
    await evaluate(win, () => document.activeElement.blur());

    const before = await geometry(win);
    // Browser input, not a synthetic scroll event: exercise wheel intent and
    // Chromium scrolling together with the production scroll hook.
    win.webContents.sendInputEvent({ type: "mouseWheel", x: Math.round(width / 2), y: 300, deltaY: 240, deltaX: 0 });
    await until(win, () => document.querySelector(".scroll-region").scrollHeight - document.querySelector(".scroll-region").clientHeight - document.querySelector(".scroll-region").scrollTop > 100);
    await frames(win);
    const away = await geometry(win);
    assert.ok(away.top < before.top);
    // This fixture loaded a persisted snapshot, not the preceding live delta
    // buffer. Seed that buffer with the full text on its first notification.
    emit(win, "item/agentMessage/delta", { thread_id: threadID, turn_id: turn.id, item_id: agent.id, delta: agent.text + "\n\nA new streamed paragraph must not steal the history viewport." });
    await frames(win);
    const streamedAway = await geometry(win);
    assert.ok(Math.abs(streamedAway.top - away.top) <= 1, `Streaming stole history scroll: ${JSON.stringify({ before, away, streamedAway })}`);
    assert.equal(streamedAway.capsuleVisible, true);
    // Controls remain reachable while the reading area ends above their band.
    const access = await evaluate(win, () => {
      const trigger = document.querySelector(".conversation-status-todo-trigger");
      const rect = trigger.getBoundingClientRect();
      const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
      trigger.focus();
      const viewport = document.querySelector(".scroll-region");
      const jumpRect = document.querySelector(".jump-to-latest-pill")?.getBoundingClientRect();
      const jumpOverlap = jumpRect ? Math.max(0, Math.min(jumpRect.right, rect.right) - Math.max(jumpRect.left, rect.left)) *
        Math.max(0, Math.min(jumpRect.bottom, rect.bottom) - Math.max(jumpRect.top, rect.top)) : 0;
      return {
        hit: trigger.contains(hit), focused: document.activeElement === trigger,
        readingBottom: viewport.getBoundingClientRect().bottom,
        inputTop: document.querySelector(".dock-composer-wrap .composer-frame").getBoundingClientRect().top,
        capsuleTop: rect.top, jumpVisible: Boolean(jumpRect), jumpOverlap,
      };
    });
    assert.ok(access.hit && access.focused, `History TODO inaccessible: ${JSON.stringify(access)}`);
    // History is clipped at the input edge; the status row floats over it.
    assert.ok(access.readingBottom <= access.inputTop + 1, `History runs under the input: ${JSON.stringify(access)}`);
    assert.ok(access.jumpVisible && access.jumpOverlap === 0, `Jump covers status: ${JSON.stringify(access)}`);
    await until(win, () => getComputedStyle(document.querySelector(".conversation-status-todo-card")).visibility === "visible");
    assert.ok(Math.abs((await geometry(win)).top - away.top) <= 1, "Opening history TODO moved the viewport");
    const card = await evaluate(win, () => {
      const rect = document.querySelector(".conversation-status-todo-card").getBoundingClientRect();
      return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: window.innerWidth };
    });
    assert.ok(card.left >= 0 && card.right <= card.width && card.top >= 0 && card.bottom <= streamedAway.composerTop, `TODO preview clipped: ${JSON.stringify(card)}`);
    await fs.promises.writeFile(path.join(evidence, `${width}-${theme}-${font}-history-todo.png`), (await win.webContents.capturePage()).toPNG());
    await evaluate(win, () => document.activeElement.blur());
    results.push({ width, phase: "history-stream", ...streamedAway, access, card });
    await evaluate(win, () => {
      const node = document.querySelector(".scroll-region");
      node.scrollTop = node.scrollHeight;
    });
    await frames(win);
  }
  await evaluate(win, () => document.querySelector(".composer-stop-button").click());
  turn.status = "interrupted";
  emit(win, "turn/completed", { thread_id: threadID, turn });
  await until(win, () => !document.querySelector(".composer-stop-button"));
  await until(win, () => Number.parseFloat(document.querySelector(".scroll-region > .scroll-region-content").style.paddingBottom || "0") === 0);
  await frames(win);
  const stopped = await geometry(win);
  assert.equal(stopped.capsuleVisible, false, "Stopped TODO returns to the existing history/side-panel presentation");
  assert.ok(stopped.composerTop - stopped.tailBottom < 70, `Stop retained run space: ${JSON.stringify(stopped)}`);
  results.push({ phase: "stopped", ...stopped });
  turn.id = "tail-completion";
  turn.status = "in_progress";
  emit(win, "turn/started", { thread_id: threadID, turn });
  await until(win, () => !!document.querySelector(".conversation-status-cluster"));
  await frames(win);
  // Complete the TODO and run through the same server notification path.
  todo.arguments = JSON.stringify({ todos: [{ content: "All checks finished", status: "completed" }] });
  agent.status = "completed";
  turn.status = "completed";
  turn.completed_at = now;
  emit(win, "turn/completed", { thread_id: threadID, turn });
  await until(win, () => !document.querySelector(".conversation-status-cluster"));
  await until(win, () => Number.parseFloat(document.querySelector(".scroll-region > .scroll-region-content").style.paddingBottom || "0") === 0);
  await frames(win);
  const completed = await geometry(win);
  assert.ok(completed.bottomDistance <= 2);
  assert.ok(completed.composerTop - completed.tailBottom < 70, `Permanent blank space: ${JSON.stringify(completed)}`);
  results.push({ phase: "completed", ...completed });
  await fs.promises.writeFile(path.join(evidence, "completed.png"), (await win.webContents.capturePage()).toPNG());
  // Real App tab actions, not replacing the DOM or merely emitting resumed on
  // the active thread. Keep two independent history positions through A/B/A.
  turn.id = "switch-running-a";
  turn.status = "in_progress";
  thread.status = "running";
  todo.arguments = JSON.stringify({ todos: [{ content: "Session A only", status: "in_progress" }] });
  emit(win, "thread/resumed", { thread });
  await until(win, () => !!document.querySelector(".conversation-status-todo-trigger"));
  await frames(win);
  await evaluate(win, () => document.querySelector(".session-tab-new").click());
  await frames(win);
  await evaluate(win, () => {
    const input = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Session B history");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await frames(win);
  await evaluate(win, () => document.querySelector(".composer-send-button").click());
  await until(win, () => document.querySelectorAll(".session-tab-main").length === 2 && !!document.querySelector(".turn"));
  const second = {
    ...thread, id: "thread-streaming-e2e", preview: "Session B history", status: "idle",
    turns: [{ id: "switch-idle-b", status: "completed", items_view: "full", started_at: now,
      items: [{ id: "answer-b", type: "agent_message", status: "completed", text: Array.from({ length: 45 }, (_, i) => `Session B paragraph ${i}: independent history position.`).join("\n\n") }] }],
  };
  // Finish B's actual submitted turn before loading its persisted history;
  // a resumed snapshot alone does not end the local in-flight turn tracker.
  emit(win, "turn/completed", { thread_id: second.id, turn: { ...second.turns[0], id: `turn-${second.id}` } });
  emit(win, "thread/resumed", { thread: second });
  await until(win, () => !document.querySelector(".composer-stop-button") && !document.querySelector(".conversation-status-cluster") && document.querySelector(".turn")?.textContent.includes("Session B paragraph"));
  await until(win, () => Number.parseFloat(document.querySelector(".scroll-region > .scroll-region-content").style.paddingBottom || "0") === 0);
  await frames(win);
  const idleBottom = await geometry(win);
  assert.equal(idleBottom.threadID, second.id);
  assert.ok(idleBottom.composerTop >= idleBottom.tailBottom && idleBottom.composerTop - idleBottom.tailBottom < 70, `A's run space leaked into B: ${JSON.stringify(idleBottom)}`);
  assert.equal(idleBottom.tailSpace, 0, "A's run space leaked into B");
  await evaluate(win, () => { document.querySelector(".scroll-region").scrollTop = 180; });
  await frames(win);
  const savedB = await geometry(win);
  const switchTab = async index => {
    await evaluate(win, index => document.querySelectorAll(".session-tab-main")[index].click(), index);
    await frames(win);
  };
  await switchTab(0);
  await until(win, () => !!document.querySelector(".conversation-status-todo-trigger"));
  await evaluate(win, () => { document.querySelector(".scroll-region").scrollTop = 420; });
  await frames(win);
  const savedA = await geometry(win);
  await switchTab(1);
  const restoredB = await geometry(win);
  assert.equal(restoredB.threadID, second.id);
  assert.equal(restoredB.capsuleVisible, false);
  assert.equal(restoredB.tailSpace, 0);
  assert.equal(restoredB.statusSpace, idleBottom.statusSpace, "A's status band leaked into B");
  assert.ok(Math.abs(restoredB.top - savedB.top) <= 1, `B scroll leaked: ${JSON.stringify({ savedB, restoredB })}`);
  await fs.promises.writeFile(path.join(evidence, "switch-idle-b.png"), (await win.webContents.capturePage()).toPNG());
  await switchTab(0);
  const restoredA = await geometry(win);
  assert.equal(restoredA.threadID, threadID);
  assert.equal(restoredA.capsuleVisible, true);
  assert.ok(Math.abs(restoredA.top - savedA.top) <= 1, `A scroll leaked: ${JSON.stringify({ savedA, restoredA })}`);
  await fs.promises.writeFile(path.join(evidence, "switch-running-a.png"), (await win.webContents.capturePage()).toPNG());
  results.push({ phase: "session-switch", idleBottom, savedB, savedA, restoredB, restoredA });
  await fs.promises.writeFile(path.join(evidence, "geometry.json"), JSON.stringify(results, null, 2));
  console.log(JSON.stringify(results, null, 2));
  if (process.env.WUU_E2E_KEEP_OPEN !== "true") app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
