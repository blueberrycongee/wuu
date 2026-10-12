// Run with Electron inside an isolated display. Uses production fixtures and
// synthetic labels; never connects to an app-server or reads saved preferences.
const fs = require("node:fs");
const path = require("node:path");

const fonts = process.env.WUU_REVIEW_FONTS
  ? JSON.parse(process.env.WUU_REVIEW_FONTS)
  : ["", "Arial", "Georgia", "Menlo", "PingFang SC", "Songti SC"];
const scenes = fonts.flatMap((font, fontIndex) => [13, 14.5, 20].flatMap(size =>
  ["light", "dark"].flatMap(theme => ["three-pane", "design-system"].map(surface => ({
    id: `${fontIndex}-${size}-${theme}-${surface}`, font, size, theme, surface,
    width: size === 20 ? 900 : 1440, height: 900,
  })))),
);

async function prepare(scene) {
  await document.fonts.ready;
  if (scene.surface === "three-pane") {
    document.querySelector('[data-wuu-tool="files"]').click();
    await new Promise(resolve => requestAnimationFrame(resolve));
    // Accents and descenders exercise the clipped, single-line tab label.
    document.querySelector(".workspace-tool-tab-main > span").textContent = "Agyp Éj";
    document.querySelector(".primary-nav .nav-item > span").textContent = "新对话 Agyp";
    document.querySelector(".thread-row.running").dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
  } else {
    document.querySelector(".select-menu-trigger").click();
  }
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
}

function measure() {
  const rect = node => node.getBoundingClientRect().toJSON();
  const selectors = [
    ".primary-nav .nav-item > span", ".project-row-name", ".thread-row-title",
    ".workspace-tool-tab-main > span:not(.workspace-tab-dirty-indicator)",
    ".select-menu-value", ".select-menu-item-text",
  ];
  return {
    viewport: { width: innerWidth, height: innerHeight, scale: devicePixelRatio },
    offsetEm: Number(document.documentElement.style.getPropertyValue("--appearance-ui-text-offset")),
    labels: selectors.flatMap(selector => [...document.querySelectorAll(selector)].map(node => {
      const style = getComputedStyle(node);
      const control = node.closest("button, [role=menuitem]");
      const icon = control?.querySelector("svg");
      return { selector, text: node.textContent, rect: rect(node),
        control: control ? rect(control) : null, icon: icon ? rect(icon) : null,
        font: style.fontFamily, size: style.fontSize, lineHeight: style.lineHeight,
        overflow: style.overflow, truncated: node.scrollWidth > node.clientWidth + 1 };
    })),
  };
}

async function run() {
  const { app, BrowserWindow } = require("electron");
  const output = path.resolve(process.env.WUU_REVIEW_OUTPUT || "artifacts/font-alignment");
  fs.mkdirSync(output, { recursive: true });
  app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
  await app.whenReady();
  const { createServer } = await import("vite");
  const desktop = path.resolve(__dirname, "../..");
  const server = await createServer({ root: desktop,
    configFile: path.join(desktop, "dev/three-pane/vite.config.ts"),
    optimizeDeps: { entries: ["dev/three-pane/index.html", "dev/design-system/index.html"] },
    server: { host: "127.0.0.1", port: 0 },
  });
  await server.listen();
  const origin = server.resolvedUrls.local[0];
  let win;
  const reports = [];
  const errors = [];
  try {
    for (const scene of scenes) {
      const previous = win;
      // Each scenario starts with its own renderer and in-memory session.
      win = new BrowserWindow({ show: true, useContentSize: true, width: scene.width, height: scene.height,
        webPreferences: { contextIsolation: true, nodeIntegration: false, partition: scene.id },
      });
      previous?.destroy();
      win.webContents.on("console-message", event => {
        if (event.level === "error") { errors.push(event.message); console.error(event.message); }
      });
      const params = new URLSearchParams({ font: scene.font, size: String(scene.size), theme: scene.theme });
      await win.loadURL(`${origin}dev/${scene.surface}/?${params}`);
      // Wait for the fixture's React tree, not a fixed rendering delay.
      await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
        const ready = () => document.querySelector('.nav-item, .select-menu-trigger');
        if (ready()) { resolve(); return; }
        const observer = new MutationObserver(() => { if (ready()) { observer.disconnect(); clearTimeout(timeout); resolve(); } });
        const timeout = setTimeout(() => { observer.disconnect(); reject(new Error('Fixture did not mount')); }, 15000);
        observer.observe(document.body, { subtree: true, childList: true });
      })`);
      // Freeze transitions so glyph inspection never captures an entry fade.
      await win.webContents.insertCSS("*, *::before, *::after { animation: none !important; transition: none !important; }");
      await win.webContents.executeJavaScript(`(${prepare})(${JSON.stringify(scene)})`);
      const metrics = await win.webContents.executeJavaScript(`(${measure})()`);
      win.webContents.debugger.attach("1.3");
      await win.webContents.debugger.sendCommand("DOM.enable");
      await win.webContents.debugger.sendCommand("CSS.enable");
      const { root } = await win.webContents.debugger.sendCommand("DOM.getDocument");
      const { nodeId } = await win.webContents.debugger.sendCommand("DOM.querySelector", {
        nodeId: root.nodeId,
        selector: scene.surface === "three-pane" ? ".primary-nav .nav-item > span" : ".select-menu-value",
      });
      const { fonts: platformFonts } = await win.webContents.debugger.sendCommand("CSS.getPlatformFontsForNode", { nodeId });
      win.webContents.debugger.detach();
      fs.writeFileSync(path.join(output, `${scene.id}.png`), (await win.webContents.capturePage()).toPNG());
      reports.push({ ...scene, metrics, platformFonts });
      console.log(`captured ${scene.id}`);
    }
  } catch (error) {
    fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
    throw error;
  } finally {
    fs.writeFileSync(path.join(output, "measurements.json"), JSON.stringify({
      platform: process.platform, versions: process.versions,
      motion: "disabled for static glyph comparison", errors, scenes: reports,
    }, null, 2));
    win.destroy();
    await server.close();
  }
  app.quit();
}

module.exports = { scenes, prepare, measure };
if (process.versions.electron) run().catch(error => {
  console.error(error);
  require("electron").app.exit(1);
});
