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
    for (const size of [14, 14.5, 15, 20]) {
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
        for (const messageStyle of ["bubble", "im"]) {
          await win.webContents.executeJavaScript(`document.documentElement.dataset.messageStyle = "${messageStyle}"`);
          const before = await measure(win);
          assert.ok(before.pageWidth <= before.viewportWidth, `Page overflow: ${JSON.stringify(before)}`);
          for (const item of before.users) {
            assert.ok(Math.abs(item.block.bottom - item.bubble.bottom) < 0.1, "Actions must not reserve an extra row");
            assert.equal(item.timeFontSize, item.textFontSize, "Timestamp must match the message font size");
            assert.ok(Math.abs(item.iconSize - item.textFontSize) < item.textFontSize * 0.15,
              "Visible action artwork must match the message text size");
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
          assert.ok(queued[0].block.bottom <= queued[1].block.top + 1, "Queued user messages must not overlap");
          let hovered;
          if (width !== 420) {
            const bubble = before.users[0].bubble;
            await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", {
              type: "mouseMoved", x: bubble.left + 8, y: bubble.top + 8,
            });
            hovered = await measure(win);
            assert.deepEqual(hovered.users.map(item => item.block), before.users.map(item => item.block),
              "Hover must not resize or move messages");
            assert.deepEqual(hovered.turns, before.turns, "Hover must not move following answers");
            assert.equal(hovered.users[0].opacity, "1", "Hover must reveal the action row");
          }
          await evaluate(win, () => document.querySelector(".user-message-actions button").focus());
          const focused = await measure(win);
          assert.deepEqual(focused.users.map(item => item.block), before.users.map(item => item.block),
            "Revealing controls must not move messages");
          assert.equal(focused.users[0].opacity, "1", "Keyboard focus must reveal its action row");
          const toolbar = focused.users[0];
          assert.ok(toolbar.actions.left >= 0 && toolbar.actions.right <= focused.viewportWidth, "Focused actions must remain in the viewport");
          assert.ok(toolbar.actions.top >= 0 && toolbar.actions.bottom <= 900 / win.webContents.getZoomFactor(), "Focused actions must remain vertically visible");
          if (width !== 420) assert.ok(Math.abs(toolbar.actions.height - toolbar.lineHeight) < 0.1, "Desktop actions must match one line of text");
          assert.deepEqual(focused.turns, before.turns, "Keyboard focus must not move following answers");
          fs.writeFileSync(path.join(output, `conversation-${messageStyle}-${width}-${size}-${theme}-focused.png`), (await win.webContents.capturePage()).toPNG());
          await evaluate(win, () => { document.activeElement.blur(); window.scrollTo(0, 0); });
          await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", x: 1, y: 1 });
          const left = await measure(win);
          assert.deepEqual(left.users.map(item => item.block), before.users.map(item => item.block),
            "Leaving the message must not change its layout");
          assert.deepEqual(left.turns, before.turns, "Leaving the message must not move following answers");
          report.push({ width, size, theme, messageStyle, before, hovered, focused, left });
          console.log("Action geometry verified");
          fs.writeFileSync(path.join(output, `conversation-${messageStyle}-${width}-${size}-${theme}.png`), (await win.webContents.capturePage()).toPNG());
        }

        if (width === 1280 && (size === 15 || size === 20)) {
          const placements = await verifyFloatingPlacements(win);
          report.push({ width, size, theme, placements });
          fs.writeFileSync(path.join(output, `floating-above-${size}-${theme}.png`), (await win.webContents.capturePage()).toPNG());
        }
        for (const notice of [false, true]) {
          await win.loadURL(`${origin}/dev/message-flow-reading/?surface=lifecycle&${query}${notice ? "&notice=1" : ""}`);
          await waitFor(win, () => !!document.querySelector(".agent-message-actions button"));
          await evaluate(win, () => document.querySelector('[aria-label="流式状态"]').click());
          await waitFor(win, () => !!document.querySelector('.agent-message-actions[aria-hidden="true"]'));
          const live = await lifecycleGeometry(win);
          await evaluate(win, () => document.querySelector('[aria-label="流式状态"]').click());
          await waitFor(win, () => !!document.querySelector(".agent-message-actions button"));
          const settled = await lifecycleGeometry(win);
          assert.ok(await evaluate(win, () => document.querySelector(".agent-message-actions time")?.dateTime),
            "Completed replies must expose their completion time with the actions");
          if (!notice) fs.writeFileSync(path.join(output, `settled-${width}-${size}-${theme}.png`), (await win.webContents.capturePage()).toPNG());
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

async function verifyFloatingPlacements(win) {
  const result = await evaluate(win, async () => {
    const block = document.querySelector(".user-message-block");
    const message = block.querySelector(".user-message");
    const paragraph = message.querySelector(".rich-paragraph");
    const actions = block.querySelector(".user-message-actions");
    const frames = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const rect = element => {
      const {top, bottom, left, right, height} = element.getBoundingClientRect();
      return {top, bottom, left, right, height};
    };
    const sample = async () => {
      block.dispatchEvent(new Event("pointerenter"));
      await frames();
      const range = document.createRange();
      range.selectNodeContents(paragraph);
      const lines = [...range.getClientRects()];
      return {placement: actions.dataset.placement, message: rect(message), actions: rect(actions),
        line: lines.at(-1), paragraph: rect(paragraph), lineHeight: parseFloat(getComputedStyle(paragraph).lineHeight)};
    };
    // Exercise real text layout on the production component at both ends of
    // the available trailing whitespace, without relying on font-specific copy.
    paragraph.textContent = "短问题。";
    actions.querySelector("button").focus({preventScroll: true});
    await frames();
    const inline = await sample();
    const style = getComputedStyle(message);
    const contentWidth = message.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
    paragraph.textContent = "测".repeat(Math.floor(contentWidth / parseFloat(getComputedStyle(paragraph).fontSize)) * 2);
    await frames();
    const below = await sample();
    const following = block.nextElementSibling;
    const originalMargin = following.style.marginTop;
    following.style.marginTop = "-10px";
    const above = await sample();
    following.style.marginTop = originalMargin;
    // A floating composer can obstruct the overlay while the scroll viewport
    // still has room. Match the product's pane-local composer boundary.
    const dock = document.createElement("div");
    dock.className = "dock-composer-wrap";
    const shell = document.createElement("div");
    shell.className = "composer-shell";
    shell.style.cssText = `position:fixed;top:${message.getBoundingClientRect().bottom + 2}px;left:0;width:100%;height:80px`;
    dock.append(shell);
    document.querySelector(".conversation-pane").append(dock);
    const composer = await sample();
    dock.remove();
    return {inline, below, above, composer};
  });
  assert.equal(result.inline.placement, "inline", "Short text should share its last line with the actions");
  assert.ok(Math.abs((result.inline.actions.top + result.inline.actions.bottom) / 2
    - (result.inline.paragraph.bottom - result.inline.lineHeight / 2)) < 0.1, "Inline actions must center on the final text line");
  assert.equal(result.below.placement, "below", "A full last line sends actions below the message");
  assert.ok(result.below.actions.top >= result.below.message.bottom, "Outside actions must clear the text frame");
  assert.equal(result.above.placement, "above", "A following message must flip the actions above");
  assert.ok(result.above.actions.bottom <= result.above.message.top, "Flipped actions must clear the top of the frame");
  assert.equal(result.composer.placement, "above", "The floating composer must also trigger a flip");
  return result;
}

function measure(win) {
  return evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const rect = node => {
      const { top, bottom, left, right, width, height } = node.getBoundingClientRect();
      return { top, bottom, left, right, width, height };
    };
    return {
      pageWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth,
      turns: [...document.querySelectorAll(".turn")].map(rect),
      users: [...document.querySelectorAll(".user-message-block")].map(block => {
        const actions = block.querySelector(".user-message-actions");
        const text = block.querySelector(".rich-paragraph");
        const icon = actions.querySelector("svg");
        const artworkHeight = icon.getBBox().height + parseFloat(getComputedStyle(icon).strokeWidth);
        return { block: rect(block), bubble: rect(block.querySelector(".user-message")),
          actions: rect(actions), buttons: [...actions.querySelectorAll("button")].map(rect),
          placement: actions.dataset.placement, timeFontSize: parseFloat(getComputedStyle(actions.querySelector("time")).fontSize),
          textFontSize: parseFloat(getComputedStyle(text).fontSize), lineHeight: parseFloat(getComputedStyle(text).lineHeight),
          iconSize: artworkHeight * icon.getBoundingClientRect().height / icon.viewBox.baseVal.height,
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
