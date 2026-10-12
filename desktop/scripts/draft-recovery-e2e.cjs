// After electron-vite build, run in a graphical session:
// electron scripts/draft-recovery-e2e.cjs
// Exercises the production App/settings/input/renderer-reload path with native
// Chromium sessionStorage and a deterministic synthetic bridge. No model calls.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const { app, BrowserWindow, ipcMain } = require("electron");
const { attachComposerFiles } = require("./composer-attachment-e2e.cjs");
const root = path.resolve(__dirname, "..");
const renderer = process.env.WUU_E2E_RENDERER || path.join(root, "out/renderer/index.html");
const output = process.env.WUU_DRAFT_RECOVERY_OUTPUT || path.join(root, "out/e2e/draft-recovery");
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-draft-recovery-"));
const key = "wuu.desktop.draftRecovery.v1";
const owner = "[data-main-conversation-composer]";
const report = { fixture: "production renderer, native storage/input, synthetic IPC", checks: [], errors: [],
  renderer, rendererSha256: crypto.createHash("sha256").update(fs.readFileSync(renderer)).digest("hex"), screenshots: [], runnerSha256: crypto.createHash("sha256").update(fs.readFileSync(__filename)).digest("hex") };
app.setPath("userData", profile);
process.env.HOME = profile;
process.env.WUU_RESIZE_E2E_CWD = profile;
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "draft-recovery-e2e-preload.cjs");
let win, context = { kind: "no_project", cwd: profile }, pendingSubmission, submissions = [];
let projects = [], initializeBlockedFor, pendingInitialize;
let holdCreation = false, pendingCreation, createdThreads = [], otherThreadCwd;
const windows = new Set();
app.on("window-all-closed", () => {});
const save = () => { fs.mkdirSync(output, { recursive: true }); fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2)); };
const pause = () => new Promise(resolve => setTimeout(resolve, 20));
const evaluate = (fn, arg, target = win) => target.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function until(fn, arg, target = win) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) { if (await evaluate(fn, arg, target)) return; await pause(); }
  throw new Error(`Timed out: ${fn}`);
}
async function waitMain(predicate, message) {
  const deadline = Date.now() + 15_000;
  while (!predicate() && Date.now() < deadline) await pause();
  assert.ok(predicate(), message);
}
async function click(selector, target = win) {
  await until(selector => Boolean(document.querySelector(selector)), selector, target);
  const point = await evaluate(selector => {
    const element = document.querySelector(selector);
    element.scrollIntoView({ block: "center" });
    const bounds = element.getBoundingClientRect();
    return { x: Math.round(bounds.x + bounds.width / 2), y: Math.round(bounds.y + bounds.height / 2) };
  }, selector, target);
  target.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
  target.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
  // Let the native click and its React updates settle before selecting input.
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), undefined, target);
}
async function ready(target = win) { await until(() => { const input = document.querySelector("[data-main-conversation-composer] textarea"); return input && !input.disabled && !input.closest("[inert]"); }, undefined, target); }
async function open() {
  const target = new BrowserWindow({ width: 1200, height: 820, show: true,
    ...(process.platform === "linux" ? { frame: false, autoHideMenuBar: true } : {}),
    webPreferences: { preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false,
      sandbox: false, backgroundThrottling: false } });
  windows.add(target);
  target.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  target.webContents.session.webRequest.onBeforeRequest(({ url }, done) => done({ cancel: /^https?:/.test(url) }));
  await target.loadFile(renderer); await ready(target);
  target.webContents.debugger.attach("1.3");
  await target.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  return target;
}
async function reload() { const loaded = new Promise(resolve => win.webContents.once("did-finish-load", resolve)); win.webContents.reload(); await loaded; await ready(); }
async function type(text) {
  await until(() => { const input = document.querySelector("[data-main-conversation-composer] textarea"); return input && !input.disabled && !input.closest("[inert]"); });
  await evaluate(() => { const input = document.querySelector("[data-main-conversation-composer] textarea"); input.focus(); input.select(); });
  await until(() => document.activeElement === document.querySelector("[data-main-conversation-composer] textarea"));
  if (text) await win.webContents.insertText(text);
  else { win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Backspace" }); win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Backspace" }); }
  await until(value => document.querySelector("[data-main-conversation-composer] textarea").value === value, text);
}
async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation => animation.playState === "running" &&
      Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
  });
}
async function input() { return evaluate(() => document.querySelector("[data-main-conversation-composer] textarea").value); }
async function stored(target = win) { return evaluate(key => JSON.parse(sessionStorage.getItem(key) || "null"), key, target); }
async function toggle(expected) {
  await click(".sidebar-account-trigger");
  await until(() => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await click('[data-settings-page="providers"]');
  await until(() => Boolean(document.querySelector(".settings-nav-item")));
  await evaluate(() => [...document.querySelectorAll(".settings-nav-item")].find(node => /^(General|常规)$/.test(node.textContent.trim())).click());
  await until(() => Boolean(document.querySelector('[data-testid="settings-draft-recovery"]')));
  assert.equal(await evaluate(() => document.querySelector('[data-testid="settings-draft-recovery"]').getAttribute("aria-checked")), String(!expected));
  await click('[data-testid="settings-draft-recovery"]');
  await until(expected => document.querySelector('[data-testid="settings-draft-recovery"]').getAttribute("aria-checked") === String(expected), expected);
  if (!report.screenshots.length) {
    await evaluate(() => document.querySelector('[data-testid="settings-draft-recovery"]').scrollIntoView({ block: "center" }));
    await settle();
    fs.writeFileSync(path.join(output, "settings-light.png"), (await win.webContents.capturePage()).toPNG());
    report.screenshots.push("settings-light.png");
    win.setContentSize(560, 820);
    await evaluate(() => {
      document.documentElement.dataset.theme = "dark";
      document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
      document.documentElement.style.setProperty("--appearance-scale", String(20 / 14));
      document.querySelector('[data-testid="settings-draft-recovery"]').scrollIntoView({ block: "center" });
    });
    await settle();
    fs.writeFileSync(path.join(output, "settings-dark-narrow-large.png"), (await win.webContents.capturePage()).toPNG());
    report.screenshots.push("settings-dark-narrow-large.png");
    win.setContentSize(1200, 820);
    await evaluate(() => {
      document.documentElement.dataset.theme = "light";
      document.documentElement.style.setProperty("--conversation-message-font-size", "14px");
      document.documentElement.style.setProperty("--appearance-scale", "1");
    });
  }
  await click(".settings-back-button"); await ready();
}
async function select(label) {
  await evaluate(label => [...document.querySelectorAll(".thread-row-main")].find(node => node.textContent.includes(label)).click(), label);
  await until(label => document.querySelector(".conversation-title-heading h1")?.textContent.includes(label), label);
}
async function send() {
  await click('[data-main-conversation-composer] [data-wuu-component="composer-send"]');
  const deadline = Date.now() + 15_000;
  while (!pendingSubmission && Date.now() < deadline) await pause();
  assert.ok(pendingSubmission, "submission reached fixture bridge");
}
async function check(name, run) { await run(); report.checks.push({ name, passed: true }); save(); console.log(`PASS ${name}`); }
app.whenReady().then(async () => {
  save();
  ipcMain.on("test:draft-context", event => { event.returnValue = context; });
  ipcMain.handle("test:draft-projects", () => projects);
  ipcMain.handle("test:draft-other-cwd", () => otherThreadCwd);
  ipcMain.handle("test:draft-created-threads", () => createdThreads);
  ipcMain.handle("test:draft-create", async (_event, base) => {
    if (holdCreation) {
      const failure = await new Promise(resolve => { pendingCreation = resolve; });
      if (failure) throw new Error(failure);
    }
    const thread = { ...base, id: `created-draft-${createdThreads.length + 1}`, preview: "Created recovery conversation", turns: [], created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
    createdThreads.push(thread); return thread;
  });
  ipcMain.handle("test:draft-initialize", event => {
    if (event.sender.id === initializeBlockedFor) return new Promise(resolve => { pendingInitialize = resolve; });
  });
  ipcMain.handle("test:draft-submit", (_event, payload) => { submissions.push(payload); return new Promise(resolve => { pendingSubmission = resolve; }); });
  win = await open();
  await check("default disabled input is never written", async () => {
    await type("Default private input"); assert.equal(await stored(), null);
    await reload(); assert.equal(await input(), "");
  });
  await check("opt-in captures input before idle publication and restores only text", async () => {
    await toggle(true);
    await attachComposerFiles(win, owner, [{ name: "only-in-memory.txt", type: "text/plain", contents: "binary-marker-not-persisted" }]);
    await type("Immediate reload draft");
    const before = await stored();
    assert.equal(before.drafts[0].prompt, "Immediate reload draft");
    assert.ok(!JSON.stringify(before).includes("binary-marker-not-persisted"));
    assert.ok(!JSON.stringify(before).includes("only-in-memory.txt"));
    await reload(); assert.equal(await input(), "Immediate reload draft");
    assert.equal(await evaluate(() => document.querySelectorAll(".composer-attachment-tray-item").length), 0);
  });
  await check("attachment preparation failure preserves text through opt-in reload without replaying bytes", async () => {
    const before = submissions.length;
    await evaluate(() => {
      const original = File.prototype.arrayBuffer;
      File.prototype.arrayBuffer = function () {
        if (this.name !== "unreadable-cross-group.txt") return original.call(this);
        return new Promise((_resolve, reject) => { window.__failDraftAttachment = () => reject(new Error("Synthetic attachment unavailable")); });
      };
    });
    await attachComposerFiles(win, owner, [{ name: "unreadable-cross-group.txt", type: "text/plain", contents: "private-attachment-marker" }]);
    await type("Recover my text after an unreadable attachment");
    await until(() => Boolean(window.__failDraftAttachment));
    await click('[data-main-conversation-composer] [data-wuu-component="composer-send"]');
    await evaluate(() => window.__failDraftAttachment());
    await until(() => Boolean(document.querySelector(".composer-attachment-failed")) && document.querySelector("[data-main-conversation-composer] textarea").value === "Recover my text after an unreadable attachment");
    assert.equal(submissions.length, before, "failed encoding must never reach the backend");
    const serialized = JSON.stringify(await stored());
    assert.ok(serialized.includes("Recover my text after an unreadable attachment"));
    assert.ok(!serialized.includes("private-attachment-marker") && !serialized.includes("unreadable-cross-group.txt"));
    await settle();
    fs.writeFileSync(path.join(output, "attachment-failed-before-reload.png"), (await win.webContents.capturePage()).toPNG());
    await reload();
    assert.equal(await input(), "Recover my text after an unreadable attachment");
    assert.equal(await evaluate(() => document.querySelectorAll(".composer-attachment-tray-item").length), 0);
    assert.equal(submissions.length, before, "recovery must not retry or auto-submit");
    await send();
    assert.equal(submissions.length, before + 1);
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve(null);
    await until(key => !sessionStorage.getItem(key), key);
    await reload(); assert.equal(await input(), "");
  });
  await check("file selection references never become stored plain text", async () => {
    await type("Typed question without its quoted file");
    await evaluate(() => {
      const panel = document.querySelector(".workspace-right-panel");
      if (panel?.getAttribute("aria-hidden") !== "false") [...document.querySelectorAll(".side-panel-toggle-button")]
        .find(button => /right sidebar|右侧栏/i.test(button.getAttribute("aria-label") || "")).click();
    });
    await click(".workspace-panel-add");
    await until(() => Boolean(document.querySelector(".workspace-tool-menu")));
    await evaluate(() => [...document.querySelectorAll(".workspace-tool-menu-item")].find(button => /^(Files|文件)$/.test(button.textContent.trim())).click());
    await until(() => Boolean(document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector('[data-item-path="private-selection.md"]')));
    await evaluate(() => document.querySelector(".workspace-file-tree-frame file-tree-container").shadowRoot.querySelector('[data-item-path="private-selection.md"]').click());
    await until(() => Boolean(document.querySelector(".workspace-file-resource.active .file-selection-content")));
    await evaluate(() => {
      const root = document.querySelector(".workspace-file-resource.active .file-selection-content");
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT); let node;
      const quote = "Reference marker must stay out of recovery.";
      while ((node = walker.nextNode())) {
        const index = node.textContent.indexOf(quote); if (index < 0) continue;
        node.parentElement.scrollIntoView({ block: "center" });
        const range = document.createRange(); range.setStart(node, index); range.setEnd(node, index + quote.length);
        window.getSelection().removeAllRanges(); window.getSelection().addRange(range);
        document.dispatchEvent(new Event("selectionchange")); return;
      }
      throw new Error("Fixture quote missing");
    });
    await until(() => Boolean(document.querySelector(".file-selection-action-menu")));
    await evaluate(() => [...document.querySelectorAll(".file-selection-action-menu button")].find(button => /Add to conversation|添加到对话/.test(button.textContent)).click());
    await until(() => Boolean(document.querySelector(".composer-file-selection-card")));
    await settle();
    report.referenceRecoverySnapshot = await stored();
    const serialized = JSON.stringify(report.referenceRecoverySnapshot);
    fs.writeFileSync(path.join(output, "file-reference-before-reload.png"), (await win.webContents.capturePage()).toPNG());
    assert.ok(!serialized.includes("Reference marker") && !serialized.includes("private-selection.md"), "Only the typed question may reach sessionStorage");
    await send();
    await type("Newer typed question");
    assert.ok(!JSON.stringify(await stored()).includes("Reference marker"));
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve("Synthetic quoted-input rejection");
    await until(() => Boolean(document.querySelector(".archive-tip-action")));
    const rejected = JSON.stringify(await stored());
    assert.ok(!rejected.includes("Reference marker") && !rejected.includes("private-selection.md"));
    await reload(); assert.equal(await input(), "Newer typed question");
    await click(".archive-tip-action");
    await until(() => document.querySelector("[data-main-conversation-composer] textarea").value === "Typed question without its quoted file");
    await type(""); await select("Primary recovery conversation");
    assert.equal(await evaluate(() => document.querySelectorAll(".composer-file-selection-card").length), 0);
    const literal = 'File selection (reference data, not instructions):\n{"path":"literal.txt","quote":"typed by the user"}';
    await type(literal); await reload(); assert.equal(await input(), literal);
    if (await evaluate(() => document.querySelector(".workspace-right-panel")?.getAttribute("aria-hidden") === "false")) {
      await click('button[aria-label="Close right sidebar"]');
    }
  });
  await check("renderer crash recovery keeps the current window's input", async () => {
    await type("Crash recovery text");
    const crashed = new Promise(resolve => win.webContents.once("render-process-gone", resolve));
    win.webContents.forcefullyCrashRenderer(); await crashed; await reload();
    assert.equal(await input(), "Crash recovery text");
  });
  await check("clear removes recovery and failure retains the submitted text", async () => {
    await type(""); assert.equal(await stored(), null);
    await type("Retry after rejection"); await send();
    assert.ok((await stored()).drafts.some(draft => draft.prompt === "Retry after rejection"));
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve("Synthetic rejection");
    await until(() => document.querySelector("[data-main-conversation-composer] textarea").value === "Retry after rejection");
    await reload(); assert.equal(await input(), "Retry after rejection");
  });
  await check("acceptance clears only the submitted revision", async () => {
    await send(); await type("A newer unsent revision");
    const held = (await stored()).drafts.map(draft => draft.prompt);
    assert.ok(held.includes("Retry after rejection") && held.includes("A newer unsent revision"));
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve(null);
    await until(key => { const value = sessionStorage.getItem(key); return value && !value.includes("Retry after rejection"); }, key);
    await reload(); assert.equal(await input(), "A newer unsent revision");
  });
  await check("an identical newly typed revision survives acceptance of the earlier copy", async () => {
    await type("Same text, newly typed"); await send(); await type("Same text, newly typed");
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve(null);
    await until(() => !document.querySelector('[data-main-conversation-composer] [data-wuu-state="submitting"]'));
    await settle(); await reload(); assert.equal(await input(), "Same text, newly typed");
    await type("A newer unsent revision");
  });
  await check("conversations keep separate text and restore the active conversation", async () => {
    await select("Other recovery conversation"); assert.equal(await input(), "");
    await type("Other conversation draft"); await reload(); assert.equal(await input(), "Other conversation draft");
    await select("Primary recovery conversation"); assert.equal(await input(), "A newer unsent revision");
  });
  await check("both split composers retain independent text through reload", async () => {
    await evaluate(() => document.querySelector(".session-message-source").click());
    await until(() => document.querySelectorAll(".conversation-split-pane textarea").length === 2);
    await settle();
    for (const [id, text] of [["resize-thread", "Split primary text"], ["draft-other", "Split secondary text"]]) {
      await click(`.conversation-split-pane[data-thread-id="${id}"] textarea`);
      await evaluate(id => { const input = document.querySelector(`.conversation-split-pane[data-thread-id="${id}"] textarea`); input.focus(); input.select(); }, id);
      await until(id => document.activeElement === document.querySelector(`.conversation-split-pane[data-thread-id="${id}"] textarea`), id);
      await win.webContents.insertText(text);
      await until(({ id, text }) => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"] textarea`).value === text, { id, text });
    }
    const drafts = (await stored()).drafts;
    assert.equal(drafts.find(draft => draft.threadID === "resize-thread").prompt, "Split primary text");
    assert.equal(drafts.find(draft => draft.threadID === "draft-other").prompt, "Split secondary text");
    await reload();
    await select("Other recovery conversation"); assert.equal(await input(), "Split secondary text");
    await select("Primary recovery conversation"); assert.equal(await input(), "Split primary text");
  });
  await check("failed split sends retain their own workspace context", async () => {
    otherThreadCwd = profile + "-secondary";
    await reload();
    await click(".session-message-source");
    await until(() => document.querySelectorAll(".conversation-split-pane textarea").length === 2); await settle();
    const selector = '.conversation-split-pane[data-thread-id="draft-other"] textarea';
    async function fillSecondary(text) {
      await click(selector);
      await evaluate(selector => { const input = document.querySelector(selector); input.focus(); input.select(); }, selector);
      await until(selector => document.activeElement === document.querySelector(selector), selector);
      await win.webContents.insertText(text);
      await until(({ selector, text }) => document.querySelector(selector).value === text, { selector, text });
    }
    await fillSecondary("Failed secondary workspace text");
    await click('.conversation-split-pane[data-thread-id="draft-other"] .composer-send-button');
    await waitMain(() => pendingSubmission, "secondary submission reached bridge");
    await fillSecondary("Newer secondary workspace text");
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve("Synthetic secondary rejection");
    await until(() => Boolean(document.querySelector(".archive-tip-action")));
    const drafts = (await stored()).drafts;
    assert.equal(drafts.find(draft => draft.prompt === "Failed secondary workspace text").context.cwd, otherThreadCwd);
    assert.equal(drafts.find(draft => draft.prompt === "Newer secondary workspace text").context.cwd, otherThreadCwd);
    // The next journey starts in the primary workspace; discard this fixture's
    // already-verified failure copy instead of reopening its notice each reload.
    await evaluate(key => {
      const data = JSON.parse(sessionStorage.getItem(key));
      data.drafts = data.drafts.filter(draft => draft.prompt !== "Failed secondary workspace text");
      sessionStorage.setItem(key, JSON.stringify(data));
    }, key);
    otherThreadCwd = undefined;
    await reload(); await select("Primary recovery conversation");
  });
  await check("a new draft survives reload without being assigned to the latest thread", async () => {
    await evaluate(() => document.querySelector(".primary-nav .nav-item").click());
    await until(() => /^(New conversation|新建对话)$/.test(document.querySelector(".conversation-title-heading h1")?.textContent ?? ""));
    await type("New conversation draft"); await reload(); assert.equal(await input(), "New conversation draft");
    assert.ok((await stored()).activeTabID.startsWith("draft:"));
  });
  await check("new conversation identity migration preserves newer input and clears only accepted text", async () => {
    const oldID = (await stored()).activeTabID;
    await send(); await type("New input after conversation creation");
    const held = await stored();
    assert.ok(held.activeTabID.startsWith("thread:created-draft-"));
    assert.ok(!held.drafts.some(draft => draft.id === oldID));
    assert.ok(held.drafts.some(draft => draft.prompt === "New conversation draft"));
    assert.ok(held.drafts.some(draft => draft.prompt === "New input after conversation creation"));
    const resolve = pendingSubmission; pendingSubmission = undefined; resolve(null);
    await until(key => !sessionStorage.getItem(key).includes("New conversation draft"), key);
    await reload(); assert.equal(await input(), "New input after conversation creation");
  });
  await check("unknown send acknowledgement recovers text without replay", async () => {
    await type("Delivery might already have happened"); await send();
    const count = submissions.length;
    const resolve = pendingSubmission; pendingSubmission = undefined;
    const crashed = new Promise(resolve => win.webContents.once("render-process-gone", resolve));
    win.webContents.forcefullyCrashRenderer(); await crashed; await reload(); await settle();
    assert.equal(await input(), "Delivery might already have happened");
    assert.equal(submissions.length, count);
    resolve(null);
  });
  await check("failed creation keeps both submitted text and newer draft", async () => {
    await click(".primary-nav .nav-item");
    await until(() => /^(New conversation|新建对话)$/.test(document.querySelector(".conversation-title-heading h1")?.textContent ?? ""));
    await type("Creation will fail"); holdCreation = true;
    await click('[data-main-conversation-composer] [data-wuu-component="composer-send"]');
    await waitMain(() => pendingCreation, "thread creation paused");
    await type("Newer input during creation");
    const resolve = pendingCreation; pendingCreation = undefined; holdCreation = false; resolve("Synthetic creation rejection");
    await until(() => Boolean(document.querySelector(".archive-tip-action")));
    await reload(); assert.equal(await input(), "Newer input during creation");
    assert.ok((await stored()).drafts.some(draft => draft.prompt === "Creation will fail"));
    await click(".archive-tip-action");
    await until(() => document.querySelector("[data-main-conversation-composer] textarea").value === "Creation will fail");
    await type("");
  });
  await check("windows are isolated and disabling purges other live windows", async () => {
    const other = await open(); assert.equal(await stored(other), null);
    assert.equal(await evaluate(() => document.querySelector("[data-main-conversation-composer] textarea").value, undefined, other), "");
    await toggle(false); assert.equal(await stored(), null);
    await until(key => sessionStorage.getItem(key) === null, key, other);
    other.destroy(); windows.delete(other);
    await toggle(true);
  });
  await check("exact workspace context prevents cross-directory restoration", async () => {
    context = { kind: "no_project", cwd: profile + "-other" };
    await reload(); assert.equal(await input(), "");
    context = { kind: "no_project", cwd: profile }; await reload();
  });
  await check("expired, oversized and malformed snapshots never restore", async () => {
    await type("Expire this draft");
    await evaluate(key => { const data = JSON.parse(sessionStorage.getItem(key)); data.drafts.forEach(draft => { draft.updatedAt = Date.now() - 25 * 60 * 60 * 1000; }); sessionStorage.setItem(key, JSON.stringify(data)); }, key);
    await reload(); assert.equal(await input(), ""); assert.equal(await stored(), null);
    await type("x".repeat(32 * 1024 + 1)); assert.equal(await stored(), null);
    await evaluate(key => sessionStorage.setItem(key, "{broken"), key);
    await reload(); assert.equal(await input(), ""); assert.equal(await stored(), null);
  });
  await check("retention caps, schema versions and consent generations are enforced", async () => {
    await type("Bounded draft");
    await evaluate(key => {
      const data = JSON.parse(sessionStorage.getItem(key));
      const context = data.drafts[0].context;
      data.activeTabID = "";
      data.drafts = Array.from({ length: 20 }, (_, index) => ({ id: `draft:fixture:${index}`, kind: "draft", context,
        prompt: `Retained ${index}`, updatedAt: Date.now() - 1000 - index,
        images: [{ data: "excluded-binary" }], credentials: "excluded-credential-field" }));
      sessionStorage.setItem(key, JSON.stringify(data));
    }, key);
    await reload(); await type("Newest bounded draft");
    const capped = await stored();
    assert.equal(capped.drafts.length, 20);
    assert.ok(capped.drafts.some(draft => draft.prompt === "Newest bounded draft"));
    assert.ok(!JSON.stringify(capped).includes("excluded-"));
    await evaluate(key => { const data = JSON.parse(sessionStorage.getItem(key)); data.version = 99; sessionStorage.setItem(key, JSON.stringify(data)); }, key);
    await reload(); assert.equal(await input(), ""); assert.equal(await stored(), null);
    await type("Old consent draft"); const old = await stored();
    await toggle(false); await toggle(true);
    await evaluate(({ key, old }) => sessionStorage.setItem(key, JSON.stringify(old)), { key, old });
    await reload(); assert.equal(await input(), ""); assert.equal(await stored(), null);
  });
  await check("relocated inactive projects cannot retain drafts for an old directory", async () => {
    await type("Current context stays available");
    projects = [{ id: "relocated-project", name: "Relocated project", path: profile + "-new", created_at: new Date().toISOString(), updated_at: new Date().toISOString() }];
    await evaluate(({ key, cwd }) => {
      const data = JSON.parse(sessionStorage.getItem(key));
      data.drafts.push({ id: "draft:recovery:relocated-project", kind: "draft", context: { kind: "project", project_id: "relocated-project", cwd }, prompt: "Old directory private draft", updatedAt: Date.now() });
      sessionStorage.setItem(key, JSON.stringify(data));
    }, { key, cwd: profile + "-old" });
    await reload();
    assert.equal(await input(), "Current context stays available");
    assert.ok(!(await stored()).drafts.some(draft => draft.prompt === "Old directory private draft"));
    projects = [];
  });
  for (const reenable of [false, true]) await check(`consent ${reenable ? "rollover" : "disable"} while startup is loading discards unread recovery`, async () => {
    await type("Revoked before runtime loaded");
    const other = await open();
    const loadingWindow = win;
    initializeBlockedFor = win.webContents.id;
    const loading = reload();
    await waitMain(() => pendingInitialize, "runtime initialization paused after recovery read");
    assert.ok((await stored()).drafts.some(draft => draft.prompt === "Revoked before runtime loaded"));
    win = other; await toggle(false); if (reenable) await toggle(true); win = loadingWindow;
    initializeBlockedFor = undefined;
    const resume = pendingInitialize; pendingInitialize = undefined; resume();
    await loading;
    assert.equal(await input(), "");
    assert.equal(await stored(), null);
    other.destroy(); windows.delete(other);
    if (!reenable) await toggle(true);
  });
  await check("closing the window discards recovery", async () => {
    await type("Window-only draft"); win.destroy(); windows.delete(win);
    win = await open(); assert.equal(await input(), ""); assert.equal(await stored(), null);
  });
  report.submissions = submissions; report.passed = true; save();
  for (const target of windows) target.destroy(); app.quit();
}).catch(async error => {
  if (win && !win.isDestroyed()) {
    try {
      report.failureInputs = await evaluate(() => [...document.querySelectorAll("textarea")].map(input => ({ value: input.value, disabled: input.disabled, focused: input === document.activeElement, owner: input.closest("[data-main-conversation-composer]")?.getAttribute("data-main-conversation-composer") })));
      fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
    } catch { /* Preserve the original failure if its renderer is gone. */ }
  }
  report.errors.push(String(error.stack || error)); report.passed = false; save();
  for (const target of windows) if (!target.isDestroyed()) target.destroy();
  console.error(error); app.exit(1);
});
