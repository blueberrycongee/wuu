// Run with wuu-e2e so keyboard focus and screenshots stay on the isolated display.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const output = path.join(desktopRoot, "out", "codex-command-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_STREAM_E2E_CWD = output;
const threadID = "thread-immediate-title-e2e";
const command = "rg -n 'ToolActivity' desktop/src/renderer\ngit diff -- desktop/src/renderer/ToolActivity.tsx\n";
const result = "<img src=x onerror=alert(1)>\n" + "  ToolActivity.tsx: command details\n".repeat(100);
const item = {
  id: "codex-command", type: "tool_call", name: "exec", status: "in_progress",
  arguments: JSON.stringify({ command, cwd: "/repo/a workspace", commandActions: [] }),
};
const turn = {
  id: "codex-command-turn", status: "in_progress", items_view: "full",
  started_at: new Date().toISOString(),
  items: [{ id: "command-user", type: "user_message", status: "completed", text: "Inspect command details" }],
};

function evaluate(win, fn) { return win.webContents.executeJavaScript(`(${fn.toString()})()`, true); }
function emit(win, method, params) {
  win.webContents.send("test:server-event", { workdir: output, kind: "notification", message: { method, params } });
}
async function waitFor(win, fn) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const value = await evaluate(win, fn);
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "failure.html"), await evaluate(win, () => document.body.innerHTML));
  throw new Error(`Timed out: ${fn.toString()}`);
}
async function settle(win) {
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await evaluate(win, async () => {
    await Promise.all(document.getAnimations()
      .filter(animation => animation.playState === "running" && Number.isFinite(animation.effect?.getComputedTiming().endTime))
      .map(animation => animation.finished.catch(() => {})));
  });
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1200, height: 960, show: true, webPreferences: {
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
    contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  win.focus();
  await win.loadFile(path.join(desktopRoot, "out/renderer/index.html"));
  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")));
  await evaluate(win, () => {
    const input = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Show Codex commands");
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  });
  await waitFor(win, () => document.querySelector(".conversation-width")?.textContent.includes("Show Codex commands"));
  await evaluate(win, () => {
    document.querySelector(".environment-panel-close-row button")?.click();
    if (!document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) {
      document.querySelector(".sidebar-toggle-button").click();
    }
  });
  await waitFor(win, () => !document.querySelector(".environment-panel"));
  await settle(win);
  emit(win, "turn/started", { thread_id: threadID, turn });
  emit(win, "item/started", { thread_id: threadID, turn_id: turn.id, item });
  await waitFor(win, () => document.querySelector('details[data-tool="exec"] summary')?.textContent.includes("rg -n"));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('details[data-tool="exec"] pre'))), false);

  // Exercise the browser's native summary keyboard behavior on the real VM display.
  await evaluate(win, () => document.querySelector('details[data-tool="exec"] summary').focus());
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Space" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Space" });
  await waitFor(win, () => document.querySelector('details[data-tool="exec"]')?.open);
  await waitFor(win, () => document.querySelector('details[data-tool="exec"] pre code'));
  assert.equal(await evaluate(win, () => document.activeElement === document.querySelector('details[data-tool="exec"] summary')), true);
  assert.equal(await evaluate(win, () => document.activeElement.matches(":focus-visible")), true);
  assert.equal(await evaluate(win, () => document.querySelector('details[data-tool="exec"] pre code')?.textContent), command);

  emit(win, "item/completed", { thread_id: threadID, turn_id: turn.id, item: { ...item, status: "completed", result } });
  await waitFor(win, () => document.querySelector('details[data-tool="exec"] .command-tool-output pre code')?.textContent.includes("ToolActivity.tsx"));
  assert.equal(await evaluate(win, () => document.querySelector('details[data-tool="exec"]')?.open), true);
  assert.equal(await evaluate(win, () => document.querySelector('details[data-tool="exec"] .command-tool-output pre code')?.textContent), result);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('details[data-tool="exec"] img'))), false);

  emit(win, "item/completed", { thread_id: threadID, turn_id: turn.id, item: {
    ...item, id: "codex-command-long", status: "completed",
    arguments: JSON.stringify({ command: "rg --files " + "long-directory/".repeat(50), cwd: "/repo" }), result: "Exit 0",
  } });
  await waitFor(win, () => document.querySelectorAll('details[data-tool="exec"]').length === 2);
  await evaluate(win, () => document.querySelector(".process-surface-fold > summary").click());
  await waitFor(win, () => document.querySelector(".process-surface-fold")?.open);
  await evaluate(win, () => document.querySelector('details[data-tool="exec"] summary').click());
  await waitFor(win, () => document.querySelector('details[data-tool="exec"] .command-tool-output pre'));
  const evidence = [];
  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      for (const width of [1200, 760]) {
        win.setContentSize(width, 960);
        await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--conversation-message-font-size','${size}px');document.documentElement.style.setProperty('--appearance-scale','${size / 14}');window.dispatchEvent(new Event('wuu-content-size-change'));`);
        await settle(win);
        await evaluate(win, () => document.querySelector('details[data-tool="exec"]').scrollIntoView({ block: "start" }));
        await settle(win);
        const geometry = await evaluate(win, () => {
          const rows = [...document.querySelectorAll('details[data-tool="exec"]')];
          const output = rows[0].querySelector(".command-tool-output pre");
          return {
            rows: rows.map(row => {
              const summary = row.querySelector("summary").getBoundingClientRect();
              const label = row.querySelector(".activity-summary-text").getBoundingClientRect();
              const chevron = row.querySelector(".program-tool-chevron").getBoundingClientRect();
              return { left: summary.left, right: summary.right, labelRight: label.right, chevronLeft: chevron.left, chevronRight: chevron.right };
            }),
            scrollable: output.scrollHeight > output.clientHeight,
            outputHeight: output.clientHeight,
            overflow: document.querySelector(".conversation-pane").scrollWidth > document.querySelector(".conversation-pane").clientWidth,
            reachable: (() => {
              const chevron = rows[0].querySelector(".program-tool-chevron").getBoundingClientRect();
              return rows[0].querySelector("summary").contains(document.elementFromPoint(chevron.x + chevron.width / 2, chevron.y + chevron.height / 2));
            })(),
          };
        });
        assert.equal(geometry.overflow, false, "Command source must not widen the conversation");
        assert.equal(geometry.reachable, true, "The command disclosure must not be clipped or covered");
        assert.equal(geometry.scrollable, true, "Long output must remain scrollable");
        assert.ok(geometry.outputHeight <= 320, "Long output must stay bounded");
        for (const row of geometry.rows) {
          assert.ok(row.right <= width + 1 && row.left >= 0, "Command row must fit the viewport");
          assert.ok(row.labelRight <= row.chevronLeft && row.chevronRight <= row.right + 1, "Long commands must leave the disclosure reachable");
        }
        evidence.push({ theme, size, width, geometry });
        fs.writeFileSync(path.join(output, `${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
  }
  await evaluate(win, () => {
    const output = document.querySelector(".command-tool-output pre");
    output.scrollTop = output.scrollHeight;
    document.querySelector('details[data-tool="exec"] summary').click();
  });
  await waitFor(win, () => !document.querySelector('details[data-tool="exec"]')?.open);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('details[data-tool="exec"] pre'))), false);
  fs.writeFileSync(path.join(output, "collapsed.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(evidence, null, 2));
  console.log("Codex command E2E passed: native keyboard disclosure, literal input/output, completion, and eight layout variants.");
  app.exit(0);
}).catch(async error => {
  console.error(error);
  const win = BrowserWindow.getAllWindows()[0];
  if (win) {
    fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
    fs.writeFileSync(path.join(output, "failure.html"), await evaluate(win, () => document.body.innerHTML));
  }
  app.exit(1);
});
