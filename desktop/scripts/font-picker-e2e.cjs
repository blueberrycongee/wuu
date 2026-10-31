const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_FONT_PICKER_OUTPUT || path.join(desktop, "out/e2e/font-picker"));
const profile = path.join(output, "profile");
const fonts = [
  "Noto Sans CJK HK",
  "Noto Sans CJK JP",
  "Noto Sans CJK KR",
  "AnExceptionallyLongFontFamilyNameWithoutSpacesForTesting",
];
const report = { platform: process.platform, cases: [], failures: [] };
fs.mkdirSync(output, { recursive: true });
fs.rmSync(profile, { recursive: true, force: true });
app.setPath("userData", profile);
process.env.WUU_RESIZE_E2E_CWD = path.dirname(desktop);

let win;
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true);
async function waitFor(fn, value) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, value)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out waiting for ${fn}`);
}
async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation => animation.playState === "running" &&
      Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
  });
}
async function click(selector) {
  await waitFor(selector => Boolean(document.querySelector(selector)), selector);
  await evaluate(selector => {
    const button = document.querySelector(selector);
    button.scrollIntoView({ block: "center" });
    button.click();
  }, selector);
}
async function inspect(selector, description, scenario) {
  const result = await evaluate(selector => {
    const element = document.querySelector(selector);
    if (!element) return null;
    const rect = element.getBoundingClientRect();
    return { text: element.textContent, width: element.clientWidth, scrollWidth: element.scrollWidth,
      height: element.clientHeight, scrollHeight: element.scrollHeight, bounds: rect.toJSON() };
  }, selector);
  scenario.checks.push({ description, ...result });
  if (!result || !result.width || result.scrollWidth > result.width + 1 || result.scrollHeight > result.height + 1) {
    report.failures.push(`${scenario.name}: ${description} is clipped: ${JSON.stringify(result)}`);
  }
}
async function capture(name) {
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
}
async function run() {
  await app.whenReady();
  win = new BrowserWindow({ width: 1280, height: 860, show: false, webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true,
    nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  win.show();
  win.focus();
  await waitFor(() => Boolean(document.querySelector(".conversation-pane")));
  await evaluate(fonts => {
    window.queryLocalFonts = async () => fonts.map(family => ({ family }));
    localStorage.setItem("wuu.appearance.v1", JSON.stringify({ codeFont: fonts[3] }));
  }, fonts);
  await click(".sidebar-account-trigger");
  await click('[data-settings-page="providers"]');
  await click('.settings-nav-item[data-wuu-page="appearance"]');
  await waitFor(() => document.querySelectorAll('[data-testid="settings-typography"] .appearance-font-picker').length === 3);

  const trigger = index => `[data-testid="settings-typography"] .settings-row:nth-child(${index + 2}) .select-menu-trigger`;
  for (const { width, size, theme } of [
    { width: 1280, size: 15, theme: "light" },
    { width: 1280, size: 20, theme: "dark" },
    { width: 760, size: 15, theme: "light" },
    { width: 760, size: 20, theme: "dark" },
  ]) {
    win.setContentSize(width, 860);
    await evaluate(({ size, theme }) => {
      document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
      document.documentElement.dataset.theme = theme;
    }, { size, theme });
    await settle();
    const scenario = { name: `${width}-${size}-${theme}`, checks: [] };
    report.cases.push(scenario);

    for (let index = 0; index < 3; index++) {
      const selector = trigger(index);
      await waitFor(selector => Boolean(document.querySelector(selector)), selector);
      await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: "center" }), selector);
      await settle();
      await inspect(`${selector} .select-menu-value`, `picker ${index + 1} current value`, scenario);
      await click(selector);
      await waitFor(() => Boolean(document.querySelector('.select-menu-panel[role="menu"]')));
      if (index < 2) {
        await waitFor(name => [...document.querySelectorAll('.select-menu-item')].some(item => item.dataset.value === name), fonts[1]);
      }
      await settle();
      const bounds = await evaluate(() => document.querySelector('.select-menu-panel[role="menu"]').getBoundingClientRect().toJSON());
      if (bounds.left < -1 || bounds.right > width + 1 || bounds.top < -1 || bounds.bottom > 861) {
        report.failures.push(`${scenario.name}: menu outside viewport: ${JSON.stringify(bounds)}`);
      }
      const optionNames = index === 2
        ? await evaluate(() => [...document.querySelectorAll('.select-menu-item')].map(item => item.dataset.value))
        : fonts;
      for (const name of optionNames) {
        const label = await evaluate(name => {
          const item = [...document.querySelectorAll('.select-menu-item')].find(element => element.dataset.value === name);
          const label = item?.querySelector('.select-menu-item-label');
          if (!label) return null;
          return { text: label.textContent, width: label.clientWidth, scrollWidth: label.scrollWidth,
            height: label.clientHeight, scrollHeight: label.scrollHeight };
        }, name);
        scenario.checks.push({ description: `picker ${index + 1} option ${name}`, ...label });
        if (!label || !label.width || label.scrollWidth > label.width + 1 || label.scrollHeight > label.height + 1) {
          report.failures.push(`${scenario.name}: picker ${index + 1} option ${name} is clipped: ${JSON.stringify(label)}`);
        }
      }
      await capture(`${scenario.name}-picker-${index + 1}`);
      if (index === 0) {
        await waitFor(() => document.activeElement === document.querySelector('.select-menu-search input'));
        await win.webContents.insertText(fonts[1]);
        await waitFor(name => {
          const items = [...document.querySelectorAll('.select-menu-item')];
          return items.length === 1 && items[0].dataset.value === name;
        }, fonts[1]);
        for (const keyCode of ["Down", "Return"]) {
          win.webContents.sendInputEvent({ type: "keyDown", keyCode });
          if (keyCode === "Return") win.webContents.sendInputEvent({ type: "char", keyCode: "\r" });
          win.webContents.sendInputEvent({ type: "keyUp", keyCode });
          if (keyCode === "Down") {
            await waitFor(name => document.activeElement?.dataset.value === name, fonts[1]);
          }
        }
        await waitFor(() => !document.querySelector('.select-menu-panel[role="menu"]'));
        await settle();
        await inspect(`${selector} .select-menu-value`, "selected interface font", scenario);
        const selection = await evaluate(selector => ({
          text: document.querySelector(`${selector} .select-menu-value`)?.textContent,
          saved: JSON.parse(localStorage.getItem("wuu.appearance.v1")).uiFont,
          focused: document.activeElement === document.querySelector(selector),
        }), selector);
        assert.deepEqual(selection, { text: fonts[1], saved: fonts[1], focused: true }, "Keyboard selection persists and returns focus");
      } else {
        await evaluate(() => document.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })));
        await waitFor(() => !document.querySelector('.select-menu-panel[role="menu"]'));
      }
    }
  }
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  assert.deepEqual(report.failures, [], report.failures.join("\n"));
  console.log(`PASS: font picker labels are readable. Evidence: ${output}`);
}
run().then(() => app.exit(0), async error => {
  report.error = error.stack || String(error);
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  if (win && !win.isDestroyed()) await capture("failure").catch(() => {});
  console.error(error);
  app.exit(1);
});
