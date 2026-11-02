// Native acceptance for the real browser host, observation coordinator and PiP.
// The pages and competing application are synthetic; no account, user profile,
// model, network, screen pixels or private window titles are inspected.
import assert from "node:assert/strict";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { once } from "node:events";
import { join } from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import type { ActivitySession } from "../src/shared/protocol";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import { createObservationPiPFactory } from "../src/main/browserPiPWindow";
import { ObservationCoordinator } from "../src/main/cuaActivityWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

type NativeSnapshot = { frontPID: number; coverNormalWindowZIndex: number; probeWindows: Array<{ id: number; zIndex: number; layer: number }> };
const output = process.env.WUU_BROWSER_PIP_E2E_ARTIFACTS ?? "/tmp/wuu-browser-pip-ordering";
const directory = mkdtempSync(join(tmpdir(), "wuu-production-pip-ordering-"));
app.setPath("userData", directory);
app.on("window-all-closed", () => undefined);
mkdirSync(output, { recursive: true });
const report: { status: string; electron: string | undefined; productionSurfaceTested: boolean; scenarios: string[]; samples: unknown[]; error?: string } = {
  status: "running", electron: process.versions.electron, productionSurfaceTested: false, scenarios: [], samples: [],
};
const save = () => writeFileSync(join(output, "report.json"), JSON.stringify(report, null, 2));
let cover: ChildProcess | undefined;
const cleanup = () => { cover?.kill(); rmSync(directory, { recursive: true, force: true }); };
const deadline = setTimeout(() => { report.status = "failed"; report.error = "Production preview acceptance timed out"; save(); cleanup(); app.exit(1); }, 120_000);
app.on("will-quit", () => { clearTimeout(deadline); cleanup(); });
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(label: string, test: () => boolean | Promise<boolean>): Promise<void> {
  const end = Date.now() + 10_000;
  while (!await test()) {
    if (Date.now() > end) throw new Error(`Timed out: ${label}`);
    await delay(50);
  }
}

app.whenReady().then(async () => {
  assert.equal(process.platform, "darwin", "Native window ordering acceptance requires macOS");
  const helper = join(directory, "observe");
  const source = join(directory, "observe.swift");
  assert.ok(process.env.WUU_ACTIVITY_ORDERING_HELPER_SOURCE, "Launcher must supply the checked-in synthetic native fixture");
  writeFileSync(source, readFileSync(process.env.WUU_ACTIVITY_ORDERING_HELPER_SOURCE));
  execFileSync("/usr/bin/xcrun", ["swiftc", source, "-o", helper], { stdio: "inherit" });
  const main = new BrowserWindow({ width: 900, height: 680, show: false, title: "Synthetic Wuu host" });
  await main.loadURL("data:text/html,<body style='background:%23dcecff'>Synthetic Wuu host</body>");
  const native = (): NativeSnapshot => JSON.parse(execFileSync(helper, [String(process.pid), String(cover?.pid ?? 0)], { encoding: "utf8" }));
  const panels = () => BrowserWindow.getAllWindows().filter(win => win.getTitle() === "Wuu Live Activity");
  const waitForPresented = (label: string, count: number) => waitFor(label, () => {
    const current = panels();
    const snapshot = native();
    return current.length === count && current.every(win => win.isVisible() && win.getOpacity() === 1
      && snapshot.probeWindows.some(item => item.id === Number(win.getMediaSourceId().split(":")[1])));
  });
  const observe = (label: string, background = true) => {
    const snapshot = native();
    const hostID = Number(main.getMediaSourceId().split(":")[1]);
    const sample = { label, ...snapshot, hostID, coverPID: cover?.pid, hostFocused: main.isFocused(), panels: panels().map(win => ({
      id: Number(win.getMediaSourceId().split(":")[1]), visible: win.isVisible(), opacity: win.getOpacity(), focused: win.isFocused(), nativeParentIsHost: win.getParentWindow() === main,
    })) };
    report.samples.push(sample); save(); console.log(JSON.stringify(sample));
    const host = snapshot.probeWindows.find(win => win.id === hostID);
    assert.ok(host, "Host window must be observable; an unavailable native baseline is not a pass");
    if (background) {
      assert.equal(snapshot.frontPID, cover?.pid, "Passive browser activity must leave the external app foreground");
      assert.ok(snapshot.coverNormalWindowZIndex >= 0, "Synthetic external normal window must be on screen");
      assert.ok(host.zIndex > snapshot.coverNormalWindowZIndex, "Passive browser activity must not raise Wuu above the external app");
      assert.equal(main.isFocused(), false);
      for (const panel of sample.panels) assert.equal(panel.focused, false, "Passive preview must not become key");
    }
    for (const panel of sample.panels) if (panel.visible) {
      assert.equal(panel.nativeParentIsHost, true, "Presented previews retain native host ownership");
      // Preparation intentionally shows a fully transparent native panel so
      // Chromium can paint. macOS can omit it from the on-screen window list.
      if (panel.opacity > 0) assert.ok(snapshot.probeWindows.some(win => win.id === panel.id), "Painted preview must have an observable native window");
    }
  };
  main.show(); main.focus();
  cover = spawn(helper, ["--cover"], { stdio: "ignore" });
  await waitFor("synthetic external foreground baseline", () => {
    assert.equal(cover!.exitCode, null, "Synthetic cover must remain running");
    assert.equal(cover!.signalCode, null, "Synthetic cover must not be terminated");
    const snapshot = native();
    const host = snapshot.probeWindows.find(win => win.id === Number(main.getMediaSourceId().split(":")[1]));
    return snapshot.frontPID === cover!.pid && snapshot.coverNormalWindowZIndex >= 0
      && Boolean(host && host.zIndex > snapshot.coverNormalWindowZIndex) && !main.isFocused();
  });
  observe("before-browser-activity");

  const workdir = "/synthetic-ordering";
  const thread = "ordering-thread";
  const views: WebContentsView[] = [];
  const results = new Map<string, unknown>();
  const errors = new Map<string, string>();
  const registry = { mainWindow: () => main } as unknown as WindowRegistry;
  const host = new BrowserHostCoordinator(registry, { respond: (id, result) => results.set(id, result), reject: (id, message) => errors.set(id, message) },
    defaultBrowserHostDeps(
      () => new BrowserWindow({ show: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
      () => { const view = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } }); views.push(view); return view as unknown as BrowserViewHandle; },
    ), () => undefined);
  let sequence = 0;
  async function open(tab: string): Promise<void> {
    const id = String(++sequence);
    await host.handleServerRequest({ workdir, kind: "server-request", message: { id, method: "browser/open_tab", params: {
      workdir, thread_id: thread, tab_id: tab, initial_url: "data:text/html,<body style='background:%23fbe2aa'>Synthetic browser page</body>",
    } } });
    assert.equal(errors.get(id), undefined); assert.ok(results.has(id));
  }
  const factory = createObservationPiPFactory({ browserHost: host, isPackaged: false, parent: () => main });
  const coordinator = new ObservationCoordinator(registry, undefined, (activity, key, sink) => factory(activity, key, sink, () => ({ x: 750, y: 450, width: 320, height: 220 })));
  coordinator.setBrowserInPanel(activity => host.isInPanel(activity.workdir, activity.target!));
  host.setRendererSink({ surface: () => undefined, userInput: () => undefined, adopted: () => undefined, presented: () => coordinator.refreshBrowserPresentation() });
  const expanded: string[] = [];
  coordinator.setBrowserExpandHandler(activity => expanded.push(activity.target!));
  let revision = 0;
  const activity = (tab: string): ActivitySession => ({ id: `activity-${tab}`, kind: "browser", thread_id: thread, workdir,
    plugin_id: "embedded-browser", target: tab, state: "background_controlled", controller: "agent",
    created_at: "2026-01-01T00:00:00Z", updated_at: new Date(Date.UTC(2026, 0, 1, 0, 0, ++revision)).toISOString(),
  });
  coordinator.setActiveThread(thread);
  await open("first");
  coordinator.update(activity("first"));
  report.productionSurfaceTested = true;
  observe("immediately-after-first-preview");
  await waitFor("first preview visible", () => panels().length === 1 && panels()[0].isVisible());
  const first = panels()[0];
  const overlay = (first.contentView.children.find(view => view instanceof WebContentsView && !views.includes(view)) as WebContentsView).webContents;
  await waitFor("production overlay ready", () => overlay.executeJavaScript("typeof window.wuuPipInteract === 'function' && document.getElementById('ph').classList.contains('gone')"));
  await waitForPresented("first preview painted in native compositor", 1);
  observe("first-preview-painted");
  report.scenarios.push("background initial browser tab and preview creation");

  for (let frame = 0; frame < 4; frame++) {
    await views[0].webContents.executeJavaScript(`document.body.textContent='Synthetic frame ${frame}'`);
    coordinator.update(activity("first")); coordinator.refreshBrowserPresentation();
    observe(`passive-refresh-${frame}`); await delay(100);
  }
  report.scenarios.push("real page frames and repeated activity refresh");
  coordinator.setActiveThread("other-thread");
  await waitFor("preview hidden on other conversation", () => !first.isVisible());
  observe("preview-hidden");
  coordinator.setActiveThread(thread);
  await waitFor("preview reshown", () => first.isVisible());
  await waitForPresented("reshown preview painted", 1); observe("preview-reshown");
  report.scenarios.push("conversation hide and repeated presentation");

  await open("second"); coordinator.update(activity("second"));
  await waitFor("two previews visible", () => panels().length === 2 && panels().every(win => win.isVisible()));
  await waitForPresented("two previews painted", 2); observe("second-preview-and-stack");
  coordinator.refreshBrowserPresentation(); observe("restack-two-previews");
  report.scenarios.push("multiple previews and stack ordering");
  await overlay.executeJavaScript("document.getElementById('expand').click()");
  await waitFor("Expand callback through production overlay", () => expanded.includes("first"));
  observe("expand-callback");
  report.scenarios.push("production overlay Expand callback (DOM activation; not physical mouse acceptance)");

  coordinator.dropWorkdir(workdir); host.onClientTorndown(workdir);
  await waitFor("old previews destroyed", () => panels().length === 0);
  await open("reconnected"); coordinator.update(activity("reconnected"));
  await waitFor("reconnected preview visible", () => panels().length === 1 && panels()[0].isVisible());
  await waitForPresented("reconnected preview painted", 1); observe("preview-after-reconnect");
  report.scenarios.push("browser host teardown and new preview after reconnect");

  // Explicit user foregrounding remains allowed; it is not a passive update.
  main.show(); main.focus();
  await waitFor("explicit host foreground", () => main.isFocused());
  coordinator.refreshBrowserPresentation();
  await waitForPresented("foreground preview painted", 1);
  observe("explicit-foreground", false);
  report.scenarios.push("explicit host foreground and live preview remain available");
  const enteredFullscreen = once(main, "enter-full-screen", { signal: AbortSignal.timeout(10_000) });
  main.setFullScreen(true);
  await enteredFullscreen;
  await waitFor("fullscreen host on active Space", () => native().probeWindows.some(win => win.id === Number(main.getMediaSourceId().split(":")[1])));
  await waitFor("preview visible in host fullscreen Space", () => {
    const snapshot = native();
    return panels().length === 1 && panels().every(win => win.isVisible() && win.getOpacity() === 1 && snapshot.probeWindows.some(item => item.id === Number(win.getMediaSourceId().split(":")[1])));
  });
  observe("host-fullscreen-preview", false);
  execFileSync(helper, ["--activate", String(cover!.pid)]);
  await waitFor("external app on its desktop Space", () => {
    const snapshot = native();
    return snapshot.frontPID === cover!.pid && !snapshot.probeWindows.some(win => win.id === Number(main.getMediaSourceId().split(":")[1]));
  });
  const away = native();
  const previewIDs = panels().map(win => Number(win.getMediaSourceId().split(":")[1]));
  report.samples.push({ label: "away-from-fullscreen-host-space", ...away, previewIDs }); save();
  assert.ok(away.coverNormalWindowZIndex >= 0, "External cover must be visible on its desktop Space");
  assert.ok(!away.probeWindows.some(win => previewIDs.includes(win.id)), "Preview must not leak onto a Space without its host");
  main.focus();
  await waitFor("return to fullscreen host Space", () => main.isFocused() && native().probeWindows.some(win => win.id === Number(main.getMediaSourceId().split(":")[1])));
  await waitFor("preview returns with fullscreen host", () => {
    const snapshot = native();
    return panels().length === 1 && panels().every(win => win.isVisible() && win.getOpacity() === 1 && snapshot.probeWindows.some(item => item.id === Number(win.getMediaSourceId().split(":")[1])));
  });
  observe("return-to-fullscreen-host-space", false);
  const leftFullscreen = once(main, "leave-full-screen", { signal: AbortSignal.timeout(10_000) });
  main.setFullScreen(false);
  await leftFullscreen;
  report.scenarios.push("native fullscreen, leave host Space, preview stays with host, and return");
  await coordinator.shutdown(); host.destroyAll();
  assert.equal(panels().length, 0);
  report.status = "passed"; save();
  main.destroy(); app.quit();
}).catch(error => { report.status = "failed"; report.error = String(error.stack ?? error); save(); console.error(error); clearTimeout(deadline); cleanup(); app.exit(1); });
