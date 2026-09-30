const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, ipcMain } = require("electron");
const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const evidence = process.env.WUU_REQUEST_LIFECYCLE_OUTPUT || path.join(desktopRoot, "out/e2e/request-lifecycle");
process.env.WUU_STREAM_E2E_CWD = repoRoot;
process.env.WUU_REQUEST_LIFECYCLE_E2E = "1";
app.setPath("userData", fs.mkdtempSync(path.join(require("node:os").tmpdir(), "wuu-request-lifecycle-")));
// The scenarios replace their window; only run() decides when the suite ends.
app.on("window-all-closed", () => {});
const gates = new Map();
const queued = [];
ipcMain.handle("test:request-lifecycle", (_event, method, params) => new Promise((resolve, reject) => gates.set(method, { params, resolve, reject })));
ipcMain.on("test:queued-input", (_event, value) => queued.push(value));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function setVisualTheme(theme, font) {
  document.documentElement.dataset.theme = theme;
  // Direct theme stamping bypasses the picker; use the base theme's CSS tokens.
  for (const name of Array.from(document.documentElement.style)) {
    if (name.startsWith("--wuu-")) document.documentElement.style.removeProperty(name);
  }
  document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
  document.documentElement.style.setProperty("--appearance-scale", String(font / 14));
  return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
    .then(() => Promise.all((document.querySelector(".user-message-edit")?.getAnimations() ?? [])
      .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map(animation => animation.finished.catch(() => {}))));
}
// Chromium activates a focused button on the character event, not on keyDown alone.
function pressEnter(win) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Return" });
  win.webContents.sendInputEvent({ type: "char", keyCode: "\r" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Return" });
}
async function until(read, label) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) { const value = await read(); if (value) return value; await sleep(25); }
  throw new Error(`Timed out: ${label}`);
}
async function run() {
  fs.mkdirSync(evidence, { recursive: true });
  if (process.argv.includes("--offers-only")) {
    await verifyQuestionOffers();
    app.quit();
    return;
  }
  const win = new BrowserWindow({ width: 1100, height: 820, show: false, webPreferences: {
    contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
  } });
  const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`);
  const notify = (method, params) => win.webContents.send("test:server-event", {
    kind: "notification", workdir: repoRoot, message: { method, params },
  });
  await win.loadFile(path.join(desktopRoot, "out/renderer/index.html"));
  await until(() => evaluate(() => Boolean(document.querySelector(".composer textarea"))), "composer");
  async function submit(text) {
    await evaluate((value) => {
      const input = document.querySelector(".composer textarea");
      input.focus();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }, text);
    await evaluate(() => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
  }
  assert(await evaluate(() => document.querySelector('[data-wuu-state="send"]')?.getAttribute("aria-busy") !== "true"));
  await submit("Review the request lifecycle");
  const creation = await until(() => gates.get("thread/start"), "thread creation gate");
  await until(() => evaluate(() => document.querySelector('[data-wuu-state="submitting"]')?.getAttribute("aria-busy") === "true"), "creation submission feedback");
  for (const [theme, width, font] of [["light", 1100, 14], ["dark", 760, 20]]) {
    win.setSize(width, 820);
    await evaluate(setVisualTheme, theme, font);
    fs.writeFileSync(path.join(evidence, `submitting-${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
  }
  await submit("Then verify the queue order");
  await submit("Keep these messages after Stop");
  assert.equal(queued.length, 0);
  await evaluate(() => document.querySelector('.composer-pending-drawer button[aria-expanded]').click());
  await until(() => evaluate(() => document.querySelectorAll(".composer-queue-list li").length === 2), "two buffered messages");
  await until(() => evaluate(() => parseInt(document.querySelector(".turn-process-meta")?.textContent) >= 2), "local waiting timer");
  creation.resolve();
  const admission = await until(() => gates.get("turn/start"), "turn admission gate");
  assert(await evaluate(() => Boolean(document.querySelector('[data-wuu-state="submitting"]'))), "submission feedback survives thread creation");
  const { threadId, turnId, text, clientId } = admission.params;
  const turn = { id: turnId, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [
    { id: `user-${threadId}`, type: "user_message", status: "completed", text, source_id: clientId },
  ] };
  notify("turn/started", { thread_id: threadId, turn });
  await until(() => evaluate(() => document.querySelectorAll(".assistant-turn-shell").length === 1), "single acknowledged turn");
  assert(await evaluate(() => parseInt(document.querySelector(".turn-process-meta")?.textContent) >= 2));
  await evaluate(() => document.querySelector(".composer-stop-button").click());
  const interruption = await until(() => gates.get("turn/interrupt"), "interrupt dispatch before admission response");
  await until(() => evaluate(() => document.querySelector('[data-wuu-state="pending"]')?.getAttribute("aria-busy") === "true"), "stop feedback");
  for (const [theme, width, font] of [["light", 1100, 14], ["dark", 760, 20]]) {
    win.setSize(width, 820);
    await evaluate(setVisualTheme, theme, font);
    await sleep(100);
    fs.writeFileSync(path.join(evidence, `stopping-${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
    assert(await evaluate(() => {
      const button = document.querySelector('[data-wuu-state="pending"]');
      const rect = button.getBoundingClientRect();
      const icon = button.querySelector("svg").getBoundingClientRect();
      return button.disabled && rect.right <= innerWidth && icon.width > 0 && icon.right <= rect.right;
    }));
  }
  interruption.resolve();
  await sleep(50);
  assert(await evaluate(() => Boolean(document.querySelector('[data-wuu-state="pending"]'))), "RPC acknowledgement alone must not claim stopped");
  notify("turn/completed", { thread_id: threadId, turn: { ...turn, status: "interrupted" } });
  admission.resolve();
  await until(() => queued.length === 2, "held follow-ups");
  assert.deepEqual(queued.map((item) => item.text), ["Then verify the queue order", "Keep these messages after Stop"]);
  assert(queued.every((item) => item.hold === true && item.threadId === threadId));
  await until(() => evaluate(() => !document.querySelector('[data-wuu-state="pending"]')), "confirmed stop");
  assert(await evaluate(() => !document.querySelector(".composer-stop-button")), "late admission must not resurrect the turn");
  fs.writeFileSync(path.join(evidence, "confirmed-stop.png"), (await win.webContents.capturePage()).toPNG());
  console.log("PASS: immediate queue, event-first timing, graphical Stop, terminal-before-RPC, ordered held inputs, two rendered layouts");
  win.destroy();
  await verifyHistoryEdits();
  await verifyQuestionOffers();
  app.quit();
}

async function verifyHistoryEdits() {
  for (const outcome of ["edit-failure", "send-failure", "stop", "success"]) {
    gates.clear();
    const win = new BrowserWindow({ width: 1100, height: 820, show: false, webPreferences: {
      contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
      preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
    } });
    const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`);
    await win.loadFile(path.join(desktopRoot, "out/renderer/index.html"));
    await until(() => evaluate(() => Boolean(document.querySelector(".composer textarea"))), "history fixture composer");
    await evaluate(() => {
      const input = document.querySelector(".composer textarea");
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Original question");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await evaluate(() => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    (await until(() => gates.get("thread/start"), "fixture creation")).resolve();
    const admission = await until(() => gates.get("turn/start"), "fixture admission");
    assert(await evaluate(() => Boolean(document.querySelector('[data-wuu-state="submitting"]'))), "normal admission feedback");
    admission.resolve();
    await until(() => evaluate(() => Boolean(document.querySelector('[data-wuu-state="stop"]'))), "RPC acceptance ends submission");
    await until(() => evaluate(() => Boolean(document.querySelector('[data-user-message-id^="user-"]'))), "fixture acknowledgement");
    const threadID = admission.params.threadId;
    win.webContents.send("test:server-event", { kind: "notification", workdir: repoRoot, message: {
      method: "turn/completed", params: { thread_id: threadID, turn: {
        id: admission.params.turnId, status: "interrupted", items_view: "full",
        items: [{ id: `user-${threadID}`, type: "user_message", text: "Original question" }],
      } },
    } });
    await until(() => evaluate(() => !document.querySelector(".composer-stop-button")), "fixture stopped");
    gates.delete("turn/start");
    await evaluate(() => document.querySelector(".message-edit-button").click());
    await until(() => evaluate(() => Boolean(document.querySelector("[data-user-message-id] textarea"))), "history editor");
    await evaluate(() => {
      const input = document.querySelector("[data-user-message-id] textarea");
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Replacement question: preserve this draft if the network fails.");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await evaluate(() => document.querySelector("[data-user-message-id] textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    const edit = await until(() => gates.get("thread/edit-message"), "edit preparation");
    await until(() => evaluate(() => document.querySelector("[data-user-message-id] textarea")?.disabled &&
      document.querySelector('[data-wuu-state="submitting"]')?.getAttribute("aria-busy") === "true"), "shared preparation feedback");
    assert.equal(gates.has("turn/start"), false);
    if (outcome === "success") {
      for (const [theme, width, font] of [["light", 1100, 14], ["dark", 760, 20]]) {
        win.setSize(width, 820);
        await evaluate(setVisualTheme, theme, font);
        fs.writeFileSync(path.join(evidence, `history-pending-${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
    if (outcome === "stop") await evaluate(() => document.querySelector(".composer-stop-button").click());
    if (outcome === "edit-failure") edit.reject(new Error("Edit unavailable"));
    else edit.resolve();
    if (outcome === "success" || outcome === "send-failure") {
      const sending = await until(() => gates.get("turn/start"), "replacement admission");
      assert.equal(sending.params.text, "Replacement question: preserve this draft if the network fails.");
      assert(await evaluate(() => !document.querySelector("[data-user-message-id] textarea") &&
        document.querySelector('[data-wuu-state="submitting"]')?.getAttribute("aria-busy") === "true"), "submission feedback spans history preparation and admission");
      if (outcome === "success") {
        fs.writeFileSync(path.join(evidence, "history-awaiting-admission.png"), (await win.webContents.capturePage()).toPNG());
        win.webContents.send("test:server-event", { kind: "notification", workdir: repoRoot, message: {
          method: "turn/started", params: { thread_id: threadID, turn: {
            id: sending.params.turnId, status: "in_progress", items_view: "full",
            items: [{ id: `user-${threadID}`, type: "user_message", text: sending.params.text, source_id: sending.params.clientId }],
          } },
        } });
        await until(() => evaluate(() => document.querySelector('[data-wuu-state="stop"]')?.getAttribute("aria-busy") !== "true" &&
          Boolean(document.querySelector('[data-wuu-state="stop"]'))), "event acknowledgement ends submission before RPC returns");
      }
      if (outcome === "send-failure") sending.reject(new Error("Send unavailable"));
      else sending.resolve();
    }
    await until(() => evaluate(outcome => {
      const editor = document.querySelector("[data-user-message-id] textarea");
      if (outcome === "edit-failure") return editor && !editor.disabled && editor.value.startsWith("Replacement question");
      if (outcome === "send-failure") return !editor && document.querySelector(".composer textarea")?.value.startsWith("Replacement question");
      if (outcome === "stop") return !editor && !document.querySelector(".composer-stop-button");
      return !editor && Boolean(document.querySelector('[data-user-message-id^="user-"]'));
    }, outcome), `history ${outcome} settlement`);
    if (outcome !== "success") assert(await evaluate(() => !document.querySelector(".composer-stop-button") &&
      !document.querySelector('[aria-busy="true"]')), "failure and cancellation clear busy feedback");
    if (outcome === "edit-failure" || outcome === "stop") assert.equal(gates.has("turn/start"), false);
    fs.writeFileSync(path.join(evidence, `history-${outcome}.png`), (await win.webContents.capturePage()).toPNG());
    win.destroy();
  }
  console.log("PASS: history preparation, edit/send failures preserve input, Stop prevents admission, success, two rendered layouts");
}
async function verifyQuestionOffers() {
  for (const action of ["answer", "cancel", "multiple"]) {
    gates.clear();
    queued.length = 0;
    const win = new BrowserWindow({ width: 1100, height: 820, show: false, webPreferences: {
      contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
      preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
    } });
    const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`);
    // A hidden window only paints on request; wait for React to commit and paint before each capture.
    const paint = () => evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const capture = async (name) => {
      await paint();
      fs.writeFileSync(path.join(evidence, name), (await win.webContents.capturePage()).toPNG());
    };
    await win.loadFile(path.join(desktopRoot, "out/renderer/index.html"));
    await until(() => evaluate(() => Boolean(document.querySelector(".composer textarea"))), "offer composer");
    await evaluate(() => {
      const input = document.querySelector(".composer textarea");
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Ask for implementation choices");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await evaluate(() => document.querySelector(".composer textarea").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    (await until(() => gates.get("thread/start"), "offer thread")).resolve();
    const admission = await until(() => gates.get("turn/start"), "offer turn");
    admission.resolve();
    await until(() => evaluate(() => Boolean(document.querySelector('[data-wuu-state="stop"]'))), "running offer turn");
    const questions = action === "multiple" ? [
      { id: "language", question: "Which language?", options: [{ label: "Go" }, { label: "Rust" }] },
      { id: "tests", question: "Which verification should accompany this change?", allow_custom: true },
    ] : [{ id: "path", question: "Which path?", options: [{ label: "Safe" }] }];
    for (const [id, items] of [["first", questions], ["second", [{ id: "next", question: "Next independent offer", options: [{ label: "Proceed" }] }]]]) {
      win.webContents.send("test:server-event", { kind: "notification", workdir: repoRoot, message: {
        method: "user-question/requested", params: { request: {
          request_id: id, plugin_id: "ask-user", execution_id: `exec-${id}`, thread_id: admission.params.threadId,
          turn_id: admission.params.turnId, mode: "offer", created_at: new Date().toISOString(),
          questions: items,
        } },
      } });
    }
    await until(() => evaluate(() => Boolean(document.querySelector('.user-question-card [role="radio"]'))), "first offer");
    if (action === "multiple") {
      assert.equal(await evaluate(() => document.querySelectorAll(".user-question-field").length), 2, "all offered questions are rendered");
      await evaluate(() => document.querySelector('.user-question-card [role="radio"]').click());
      assert.equal(gates.has("user-question/respond"), false, "partial answers are not submitted");
      (await until(() => gates.get("user-question/hold"), "hold while answering")).resolve();
      await evaluate(() => {
        const input = document.querySelector(".user-question-card input");
        input.focus();
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, "Integration tests");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      for (const [theme, width, font] of [["light", 1100, 14], ["dark", 760, 20]]) {
        win.setSize(width, 820);
        await evaluate(setVisualTheme, theme, font);
        await capture(`offer-multiple-${theme}-${width}.png`);
        assert(await evaluate(() => {
          const card = document.querySelector(".user-question-card").getBoundingClientRect();
          return card.top >= 0 && card.bottom <= innerHeight && card.left >= 0 && card.right <= innerWidth;
        }), "multi-question card fits viewport");
      }
      await evaluate(() => document.querySelector(".user-question-submit").focus());
      pressEnter(win);
    } else {
      await evaluate(action => document.querySelector(action === "answer" ? '.user-question-card [role="radio"]' : ".user-question-skip").click(), action);
    }
    const method = action === "cancel" ? "user-question/cancel" : "user-question/respond";
    const response = await until(() => gates.get(method), `${action} first offer`);
    assert.equal(response.params.requestId, "first");
    if (action === "multiple") assert.deepEqual(response.params.answer.answers, [
      { id: "language", selected: ["Go"] }, { id: "tests", selected: [], custom: "Integration tests" },
    ]);
    assert(await evaluate(() => [...document.querySelectorAll(".user-question-card button")].every(button => button.disabled)), "pending offer disables repeat actions");
    await capture(`offer-${action}-pending.png`);
    response.resolve({ request_id: "first", resolved: true });
    await until(() => evaluate(() => document.querySelector(".user-question-card")?.textContent.includes("Next independent offer")), "next offer");
    assert(await evaluate(() => !document.querySelector('.user-question-card [role="radio"]').disabled), "next offer must not inherit submitting state");
    if (action !== "cancel") await until(() => queued.length === 1, "first answer steered");
    if (action === "multiple") assert(queued[0].text.includes("Integration tests") && queued[0].text.includes("Go"));
    gates.delete("user-question/respond");
    await evaluate(() => document.querySelector('.user-question-card [role="radio"]').focus());
    pressEnter(win);
    const next = await until(() => gates.get("user-question/respond"), "keyboard answer next offer");
    assert.deepEqual(next.params, { requestId: "second", answer: { answers: [{ id: "next", selected: ["Proceed"] }] } });
    next.resolve({ request_id: "second", resolved: true });
    await until(() => evaluate(() => !document.querySelector(".user-question-card")), "offers cleared");
    assert(await evaluate(() => Boolean(document.querySelector(".composer textarea"))), "composer restored");
    if (action === "multiple") {
      win.webContents.send("test:server-event", { kind: "notification", workdir: repoRoot, message: {
        method: "user-question/requested", params: { request: {
          request_id: "long", plugin_id: "ask-user", execution_id: "exec-long", thread_id: admission.params.threadId,
          turn_id: admission.params.turnId, mode: "offer", created_at: new Date().toISOString(),
          questions: Array.from({ length: 8 }, (_, index) => ({ id: `q${index}`, allow_custom: true,
            question: `Question ${index + 1}: Explain the verification approach for this implementation, including recovery when the operation fails.` })),
        } },
      } });
      await until(() => evaluate(() => document.querySelectorAll(".user-question-card input").length === 8), "eight-question offer");
      assert(await evaluate(() => {
        const card = document.querySelector(".user-question-card");
        const rect = card.getBoundingClientRect();
        return card.scrollHeight > card.clientHeight && rect.top >= 0 && rect.bottom <= innerHeight;
      }), "long offer is bounded and scrollable");
      await evaluate(() => {
        const inputs = [...document.querySelectorAll(".user-question-card input")];
        inputs.at(-1).focus();
      });
      await until(() => evaluate(() => document.querySelector(".user-question-card").scrollTop > 0), "keyboard focus scrolls final question into view");
      assert(await evaluate(() => {
        const rect = document.activeElement.getBoundingClientRect();
        return rect.top >= 0 && rect.bottom <= innerHeight;
      }), "last input remains reachable");
      await capture("offer-eight-questions-focus.png");
      gates.get("user-question/hold")?.resolve();
    }
    console.log(`PASS: ${action} offer, pending feedback, next offer keyboard answer, composer restored`);
    win.destroy();
  }
}
app.whenReady().then(run).catch((error) => { console.error(error); app.exit(1); });
