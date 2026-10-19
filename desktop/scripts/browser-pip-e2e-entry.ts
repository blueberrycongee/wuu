// Runs the production browser host and PiP with isolated, synthetic pages.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { once } from "node:events";
import { mkdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { join } from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import type { ActivitySession, BrowserSurfaceSnapshot } from "../src/shared/protocol";
import { ActivityPiPSurface, type BrowserPiPWindowHandle } from "../src/main/activityPiPWindow";
import { createObservationPiPFactory } from "../src/main/browserPiPWindow";
import { createBrowserView } from "../src/main/browserView";
import { ObservationCoordinator } from "../src/main/cuaActivityWindows";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

const artifacts = process.env.WUU_BROWSER_PIP_E2E_ARTIFACTS ?? "/tmp/wuu-browser-cursor-e2e";
const fixture = process.env.WUU_BROWSER_PIP_FIXTURE;
assert(fixture, "Run through browser-pip-e2e.cjs");
app.setPath("userData", join(fixture, "profile"));
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

async function verifyLifecycleRecovery(main: BrowserWindow): Promise<void> {
  main.show();
  main.focus();
  await waitFor("lifecycle host focused", () => main.isFocused());
  const root = "/lifecycle-root";
  const linked = "/lifecycle-worktree";
  const threadID = "lifecycle-thread";
  const tabID = "lifecycle-tab";
  const views: WebContentsView[] = [];
  const replies = new Map<string, unknown>();
  const errors = new Map<string, string>();
  const invalidations: Array<{ workdir: string; threadID?: string }> = [];
  const surfaces: BrowserSurfaceSnapshot[] = [];
  const host = new BrowserHostCoordinator(
    { mainWindow: () => main } as unknown as WindowRegistry,
    { respond: (id, result) => replies.set(id, result), reject: (id, message) => errors.set(id, message) },
    defaultBrowserHostDeps(
      () => new BrowserWindow({ show: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
      () => {
        const view = createBrowserView();
        views.push(view);
        return view as unknown as BrowserViewHandle;
      },
    ),
    (workdir, threadID) => invalidations.push({ workdir, threadID }),
  );
  const factory = createObservationPiPFactory({ browserHost: host, isPackaged: false, parent: () => main });
  const coordinator = new ObservationCoordinator(
    { mainWindow: () => main } as unknown as WindowRegistry, undefined,
    (activity, key, sink) => factory(activity, key, sink, () => ({ x: 140, y: 140, width: 384, height: 240 })),
  );
  host.addTabCreatedListener((workdir, tabID, threadID) => coordinator.restoreBrowserTab(workdir, tabID, threadID));
  host.setRendererSink({ surface: (snapshot) => surfaces.push(snapshot), userInput: () => undefined,
    adopted: () => undefined, presented: () => coordinator.refreshBrowserPresentation() });
  coordinator.setBrowserInPanel((activity) => host.isInPanel(activity.workdir, activity.target!));
  let sequence = 0;
  const rawRequest = (params: Record<string, unknown>, method = "browser/open_tab") => {
    const id = `lifecycle-${++sequence}`;
    return { id, done: host.handleServerRequest({ workdir: root, kind: "server-request", message: {
      id, method, params: { request_id: id, workdir: linked, thread_id: threadID, tab_id: tabID, ...params },
    } }) };
  };
  const open = async (params: Record<string, unknown> = {}) => {
    const request = rawRequest({ initial_url: `data:text/html,${encodeURIComponent(page)}`, ...params });
    await request.done;
    assert.equal(errors.get(request.id), undefined);
    assert.ok(replies.has(request.id));
  };
  const nextActivity: ActivitySession = { ...activity, id: "lifecycle-activity", workdir: linked,
    thread_id: threadID, target: tabID, execution_id: "lifecycle-execution" };
  const notify = (method: string, params: Record<string, unknown>) => {
    const event = { workdir: root, kind: "notification" as const, message: { method, params } };
    host.handleServerEvent(event);
    coordinator.handleServerEvent(event);
  };
  const previewWindows = () => BrowserWindow.getAllWindows().filter((win) => win.getParentWindow() === main);
  await open();
  assert.equal(host.hasAgentTabs(root), true, "Linked-worktree tabs pin their transport owner");
  assert.equal(host.hasAgentTabs(linked), false, "A linked CWD is not a second transport owner");
  coordinator.setActiveThread(threadID);
  notify("activity/started", nextActivity);
  await waitFor("linked-worktree preview", () => previewWindows().some((win) => win.isVisible()));
  const crashedPreview = previewWindows()[0];
  const crashedOverlay = (crashedPreview.contentView.children.find((view) => view !== views[0]) as WebContentsView).webContents;
  const crashedContents = views[0].webContents;
  console.log("browser lifecycle: linked-worktree preview mounted");
  const crashed = once(crashedContents, "render-process-gone", { signal: AbortSignal.timeout(5000) });
  crashedContents.forcefullyCrashRenderer();
  await crashed;
  console.log("browser lifecycle: renderer crash delivered");
  await waitFor("crashed contents and preview disposed", () => crashedContents.isDestroyed() && crashedPreview.isDestroyed() && crashedOverlay.isDestroyed());
  console.log("browser lifecycle: crashed contents, preview and overlay disposed");
  assert.equal(host.surface(linked, tabID), undefined);
  assert.ok(surfaces.some((snapshot) => snapshot.tabID === tabID && snapshot.error));
  const deniedInput = rawRequest({ method: "click", params: { x: 240, y: 200 } }, "browser/cdp");
  await deniedInput.done;
  assert.match(errors.get(deniedInput.id) ?? "", /tab_not_found/);
  await open();
  notify("activity/updated", { ...nextActivity, updated_at: new Date().toISOString() });
  await waitFor("same-execution preview restored after real tab recreation", () => previewWindows().some((win) => win.isVisible()));
  const recoveredContents = views.at(-1)!.webContents;
  assert.equal(await recoveredContents.executeJavaScript("window.clicks"), 0, "A failed input is not replayed on the recovered page");
  console.log("browser lifecycle: same-execution preview restored without input replay");
  const secondCrash = once(recoveredContents, "render-process-gone", { signal: AbortSignal.timeout(5000) });
  recoveredContents.forcefullyCrashRenderer();
  await secondCrash;
  await waitFor("second crashed contents removed", () => host.surface(linked, tabID) === undefined);
  const snapshot = await host.runCommand(linked, tabID, "reload", `data:text/html,${encodeURIComponent(page)}`, threadID);
  assert.ok(snapshot && !snapshot.error, "User reload reopens a crashed page");
  assert.equal(await views.at(-1)!.webContents.executeJavaScript("window.clicks"), 0);
  await waitFor("reload restores preview", () => previewWindows().some((win) => win.isVisible()));
  console.log("browser lifecycle: user reload restored page and preview");

  await open({ thread_id: "sibling-thread", tab_id: "sibling-tab" });
  const sibling = views.at(-1)!.webContents;
  await open({ workdir: "/second-worktree", tab_id: "second-tab" });
  const secondWorktreeContents = views.at(-1)!.webContents;
  let navigationStarted!: () => void;
  const started = new Promise<void>((resolve) => { navigationStarted = resolve; });
  let releaseNavigation: (() => void) | undefined;
  const server = createServer((_request, response) => {
    releaseNavigation = () => response.end("<!doctype html><title>Late deleted page</title>");
    navigationStarted();
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const pending = rawRequest({ tab_id: "pending-tab", initial_url: `http://127.0.0.1:${address.port}/held` });
  await Promise.race([started, new Promise<never>((_, reject) => setTimeout(() => reject(new Error("Navigation never reached the local fixture")), 5000))]);
  const pendingContents = views.at(-1)!.webContents;
  const deletedContentsDestroyed = Promise.all([pendingContents, secondWorktreeContents].map((contents) =>
    once(contents, "destroyed", { signal: AbortSignal.timeout(5000) })));
  notify("thread/deleted", { thread_id: threadID, workdir: linked });
  releaseNavigation?.();
  await pending.done;
  server.closeAllConnections();
  await new Promise<void>((resolve) => server.close(() => resolve()));
  await deletedContentsDestroyed;
  assert.ok(errors.has(pending.id), "An in-flight navigation cannot report success after deletion");
  assert.equal(pendingContents.isDestroyed(), true);
  assert.equal(secondWorktreeContents.isDestroyed(), true, "Deletion cleans the same thread in every workdir");
  assert.equal(sibling.isDestroyed(), false, "Deletion retains unrelated sessions");
  assert.ok(invalidations.some((entry) => entry.workdir === linked && entry.threadID === threadID));
  assert.ok(invalidations.some((entry) => entry.workdir === "/second-worktree" && entry.threadID === threadID));
  await waitFor("deleted previews closed", () => previewWindows().length === 0);
  coordinator.update({ ...nextActivity, updated_at: new Date().toISOString() });
  coordinator.setActiveThread(threadID);
  assert.equal(previewWindows().length, 0, "A late snapshot cannot restore a deleted thread");
  console.log("browser lifecycle: deletion closes all workdirs and rejects late navigation and snapshots");
  const siblingDestroyed = once(sibling, "destroyed", { signal: AbortSignal.timeout(5000) });
  host.onClientTorndown(root);
  coordinator.dropWorkdir(root);
  await siblingDestroyed;
  assert.equal(sibling.isDestroyed(), true);
  assert.equal(host.hasAgentTabs(root), false, "Transport teardown releases its linked-worktree pin");
  await coordinator.shutdown();
  host.destroyAll();
  console.log("browser lifecycle: transport teardown disposes sibling and releases pin");

  let nativeWindow!: BrowserWindow;
  let beginStop!: (finish: () => void) => void;
  const captureStopping = new Promise<() => void>((resolve) => { beginStop = resolve; });
  const surface = new ActivityPiPSurface({ activity: nextActivity, bounds: { x: 140, y: 140, width: 384, height: 240 }, isPackaged: false,
    createWindow: (bounds) => {
      nativeWindow = new BrowserWindow({ ...bounds, show: false, frame: false, webPreferences: { sandbox: true } });
      return nativeWindow as unknown as BrowserPiPWindowHandle;
    },
    reducedMotion: () => false,
    sink: { onEvent: () => undefined, onFailure: (message) => { throw new Error(message); }, onGone: () => undefined },
    createContent: () => ({ start: () => undefined, setVisible: () => undefined, layout: () => undefined,
      stop: (done) => beginStop(done) }),
  });
  surface.start();
  const orphanOverlay = (nativeWindow.contentView.children[0] as WebContentsView).webContents;
  nativeWindow.showInactive();
  let stopCompleted = false;
  const stopped = new Promise<void>((resolve) => surface.stop(() => { stopCompleted = true; resolve(); }));
  nativeWindow.destroy();
  const completeCaptureStop = await captureStopping;
  const prematureCompletion = stopCompleted;
  completeCaptureStop();
  await stopped;
  assert.equal(prematureCompletion, false, "Window destruction cannot report completion before capture disposal");
  await waitFor("orphan overlay destroyed", () => orphanOverlay.isDestroyed());
  assert.equal(orphanOverlay.isDestroyed(), true, "Externally destroyed PiP windows release their overlay WebContents");
  console.log("browser lifecycle: external window destruction awaits capture and disposes overlay");
  writeFileSync(join(artifacts, "lifecycle-recovery.json"), JSON.stringify({ linkedWorktreeOwner: true,
    realRendererCrash: true, sameExecutionPreviewRecovery: true, userReload: true, inputReplayCount: 0,
    deletedAcrossWorkdirs: true, pendingNavigationRejected: true, siblingRetained: true,
    lateDeletedSnapshotRejected: true, pinReleased: true, externalWindowStopOrdering: true, overlayDisposed: true }, null, 2));
}

app.whenReady().then(async () => {
  mkdirSync(artifacts, { recursive: true });
  const main = new BrowserWindow({ width: 1000, height: 720, show: false });
  await main.loadURL("data:text/html;charset=utf-8,<body style='margin:0;background:%23edf0f5;font:14px system-ui;padding:12px'>Wuu · Browser panel</body>");
  if (process.env.WUU_BROWSER_PIP_LIFECYCLE_ONLY === "1") {
    await verifyLifecycleRecovery(main);
    console.log(`browser-pip-e2e: PASS (browser lifecycle recovery; ${artifacts})`);
    main.destroy();
    app.exit(0);
    return;
  }
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

  const stoppedActivity: ActivitySession = {
    ...activity, state: "stopped", controller: "none", updated_at: new Date().toISOString(),
  };
  coordinator.update(stoppedActivity);
  coordinator.setActiveThread("other-thread");
  coordinator.setActiveThread(activity.thread_id);
  coordinator.handleServerEvent(completedEvent);
  assert.equal(pip.isVisible(), true, "Completion remains visible before its deadline");
  await waitFor("completed preview dismissed", () => !pip.isVisible(), 35_000);
  const dismissedAfterMs = Date.now() - completedAt;
  assert.ok(dismissedAfterMs >= 30_000 && dismissedAfterMs < 33_000, `Dismissed after ${dismissedAfterMs}ms`);
  assert.equal(contents.isDestroyed(), false, "Auto-dismiss retains the browser page");
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
    dismissedAfterMs, retainedPage: !contents.isDestroyed(),
    stayedDismissedAfterRefresh: true, explicitReopen: true, newTaskSurvivedOldDeadline: true,
  }, null, 2));

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
  await verifyLifecycleRecovery(main);
  console.log(`browser-pip-e2e: PASS (default preview, session visibility, docking, completion auto-dismiss/reopen/cancellation, PiP/panel input, projection, resize, navigation, reduced motion, takeover, linked ownership, renderer crash/recovery, deletion, external window cleanup; ${artifacts})`);
  main.destroy();
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
