// The real renderer consumes the backend capability; Go behavioral tests cover
// creation, execution and recovery with both build-tag configurations.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const evidence = path.join(desktopRoot, "out", "e2e", "project-agent-gate");
fs.mkdirSync(evidence, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(evidence, "profile-")));
app.on("window-all-closed", () => {});
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true);

async function waitFor(win, fn) {
  const end = Date.now() + 20000;
  while (Date.now() < end) {
    if (await evaluate(win, fn)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out: ${fn}`);
}

app.whenReady().then(async () => {
  const samples = [];
  for (const capability of ["absent", "disabled", "enabled"]) {
    const win = new BrowserWindow({
      width: 1180, height: 860, show: false,
      webPreferences: {
        contextIsolation: true, sandbox: false, backgroundThrottling: false,
        preload: path.join(__dirname, "resize-e2e-preload.cjs"),
        additionalArguments: [`--project-agent-e2e-${capability}`],
      },
    });
    await win.loadFile(path.join(desktopRoot, "out", "renderer", "index.html"));
    win.webContents.debugger.attach("1.3");
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "reduce" }],
    });
    await waitFor(win, () => Boolean(document.querySelector('[data-functional-group-id="workspace"]')));
    for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 640]) {
      win.setContentSize(width, 860);
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme = '${theme}'; document.documentElement.style.setProperty('--font-ui', '${font}px');`);
      await evaluate(win, async () => {
        const toggle = document.querySelector('.conversation-pane [data-wuu-component="sidebar-toggle"]');
        if (toggle?.getAttribute("aria-pressed") === "false") toggle.click();
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      });
      await waitFor(win, () => document.getAnimations().every(animation =>
        animation.playState !== "running" || animation.effect?.getComputedTiming().iterations === Infinity,
      ));
      const projectEntry = await evaluate(win, () => {
        const group = document.querySelector('[data-functional-group-id="projects"]');
        return { section: Boolean(group), create: Boolean(group?.querySelector('.sidebar-functional-action')) };
      });
      assert.deepEqual(projectEntry, { section: capability === "enabled", create: capability === "enabled" });
      const name = `${capability}-${theme}-${font}-${width}`;
      fs.writeFileSync(path.join(evidence, `${name}.png`), (await win.webContents.capturePage()).toPNG());
      samples.push({ capability, theme, font, width, ...projectEntry });
      console.log(name);
    }
    win.destroy();
  }
  fs.writeFileSync(path.join(evidence, "results.json"), JSON.stringify(samples, null, 2));
  console.log(`Project Agent capability acceptance passed: ${samples.length} samples in ${evidence}`);
  app.quit();
}).catch(error => {
  console.error(error);
  app.exit(1);
});
