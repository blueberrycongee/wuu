// Manual rendered-geometry review, not a CSS-source or golden-image merge gate.
// Start Vite with this directory's config, then run with desktop's Electron.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const output = path.resolve(__dirname, "../../../artifacts/sidebar-accessories");
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
  out.readingGaps = [...document.querySelectorAll(".thread-row:has(.thread-row-fork-icon)")].map(row => {
    const title = row.querySelector(".thread-row-title");
    const rect = title.getBoundingClientRect();
    const end = rect.right - parseFloat(getComputedStyle(title).paddingRight);
    return row.querySelector(".thread-row-fork-icon").getBoundingClientRect().left - end;
  });
  out.focus = document.activeElement?.className;
  return out;
};

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 960,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") { errors.push(event.message); console.error(event.message); } });
  const report = {};
  for (const theme of ["light", "dark"]) for (const size of [14, 20]) {
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
    await win.webContents.executeJavaScript(`document.querySelector('.thread-row:has(.thread-row-fork-icon) .thread-row-action').focus()`);
    await new Promise(resolve => setTimeout(resolve, 350));
    report[name].focus = await win.webContents.executeJavaScript(`(${measure})()`);
    console.log(`captured ${name}`);
  }
  await win.loadURL("http://127.0.0.1:5208/dev/sidebar-accessories/?empty&size=20");
  await new Promise(resolve => setTimeout(resolve, 500));
  fs.writeFileSync(path.join(output, "empty.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify({ report, errors }, null, 2));
  for (const [name, states] of Object.entries(report)) {
    const idle = states.idle;
    const axis = idle.bell[0].x;
    const aligned = [idle.collapse[0], idle.heading[0], ...idle.add, ...idle.newThread, idle.fork[0], ...idle.spinner, ...idle.archive, ...idle.account];
    if (aligned.some(icon => Math.abs(icon.x - axis) > 0.1)) throw new Error(`${name}: trailing column drift`);
    if (Math.abs(idle.heading[1].x - idle.pin[0].x) > 0.1) throw new Error(`${name}: second column drift`);
    for (const [state, measurement] of Object.entries(states)) {
      if (measurement.readingGaps.some(gap => gap <= 0)) throw new Error(`${name}/${state}: title overlaps accessories`);
      if (state !== "idle" && measurement.fork[0].x >= measurement.pin[1].x) throw new Error(`${name}/${state}: fork overlaps actions`);
    }
    console.log(`${name}: aligned at ${axis}px; idle/hover/focus reading gaps positive`);
  }
  win.destroy();
  app.exit(errors.length ? 1 : 0);
}).catch(error => { console.error(error); app.exit(1); });
