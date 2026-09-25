/**
 * Render build/dmg-background.svg into the 1x and 2x DMG window backgrounds.
 * Run `npm run dmg-background:generate` from desktop on macOS so the text uses
 * the system fonts Finder users see. Commit both PNGs; electron-builder combines
 * the pair into one HiDPI TIFF while packaging, without regenerating artwork.
 */
const { app, BrowserWindow } = require("electron");
const { readFileSync, writeFileSync } = require("node:fs");
const path = require("node:path");

const BUILD_DIR = path.join(__dirname, "..", "build");

// Rasterize separately at each scale to preserve sharp text and edges.
function renderInPage(svg, scale) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => {
      const canvas = document.createElement("canvas");
      canvas.width = image.naturalWidth * scale;
      canvas.height = image.naturalHeight * scale;
      canvas.getContext("2d").drawImage(image, 0, 0, canvas.width, canvas.height);
      resolve(canvas.toDataURL("image/png"));
    };
    image.onerror = () => reject(new Error("Cannot load the DMG background SVG"));
    image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  });
}

if (process.platform !== "darwin") {
  console.error("Generate DMG backgrounds on macOS to use the Finder system fonts.");
  app.exit(1);
} else {
  app.dock?.hide();
  app.whenReady().then(async () => {
    const svg = readFileSync(path.join(BUILD_DIR, "dmg-background.svg"), "utf8");
    const window = new BrowserWindow({ show: false });
    await window.loadURL("about:blank");
    for (const [scale, name] of [[1, "dmg-background.png"], [2, "dmg-background@2x.png"]]) {
      const url = await window.webContents.executeJavaScript(`(${renderInPage})(${JSON.stringify(svg)}, ${scale})`);
      writeFileSync(path.join(BUILD_DIR, name), Buffer.from(url.split(",")[1], "base64"));
    }
    console.log("DMG backgrounds updated from build/dmg-background.svg.");
    app.quit();
  }).catch((error) => {
    console.error(error);
    app.exit(1);
  });
}
