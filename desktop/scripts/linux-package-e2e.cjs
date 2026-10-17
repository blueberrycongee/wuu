const assert = require("node:assert/strict");
const { execFileSync, spawn } = require("node:child_process");
const { once } = require("node:events");
const { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } = require("node:fs");
const { createServer } = require("node:net");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const { setTimeout: delay } = require("node:timers/promises");

const root = resolve(__dirname, "../release");
const output = resolve(process.env.WUU_LINUX_PACKAGE_OUTPUT || join(root, "linux-package-evidence"));
const scratch = mkdtempSync(join(tmpdir(), "wuu-linux-package-e2e-"));
mkdirSync(output, { recursive: true });

function rendererSandbox(processID) {
  const renderers = [];
  const visit = pid => {
    let children;
    try { children = readFileSync(`/proc/${pid}/task/${pid}/children`, "utf8").trim(); }
    catch (error) { if (error.code === "ENOENT") return; throw error; }
    for (const child of children.split(/\s+/).filter(Boolean)) visit(Number(child));
    let args;
    try { args = readFileSync(`/proc/${pid}/cmdline`, "utf8").split("\0"); }
    catch (error) { if (error.code === "ENOENT") return; throw error; }
    if (!args.includes("--type=renderer")) return;
    const status = readFileSync(`/proc/${pid}/status`, "utf8");
    assert.ok(!args.some(arg => /^--(?:no-sandbox|disable-setuid-sandbox)(?:=|$)/.test(arg)), "Renderer disabled sandboxing");
    const noNewPrivileges = /^NoNewPrivs:\s+1$/m.test(status);
    const seccompFiltering = /^Seccomp:\s+2$/m.test(status);
    assert.ok(noNewPrivileges && seccompFiltering, "Renderer lacks Linux sandbox process restrictions");
    renderers.push({ pid, noNewPrivileges, seccompFiltering });
  };
  visit(processID);
  assert.ok(renderers.length, "No packaged renderer process found");
  return renderers;
}

async function launch(name, executable, extraEnv = {}) {
  const home = join(scratch, `home-${name}`);
  mkdirSync(home, { recursive: true });
  const reservation = createServer();
  await new Promise(resolve => reservation.listen(0, "127.0.0.1", resolve));
  const port = reservation.address().port;
  await new Promise(resolve => reservation.close(resolve));
  const env = { ...process.env, ...extraEnv, HOME: home, WUU_HOME: join(home, ".wuu"),
    XDG_CONFIG_HOME: join(home, ".config"), XDG_CACHE_HOME: join(home, ".cache") };
  delete env.ELECTRON_RUN_AS_NODE;
  const child = spawn(executable, ["--ozone-platform=x11", `--user-data-dir=${join(home, "electron")}`, `--remote-debugging-port=${port}`],
    { env, stdio: ["ignore", "pipe", "pipe"] });
  let log = "";
  child.stdout.on("data", data => { log += data; });
  child.stderr.on("data", data => { log += data; });
  let socket;
  try {
    const deadline = Date.now() + 25_000;
    let page;
    while (Date.now() < deadline) {
      assert.equal(child.exitCode, null, `${name} exited before rendering: ${log}`);
      try {
        const pages = await fetch(`http://127.0.0.1:${port}/json/list`, { signal: AbortSignal.timeout(1000) }).then(response => response.json());
        page = pages.find(page => page.type === "page" && page.url.startsWith("file:") && page.url.includes("index.html"));
        if (page) break;
      } catch { /* Chromium opens its debugging socket after startup. */ }
      await delay(200);
    }
    assert.ok(page, `${name} did not expose its packaged renderer: ${log}`);
    socket = new WebSocket(page.webSocketDebuggerUrl);
    await once(socket, "open");
    let id = 0;
    const pending = new Map();
    socket.addEventListener("message", ({ data }) => {
      const response = JSON.parse(data);
      const request = pending.get(response.id);
      if (!request) return;
      pending.delete(response.id);
      if (response.error) request.reject(new Error(JSON.stringify(response.error)));
      else request.resolve(response.result);
    });
    const send = (method, params = {}) => new Promise((resolve, reject) => {
      const requestId = ++id;
      const timer = setTimeout(() => { pending.delete(requestId); reject(new Error(`${method} timed out`)); }, 15000);
      pending.set(requestId, { resolve: value => { clearTimeout(timer); resolve(value); }, reject: error => { clearTimeout(timer); reject(error); } });
      socket.send(JSON.stringify({ id: requestId, method, params }));
    });
    let state;
    while (Date.now() < deadline) {
      const response = await send("Runtime.evaluate", { expression: `(async () => { if (!window.wuu) return { preload: false }; await window.wuu.initialize(); const onboarding = document.querySelector('[data-testid="first-run-onboarding"]'); const bounds = onboarding?.getBoundingClientRect(); return { text: document.body.innerText, preload: true, onboarding: Boolean(bounds?.width && bounds?.height), build: await window.wuu.getBuildInfo(), updater: await window.wuu.getDesktopUpdateState() }; })()`, awaitPromise: true, returnByValue: true });
      assert.ok(!response.exceptionDetails, JSON.stringify(response.exceptionDetails));
      state = response.result.value;
      if (state?.preload && state.onboarding) break;
      await delay(200);
    }
    assert.ok(state?.preload && state.onboarding, `${name} onboarding renderer/preload did not load`);
    const version = require("../package.json").version;
    assert.equal(state.build?.desktop.version, version, "Packaged desktop version mismatch");
    assert.ok(state.build?.core.version.includes(version), "Packaged core did not initialize");
    assert.equal(state.updater.kind, name === "appimage" ? "idle" : "unsupported", "Wrong package updater");
    const sandbox = rendererSandbox(child.pid);
    const screenshot = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(join(output, `${name}.png`), Buffer.from(screenshot.data, "base64"));
    writeFileSync(join(output, `${name}.json`), JSON.stringify(state, null, 2) + "\n");
    return { renderer: true, preload: true, core: true, updater: state.updater.kind, build: state.build, sandbox };
  } finally {
    socket?.close();
    child.kill("SIGTERM");
    if (child.exitCode === null) await Promise.race([once(child, "exit"), delay(5000).then(() => child.kill("SIGKILL"))]);
    writeFileSync(join(output, `${name}.log`), log);
  }
}

(async () => {
  assert.equal(process.platform, "linux");
  assert.ok(process.env.DISPLAY, "Run in an X11/XWayland session or xvfb-run; the sandbox remains enabled");
  const image = join(root, "wuu-linux-x64.AppImage");
  execFileSync(image, ["--appimage-extract"], { cwd: scratch, stdio: "ignore" });
  const imageRoot = join(scratch, "squashfs-root");
  const imageResult = await launch("appimage", join(imageRoot, "AppRun"), { APPIMAGE: image, APPDIR: imageRoot });
  const debRoot = join(scratch, "deb");
  execFileSync("dpkg-deb", ["--extract", join(root, "wuu-linux-x64.deb"), debRoot]);
  const debResult = await launch("deb", join(debRoot, "opt/wuu/wuu"), { APPIMAGE: "", APPDIR: "" });
  const result = { appImage: imageResult, deb: debResult, session: "X11/XWayland", packageManagerInstall: false, fuseMount: false };
  writeFileSync(join(output, "result.json"), JSON.stringify(result, null, 2) + "\n");
  const verificationPath = join(root, "verification-linux.json");
  const verification = JSON.parse(readFileSync(verificationPath));
  verification.checks.launch = result;
  writeFileSync(verificationPath, JSON.stringify(verification, null, 2) + "\n");
  console.log("PASS: extracted AppImage and Debian packages render with the packaged preload and Chromium sandbox enabled");
})().catch(error => { console.error(error); process.exitCode = 1; })
  .finally(() => rmSync(scratch, { recursive: true, force: true }));
