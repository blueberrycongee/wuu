const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

// Run after npm run build. Uses the real renderer with an isolated mock bridge;
// no provider, user settings, or existing desktop profile is accessed.
const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_SCROLL_E2E_OUTPUT || path.join(desktop, "out/scroll-input");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_STREAM_E2E_CWD = path.dirname(desktop);
const evaluate = (win, fn, ...args) => win.webContents.executeJavaScript(
  `(${fn})(${args.map(value => JSON.stringify(value)).join(",")})`, true,
);
async function until(win, fn) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const result = await evaluate(win, fn);
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`Timed out: ${fn}`);
}
const frames = win => evaluate(win, () => new Promise(resolve => {
  let remaining = 30;
  const tick = () => --remaining ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}));
const geometry = win => evaluate(win, () => {
  const node = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
  return { top: node.scrollTop, height: node.scrollHeight, viewport: node.clientHeight };
});
function emit(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD,
    kind: "notification", message: { method, params },
  });
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width: 1200, height: 820, show: false,
    webPreferences: {
      preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  win.webContents.on("console-message", (_event, level, message) => {
    if (level >= 2) console.error(message);
  });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(win, () => !!document.querySelector(".composer textarea"));
  await evaluate(win, () => {
    const input = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Scroll input regression");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await frames(win);
  await evaluate(win, () => document.querySelector(".composer-send-button").click());
  await until(win, () => !!document.querySelector(".turn"));
  const threadID = "thread-immediate-title-e2e";
  const now = new Date().toISOString();
  const results = [];
  for (const [width, theme, font] of [[1200, "light", 14], [600, "dark", 20]]) {
    win.setContentSize(width, 780);
    await evaluate(win, (theme, font) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--appearance-scale", String(font / 14));
    }, theme, font);
    await frames(win);
    for (const input of ["keyboard", "touch", "scrollbar", "wheel"]) {
      const id = `${width}-${input}`;
      const text = Array.from({ length: 45 }, (_, i) => `Paragraph ${i + 1}: Read this message while output continues below.`).join("\n\n");
      const agent = { id: `agent-${id}`, type: "agent_message", status: "in_progress", text };
      const turn = { id, status: "in_progress", items_view: "full", started_at: now, items: [
        { id: `user-${id}`, type: "user_message", status: "completed", text: "Keep the reading position stable." }, agent,
      ] };
      emit(win, "thread/resumed", { thread: {
        id: threadID, preview: "Scroll input regression", model_provider: "e2e", model: "mock-stream",
        cwd: path.dirname(desktop), status: "running", created_at: now, updated_at: now, turns: [turn],
      } });
      await frames(win);
      await evaluate(win, () => {
        const node = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
        node.scrollTop = node.scrollHeight;
      });
      await frames(win);
      const before = await geometry(win);
      assert.ok(before.height - before.viewport > 200, "Fixture must scroll");
      assert.ok(Math.abs(before.height - before.viewport - before.top) <= 1, "Start following at latest");
      if (input === "wheel") {
        // Native Chromium input also checks the established wheel path.
        win.webContents.sendInputEvent({ type: "mouseWheel", x: Math.round(width * 0.7), y: 300, deltaY: 240, deltaX: 0 });
        await until(win, () => {
          const node = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
          return node.scrollHeight - node.clientHeight - node.scrollTop > 100;
        });
        await frames(win);
      } else {
        // Deterministically hold the input-to-scroll gap open. These events do
        // not perform native default scrolling; real layout/streaming still run.
        await evaluate(win, input => {
          const node = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
          if (input === "keyboard") node.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true }));
          else if (input === "scrollbar") node.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
          else {
            const touch = y => new Touch({ identifier: 1, target: node, clientY: y });
            node.dispatchEvent(new TouchEvent("touchstart", { touches: [touch(100)], bubbles: true }));
            node.dispatchEvent(new TouchEvent("touchmove", { touches: [touch(140)], bubbles: true }));
          }
        }, input);
      }
      const reading = await geometry(win);
      // Seed the live buffer from the persisted snapshot, then append output.
      emit(win, "item/agentMessage/delta", { thread_id: threadID, turn_id: id, item_id: agent.id,
        delta: text + "\n\nNew streamed output must not take over the viewport.\n\nSCROLL_INPUT_END",
      });
      await until(win, () => document.querySelector(".agent-text")?.textContent.includes("SCROLL_INPUT_END"));
      await frames(win);
      const after = await geometry(win);
      results.push({ width, theme, font, input, before, reading, after });
      fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(results, null, 2));
      fs.writeFileSync(path.join(output, `${id}.png`), (await win.webContents.capturePage()).toPNG());
      assert.ok(after.height > reading.height, "Stream must grow the real layout");
      assert.ok(Math.abs(after.top - reading.top) <= 1, `Stream stole scroll: ${JSON.stringify(results.at(-1))}`);
      await evaluate(win, () => {
        window.dispatchEvent(new PointerEvent("pointerup"));
        const node = document.querySelector(".scroll-region:not(.workspace-scroll-region)");
        node.dispatchEvent(new TouchEvent("touchend", { touches: [] }));
        node.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
        node.scrollTop = node.scrollHeight;
      });
      await frames(win);
      emit(win, "item/agentMessage/delta", { thread_id: threadID, turn_id: id, item_id: agent.id,
        delta: Array.from({ length: 8 }, (_, i) => `\n\nResumed paragraph ${i + 1}: Output continues after returning to latest.`).join("") + "\n\nFollowing resumed.",
      });
      await until(win, () => document.querySelector(".agent-text")?.textContent.includes("Following resumed."));
      await frames(win);
      const resumed = await geometry(win);
      results.at(-1).resumed = resumed;
      fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(results, null, 2));
      assert.ok(resumed.height > after.height, `Resumed stream must grow the layout: ${JSON.stringify(results.at(-1))}`);
      assert.ok(Math.abs(resumed.height - resumed.viewport - resumed.top) <= 1, "Returning to latest must resume follow");
      console.log(`PASS ${id} ${theme}/${font}: reading drift ${after.top - reading.top}px; follow resumed`);
    }
  }
  win.close();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
