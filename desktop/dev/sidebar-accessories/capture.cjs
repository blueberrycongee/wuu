// Manual rendered-geometry review, not a CSS-source or golden-image merge gate.
// Start Vite with this directory's config, then run with desktop's Electron.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const output = process.env.WUU_SIDEBAR_OUTPUT || path.resolve(__dirname, "../../../artifacts/sidebar-accessories");
// Compare the pre-refinement surfaces with the current product renderer.
const baseline = process.env.WUU_SIDEBAR_BASELINE === "1";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));

const measure = () => {
  const selectors = {
    collapse: ".sidebar-collapse-toggle > svg",
    bell: ".sidebar-notifications-button > svg",
    heading: ".sidebar-functional-heading-chevron",
    add: ".sidebar-functional-action > svg",
    newThread: ".project-row-new-thread > svg",
    fork: ".thread-row-fork-icon",
    spinner: ".thread-row-spinner",
    archive: ".thread-row-action.archive > svg",
    pin: ".thread-row-action:not(.archive) > svg",
    account: ".sidebar-account-trigger > svg",
  };
  const out = {};
  for (const [name, selector] of Object.entries(selectors)) {
    out[name] = [...document.querySelectorAll(selector)].map(el => {
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return { x: r.x + r.width / 2, y: r.y + r.height / 2, width: r.width,
        glyphWidth: r.width - parseFloat(s.paddingLeft) - parseFloat(s.paddingRight),
        stroke: s.strokeWidth, color: s.color, row: el.closest(".thread-row")?.className };
    });
  }
  out.headingGaps = [...document.querySelectorAll(".sidebar-functional-heading-toggle")].map(toggle => {
    const label = toggle.querySelector(".sidebar-functional-heading-label");
    const chevron = toggle.querySelector(".sidebar-functional-heading-chevron");
    if (!label || !chevron) return null;
    return chevron.getBoundingClientRect().left - label.getBoundingClientRect().right;
  });
  out.readingGaps = [...document.querySelectorAll(".thread-row:has(.thread-row-fork-icon)")].map(row => {
    const title = row.querySelector(".thread-row-title");
    const rect = title.getBoundingClientRect();
    const end = rect.right - parseFloat(getComputedStyle(title).paddingRight);
    return row.querySelector(".thread-row-fork-icon").getBoundingClientRect().left - end;
  });
  out.rows = [...document.querySelectorAll(".thread-row")].map(row => ({
    bounds: row.getBoundingClientRect().toJSON(),
    title: row.querySelector(".thread-row-title").getBoundingClientRect().toJSON(),
  }));
  out.focus = document.activeElement?.className;
  out.keyboardFocusVisible = document.documentElement.dataset.focusModality === "keyboard" && document.activeElement?.matches(":focus-visible");
  return out;
};

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: true, width: 1100, height: 960,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") { errors.push(event.message); console.error(event.message); } });
  const report = {};
  for (const theme of ["light", "dark"]) for (const size of [15, 20]) {
    const name = `${theme}-${size}`;
    win.setContentSize(size === 20 ? 900 : 1100, 960);
    await win.loadURL(`http://127.0.0.1:5208/dev/sidebar-accessories/?theme=${theme}&size=${size}&width=${size === 20 ? 260 : 296}`);
    await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
      const deadline = Date.now() + 10000;
      function ready() {
        if (document.querySelector('.thread-row-fork-icon')) return requestAnimationFrame(() => requestAnimationFrame(resolve));
        if (Date.now() > deadline) return reject(new Error('Sidebar did not mount'));
        requestAnimationFrame(ready);
      } ready();
    })`);
    if (baseline) await win.webContents.insertCSS(require("./baseline.cjs"));
    await win.webContents.executeJavaScript(`document.querySelector('.project-section .thread-row-main').click(); document.fonts.ready`);
    // Settle section transitions before measuring; no live application is touched.
    await new Promise(resolve => setTimeout(resolve, 350));
    report[name] = { idle: await win.webContents.executeJavaScript(`(${measure})()`) };
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    const point = await win.webContents.executeJavaScript(`(() => {
      const r = document.querySelector('.thread-row:has(.thread-row-fork-icon)').getBoundingClientRect();
      return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
    })()`);
    win.webContents.sendInputEvent({ type: "mouseMove", ...point });
    await new Promise(resolve => setTimeout(resolve, 350));
    report[name].hover = await win.webContents.executeJavaScript(`(${measure})()`);
    fs.writeFileSync(path.join(output, `${name}-hover.png`), (await win.webContents.capturePage()).toPNG());
    win.webContents.sendInputEvent({ type: "mouseMove", x: 800, y: 10 });
    win.focus();
    win.webContents.focus();
    await win.webContents.executeJavaScript(`document.querySelector('.thread-row:has(.thread-row-fork-icon) .thread-row-main').focus()`);
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
    await new Promise(resolve => setTimeout(resolve, 350));
    report[name].focus = await win.webContents.executeJavaScript(`(${measure})()`);
    if (!report[name].focus.keyboardFocusVisible) throw new Error(`${name}: keyboard focus is not visible`);
    fs.writeFileSync(path.join(output, `${name}-focus.png`), (await win.webContents.capturePage()).toPNG());
    console.log(`captured ${name}`);
  }
  await win.loadURL("http://127.0.0.1:5208/dev/sidebar-accessories/?empty&size=20");
  if (baseline) await win.webContents.insertCSS(require("./baseline.cjs"));
  await win.webContents.executeJavaScript("document.fonts.ready");
  await new Promise(resolve => setTimeout(resolve, 500));
  fs.writeFileSync(path.join(output, "empty.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify({ report, errors }, null, 2));
  for (const [name, states] of Object.entries(report)) {
    const idle = states.idle;
    const axis = idle.bell[0].x;
    // Trailing column: header toggle, group actions, row actions, account chevron.
    const trailing = [idle.collapse[0], ...idle.add, ...idle.newThread, idle.fork[0], ...idle.spinner, ...idle.archive, ...idle.account];
    if (trailing.some(icon => Math.abs(icon.x - axis) > 0.1)) throw new Error(`${name}: trailing column drift`);
    // Row disclosure marks stay in the column beside the actions. Group
    // heading chevrons sit immediately after their labels instead.
    const second = [...idle.pin, ...idle.fork.slice(1)];
    if (second.some(icon => Math.abs(icon.x - idle.pin[0].x) > 0.1)) throw new Error(`${name}: disclosure column drift`);
    if (idle.heading.length === 0) throw new Error(`${name}: missing heading chevrons`);
    if (idle.headingGaps.some(gap => gap == null || gap < 0 || gap > 12)) {
      throw new Error(`${name}: heading chevron not beside label (${idle.headingGaps.join(", ")})`);
    }
    if (idle.add.length && idle.heading.some(icon => icon.x >= idle.add[0].x - 8)) {
      throw new Error(`${name}: heading chevron drifted into the action column`);
    }
    for (const [state, measurement] of Object.entries(states)) {
      if (measurement.readingGaps.some(gap => gap <= 0)) throw new Error(`${name}/${state}: title overlaps accessories`);
      if (state !== "idle" && measurement.fork[0].x >= measurement.pin[1].x) throw new Error(`${name}/${state}: fork overlaps actions`);
    }
    console.log(`${name}: aligned at ${axis}px; idle/hover/focus reading gaps positive`);
  }
  win.destroy();
  app.exit(errors.length ? 1 : 0);
}).catch(error => { console.error(error); app.exit(1); });
