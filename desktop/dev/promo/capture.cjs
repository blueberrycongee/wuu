// Renders the promo frame by frame and encodes it. Run from the repository root:
//   desktop/node_modules/.bin/electron desktop/dev/promo/capture.cjs
// Environment:
//   PROMO_STILLS="1.2,5.5"  write only these moments as PNG stills
//   PROMO_SHEETS="1,2,3"    write 3×3 contact sheets of these moments for review
//   PROMO_FPS=60            frame rate for the full render
//   PROMO_RANGE="31,59"     render only part of the film (seconds)
const { app, BrowserWindow } = require("electron");
const { mkdir, rm, writeFile } = require("node:fs/promises");
const { spawnSync } = require("node:child_process");
const path = require("node:path");

const repo = path.resolve(__dirname, "../../..");
const output = path.join(repo, "desktop/out-dev/promo");
app.setPath("userData", path.join(output, "profile"));

app.whenReady().then(async () => {
  const { createServer } = await import("vite");
  const server = await createServer({
    root: __dirname,
    configFile: false,
    cacheDir: path.join(repo, "desktop/node_modules/.vite/promo"),
    logLevel: "warn",
    server: { host: "127.0.0.1", port: 0, fs: { allow: [repo] } },
  });
  const win = new BrowserWindow({
    width: 1920, height: 1200, show: false,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  win.webContents.on("console-message", (event) => {
    if (event.level === "error" || event.level === "warning") console.error(event.message);
  });
  try {
    await server.listen();
    await win.loadURL(server.resolvedUrls.local[0]);
    await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
      const deadline = performance.now() + 20000;
      (function ready() {
        if (window.promo) resolve();
        else if (performance.now() > deadline) reject(new Error("Promo did not load"));
        else requestAnimationFrame(ready);
      })();
    })`);
    const duration = await win.webContents.executeJavaScript("window.promo.duration");
    const frame = (t) => win.webContents.executeJavaScript(`(() => {
      window.promo.render(${t});
      return window.promo.canvas.toDataURL("image/png");
    })()`);
    const png = (url) => Buffer.from(url.slice(url.indexOf(",") + 1), "base64");

    if (process.env.PROMO_SHEETS) {
      const dir = path.join(output, "sheets");
      await rm(dir, { recursive: true, force: true });
      await mkdir(dir, { recursive: true });
      const times = process.env.PROMO_SHEETS.split(",").map(Number);
      for (let page = 0; page * 9 < times.length; page++) {
        const url = await win.webContents.executeJavaScript(`(() => {
          const sheet = document.createElement("canvas");
          sheet.width = 1920; sheet.height = 1080;
          const c = sheet.getContext("2d");
          ${JSON.stringify(times.slice(page * 9, page * 9 + 9))}.forEach((t, i) => {
            window.promo.render(t);
            const x = (i % 3) * 640, y = Math.floor(i / 3) * 360;
            c.drawImage(window.promo.canvas, x, y, 640, 360);
            c.fillStyle = "rgba(0,0,0,0.6)";
            c.fillRect(x, y, 74, 24);
            c.fillStyle = "#fff";
            c.font = "16px monospace";
            c.fillText(t.toFixed(2), x + 6, y + 17);
            c.strokeStyle = "#000";
            c.strokeRect(x + 0.5, y + 0.5, 639, 359);
          });
          return sheet.toDataURL("image/png");
        })()`);
        const name = path.join(dir, `sheet-${page}.png`);
        await writeFile(name, png(url));
        console.log(name);
      }
      return;
    }

    if (process.env.PROMO_STILLS) {
      const dir = path.join(output, "stills");
      await mkdir(dir, { recursive: true });
      for (const t of process.env.PROMO_STILLS.split(",").map(Number)) {
        const name = `t${t.toFixed(2).padStart(6, "0")}.png`;
        await writeFile(path.join(dir, name), png(await frame(t)));
        console.log(path.join(dir, name));
      }
      return;
    }

    const fps = Number(process.env.PROMO_FPS ?? 60);
    const [start, end] = (process.env.PROMO_RANGE ?? `0,${duration}`).split(",").map(Number);
    const dir = path.join(output, "frames");
    await rm(dir, { recursive: true, force: true });
    await mkdir(dir, { recursive: true });
    const count = Math.round((end - start) * fps);
    const began = Date.now();
    for (let i = 0; i < count; i++) {
      await writeFile(path.join(dir, `${String(i).padStart(5, "0")}.png`), png(await frame(start + i / fps)));
      if (i % 240 === 0) console.log(`frame ${i}/${count} (${((Date.now() - began) / 1000).toFixed(0)}s)`);
    }
    const video = path.join(output, `wuu-promo${process.env.PROMO_RANGE ? "-part" : ""}.mp4`);
    const encoded = spawnSync("swift", [path.join(__dirname, "encode.swift"), dir, video, String(fps)], { stdio: "inherit" });
    if (encoded.status !== 0) throw new Error("Encoding failed");
    console.log(video);
  } finally {
    win.destroy();
    await server.close();
  }
}).then(() => app.quit()).catch((error) => {
  console.error(error);
  app.exit(1);
});
