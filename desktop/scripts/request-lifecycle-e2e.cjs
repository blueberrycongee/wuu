const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, ipcMain } = require("electron");
const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const evidence = path.join(desktopRoot, "out/e2e/request-lifecycle");
process.env.WUU_STREAM_E2E_CWD = repoRoot;
process.env.WUU_REQUEST_LIFECYCLE_E2E = "1";
app.setPath("userData", fs.mkdtempSync(path.join(require("node:os").tmpdir(), "wuu-request-lifecycle-")));
const gates = new Map();
const queued = [];
ipcMain.handle("test:request-lifecycle", (_event, method, params) => new Promise((resolve) => gates.set(method, { params, resolve })));
ipcMain.on("test:queued-input", (_event, value) => queued.push(value));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(read, label) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) { const value = await read(); if (value) return value; await sleep(25); }
  throw new Error(`Timed out: ${label}`);
}
async function run() {
  fs.mkdirSync(evidence, { recursive: true });
  const win = new BrowserWindow({ width: 1100, height: 820, show: false, webPreferences: {
    contextIsolation: true, nodeIntegration: false, sandbox: false,
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
  await submit("Review the request lifecycle");
  const creation = await until(() => gates.get("thread/start"), "thread creation gate");
  await submit("Then verify the queue order");
  await submit("Keep these messages after Stop");
  assert.equal(queued.length, 0);
  await evaluate(() => document.querySelector('.composer-pending-drawer button[aria-expanded]').click());
  await until(() => evaluate(() => document.querySelectorAll(".composer-queue-list li").length === 2), "two buffered messages");
  await until(() => evaluate(() => parseInt(document.querySelector(".turn-process-meta")?.textContent) >= 2), "local waiting timer");
  creation.resolve();
  const admission = await until(() => gates.get("turn/start"), "turn admission gate");
  const { threadId, text, clientId } = admission.params;
  const turn = { id: `turn-${threadId}`, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [
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
    await evaluate((theme, font) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--appearance-scale", String(font / 14));
    }, theme, font);
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
  app.quit();
}
app.whenReady().then(run).catch((error) => { console.error(error); app.exit(1); });
