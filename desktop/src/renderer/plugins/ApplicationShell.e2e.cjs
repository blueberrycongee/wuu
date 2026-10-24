const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, protocol } = require("electron");
const desktop = path.resolve(__dirname, "../../..");
const output = path.join(desktop, "out/e2e/application-shell");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktop, "..");
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "ApplicationShell.fixture.cjs");
protocol.registerSchemesAsPrivileged([{ scheme: "wuu-plugin", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
app.whenReady().then(async () => {
  protocol.handle("wuu-plugin", request => {
    const name = new URL(request.url).pathname.slice(1).replace(/\.js$/, "");
    const source = require("./ApplicationShell.fixture.cjs").sources[name];
    return new Response(source, { headers: { "Content-Type": "text/javascript" } });
  });
  const errors = [];
  const win = new BrowserWindow({ width: 1180, height: 820, show: true, webPreferences: {
    preload: path.join(desktop, "scripts/resize-e2e-preload.cjs"), contextIsolation: true,
    nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.on("console-message", (_event, level, message) => { if (level === 3) errors.push(message); });
  const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(value => JSON.stringify(value)).join(",")})`);
  async function until(fn) {
    const deadline = Date.now() + 15000;
    while (Date.now() < deadline) {
      if (await evaluate(fn)) return;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`Timed out: ${fn}; ${errors.join("\n")}`);
  }
  async function capture(name) { fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG()); }
  try {
    await win.loadFile(path.join(desktop, "out/renderer/index.html"));
    await until(() => Boolean(document.querySelector(".conversation-pane .turn")));
    const title = await evaluate(() => document.querySelector(".conversation-title-heading")?.textContent);
    await capture("default-light-wide");
    await evaluate(() => window.wuu.testShell("research"));
    await until(() => Boolean(document.querySelector("[data-research-shell] [data-evidence]")));
    assert.equal(await evaluate(() => document.querySelector(".app-shell")), null, "default shell must unmount");
    await evaluate(() => {
      const textarea = document.querySelector('[aria-label="Research draft"]');
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set;
      setter.call(textarea, "Draft survives the shell change");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      [...document.querySelectorAll("button")].find(button => button.textContent === "Settings").click();
    });
    await until(() => Boolean(document.querySelector("[data-research-settings]")));
    await capture("research-light-wide");
    const beforeRestore = await evaluate(async () => {
      window.__shellBeforeRestore = document.querySelector("[data-research-shell]");
      return (await window.wuu.testDesktopActivations()).filter(report => report.id === "user:research-shell" && report.phase === "registration").at(-1);
    });
    await evaluate(() => window.wuu.testRestore());
    await until(() => Boolean(document.querySelector("[data-research-shell] [data-evidence]")) && document.querySelector("[data-research-shell]") !== window.__shellBeforeRestore);
    const afterRestore = await evaluate(async () => (await window.wuu.testDesktopActivations()).filter(report => report.id === "user:research-shell" && report.phase === "registration").at(-1));
    assert.equal(afterRestore.graph_generation, beforeRestore.graph_generation, "connection restoration cannot invent a new graph");
    assert.ok(afterRestore.activation > beforeRestore.activation, "connection restoration must register a fresh activation");
    assert.equal(await evaluate(() => document.querySelector('[aria-label="Research draft"]').value), "Draft survives the shell change");
    assert.ok(await evaluate(() => Boolean(document.querySelector("[data-research-settings]"))));
    await capture("research-after-restore");
    await evaluate(() => window.wuu.testShell("disable"));
    await until(() => Boolean(document.querySelector(".application-recovery")) && !document.querySelector("[data-research-shell]"));
    assert.equal(await evaluate(() => Boolean(document.querySelector(".app-shell"))), false, "disable cannot fallback");
    await capture("disabled-recovery");
    await evaluate(() => window.wuu.testShell("default"));
    await until(() => Boolean(document.querySelector(".settings-screen, .settings-shell, .settings-view")));
    await evaluate(() => document.querySelector('[data-wuu-component="conversations-section"]').click());
    await until(() => !document.querySelector(".settings-shell"));
    await until(() => [...document.querySelectorAll("textarea")].some(input => input.value === "Draft survives the shell change"));
    // Appearance checks use the same real default shell after a round trip.
    for (const theme of ["light", "dark"]) for (const size of [15, 20]) {
      win.setContentSize(size === 15 ? 1180 : 720, 820);
      await evaluate((theme, size) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        window.dispatchEvent(new Event("wuu-content-size-change"));
      }, theme, size);
      await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await capture(`restored-${theme}-${size}`);
    }
    await evaluate(() => window.wuu.testShell("bridge-failure"));
    await until(() => document.body.textContent.includes("fixture ACK bridge unavailable") && Boolean(document.querySelector(".app-shell")));
    const failureLayout = await evaluate(() => {
      const shell = document.querySelector(".app-shell").getBoundingClientRect();
      return { top: shell.top, bottom: shell.bottom, viewport: window.innerHeight };
    });
    assert.ok(failureLayout.top <= 1 && failureLayout.bottom <= failureLayout.viewport + 1,
      `ACK diagnostics must not displace the active shell: ${JSON.stringify(failureLayout)}`);
    await capture("bridge-failure");
    await evaluate(() => window.wuu.testShell("default"));
    await until(() => !document.body.textContent.includes("fixture ACK bridge unavailable") && Boolean(document.querySelector(".app-shell")));
    for (const [mode, owner, message] of [["broken-child", "user:evidence", "fixture child failure"], ["broken-root", "user:research-shell", "fixture root failure"]]) {
      await evaluate(mode => window.wuu.testShell(mode), mode);
      await until(() => Boolean(document.querySelector('[role="alert"]')) && !document.querySelector(".app-shell"));
      await evaluate(() => new Promise(resolve => setTimeout(resolve, 100)));
      const reports = await evaluate(() => window.wuu.testDesktopActivations());
      assert.ok(reports.some(report => report.id === owner && report.phase === "render" && report.status === "failed"), `${owner} must report render failure`);
      assert.ok(await evaluate(message => document.body.textContent.includes(message), message));
      await capture(mode);
    }
    const reports = await evaluate(() => window.wuu.testDesktopActivations());
    for (const owner of ["builtin:wuu.default-shell", "user:research-shell", "user:evidence"]) {
      const registrations = reports.filter(report => report.id === owner && report.phase === "registration" && report.status === "active");
      assert.ok(registrations.length >= 2, `${owner} must reactivate on graph changes`);
      assert.ok(registrations.every((report, index) => index === 0 || report.activation > registrations[index - 1].activation));
    }
    fs.writeFileSync(path.join(output, "activation-reports.json"), JSON.stringify(reports, null, 2));
    const unexpectedErrors = errors.filter(message => !message.includes("fixture child failure") && !message.includes("fixture root failure"));
    assert.equal(unexpectedErrors.length, 0, unexpectedErrors.join("\n"));
    fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({ passed: true, title, errors: unexpectedErrors, expectedRenderErrors: errors,
      checked: ["same-graph restore reactivation with draft/settings retained", "ACK transport failure and recovery without layout shift", "registration/render ACKs", "graph reactivation", "root/nested render failures", "selected default mounts", "external shell mounts", "third-party slot renders", "default unmounts", "shared settings action", "disable has no fallback", "default restores", "light/dark 15/20 screenshot evidence"] }, null, 2));
    console.log(`PASS application shell assembly; evidence ${output}`);
    win.destroy(); app.exit(0);
  } catch (error) {
    fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack || error) + "\nRenderer errors:\n" + errors.join("\n"));
    await capture("failure"); console.error(error); win.destroy(); app.exit(1);
  }
}).catch(error => { console.error(error); app.exit(1); });
