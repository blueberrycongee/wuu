const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
app.setPath("userData", fs.mkdtempSync(path.join(desktopRoot, "out", "composer-height-e2e-")));
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktopRoot, "..");
app.whenReady().then(run).catch((error) => { console.error(error); app.exit(1); });

async function run() {
  const win = new BrowserWindow({
    width: 1200, height: 820, show: process.env.WUU_E2E_VISIBLE === "true",
    webPreferences: {
      preload: path.join(__dirname, "resize-e2e-preload.cjs"),
      contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
    },
  });
  const errors = [];
  let phase = "initial load";
  win.webContents.on("console-message", ({ message }) => {
    if (message.includes("ResizeObserver loop")) errors.push(`${phase}: ${message}`);
  });
  await win.loadFile(process.env.WUU_E2E_RENDERER || path.join(desktopRoot, "out", "renderer", "index.html"));
  await waitFor(win, () => !!document.querySelector(".turn"));
  await evaluate(win, () => {
    for (const toggle of document.querySelectorAll('.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]')) toggle.click();
    if (!document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) document.querySelector(".sidebar-toggle-button").click();
  });

  for (const variant of ["populated", "empty"]) {
    if (variant === "empty") {
      await evaluate(win, () => document.querySelector(".session-tab-new").click());
      await waitFor(win, () => !!document.querySelector(".empty-home-inner"));
    }
    for (const [width, height, fontSize, theme] of [
      [1200, 820, 14, "light"], [600, 820, 20, "dark"], [390, 700, 14, "light"], [800, 390, 20, "dark"],
    ]) {
      phase = `${variant}/${width}: viewport and font resize`;
      win.setContentSize(width, height);
      await evaluate(win, (size, theme) => {
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        document.documentElement.dataset.theme = theme;
      }, fontSize, theme);
      const label = `${variant}/${width}x${height}/${fontSize}/${theme}`;
      await setDraft(win, "");
      const empty = await geometry(win);
      if (variant === "populated") await checkEndClearance(win, `${label}: short input`);
      phase = `${label}: input resizing`;
      await setDraft(win, Array(5).fill("逐行输入，自动增高。").join("\n"));
      const growing = await geometry(win);
      assert.ok(growing.inputHeight > empty.inputHeight, `${label}: multiline input must grow`);
      near(growing.bottom, empty.bottom, `${label}: send row must not move`);
      await setDraft(win, Array(60).fill("长消息继续输入，达到上限后在输入区内滚动。").join("\n"));
      const full = await geometry(win);
      assert.ok(full.scrollHeight > full.inputHeight, `${label}: full input must scroll`);
      assert.ok(full.top >= 0 && full.bottom <= height, `${label}: input must stay in viewport: ${JSON.stringify(full)}`);
      near(full.bottom, empty.bottom, `${label}: capped input keeps bottom anchored`);
      if (variant === "populated") await checkEndClearance(win, `${label}: expanded input`);
      await evaluate(win, () => {
        const input = document.querySelector('[data-main-conversation-composer] textarea');
        input.setSelectionRange(input.value.length, input.value.length);
        input.scrollTop = input.scrollHeight;
      });
      await win.webContents.insertText("\n继续输入，光标保持可见。");
      await geometry(win);
      const caret = await evaluate(win, () => {
        const input = document.querySelector('[data-main-conversation-composer] textarea');
        const style = getComputedStyle(input);
        return {
          height: input.clientHeight, scrollHeight: input.scrollHeight, scrollTop: input.scrollTop,
          start: input.selectionStart, length: input.value.length,
          trailingSpace: parseFloat(style.paddingBottom) + parseFloat(style.lineHeight) - parseFloat(style.fontSize),
        };
      });
      // Chromium reveals the caret's glyph box, not the trailing line spacing
      // and padding. Those may remain below the viewport without hiding text.
      assert.ok(caret.scrollHeight - caret.height - caret.scrollTop <= caret.trailingSpace + 2,
        `${label}: native typing keeps the caret visible at the cap: ${JSON.stringify(caret)}`);
      await toggle(win);
      const manualFull = await geometry(win);
      near(manualFull.inputHeight, full.inputHeight, `${label}: auto and manual use the same cap`);
      await setDraft(win, "short");
      near((await geometry(win)).inputHeight, full.inputHeight, `${label}: manual mode stays expanded`);
      await toggle(win);
      near((await geometry(win)).inputHeight, empty.inputHeight, `${label}: toggle returns to auto sizing`);
      await setDraft(win, Array(5).fill("内容").join("\n"));
      await setDraft(win, "");
      near((await geometry(win)).inputHeight, empty.inputHeight, `${label}: clearing shrinks input`);
      if (variant === "populated") {
        await evaluate(win, () => {
          const transfer = new DataTransfer();
          transfer.items.add(new File(["%PDF-1.4 layout fixture"], "layout-fixture.pdf", { type: "application/pdf" }));
          const input = document.querySelector('[data-main-conversation-composer] input[type="file"]');
          input.files = transfer.files;
          input.dispatchEvent(new Event("change", { bubbles: true }));
        });
        await waitFor(win, () => !!document.querySelector('[data-main-conversation-composer] .composer-file-attachment'));
        await geometry(win);
        await checkEndClearance(win, `${label}: attachment`);
        await evaluate(win, () => document.querySelector('[data-main-conversation-composer] .composer-attachment-remove').click());
        await geometry(win);
      }
      console.log(`PASS ${label}`);
    }
    // Width and font changes must reflow existing text without another keystroke.
    phase = `${variant}: wrapping and font resize`;
    win.setContentSize(1200, 820);
    await setDraft(win, "Continuous words wrap automatically as the available width changes. ".repeat(5));
    const wide = await geometry(win);
    win.setContentSize(600, 820);
    const narrow = await geometry(win);
    assert.ok(narrow.inputHeight > wide.inputHeight, `${variant}: soft wrapping grows without explicit newlines`);
    win.setContentSize(1200, 820);
    near((await geometry(win)).inputHeight, wide.inputHeight, `${variant}: widening shrinks again`);
    await evaluate(win, () => document.documentElement.style.setProperty("--conversation-message-font-size", "24px"));
    assert.ok((await geometry(win)).inputHeight > wide.inputHeight, `${variant}: font changes remeasure existing draft`);
    await setDraft(win, "");
    if (variant === "populated") {
      await evaluate(win, () => {
        const scroll = document.querySelector(".conversation-pane .scroll-region");
        scroll.dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true }));
        scroll.scrollTop = 200;
        scroll.dispatchEvent(new Event("scroll"));
      });
      await waitFor(win, () => !!document.querySelector(".jump-to-latest-pill"));
      const before = await evaluate(win, () => document.querySelector(".conversation-pane .scroll-region").scrollTop);
      await setDraft(win, Array(60).fill("Reading history while writing a long draft.").join("\n"));
      const full = await geometry(win);
      const position = await evaluate(win, () => ({
        pillBottom: document.querySelector(".jump-to-latest-pill").getBoundingClientRect().bottom,
        scrollTop: document.querySelector(".conversation-pane .scroll-region").scrollTop,
      }));
      assert.ok(position.pillBottom < full.top, "Jump-to-latest must remain above the growing input");
      near(position.scrollTop, before, "Typing must preserve the history reading position");
      await setDraft(win, "");
    }
  }
  assert.deepEqual(errors, [], "Resizing must settle without ResizeObserver loops");
  console.log("Composer height verified with actual Chromium layout.");
  if (process.env.WUU_E2E_KEEP_OPEN === "true") {
    await setDraft(win, "输入框会随内容自动长高。\n手动展开后保持高度。\n再次点击恢复自动调整。\n达到上限后，内容在框内滚动。");
    win.showInactive();
    return;
  }
  win.close();
  app.quit();
}

function near(actual, expected, message) {
  assert.ok(Math.abs(actual - expected) <= 2, `${message}: ${actual} vs ${expected}`);
}
async function checkEndClearance(win, label) {
  // Window-resize settlement deliberately defers the dock measurement. Wait
  // for that contract, not just for the textarea's own rectangle to stabilize.
  await waitFor(win, () => {
    const pane = document.querySelector(".conversation-pane");
    const dock = pane.querySelector(".dock-composer-wrap");
    const frame = dock.querySelector(".composer-frame");
    const expected = Math.ceil(dock.getBoundingClientRect().height)
      + (parseFloat(getComputedStyle(frame).getPropertyValue("--composer-expanded-offset")) || 0);
    return !document.documentElement.matches(".window-resizing, .layout-motion-active")
      && Math.abs(parseFloat(getComputedStyle(pane).getPropertyValue("--dock-composer-height")) - expected) < 1;
  });
  await evaluate(win, () => {
    const scroll = document.querySelector(".conversation-pane .scroll-region");
    scroll.scrollTop = scroll.scrollHeight;
  });
  await geometry(win);
  const measured = await evaluate(win, () => {
    const input = document.querySelector('[data-main-conversation-composer] .composer-frame');
    const turn = document.querySelector('.cached-conversation-pane[data-active="true"] .turn[data-latest-turn="true"]');
    const pane = document.querySelector(".conversation-pane");
    const scroll = pane.querySelector(".scroll-region");
    return {
      gap: input.getBoundingClientRect().top - turn.getBoundingClientRect().bottom,
      dockHeight: getComputedStyle(pane).getPropertyValue("--dock-composer-height"),
      expandedOffset: getComputedStyle(input).getPropertyValue("--composer-expanded-offset"),
      flowPadding: getComputedStyle(turn.closest(".conversation-width")).paddingBottom,
      scrollTop: scroll.scrollTop, scrollMax: scroll.scrollHeight - scroll.clientHeight,
    };
  });
  // Allow one CSS pixel for fractional zoom and scroll quantization.
  assert.ok(measured.gap >= 31 && measured.gap <= 53, `${label}: keep a comfortable, unobstructed end gap: ${JSON.stringify(measured)}`);
  console.log(`PASS end clearance ${label}: ${measured.gap}px`);
}
function evaluate(win, fn, ...args) {
  return win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`, true);
}
async function setDraft(win, value) {
  await evaluate(win, (value) => {
    const input = document.querySelector('[data-main-conversation-composer] textarea');
    input.focus({ preventScroll: true });
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
}
async function toggle(win) {
  await evaluate(win, () => document.querySelector('[data-main-conversation-composer] .composer-expand-button').click());
}
async function waitFor(win, predicate) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(win, predicate)) return;
    await evaluate(win, () => new Promise(requestAnimationFrame));
  }
  throw new Error(`Timed out: ${predicate}`);
}
async function geometry(win) {
  return evaluate(win, () => new Promise((resolve, reject) => {
    let last = "", stable = 0;
    const deadline = performance.now() + 10000;
    function sample() {
      const input = document.querySelector('[data-main-conversation-composer] textarea');
      const frame = input.closest(".composer-frame").getBoundingClientRect();
      const value = {
        width: innerWidth, inputHeight: input.offsetHeight, scrollHeight: input.scrollHeight,
        top: frame.top, bottom: frame.bottom,
      };
      const next = JSON.stringify(value);
      stable = next === last ? stable + 1 : 0;
      last = next;
      if (stable >= 5) return setTimeout(() => resolve(value), 0);
      if (performance.now() > deadline) return reject(new Error(`Geometry did not settle: ${last}`));
      requestAnimationFrame(sample);
    }
    requestAnimationFrame(sample);
  }));
}
