// Run after npm run build with the current checkout's Wuu Dev host:
// "build/dev-host/Wuu Dev.app/Contents/MacOS/Electron" scripts/sidebar-recents-e2e.cjs
// Writes screenshots and results.json under out/sidebar-recents-e2e-*.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = fs.mkdtempSync(path.join(desktop, "out/sidebar-recents-e2e-"));
app.setPath("userData", path.join(output, "profile"));
process.env.WUU_RECENTS_E2E_CWD = path.dirname(desktop);
const group = '[data-functional-group-id="recents"]';
const body = `${group} [data-sidebar-recents]`;
const checks = [];
const check = (message) => { checks.push(message); console.log(message); };
app.whenReady().then(async () => {
  const timer = setTimeout(() => app.exit(1), 120000);
  const win = new BrowserWindow({
    width: 1200, height: 900, show: true, title: "Wuu Dev — Recent conversations",
    webPreferences: { preload: path.join(__dirname, "sidebar-recents-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false },
  });
  const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value ?? null)})`);
  const wait = async (fn, value) => {
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const result = await evaluate(fn, value);
      if (result) return result;
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
    throw new Error(`Timed out: ${fn}`);
  };
  const titles = () => evaluate((selector) =>
    [...document.querySelectorAll(`${selector} .thread-row-title`)].map((el) => el.textContent), body);
  const click = async (selector) => {
    const point = await evaluate(async (selector) => {
      const el = document.querySelector(selector);
      if (!el) throw new Error(`Missing: ${selector}`);
      el.scrollIntoView({ block: "nearest" });
      await new Promise(requestAnimationFrame);
      const r = el.getBoundingClientRect();
      return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
    }, selector);
    win.webContents.sendInputEvent({ type: "mouseMove", ...point });
    for (const type of ["mouseDown", "mouseUp"])
      win.webContents.sendInputEvent({ type, ...point, button: "left", clickCount: 1 });
  };
  const frame = () => evaluate(async () => {
    for (const animation of document.getAnimations()) {
      if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
    }
    await new Promise(requestAnimationFrame);
    await new Promise(requestAnimationFrame);
  });
  const capture = async (name) => {
    await frame();
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  };
  const load = async () => {
    await win.loadFile(path.join(desktop, "out/renderer/index.html"));
    await wait((selector) => document.querySelector(`${selector} .thread-row-title`), body);
  };
  try {
    await load();
    assert.equal((await titles()).length, 10);
    assert.equal((await titles())[0], "Review navigation and keyboard access");
    assert.ok((await titles()).includes("Quick question"));
    assert.ok(!(await titles()).some((title) => ["pinned", "archived", "child", "coordinator", "temporary"].includes(title)));
    check("Global catalog, initial ten, and excluded sessions");
    await click(`${body} .sidebar-heading-text-action`);
    await wait((selector) => document.querySelectorAll(`${selector} .thread-row-title`).length === 15, body);
    check("Show more reveals all older conversations");
    await click(`${body} .thread-row-main`);
    await wait(() => window.recentsEvidence.resumed().includes("recent-0"));
    check("Recent entry opens the original conversation");
    await evaluate((selector) => {
      const row = [...document.querySelectorAll(`${selector} .thread-row-main`)]
        .find((el) => el.textContent.includes("Conversation 14"));
      row.dataset.recentsOldest = "";
    }, body);
    await click("[data-recents-oldest]");
    await wait(() => window.recentsEvidence.resumed().includes("recent-13"));
    await click(`${body} .sidebar-heading-text-action`);
    await frame();
    assert.ok((await titles()).includes("Conversation 14"));
    await click(`${body} .thread-row-main`);
    await wait((selector) => document.querySelectorAll(`${selector} .thread-row-title`).length === 10, body);
    check("Show less retains a selected older conversation until selection changes");
    await click(`${body} .sidebar-heading-text-action`);
    await wait((selector) => document.querySelectorAll(`${selector} .thread-row-title`).length === 15, body);
    const before = await titles();
    win.webContents.send("test:recents-event", { method: "turn/completed", params: { thread_id: "recent-12",
      turn: { id: "older", status: "completed", started_at: "2026-01-01T10:00:00Z",
        completed_at: new Date().toISOString(), items: [] } } });
    await frame();
    assert.deepEqual(await titles(), before);
    win.webContents.send("test:recents-event", { method: "turn/started", params: { thread_id: "recent-12",
      turn: { id: "followup", status: "in_progress", started_at: new Date().toISOString(),
        items: [{ id: "user", type: "user_message", text: "Continue" }] } } });
    await wait((selector) => document.querySelector(`${selector} .thread-row-title`)?.textContent === "Conversation 13", body);
    check("Completion preserves order; a new user turn moves to the top");
    await click(`${group} .sidebar-functional-action`);
    await wait(() => document.querySelector('[role="menuitem"]'));
    await capture("project-filter-menu");
    await click('[role="menuitem"]');
    await wait((selector) => document.querySelectorAll(`${selector} .thread-row-title`).length === 1, body);
    assert.deepEqual(await titles(), ["Quick question"]);
    await load();
    assert.deepEqual(await titles(), ["Quick question"]);
    check("Hide projects persists across a renderer reload");
    win.webContents.send("test:recents-event", { method: "thread/updated", params: {
      thread: { id: "scratch", title: "Quick question", cwd: "/tmp/wuu-recents-scratch",
        workspace_kind: "scratch", status: "idle", archived: true, turns: [],
        created_at: "2026-01-01T11:10:00Z", updated_at: "2026-01-01T11:55:00Z" } } });
    await wait((selector) => document.querySelector(`${selector} [role="status"]`), body);
    await capture("empty-filtered-recents");
    check("Archiving the last matching conversation shows the empty state");
    await load();
    await click(`${group} .sidebar-functional-action`);
    await wait(() => document.querySelector('[role="menuitem"]'));
    await click('[role="menuitem"]');
    await wait((selector) => document.querySelectorAll(`${selector} .thread-row-title`).length === 10, body);
    await click(`${group} .sidebar-functional-heading-toggle`);
    await frame();
    await win.reload();
    await wait((selector) => document.querySelector(`${selector} .sidebar-functional-heading-toggle`)?.getAttribute("aria-expanded") === "false", group);
    check("Collapsed state persists across reload");
    await click(`${group} .sidebar-functional-heading-toggle`);
    await frame();
    for (const [theme, size, width] of [["light", 15, 1200], ["dark", 15, 1200], ["light", 20, 900], ["dark", 20, 900]]) {
      win.setContentSize(width, 900);
      await evaluate(({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        window.dispatchEvent(new Event("wuu-content-size-change"));
        document.querySelector(".sidebar-main").scrollTop = 0;
      }, { theme, size });
      await capture(`${theme}-${size}-${width}`);
      await evaluate((selector) => {
        const header = document.querySelector(selector);
        const scroll = document.querySelector(".sidebar-main");
        scroll.scrollTop += header.getBoundingClientRect().top - scroll.getBoundingClientRect().top - 12;
      }, group);
      await capture(`${theme}-${size}-${width}-recent-rows`);
    }
    check("Captured light/dark, default/large text, wide/narrow viewports");
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ checks, executable: process.execPath, checkout: path.dirname(desktop) }, null, 2));
    console.log(JSON.stringify({ output, checks }, null, 2));
    clearTimeout(timer);
    if (!process.env.WUU_RECENTS_KEEP_OPEN) app.quit();
  } catch (error) {
    await capture("failure");
    console.error(error);
    console.error(output);
    app.exit(1);
  }
});
