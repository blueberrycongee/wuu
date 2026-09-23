// Runs the production browser host and PiP with isolated, synthetic pages.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import type { ActivitySession } from "../src/shared/protocol";
import { createObservationPiPFactory } from "../src/main/browserPiPWindow";
import { ObservationCoordinator } from "../src/main/cuaActivityWindows";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

const artifacts = process.env.WUU_BROWSER_PIP_E2E_ARTIFACTS ?? "/tmp/wuu-browser-cursor-e2e";
const profile = mkdtempSync(join(tmpdir(), "wuu-browser-cursor-"));
app.setPath("userData", profile);
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

async function waitFor(label: string, check: () => Promise<boolean> | boolean): Promise<void> {
  const deadline = Date.now() + 5000;
  while (!(await check())) {
    if (Date.now() > deadline) throw new Error(`Timed out: ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

function capture(win: BrowserWindow, name: string): void {
  if (process.platform !== "darwin") return;
  // BrowserWindow.capturePage excludes sibling WebContentsViews. Capture the
  // native composition so the page and the pointer are verified together.
  const nativeID = win.getMediaSourceId().split(":")[1];
  execFileSync("screencapture", ["-x", "-o", "-l", nativeID, join(artifacts, name)]);
}

app.whenReady().then(async () => {
  mkdirSync(artifacts, { recursive: true });
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
    await host.handleServerRequest({ workdir, kind: "server-request", message: { id, method, params: { workdir, tab_id: tabID, ...params } } });
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
  await waitFor("preview restored after minimize", () => pip.isVisible());
  coordinator.setActiveThread("other-thread");
  coordinator.refreshBrowserPresentation();
  assert.equal(pip.isVisible(), false, "Switching conversations hides the preview");
  coordinator.update({ ...activity, updated_at: new Date().toISOString() });
  assert.equal(pip.isVisible(), false, "Background updates cannot reveal another conversation's preview");
  coordinator.setActiveThread(activity.thread_id);
  assert.equal(pip.isVisible(), true, "Returning to the owning conversation restores its preview");
  const click = () => request("browser/cdp", { method: "click", params: { x: 240, y: 200 } });
  const pointer = (wc: typeof contents) => wc.executeJavaScript(`(()=>{const el=document.getElementById('__wuu_agent_cursor');if(!el)return null;const m=new DOMMatrix(getComputedStyle(el).transform);return {x:m.e,y:m.f,width:el.offsetWidth,svg:el.querySelector('svg')?.outerHTML};})()`);

  coordinator.handleServerEvent({ workdir, kind: "notification", message: {
    method: "turn/completed", params: { thread_id: activity.thread_id, turn: { status: "completed" } },
  } });
  await waitFor("completion shown", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'false'`));
  await overlay.executeJavaScript(`Promise.all(document.getElementById('completion').getAnimations({subtree:true}).map(a=>a.finished))`);
  capture(pip, "pip-completed.png");

  await click();
  await waitFor("new work clears completion", () => overlay.executeJavaScript(`document.getElementById('completion').getAttribute('aria-hidden') === 'true'`));
  assert.equal(await contents.executeJavaScript("window.clicks"), 1, "PiP input reaches the page target");
  const first = await pointer(overlay);
  assert.ok(first);
  assert.ok(Math.abs(first.x - (240 * .3 - 3)) < .1);
  assert.ok(Math.abs(first.y - (200 * .3 - 2.5)) < .1);
  assert.equal(await pointer(contents), null, "PiP paints only one pointer");
  capture(pip, "pip-light.png");

  pip.setBounds({ x: 120, y: 120, width: 512, height: 320 });
  await waitFor("resize projection", async () => Math.abs((await pointer(overlay)).x - (240 * .4 - 3)) < .1);
  assert.equal((await pointer(overlay)).width, first.width, "Pointer size is independent of page zoom");
  await contents.executeJavaScript("document.body.classList.add('dark')");
  capture(pip, "pip-dark.png");

  host.reportBounds(workdir, tabID, main as unknown as BrowserHostWindowHandle, { x: 0, y: 42, width: 950, height: 620 }, 1, true);
  assert.equal(pip.isVisible(), false, "Docking the page replaces its preview");
  await click();
  assert.equal(await contents.executeJavaScript("window.clicks"), 2);
  const panel = await pointer(contents);
  assert.equal(panel.svg, first.svg, "Both surfaces use the same pointer artwork");
  assert.ok(Math.abs(panel.x - 237) < .1);
  await waitFor("PiP pointer removed", async () => (await pointer(overlay)) === null);
  capture(main, "panel-dark.png");

  // Navigation clears the old point; reduced motion arrives without travel.
  await request("browser/cdp", { method: "navigate", params: { url: `data:text/html,${encodeURIComponent(page)}` } });
  await contents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  main.setSize(620, 600);
  host.reportBounds(workdir, tabID, main as unknown as BrowserHostWindowHandle, { x: 0, y: 42, width: 580, height: 500 }, 1, true);
  await contents.executeJavaScript("document.body.style.fontSize='20px'");
  await click();
  assert.equal(await contents.executeJavaScript("window.clicks"), 1);
  capture(main, "panel-light-large-text.png");
  host.updateActivity({ ...activity, controller: "user", state: "foreground_controlled" });
  await waitFor("takeover clears pointer", async () => (await pointer(contents)) === null);

  await coordinator.shutdown();
  host.destroyAll();
  assert.ok(pip.isDestroyed());
  console.log(`browser-pip-e2e: PASS (default preview, session visibility, docking, completion, PiP/panel input, projection, resize, navigation, reduced motion, takeover; ${artifacts})`);
  main.destroy();
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
