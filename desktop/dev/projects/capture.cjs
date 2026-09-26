// Manual rendered review, not a golden-image merge gate.
// Start Vite with this directory's config, then run with desktop's Electron.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const output = path.resolve(__dirname, "../../../artifacts/projects");
const base = process.env.PROJECTS_PREVIEW_URL || "http://127.0.0.1:5243";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const errors = [];

// name, query, window width, and an element to hover
const scenes = [
  ["coordinator-light-14", "view=coordinator&panel=project", 1440],
  ["coordinator-dark-14", "view=coordinator&panel=project&theme=dark", 1440],
  ["coordinator-light-20", "view=coordinator&panel=project&size=20", 1440],
  ["coordinator-dark-20", "view=coordinator&panel=project&theme=dark&size=20", 1440],
  ["coordinator-narrow", "view=coordinator&panel=project", 1085],
  ["session-proposal-light-14", "view=session&panel=proposal", 1440],
  ["session-proposal-dark-20", "view=session&panel=proposal&theme=dark&size=20", 1440],
  ["draft-light-14", "view=draft&panel=none", 1280],
  ["empty-light-14", "empty&panel=none", 1280],
  ["coordinator-side-taken-over-dark-20", "view=coordinator&panel=project&theme=dark&size=20&side-control=taken_over", 1085, ".project-panel-row-main"],
  ["coordinator-keyboard-focus", "view=coordinator&panel=project", 1440, undefined, ".project-panel-row-main"],
  ["coordinator-hover-session", "view=coordinator&panel=project", 1440, ".project-panel-list .project-panel-row-main"],
];

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1440, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  win.webContents.debugger.attach("1.3");
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  for (const [name, query, width, hover, focus] of scenes) {
    win.setContentSize(width, 900);
    await win.loadURL(`${base}/dev/projects/?${query}`);
    await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
      const deadline = Date.now() + 10000;
      function ready() {
        if (document.querySelector('.project-thread-row, .project-new-item')) {
          return setTimeout(() => requestAnimationFrame(() => requestAnimationFrame(resolve)), 600);
        }
        if (Date.now() > deadline) return reject(new Error('Projects preview did not mount'));
        requestAnimationFrame(ready);
      } ready();
    })`);
    if (hover) {
      const point = await win.webContents.executeJavaScript(`(() => {
        const rects = [...document.querySelectorAll(${JSON.stringify(hover)})].map(node => node.getBoundingClientRect());
        const rect = rects[0];
        return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
      })()`);
      win.webContents.sendInputEvent({ type: "mouseMove", x: point.x, y: point.y });
      await new Promise(resolve => setTimeout(resolve, 300));
    }
    if (focus) {
      await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
      win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
      win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
      await win.webContents.executeJavaScript(`new Promise(resolve => requestAnimationFrame(() => {
        document.querySelector(${JSON.stringify(focus)}).focus();
        requestAnimationFrame(resolve);
      }))`);
      console.log(name, await win.webContents.executeJavaScript(`JSON.stringify({
        focused: document.activeElement?.className,
        modality: document.documentElement.dataset.focusModality,
        visible: document.activeElement?.matches(':focus-visible')
      })`));
    }
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  }
  if (errors.length) console.error(errors.join("\n"));
  console.log(`Wrote ${output}`);
  app.quit();
}).catch(error => {
  console.error([...errors, error].join("\n"));
  app.exit(1);
});
