const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

// Development fixture, production message components and styles; no app-server
// or personal profile. Assertions measure rendered behavior, not stylesheet text.
const output = path.resolve(__dirname, "../out/message-flow-layout");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://localhost:5173";
app.whenReady().then(run).catch(error => { console.error(error); app.exit(1); });

async function run() {
  const timeout = setTimeout(() => { console.error("Message layout verification timed out"); app.exit(1); }, 60000);
  const win = new BrowserWindow({
    show: false, width: 1280, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  win.webContents.debugger.attach("1.3");
  win.webContents.on("console-message", ({ level, message }) => { if (level >= 2) console.error(message); });
  const report = [];
  for (const width of [1280, 420]) {
    for (const size of [14, 14.5, 20]) {
      for (const theme of ["light", "dark"]) {
        win.setContentSize(width, 900);
        const query = `size=${size}&theme=${theme}`;
        const zoomLevel = width === 1280 && size === 14.5 ? -0.5 : 0;
        win.webContents.setZoomLevel(zoomLevel);
        console.log(`CHECK message layout ${width}/${size}/${theme}`);
        await win.loadURL(`${origin}/dev/message-flow-reading/?surface=conversation&${query}`);
        await win.webContents.debugger.sendCommand("Emulation.setTouchEmulationEnabled", { enabled: width === 420 });
        await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
          features: [{ name: "prefers-reduced-motion", value: "reduce" }],
        });
        await waitFor(win, () => document.querySelectorAll(".user-message-block").length === 3);
        const before = await measure(win);
        assert.ok(before.pageWidth <= before.viewportWidth, `Page overflow: ${JSON.stringify(before)}`);
        for (const item of before.users) {
          assert.ok(item.actions.top >= item.bubble.bottom - 0.1, "Actions must not cover the message");
          for (const button of item.buttons) {
            assert.ok(button.width >= (width === 420 ? 43.9 : 19.9) && button.height >= (width === 420 ? 43.9 : 19.9),
              `Action target too small: ${JSON.stringify(button)}`);
          }
          // Compact desktop targets use the surrounding space; their centers
          // must remain separated even at the default page zoom.
          for (let index = 1; index < item.buttons.length; index += 1) {
            const previous = item.buttons[index - 1];
            const current = item.buttons[index];
            const pitch = current.left + current.width / 2 - previous.left - previous.width / 2;
            assert.ok(pitch * win.webContents.getZoomFactor() >= 24, "Action targets must stay separated at page zoom");
          }
          if (width === 420) assert.equal(item.opacity, "1", "Touch users must discover actions without hover");
        }
        const queued = before.users.slice(1);
        assert.ok(Math.max(queued[0].block.bottom, queued[0].actions.bottom) <= queued[1].block.top + 1, "Queued user messages must not overlap");
        await evaluate(win, () => document.querySelector(".user-message-actions button").focus());
        const focused = await measure(win);
        assert.deepEqual(focused.users.map(item => item.block), before.users.map(item => item.block),
          "Revealing controls must not move messages");
        assert.equal(focused.users[0].opacity, "1", "Keyboard focus must reveal its action row");
        await evaluate(win, () => { document.activeElement.blur(); window.scrollTo(0, 0); });
        console.log("Action geometry verified");

        for (const notice of [false, true]) {
          await win.loadURL(`${origin}/dev/message-flow-reading/?surface=lifecycle&${query}${notice ? "&notice=1" : ""}`);
          await waitFor(win, () => !!document.querySelector(".agent-message-actions button"));
          await evaluate(win, () => document.querySelector('[aria-label="流式状态"]').click());
          await waitFor(win, () => !!document.querySelector('.agent-message-actions[aria-hidden="true"]'));
          const live = await lifecycleGeometry(win);
          await evaluate(win, () => document.querySelector('[aria-label="流式状态"]').click());
          await waitFor(win, () => !!document.querySelector(".agent-message-actions button"));
          const settled = await lifecycleGeometry(win);
          for (const key of ["answerHeight", "actionsHeight", "turnHeight", "nextTop"]) {
            assert.ok(Math.abs(live[key] - settled[key]) <= 1,
              `Completion moved ${key}: ${JSON.stringify({ live, settled, width, size })}`);
          }
          report.push({ width, size, theme, touch: width === 420, notice, live, settled });
        }
        console.log(`PASS message layout ${width}/${size}/${theme}`);
      }
    }
  }
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  clearTimeout(timeout);
  win.close();
  app.quit();
}

function measure(win) {
  return evaluate(win, () => {
    const rect = node => {
      const { top, bottom, left, right, width, height } = node.getBoundingClientRect();
      return { top, bottom, left, right, width, height };
    };
    return {
      pageWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth,
      users: [...document.querySelectorAll(".user-message-block")].map(block => {
        const actions = block.querySelector(".user-message-actions");
        return { block: rect(block), bubble: rect(block.querySelector(".user-message")),
          actions: rect(actions), buttons: [...actions.querySelectorAll("button")].map(rect),
          opacity: getComputedStyle(actions).opacity };
      }),
    };
  });
}

function lifecycleGeometry(win) {
  return evaluate(win, async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    return {
      answerHeight: document.querySelector(".agent-block-with-action-slot").getBoundingClientRect().height,
      actionsHeight: document.querySelector(".agent-message-actions").getBoundingClientRect().height,
      turnHeight: document.querySelector('[data-turn-id="fixture-lifecycle"]').getBoundingClientRect().height,
      nextTop: document.querySelector('[data-testid="following-turn"]').getBoundingClientRect().top,
    };
  });
}

async function waitFor(win, predicate) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(win, predicate)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out: ${predicate}`);
}

function evaluate(win, fn) {
  return win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
}
