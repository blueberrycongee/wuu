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

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1280, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  async function open(query) {
    await win.loadURL(`${base}/dev/projects/?${query}`);
    await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
      const deadline = Date.now() + 10000;
      function ready() {
        if (document.querySelector('.project-thread-row') && document.querySelector('.project-candidate-card')) {
          return requestAnimationFrame(() => requestAnimationFrame(resolve));
        }
        if (Date.now() > deadline) return reject(new Error('Projects preview did not mount'));
        requestAnimationFrame(ready);
      } ready();
    })`);
  }
  async function capture(name) {
    await new Promise(resolve => setTimeout(resolve, 400));
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  }
  for (const theme of ["light", "dark"]) for (const size of [14, 20]) {
    win.setContentSize(size === 20 ? 1100 : 1280, 900);
    await open(`theme=${theme}&size=${size}&width=${size === 20 ? 280 : 296}`);
    // Open the session list and the pending card's diff so both appear in the capture.
    await win.webContents.executeJavaScript(`(() => {
      document.querySelector('.project-sessions-button')?.click();
      document.querySelector('.project-candidate-card .project-candidate-link[aria-expanded]')?.click();
    })()`);
    await capture(`${theme}-${size}`);
  }
  // Expanded history overflows the bounded list with the project's sessions inside it.
  win.setContentSize(1280, 900);
  await open("history=12");
  await win.webContents.executeJavaScript("document.querySelector('.thread-list-more')?.click()");
  await capture("history-expanded");
  if (errors.length) console.error(errors.join("\n"));
  console.log(`Wrote ${output}`);
  app.quit();
}).catch(error => {
  console.error([...errors, error].join("\n"));
  app.exit(1);
});
