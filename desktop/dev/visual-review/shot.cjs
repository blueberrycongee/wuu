// Visual review capture: start the desktop development server, then run
//   ./node_modules/.bin/electron dev/visual-review/shot.cjs
// from desktop/. Surfaces are the development fixtures that mount production
// components and the complete renderer stylesheet. Output: artifacts/visual-review/.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const output = path.resolve(__dirname, "../../../artifacts/visual-review");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5173";

const shots = [
  { name: "design-light-14", url: "/dev/design-system/", width: 1280, height: 900, theme: "light", size: 14, selects: true },
  { name: "design-light-145", url: "/dev/design-system/", width: 1280, height: 900, theme: "light", size: 14.5, selects: true },
  { name: "design-light-20", url: "/dev/design-system/", width: 1280, height: 900, theme: "light", size: 20, selects: true },
  { name: "design-dark-145", url: "/dev/design-system/", width: 1280, height: 900, theme: "dark", size: 14.5, selects: true },
  { name: "three-pane-light-145", url: "/dev/three-pane/", width: 1440, height: 900, theme: "light", size: 14.5 },
  { name: "three-pane-dark-145", url: "/dev/three-pane/", width: 1440, height: 900, theme: "dark", size: 14.5 },
  { name: "three-pane-narrow", url: "/dev/three-pane/", width: 900, height: 900, theme: "light", size: 14.5 },
  { name: "message-flow-conversation", url: "/dev/message-flow-reading/?surface=conversation", width: 1280, height: 900, theme: "light", size: 14.5 },
  { name: "message-flow-prose", url: "/dev/message-flow-reading/", width: 1280, height: 900, theme: "light", size: 14.5 },
  { name: "message-flow-dark", url: "/dev/message-flow-reading/", width: 1280, height: 900, theme: "dark", size: 14.5 },
];

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    show: false, width: 1280, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const only = process.env.WUU_SHOT_ONLY;
  for (const shot of shots) {
    if (only && !shot.name.includes(only)) continue;
    win.setContentSize(shot.width, shot.height);
    const url = `${origin}${shot.url}${shot.url.includes("?") ? "&" : "?"}theme=${shot.theme}&size=${shot.size}`;
    await win.loadURL(url);
    await win.webContents.executeJavaScript("new Promise(r => setTimeout(r, 900))");
    if (shot.selects) {
      // The design-system fixture keeps theme and font size in React state and
      // ignores the query string, so drive its own controls.
      await win.webContents.executeJavaScript(`(() => {
        const [theme, size] = document.querySelectorAll(".review-toolbar select");
        const apply = (el, value) => {
          el.value = value;
          el.dispatchEvent(new Event("change", { bubbles: true }));
        };
        apply(theme, ${JSON.stringify(shot.theme)});
        apply(size, ${JSON.stringify(String(shot.size))});
      })()`);
      await win.webContents.executeJavaScript("new Promise(r => setTimeout(r, 600))");
    }
    const image = await win.webContents.capturePage();
    fs.writeFileSync(path.join(output, `${shot.name}.png`), image.toPNG());
    console.log(`captured ${shot.name}`);
  }
  if (errors.length) console.error(errors.join("\n"));
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
