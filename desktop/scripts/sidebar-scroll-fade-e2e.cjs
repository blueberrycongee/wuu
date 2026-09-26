// Real product renderer and Chromium scroll timelines with synthetic IPC data.
// Run after building: electron scripts/sidebar-scroll-fade-e2e.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = fs.mkdtempSync(path.join(desktop, "out", "sidebar-scroll-fade-e2e-"));
app.setPath("userData", path.join(output, "profile"));
process.env.WUU_STREAM_E2E_CWD = path.resolve(desktop, "..");
process.env.WUU_STREAM_E2E_SIDEBAR_THREADS = "48";
const results = [];

app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Scroll fade E2E timed out"); app.exit(1); }, 60000);
  const win = new BrowserWindow({
    width: 1100, height: 820, show: true,
    webPreferences: {
      preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  let focusWindow;
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error") console.error(`Renderer: ${message}`);
  });
  const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
  const waitFor = async (fn) => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      if (await evaluate(fn)) return;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`Timed out: ${fn}`);
  };
  const sample = async (label, selector = ".sidebar-main") => {
    const state = await evaluate(async (selector) => {
      await new Promise(requestAnimationFrame);
      await new Promise(requestAnimationFrame);
      const node = document.querySelector(selector);
      const style = getComputedStyle(node);
      return {
        following: document.documentElement.hasAttribute("data-stream-following"),
        focused: document.hasFocus(),
        scrollTop: node.scrollTop, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight,
        mask: style.maskImage,
        top: parseFloat(style.getPropertyValue("--scroll-fade-top")),
        bottom: parseFloat(style.getPropertyValue("--scroll-fade-bottom")),
      };
    }, selector);
    results.push({ label, selector, ...state });
    return state;
  };
  const unchanged = (before, after, label) => {
    for (const key of ["scrollTop", "scrollHeight", "clientHeight", "mask", "top", "bottom"]) {
      assert.equal(after[key], before[key], `${label}: sidebar ${key} stays independent`);
    }
  };
  const emit = (method, params) => win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD, kind: "notification", message: { method, params },
  });
  const capture = async (name) => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  try {
    await win.loadURL("about:blank");
    win.webContents.debugger.attach("1.3");
    const media = (features = [], media = "screen") => win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      media, features: [{ name: "prefers-reduced-motion", value: "no-preference" }, ...features],
    });
    await media();
    const threadID = "sidebar-fade-0";
    const now = new Date().toISOString();
    const turn = {
      id: "fade-turn", status: "in_progress", started_at: now, items_view: "full",
      items: [
        { id: "fade-user", type: "user_message", status: "completed", text: "Inspect scroll boundaries." },
        { id: "fade-reasoning", type: "reasoning", status: "in_progress", text: "Reasoning line.\n\n".repeat(70) },
        { id: "fade-answer", type: "agent_message", status: "in_progress", text: "Streaming answer paragraph.\n\n".repeat(45) },
      ],
    };

    for (const theme of ["light", "dark"]) {
      for (const size of [14, 20]) {
        const label = `${theme}-${size}`;
        win.setContentSize(size === 14 ? 1100 : 940, 820);
        await win.loadFile(path.join(desktop, "out/renderer/index.html"));
        await waitFor(() => Boolean(document.querySelector(".thread-list-more")));
        const fitting = await sample(`${label}-initial-fitting-list`);
        assert.ok(fitting.scrollHeight === fitting.clientHeight && fitting.top === 0 && fitting.bottom === 0, "an initially fitting list has no faded edges");
        await evaluate(async () => {
          while (document.querySelector(".thread-list-more")) {
            document.querySelector(".thread-list-more").click();
            await new Promise(requestAnimationFrame);
          }
        });
        await waitFor(() => document.querySelectorAll(".thread-row").length === 48);
        await evaluate(() => document.querySelector(".thread-row-main").click());
        await waitFor(() => Boolean(document.querySelector(".thread-row.active")));
        app.focus({ steal: true });
        win.focus();
        await waitFor(() => document.hasFocus());
        await evaluate(({ theme, size }) => {
          document.documentElement.dataset.theme = theme;
          document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
          window.dispatchEvent(new Event("wuu-content-size-change"));
        }, { theme, size });
        assert.ok(await evaluate(() => document.querySelector(".sidebar").getBoundingClientRect().right > 200), "sidebar remains visible at the tested width");
        await evaluate(() => { const node = document.querySelector(".sidebar-main"); node.scrollTop = node.scrollHeight / 2; });
        const idle = await sample(`${label}-idle`);
        assert.ok(idle.scrollTop > 0 && idle.top > 0 && idle.bottom > 0 && idle.mask !== "none", "overflow fades both edges");

        await evaluate(() => {
          const sidebar = document.querySelector(".sidebar-main");
          const bounds = sidebar.getBoundingClientRect();
          const row = [...sidebar.querySelectorAll(".thread-row")].find(node => {
            const rect = node.getBoundingClientRect();
            return rect.top > bounds.top + 60 && rect.bottom < bounds.bottom - 60;
          });
          row.querySelector(".thread-row-main").focus({ preventScroll: true });
        });
        unchanged(idle, await sample(`${label}-sidebar-focus`), "sidebar focus");
        await evaluate(() => document.querySelector(".composer textarea").focus({ preventScroll: true }));
        unchanged(idle, await sample(`${label}-composer-focus`), "composer focus");
        if (!focusWindow) {
          focusWindow = new BrowserWindow({ width: 220, height: 120, show: true });
          await focusWindow.loadURL("about:blank");
        }
        focusWindow.show();
        win.blur();
        focusWindow.focus();
        await waitFor(() => !document.hasFocus());
        unchanged(idle, await sample(`${label}-window-blur`), "window blur");
        win.focus();
        await waitFor(() => document.hasFocus());
        focusWindow.hide();
        unchanged(idle, await sample(`${label}-window-focus`), "window focus");

        await capture(`${label}-idle`);
        emit("turn/started", { thread_id: threadID, turn });
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        await waitFor(() => Boolean(document.querySelector(".turn-reasoning-scroll")));
        await evaluate(() => document.querySelector(".turn-reasoning-summary").click());
        await waitFor(() => document.querySelector(".turn-reasoning-scroll").clientHeight > 0);
        const following = await sample(`${label}-following`);
        await capture(`${label}-following`);
        unchanged(idle, following, "stream following");
        assert.equal((await sample(`${label}-reasoning-following`, ".turn-reasoning-scroll")).mask, "none", "live inspection retains paint degradation");
        emit("item/agentMessage/delta", { thread_id: threadID, turn_id: turn.id, item_id: "fade-answer", delta: `${turn.items[2].text}Fresh streamed content.` });
        await waitFor(() => document.querySelector(".agent-text")?.textContent.includes("Fresh streamed content."));
        unchanged(idle, await sample(`${label}-delta`), "streamed content");

        await evaluate(() => document.querySelector('[data-pip-anchor-host="conversation"]').dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true })));
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-paused`), "paused following");
        assert.notEqual((await sample(`${label}-reasoning-paused`, ".turn-reasoning-scroll")).mask, "none", "inspection fade returns while reading");

        await evaluate(() => { document.querySelector('[data-pip-anchor-host="conversation"]').scrollTop = 0; });
        await waitFor(() => Boolean(document.querySelector("button.jump-to-latest-pill")));
        await evaluate(() => document.querySelector("button.jump-to-latest-pill").click());
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-resumed`), "resumed following");

        // Retain the streamed snapshot in the IPC fixture so normal resume
        // requests can switch away and back without losing the running turn.
        const snapshot = await evaluate(async () => (await window.wuu.resumeThread("sidebar-fade-0")).thread);
        emit("thread/updated", { thread: { ...snapshot, status: "in_progress", turns: [turn] } });
        await evaluate(() => document.querySelectorAll(".sidebar-main .thread-row-main")[1].click());
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-switch-idle`), "switch to idle conversation");
        await evaluate(() => document.querySelector(".sidebar-main .thread-row-main").click());
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-switch-running`), "switch to running conversation");

        emit("turn/completed", { thread_id: threadID, turn: { ...turn, status: "completed", completed_at: now } });
        await waitFor(() => !document.querySelector(".composer-stop-button"));
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-completed`), "turn completion");

        await evaluate(() => { document.querySelector(".sidebar-main").scrollTop = 0; });
        const top = await sample(`${label}-top`);
        assert.ok(top.top === 0 && top.bottom > 0, "top boundary leaves only the bottom fade");
        await evaluate(() => { const node = document.querySelector(".sidebar-main"); node.scrollTop = node.scrollHeight; });
        const bottom = await sample(`${label}-bottom`);
        assert.ok(bottom.top > 0 && bottom.bottom === 0, "bottom boundary leaves only the top fade");
      }
    }

    const unreadTurn = { ...turn, id: "fade-unread-turn", items: turn.items.map(item => ({ ...item, id: `${item.id}-unread` })) };
    for (let index = 1; index < 48; index++) {
      emit("turn/started", { thread_id: `sidebar-fade-${index}`, turn: { id: `background-${index}`, status: "in_progress", started_at: now, items: [] } });
    }
    emit("turn/started", { thread_id: threadID, turn: unreadTurn });
    await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
    await evaluate(() => document.querySelector(".sidebar-notifications-button").click());
    await waitFor(() => document.querySelectorAll(".sidebar-unread-row").length === 48);
    await evaluate(() => { const node = document.querySelector(".sidebar-unread-view"); node.scrollTop = node.scrollHeight / 2; });
    const unread = await sample("unread-following", ".sidebar-unread-view");
    assert.ok(unread.top > 0 && unread.bottom > 0 && unread.mask !== "none", "unread overflow keeps both fades during streaming");
    await evaluate(() => document.querySelector('[data-pip-anchor-host="conversation"]').dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true })));
    await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
    unchanged(unread, await sample("unread-paused", ".sidebar-unread-view"), "unread pause");
    await capture("unread-paused");
    await evaluate(() => document.querySelector(".sidebar-notifications-button").click());
    await waitFor(() => Boolean(document.querySelector(".sidebar-main")));

    for (const [name, features] of [
      ["reduced-motion", [{ name: "prefers-reduced-motion", value: "reduce" }]],
      ["forced-colors", [{ name: "forced-colors", value: "active" }]],
      ["print", []],
    ]) {
      await media(features, name === "print" ? "print" : "screen");
      assert.equal((await sample(name)).mask, "none", `${name} remains unmasked`);
    }
    await media();
    await evaluate(() => { document.documentElement.dataset.appearanceMotion = "reduce"; });
    assert.equal((await sample("app-reduced-motion")).mask, "none");
    console.log("PASS: sidebar fades track scroll bounds across stream, control/window focus, light/dark, 14/20px, wide/narrow, and accessibility states.");
  } catch (error) {
    await capture("failure");
    throw error;
  } finally {
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ versions: process.versions, results }, null, 2));
    console.log(`Evidence: ${output}`);
    clearTimeout(timeout);
    focusWindow?.destroy();
    win.destroy();
  }
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
