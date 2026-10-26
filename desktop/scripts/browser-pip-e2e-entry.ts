// Runs the production browser host and PiP with isolated, synthetic pages.
import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { execFileSync } from "node:child_process";
import { once } from "node:events";
import { cpSync, existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { app, BrowserWindow, session, WebContentsView } from "electron";
import { BrowserExtensionManager } from "../src/main/browserExtensions";
import type { ActivitySession } from "../src/shared/protocol";
import { createObservationPiPFactory } from "../src/main/browserPiPWindow";
import { ObservationCoordinator } from "../src/main/cuaActivityWindows";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

const artifacts = process.env.WUU_BROWSER_PIP_E2E_ARTIFACTS ?? "/tmp/wuu-browser-cursor-e2e";
const profile = mkdtempSync(join(tmpdir(), "wuu-browser-cursor-"));
app.setPath("userData", profile);
app.on("window-all-closed", () => undefined);
app.on("quit", () => rmSync(profile, { recursive: true, force: true }));
const workdir = "/e2e";
const tabID = "cursor-tab";
const activity: ActivitySession = {
  id: "cursor-activity", kind: "browser", thread_id: "cursor-thread", workdir,
  plugin_id: "embedded-browser", target: tabID, state: "background_controlled",
  controller: "agent", created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
};
const page = `<!doctype html><html><style>
  *{box-sizing:border-box}body{margin:0;background:#f7f8fa;color:#24324a;font:16px system-ui}
  body.dark{background:#18202d;color:#e7edf7}header{padding:30px 40px;border-bottom:1px solid #8190a433}
  h1{margin:0 0 8px;font-size:24px;font-weight:600}p{margin:0;opacity:.7}
  button{position:absolute;left:170px;top:170px;width:180px;height:60px;border:1px solid #759ad6;
    background:#e0ecff;color:#284d85;border-radius:14px;font:inherit}
  button:hover{background:#c4daff}article{position:absolute;left:40px;top:280px;max-width:550px;line-height:1.8}
</style><body><header><h1>Browser workspace</h1><p>Live page · pointer interaction preview</p></header>
<button id="target">Explore the page</button><article>One page, two views.<br>The pointer stays clear as the preview resizes.<br>Open the panel to continue browsing.</article>
<script>window.clicks=0;document.getElementById('target').onclick=()=>{window.clicks++;document.querySelector('p').textContent='Completed actions: '+window.clicks};</script></body></html>`;

async function waitFor(label: string, check: () => Promise<boolean> | boolean, timeout = 5000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!(await check())) {
    if (Date.now() > deadline) throw new Error(`Timed out: ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

async function capture(win: BrowserWindow, name: string): Promise<void> {
  if (process.platform !== "darwin") return;
  // Flush each Chromium surface before asking the native compositor for pixels.
  // DOM/animation readiness alone can precede the first submitted page frame.
  for (const view of win.contentView.children) {
    if (view instanceof WebContentsView) await view.webContents.capturePage();
  }
  // BrowserWindow.capturePage excludes sibling WebContentsViews. Capture the
  // native composition so the page and the pointer are verified together.
  const nativeID = win.getMediaSourceId().split(":")[1];
  execFileSync("screencapture", ["-x", "-o", "-l", nativeID, join(artifacts, name)]);
}

async function verifyBrowserExtensions(): Promise<void> {
  const root = join(profile, "extension-fixture");
  const source = join(root, "source");
  mkdirSync(source, { recursive: true });
  const store = join(root, "browser-extensions.json");
  const browserSession = session.fromPartition("persist:extension-lifecycle-fixture");
  const pageSession = session.fromPartition("persist:extension-isolation-fixture");
  const manager = new BrowserExtensionManager(browserSession, store);
  await manager.initialize();
  const manifest = {
    manifest_version: 3, name: "Synthetic local extension", version: "1.0.0",
    permissions: ["storage"], optional_permissions: ["history"],
    content_scripts: [{ matches: ["http://127.0.0.1/*"], js: ["content.js"], run_at: "document_end" }],
  };
  const writeVersion = (version: string, permissions = ["storage"], script = "content.js") => {
    writeFileSync(join(source, "manifest.json"), JSON.stringify({ ...manifest, version, permissions, content_scripts: [{ ...manifest.content_scripts[0], js: [script] }] }));
    writeFileSync(join(source, "content.js"), `
      document.documentElement.dataset.extensionFixture = ${JSON.stringify(version)};
      chrome.storage.local.get("fixtureToken").then(async value => {
        const token = value.fixtureToken || crypto.randomUUID();
        await chrome.storage.local.set({ fixtureToken: token });
        document.documentElement.dataset.extensionStorage = token;
      });
    `);
  };
  writeVersion("1.0.0");
  const checks: string[] = [];
  let reviews = 0;
  const approve = async () => { reviews++; return true; };
  const declined = await manager.addFromDirectory(source, async reviewed => {
    assert.deepEqual(reviewed.sites, ["http://127.0.0.1/*"]);
    assert.deepEqual(reviewed.permissions, ["storage"]);
    return false;
  });
  assert.equal(declined.length, 0);
  assert.equal(browserSession.extensions.getAllExtensions().length, 0);
  checks.push("Declining permission review executes no extension code");
  if (process.platform !== "win32") {
    rmSync(join(source, "manifest.json"));
    execFileSync("mkfifo", [join(source, "manifest.json")]);
    await assert.rejects(manager.addFromDirectory(source, approve), /manifest.json/);
    rmSync(join(source, "manifest.json"));
    writeVersion("1.0.0");
  }
  symlinkSync(join(source, "content.js"), join(source, "linked.js"));
  await assert.rejects(manager.addFromDirectory(source, approve), /symbolic links|special files/);
  rmSync(join(source, "linked.js"));
  writeVersion("1.0.0", ["storage"], "../outside.js");
  await assert.rejects(manager.addFromDirectory(source, approve), /inside the selected folder/);
  writeVersion("1.0.0");
  assert.equal(reviews, 0);
  checks.push("Special manifests, linked files and resource traversal are rejected before consent or execution");
  const installed = (await manager.addFromDirectory(source, approve))[0];
  assert.equal(installed.loaded, true);
  assert.equal(reviews, 1);
  const id = installed.id;
  const server = createServer((_req, res) => { res.writeHead(200, { "Content-Type": "text/html" }); res.end("<!doctype html><title>Extension fixture</title><p>Local synthetic page</p>"); });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  const page = new BrowserWindow({ show: false, webPreferences: { session: browserSession, sandbox: true, nodeIntegration: false, contextIsolation: true } });
  const isolated = new BrowserWindow({ show: false, webPreferences: { session: pageSession, sandbox: true, nodeIntegration: false, contextIsolation: true } });
  const url = `http://127.0.0.1:${port}/`;
  const versionOnPage = async () => {
    await page.loadURL(url);
    return page.webContents.executeJavaScript("document.documentElement.dataset.extensionFixture ?? null");
  };
  try {
    assert.equal(await versionOnPage(), "1.0.0");
    await waitFor("extension storage initialized", () => page.webContents.executeJavaScript("Boolean(document.documentElement.dataset.extensionStorage)"));
    const storageToken = await page.webContents.executeJavaScript("document.documentElement.dataset.extensionStorage");
    await isolated.loadURL(url);
    assert.equal(await isolated.webContents.executeJavaScript("document.documentElement.dataset.extensionFixture ?? null"), null);
    // Match the Linux package acceptance contract: the real fixture renderers
    // must retain Chromium's process restrictions, not merely sandbox:true.
    const rendererSandbox = process.platform === "linux" ? [page, isolated].map(window => {
      const pid = window.webContents.getOSProcessId();
      const args = readFileSync(`/proc/${pid}/cmdline`, "utf8").split("\0");
      const status = readFileSync(`/proc/${pid}/status`, "utf8");
      assert.ok(!args.some(arg => /^--(?:no-sandbox|disable-setuid-sandbox)(?:=|$)/.test(arg)), "Extension fixture renderer disabled sandboxing");
      const noNewPrivileges = /^NoNewPrivs:\s+1$/m.test(status);
      const seccompFiltering = /^Seccomp:\s+2$/m.test(status);
      assert.ok(noNewPrivileges && seccompFiltering, "Extension fixture renderer lacks Linux sandbox restrictions");
      return { pid, noNewPrivileges, seccompFiltering };
    }) : undefined;
    if (rendererSandbox) checks.push("Both real Linux renderers retain Chromium sandbox process restrictions");
    checks.push("Real Chromium executes the content script only in the browser session");
    writeVersion("2.0.0", ["storage", "tabs"]);
    let escalationReviewed = false;
    await manager.reload(id, async (next, _source, previous) => {
      escalationReviewed = true;
      assert(next.permissions.includes("tabs"));
      assert(!previous?.permissions.includes("tabs"));
      return false;
    });
    assert(escalationReviewed);
    assert.equal(await versionOnPage(), "1.0.0");
    assert.equal((await manager.list())[0].version, "1.0.0");
    checks.push("Permission escalation waits for approval and cancellation preserves the running copy");
    const updated = (await manager.reload(id, approve))[0];
    assert.equal(updated.id, id);
    assert.equal(updated.version, "2.0.0");
    assert.equal(await versionOnPage(), "2.0.0");
    await waitFor("extension storage survives update", () => page.webContents.executeJavaScript(`document.documentElement.dataset.extensionStorage === ${JSON.stringify(storageToken)}`));
    checks.push("Approved local update preserves extension identity and storage");
    writeVersion("2.1.0", ["storage", "tabs", "history"]);
    let promotedOptionalReviewed = false;
    await manager.reload(id, async () => { promotedOptionalReviewed = true; return false; });
    assert(promotedOptionalReviewed);
    const moreSites = JSON.parse(readFileSync(join(source, "manifest.json"), "utf8"));
    moreSites.permissions = ["storage", "tabs"];
    moreSites.host_permissions = ["https://example.invalid/*"];
    writeFileSync(join(source, "manifest.json"), JSON.stringify(moreSites));
    let expandedSitesReviewed = false;
    await manager.reload(id, async next => { expandedSitesReviewed = next.sites.includes("https://example.invalid/*"); return false; });
    assert(expandedSitesReviewed);
    assert.equal(await versionOnPage(), "2.0.0");
    checks.push("Optional-to-required permission changes and additional websites require review");
    delete moreSites.host_permissions;
    moreSites.devtools_page = "devtools.html";
    writeFileSync(join(source, "devtools.html"), "<!doctype html><title>Synthetic DevTools fixture</title>");
    writeFileSync(join(source, "manifest.json"), JSON.stringify(moreSites));
    let devtoolsReviewed = false;
    await manager.reload(id, async next => { devtoolsReviewed = next.compatibility.includes("devtools"); return false; });
    assert(devtoolsReviewed);
    checks.push("A DevTools page requires review even without new API permissions or website matches");
    writeVersion("3.0.0", ["storage", "tabs"], "missing-script.js");
    await assert.rejects(manager.reload(id, approve));
    assert.equal((await manager.list())[0].version, "2.0.0");
    assert.equal(await versionOnPage(), "2.0.0");
    assert.equal(reviews, 2);
    checks.push("Electron load failure restores the working snapshot without granting extra access");
    await manager.setEnabled(id, false);
    assert.equal(await versionOnPage(), null);
    await assert.rejects(manager.reload(id, approve), /Enable the extension/);
    writeVersion("4.0.0", ["storage", "tabs", "history"]);
    await manager.setEnabled(id, true);
    assert.equal(await versionOnPage(), "2.0.0");
    checks.push("Disable removes scripts from fresh pages; enable uses the approved copy rather than changed source");
    const loadedPath = browserSession.extensions.getAllExtensions().find(extension => extension.id === id)!.path;
    cpSync(loadedPath, join(dirname(loadedPath), "previous"), { recursive: true });
    writeFileSync(join(loadedPath, ".wuu-installed-snapshot"), "uncommitted-fixture-revision");
    rmSync(join(loadedPath, "content.js"));
    browserSession.extensions.removeExtension(id);
    const restarted = new BrowserExtensionManager(browserSession, store);
    await restarted.initialize();
    assert.equal((await restarted.list())[0].id, id);
    assert.equal(await versionOnPage(), "2.0.0");
    checks.push("Manager restart restores an interrupted JS-only update from its committed snapshot without importing source permission changes");
    await restarted.remove(id);
    assert.equal((await restarted.list()).length, 0);
    assert.equal(await versionOnPage(), null);
    assert(existsSync(join(source, "manifest.json")));
    assert.equal(JSON.parse(readFileSync(join(source, "manifest.json"), "utf8")).version, "4.0.0");
    checks.push("Removal unloads the extension and preserves the source directory");
    writeVersion("2.0.0", ["storage", "tabs"]);
    const reinstalled = (await restarted.addFromDirectory(source, approve))[0];
    assert.equal(reinstalled.id, id);
    assert.equal(await versionOnPage(), "2.0.0");
    await waitFor("extension storage survives removal", () => page.webContents.executeJavaScript(`document.documentElement.dataset.extensionStorage === ${JSON.stringify(storageToken)}`));
    await restarted.remove(id);
    checks.push("Re-adding the same source retains its extension ID and browser storage");
    const legacyPath = join(root, "legacy-source");
    writeFileSync(store, JSON.stringify({ version: 1, extensions: [{ id, path: legacyPath, enabled: false }] }));
    const legacyManager = new BrowserExtensionManager(browserSession, store);
    await legacyManager.initialize();
    await assert.rejects(legacyManager.addFromDirectory(source, approve), /identity is already managed/);
    assert.deepEqual((await legacyManager.list()).map(extension => ({ id: extension.id, path: extension.path, loaded: extension.loaded })), [{ id, path: legacyPath, loaded: false }]);
    assert.equal(browserSession.extensions.getAllExtensions().length, 0);
    checks.push("Legacy IDs cannot collide with a new managed entry or redirect row actions");
    const key = generateKeyPairSync("rsa", { modulusLength: 2048 }).publicKey.export({ type: "spki", format: "der" }).toString("base64");
    writeFileSync(join(source, "manifest.json"), JSON.stringify({ ...JSON.parse(readFileSync(join(source, "manifest.json"), "utf8")), key }));
    const direct = await browserSession.extensions.loadExtension(source, { allowFileAccess: false });
    writeFileSync(store, JSON.stringify({ version: 1, extensions: [{ id: direct.id, path: source, enabled: true }] }));
    const keyedLegacy = new BrowserExtensionManager(browserSession, store);
    await keyedLegacy.initialize();
    const replacement = join(root, "replacement");
    cpSync(source, replacement, { recursive: true });
    const reviewsBeforeCollision = reviews;
    await assert.rejects(keyedLegacy.addFromDirectory(replacement, approve), /identity is already managed/);
    assert.equal(reviews, reviewsBeforeCollision);
    assert.equal(browserSession.extensions.getAllExtensions().find(extension => extension.id === direct.id)?.path, source);
    assert.equal(await versionOnPage(), "2.0.0");
    browserSession.extensions.removeExtension(direct.id);
    checks.push("Keyed legacy collisions are rejected before consent and preserve the original running extension");
    writeFileSync(join(artifacts, "extension-lifecycle.json"), JSON.stringify({ electron: process.versions.electron, checks, id, rendererSandbox }, null, 2));
    console.log(`PASS: ${checks.length} real browser extension lifecycle checks`);
  } finally {
    page.destroy();
    isolated.destroy();
    server.close();
  }
}

app.whenReady().then(async () => {
  mkdirSync(artifacts, { recursive: true });
  if (process.env.WUU_BROWSER_PIP_SKIP_EXTENSIONS !== "1") await verifyBrowserExtensions();
  if (process.env.WUU_BROWSER_EXTENSION_E2E_ONLY === "1") { app.quit(); return; }
  const main = new BrowserWindow({ width: 1000, height: 720, show: false });
  await main.loadURL("data:text/html;charset=utf-8,<body style='margin:0;background:%23edf0f5;font:14px system-ui;padding:12px'>Wuu · Browser panel</body>");
  const views: WebContentsView[] = [];
  const replies = new Map<string, unknown>();
  const errors = new Map<string, string>();
  const host = new BrowserHostCoordinator(
    { mainWindow: () => main } as unknown as WindowRegistry,
    { respond: (id, result) => replies.set(id, result), reject: (id, message) => errors.set(id, message) },
    defaultBrowserHostDeps(
      () => new BrowserWindow({ show: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
      () => {
        const view = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
        views.push(view);
        return view as unknown as BrowserViewHandle;
      },
    ), () => undefined,
  );
  let sequence = 0;
  async function request(method: string, params: Record<string, unknown>): Promise<void> {
    const id = String(++sequence);
    await host.handleServerRequest({ workdir, kind: "server-request", message: { id, method, params: { workdir, thread_id: activity.thread_id, tab_id: tabID, ...params } } });
    assert.equal(errors.get(id), undefined);
    assert.ok(replies.has(id));
  }
  await request("browser/open_tab", { initial_url: `data:text/html,${encodeURIComponent(page)}` });
  const contents = views[0].webContents;
  const factory = createObservationPiPFactory({ browserHost: host, isPackaged: false, parent: () => main });
  const coordinator = new ObservationCoordinator(
    { mainWindow: () => main } as unknown as WindowRegistry, undefined,
    (next, key, sink) => factory(next, key, sink, () => ({ x: 120, y: 120, width: 384, height: 240 })),
  );
  coordinator.setBrowserInPanel((next) => host.isInPanel(next.workdir, next.target!));
  host.setRendererSink({
    surface: () => undefined, userInput: () => undefined, adopted: () => undefined,
    presented: () => coordinator.refreshBrowserPresentation(),
  });
  coordinator.setActiveThread(activity.thread_id);
  coordinator.update(activity);
  const pip = BrowserWindow.getAllWindows().find((win) => win.getParentWindow() === main)!;
  assert.ok(pip);
  assert.equal(main.isVisible(), false, "Preview does not show the hidden main window");
  assert.equal(pip.isVisible(), false, "Preview waits for its main window to be shown");
  let shown = once(main, "show", { signal: AbortSignal.timeout(5000) });
  main.show();
  await shown;
  await waitFor("default background preview", () => pip.isVisible());
  assert.equal(pip.isFocused(), false, "Preview does not take keyboard focus");
  const overlay = (pip.contentView.children.find((view) => view !== views[0]) as WebContentsView).webContents;
  await waitFor("overlay ready", () => overlay.executeJavaScript(`typeof window.wuuPipInteract === 'function' && document.getElementById('ph').classList.contains('gone')`));
  const hidden = once(main, "hide", { signal: AbortSignal.timeout(5000) });
  main.hide();
  await hidden;
  coordinator.refreshBrowserPresentation();
  assert.equal(main.isVisible(), false, "Activity refresh cannot reveal the hidden main window");
  assert.equal(pip.isVisible(), false);
  shown = once(main, "show", { signal: AbortSignal.timeout(5000) });
  main.show();
  await shown;
  await waitFor("preview restored with main window", () => pip.isVisible());
  const minimized = once(main, "minimize", { signal: AbortSignal.timeout(5000) });
  main.minimize();
  await minimized;
  coordinator.refreshBrowserPresentation();
  assert.equal(pip.isVisible(), false, "Minimizing the main window hides the preview");
  const restored = once(main, "restore", { signal: AbortSignal.timeout(5000) });
  main.restore();
  await restored;
  main.focus();
  await waitFor("restored host focused", () => main.isFocused());
  await waitFor("preview restored after minimize", () => pip.isVisible());
  coordinator.setActiveThread("other-thread");
  coordinator.refreshBrowserPresentation();
  await waitFor("switching conversations hides preview", () => !pip.isVisible());
  coordinator.update({ ...activity, updated_at: new Date().toISOString() });
  assert.equal(pip.isVisible(), false, "Background updates cannot show another conversation's preview");
  await contents.executeJavaScript("window.sessionSwitchProbe = 42");
  assert.equal(await contents.executeJavaScript("window.sessionSwitchProbe"), 42, "The hidden browser remains operational");
  coordinator.setActiveThread(activity.thread_id);
  await waitFor("returning restores preview", () => pip.isVisible());
  const click = () => request("browser/cdp", { method: "click", params: { x: 240, y: 200 } });
  const pointer = (wc: typeof contents) => wc.executeJavaScript(`(() => {
    const el = document.getElementById('__wuu_agent_cursor');
    if (!el) return null;
    const style = getComputedStyle(el);
    const matrix = new DOMMatrix(style.transform);
    const [tipX, tipY] = style.transformOrigin.split(' ').map(parseFloat);
    return { x: matrix.e + tipX, y: matrix.f + tipY, width: el.offsetWidth, svg: el.querySelector('svg')?.outerHTML };
  })()`);

  const completedEvent = { workdir, kind: "notification" as const, message: {
    method: "turn/completed", params: { thread_id: activity.thread_id, turn: { status: "completed" } },
  } };
  // Completing after a user takeover must still expire. Ordinary activity
  // snapshots keep that controller value; they are not repeated takeovers.
  const completedActivity: ActivitySession = {
    ...activity, state: "user_controlled", controller: "user", updated_at: new Date().toISOString(),
  };
  coordinator.update(completedActivity);
  const completedAt = Date.now();
  coordinator.handleServerEvent(completedEvent);
  await waitFor("completion shown", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'false'`));
  // Seek the real animations, rather than racing screenshots against timers.
  for (const [time, name] of [[600, "icon"], [1800, "check"], [3200, "badge"]] as const) {
    await overlay.executeJavaScript(`(() => {
      document.getElementById('completion').getAnimations({subtree:true}).forEach(a => { a.pause(); a.currentTime = ${time}; });
      return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    })()`);
    await capture(pip, `pip-completion-${name}.png`);
  }
  await capture(pip, "pip-completed.png");
  await overlay.executeJavaScript(`document.getElementById('expand').focus();
    Promise.all(document.getAnimations().filter(a=>a.playState==='running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a=>a.finished))`);
  await capture(pip, "pip-controls-focused.png");
  assert.equal(await overlay.executeJavaScript(`document.activeElement.id`), "expand");
  await overlay.executeJavaScript(`document.activeElement.blur()`);

  coordinator.setActiveThread("other-thread");
  coordinator.setActiveThread(activity.thread_id);
  coordinator.handleServerEvent(completedEvent);
  assert.equal(pip.isVisible(), true, "Completion remains visible before its deadline");
  let unrelatedRefreshes = 0;
  const churn = setInterval(() => {
    unrelatedRefreshes++;
    // These are the native calls caused by renderer layout/state subscriptions,
    // including repeated success signals and fresh objects with the same owner.
    coordinator.update({ ...completedActivity, updated_at: new Date().toISOString() });
    coordinator.setActiveThread(activity.thread_id);
    coordinator.refreshBrowserPresentation();
    coordinator.setAppearance(false);
    const { width, height } = main.getContentBounds();
    coordinator.setBrowserPiPHostLayout(main.webContents.id, main, { host: { x: 0, y: 0, width, height }, obstacles: [] });
    pip.emit("ready-to-show");
    coordinator.handleServerEvent(completedEvent);
  }, 200);
  try {
    await waitFor("completed preview dismissed despite unrelated updates", () => !pip.isVisible(), 35_000);
  } finally {
    clearInterval(churn);
  }
  assert.ok(unrelatedRefreshes > 20, `Exercised ${unrelatedRefreshes} unrelated refreshes`);
  const dismissedAfterMs = Date.now() - completedAt;
  assert.ok(dismissedAfterMs >= 30_000 && dismissedAfterMs < 33_000, `Dismissed after ${dismissedAfterMs}ms`);
  assert.equal(contents.isDestroyed(), false, "Auto-dismiss retains the browser page");
  const stoppedActivity: ActivitySession = {
    ...activity, state: "stopped", controller: "none", updated_at: new Date().toISOString(),
  };
  coordinator.update(stoppedActivity);
  coordinator.refreshBrowserPresentation();
  coordinator.setActiveThread("other-thread");
  coordinator.setActiveThread(activity.thread_id);
  assert.equal(pip.isVisible(), false, "Snapshots and conversation changes cannot reopen an expired preview");
  await capture(main, "pip-auto-dismissed.png");
  // Reopening is a foreground user action; the preview deliberately waits
  // when its parent has lost focus during the real-time dismissal check.
  main.focus();
  await waitFor("host focused for reopen", () => main.isFocused());
  coordinator.showForThread(activity.thread_id);
  await waitFor("explicitly reopened preview", () => pip.isVisible());
  await waitFor("reopen clears completion", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'true'`));

  coordinator.handleServerEvent(completedEvent);
  const restartedAt = Date.now();
  coordinator.handleServerEvent({ workdir, kind: "notification", message: {
    method: "turn/started", params: { thread_id: activity.thread_id },
  } });
  coordinator.update({ ...activity, execution_id: "next-execution", updated_at: new Date().toISOString() });
  await waitFor("new turn clears completion", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'true'`));
  // Cross the previous completion's real deadline while checking that new work
  // stays visible, including throughout the timer's cancellation boundary.
  await waitFor("old completion deadline passed", () => {
    assert.equal(pip.isVisible(), true, "An old completion cannot dismiss the next task");
    return Date.now() - restartedAt >= 31_000;
  }, 35_000);
  await capture(pip, "pip-new-task-retained.png");
  writeFileSync(join(artifacts, "completion-dismissal.json"), JSON.stringify({
    dismissedAfterMs, unrelatedRefreshes, retainedPage: !contents.isDestroyed(),
    stayedDismissedAfterRefresh: true, explicitReopen: true, newTaskSurvivedOldDeadline: true,
  }, null, 2));

  // Host measurements above choose the product's compact card. The input
  // projection checks below deliberately use the fixture's fixed 0.3 scale.
  pip.setBounds({ x: 120, y: 120, width: 384, height: 240 });
  coordinator.handleServerEvent(completedEvent);
  await waitFor("completion precedes real browser input", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'false'`));
  await click();
  await waitFor("new work clears completion", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'true'`));
  assert.equal(await contents.executeJavaScript("window.clicks"), 1, "PiP input reaches the page target");
  // Input dispatch waits for arrival; the pointer can still be settling onto
  // its exact destination when the click response returns.
  await waitFor("PiP pointer settled", async () => {
    const point = await pointer(overlay);
    return point && Math.abs(point.x - 240 * .3) < .1 && Math.abs(point.y - 200 * .3) < .1;
  });
  const first = await pointer(overlay);
  assert.ok(first);
  assert.equal(await pointer(contents), null, "PiP paints only one pointer");
  await capture(pip, "pip-light.png");

  pip.setBounds({ x: 120, y: 120, width: 512, height: 320 });
  await waitFor("resize projection", async () => Math.abs((await pointer(overlay)).x - 240 * .4) < .1);
  assert.equal((await pointer(overlay)).width, first.width, "Pointer size is independent of page zoom");
  await contents.executeJavaScript("document.body.classList.add('dark')");
  await capture(pip, "pip-dark.png");
  await overlay.executeJavaScript(`window.wuuPipAppearance({dark:true});document.getElementById('close').focus();
    Promise.all(document.getAnimations().filter(a=>a.playState==='running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a=>a.finished))`);
  await capture(pip, "pip-dark-controls.png");
  await overlay.executeJavaScript("document.activeElement.blur()");
  pip.setSize(160, 120);
  await overlay.debugger.attach("1.3");
  await overlay.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await overlay.executeJavaScript("document.documentElement.style.fontSize='20px';window.wuuPipCompleted(true)");
  assert.equal(await overlay.executeJavaScript("document.getElementById('completion').getAnimations({subtree:true}).length"), 0, "Reduced motion shows completion without animation");
  await capture(pip, "pip-small-reduced-motion.png");
  overlay.debugger.detach();
  await overlay.executeJavaScript("window.wuuPipCompleted(false)");

  host.reportBounds(workdir, tabID, main as unknown as BrowserHostWindowHandle, { x: 0, y: 42, width: 950, height: 620 }, 1, true);
  await waitFor("docking the page dismisses its preview", () => !pip.isVisible());
  await click();
  assert.equal(await contents.executeJavaScript("window.clicks"), 2);
  await waitFor("panel pointer settled", async () => {
    const point = await pointer(contents);
    return point && Math.abs(point.x - 240) < .1 && Math.abs(point.y - 200) < .1;
  });
  const panel = await pointer(contents);
  assert.equal(panel.svg, first.svg, "Both surfaces use the same pointer artwork");
  await waitFor("PiP pointer removed", async () => (await pointer(overlay)) === null);
  await capture(main, "panel-dark.png");

  // Navigation clears the old point; reduced motion arrives without travel.
  await request("browser/cdp", { method: "navigate", params: { url: `data:text/html,${encodeURIComponent(page)}` } });
  await contents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  main.setSize(620, 600);
  host.reportBounds(workdir, tabID, main as unknown as BrowserHostWindowHandle, { x: 0, y: 42, width: 580, height: 500 }, 1, true);
  await contents.executeJavaScript("document.body.style.fontSize='20px'");
  await click();
  assert.equal(await contents.executeJavaScript("window.clicks"), 1);
  await capture(main, "panel-light-large-text.png");
  host.updateActivity({ ...activity, controller: "user", state: "foreground_controlled" });
  await waitFor("takeover clears pointer", async () => (await pointer(contents)) === null);

  await coordinator.shutdown();
  host.destroyAll();
  assert.ok(pip.isDestroyed());
  console.log(`browser-pip-e2e: PASS (default preview, session visibility, docking, completion auto-dismiss/reopen/cancellation, PiP/panel input, projection, resize, navigation, reduced motion, takeover; ${artifacts})`);
  main.destroy();
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
