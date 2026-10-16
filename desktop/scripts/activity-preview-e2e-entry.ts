// Production Electron preview shell and content adapters, isolated browser
// pages, and a real subprocess producing synthetic native frames. This does
// not establish native macOS capture, input, permissions, or compositor support.
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer, type ServerResponse } from "node:http";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { app, BrowserWindow, WebContentsView } from "electron";
import type { ActivitySession, JsonValue } from "../src/shared/protocol";
import { ActivityPiPSurface } from "../src/main/activityPiPWindow";
import type { ActivityPiPContentContext } from "../src/main/activityPiPContent";
import { BrowserPiPContent } from "../src/main/browserPiPWindow";
import { NativePiPContent } from "../src/main/nativePiPContent";
import { ObservationCoordinator } from "../src/main/cuaActivityWindows";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

const desktop = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = process.env.WUU_ACTIVITY_PREVIEW_OUTPUT ?? join(desktop, "out", "activity-preview");
const fixture = process.env.WUU_ACTIVITY_PREVIEW_FIXTURE;
assert(fixture, "Run through activity-preview-e2e.cjs");
mkdirSync(output, { recursive: true });
app.setPath("userData", join(fixture, "profile"));
process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG = join(output, "native-transport.jsonl");
writeFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG, "");
const checks: { name: string; detail?: unknown }[] = [];
const events: unknown[] = [];
function check(name: string, condition: unknown, detail?: unknown) {
  assert(condition, `${name}${detail ? `: ${JSON.stringify(detail)}` : ""}`);
  checks.push({ name, ...(detail === undefined ? {} : { detail }) });
}
async function waitFor(label: string, predicate: () => Promise<unknown> | unknown) {
  const deadline = Date.now() + 10_000;
  while (!(await predicate())) {
    if (Date.now() > deadline) throw new Error(`Timed out: ${label}`);
    await new Promise(resolve => setTimeout(resolve, 15));
  }
}
// Samples a card's frame until its appearance or dismissal motion has ended.
async function settledMotion(window: BrowserWindow) {
  const samples: { x: number; y: number; width: number; height: number; opacity: number; visible: boolean }[] = [];
  await waitFor("card motion settled", () => {
    samples.push({ ...window.getBounds(), opacity: window.getOpacity(), visible: window.isVisible() });
    // Long enough to rule out a spring momentarily at its peak.
    const last = samples.slice(-16);
    return last.length === 16 && last.every(sample => JSON.stringify(sample) === JSON.stringify(last[0]))
      && (!last[0].visible || last[0].opacity === 1);
  });
  return samples;
}
const sources = new Map<string, ServerResponse>();
const server = createServer((request, response) => {
  const url = new URL(request.url ?? "/", "http://127.0.0.1");
  if (url.pathname !== "/source") { response.writeHead(404).end(); return; }
  const target = url.searchParams.get("target")!;
  response.writeHead(200, { "Content-Type": "application/x-ndjson" });
  response.flushHeaders();
  sources.set(target, response);
  response.on("close", () => { if (sources.get(target) === response) sources.delete(target); });
});
async function emitNative(target: string, event: unknown) {
  await waitFor(`native source ${target} connected`, () => sources.has(target));
  sources.get(target)!.write(`${JSON.stringify(event)}\n`);
}
// Recorded AVC samples from a real VideoEncoder (160x100 solid RGB canvases,
// baseline, avc format). The second color is a delta frame referencing the first.
// Fixed bytes keep Linux acceptance independent of hardware encoder availability.
const videoFrames = JSON.parse(readFileSync(join(desktop, "scripts/activity-preview-frames.json"), "utf8"));
function frame(red: number, green: number, blue: number) {
  const sample = videoFrames.find((sample: { rgb: number[] }) => sample.rgb.join() === [red, green, blue].join());
  assert(sample, "missing AVC color fixture");
  const { rgb: _rgb, ...encoded } = sample;
  return encoded;
}
const workdir = join(fixture, "project");
const threadID = "activity-preview-thread";
let revision = 0;
function activity(kind: "browser" | "cua", target: string, extra: Partial<ActivitySession> = {}): ActivitySession {
  const timestamp = new Date(Date.UTC(2026, 0, 1) + ++revision).toISOString();
  return { id: `${kind}-activity`, execution_id: "fixture-run-1", kind, thread_id: threadID, workdir, plugin_id: kind === "browser" ? "embedded-browser" : "fixture-native",
    target, state: "background_controlled", controller: "agent", created_at: timestamp, updated_at: timestamp,
    ...(kind === "cua" ? { process_id: 1234, window_id: 5678 } : {}), ...extra };
}
let host: BrowserHostCoordinator | undefined;
let coordinator: ObservationCoordinator | undefined;
let main: BrowserWindow | undefined;
const browserViews: WebContentsView[] = [];
const contexts: { activity: ActivitySession; context: ActivityPiPContentContext }[] = [];
const helper = join(desktop, "scripts", "activity-preview-native-fixture.cjs");
let surfaceCount = 0;
let reconciliationCount = 0;
let snapshotActivities: ActivitySession[] = [];

app.whenReady().then(async () => {
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_URL = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
  main = new BrowserWindow({ width: 1000, height: 720, show: false, ...(process.platform === "linux" ? { frame: false, autoHideMenuBar: true } : {}) });
  await main.loadURL("data:text/html,<body style='background:rgb(235,238,243)'>Preview acceptance</body>");
  const registry = { mainWindow: () => main } as unknown as WindowRegistry;
  const replies = new Map<string, unknown>(), errors = new Map<string, string>();
  host = new BrowserHostCoordinator(registry, { respond: (id, value) => replies.set(id, value), reject: (id, value) => errors.set(id, value) },
    defaultBrowserHostDeps(
      () => new BrowserWindow({ show: false, frame: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
      () => {
        const view = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
        browserViews.push(view); return view as unknown as BrowserViewHandle;
      },
    ), () => undefined,
  );
  async function open(tabID: string, color: string) {
    const id = `open-${tabID}`;
    await host!.handleServerRequest({ workdir, kind: "server-request", message: { id, method: "browser/open_tab", params: { workdir, thread_id: threadID, tab_id: tabID,
      initial_url: `data:text/html,${encodeURIComponent(`<!doctype html><title>${tabID}</title><body style="margin:0;width:2400px;height:2400px;background:${color};font:24px system-ui"><h1>${tabID}</h1><button id="target" style="position:absolute;left:170px;top:170px;width:180px;height:60px">Target</button><button id="edge" style="position:absolute;right:30px;bottom:30px;width:100px;height:60px">Edge</button><div id="scroller" style="position:absolute;left:400px;top:300px;width:240px;height:160px;overflow:auto"><div style="width:1200px;height:1200px">Nested scrolling</div></div><script>window.clicks=0;document.onclick=e=>{window.clicks++;window.hit={x:e.clientX,y:e.clientY,target:e.target.id}};document.onwheel=e=>{window.wheel={x:e.clientX,y:e.clientY,dy:e.deltaY,cancelled:e.defaultPrevented}};document.getElementById('scroller').addEventListener('wheel',e=>{if(window.blockWheel)e.preventDefault()},{passive:false});</script></body>`)}` } } });
    assert.equal(errors.get(id), undefined); assert(replies.has(id));
  }
  await open("browser-a", "#dcecff"); await open("browser-b", "#ffe5ca");
  coordinator = new ObservationCoordinator(registry, async () => { reconciliationCount++; return snapshotActivities; }, (next, key, sink, bounds) => {
    surfaceCount++;
    return new ActivityPiPSurface({ activity: next, bounds: bounds(), sink, isPackaged: false, parent: () => main,
      createContent(next, context) {
        const traced = { ...context,
          onEvent(event: Parameters<typeof context.onEvent>[0]) { events.push({ target: next.target, event }); context.onEvent(event); },
          onFailure(message: string) { events.push({ target: next.target, failure: message }); context.onFailure(message); },
          onGone() { events.push({ target: next.target, gone: true }); context.onGone(); },
        };
        contexts.push({ activity: next, context: traced });
        if (next.kind === "browser") return new BrowserPiPContent(next, host!, traced);
        return new NativePiPContent(next, traced, { helper, isPackaged: false,
          preload: join(desktop, "out-e2e/nativePreview.cjs"),
          pageURL: pathToFileURL(join(desktop, "out-e2e/native-preview.html")).href,
        });
      },
    });
  });
  coordinator.setBrowserInPanel(next => host!.isInPanel(next.workdir, next.target!));
  coordinator.setActiveThread(threadID);
  let current = activity("browser", "browser-a");
  coordinator.update(current);
  const pip = BrowserWindow.getAllWindows().find(window => window.getParentWindow() === main)!;
  assert(pip);
  const pipID = pip.id;
  const overlay = pip.contentView.children.find(view => !browserViews.includes(view as WebContentsView)) as WebContentsView;
  const overlayID = overlay.webContents.id;
  await waitFor("chrome ready", () => overlay.webContents.executeJavaScript("typeof window.wuuPipMount === 'function'"));
  check("preview does not reveal a hidden main window", !main.isVisible() && !pip.isVisible());
  main.show();
  await waitFor("browser shown", () => pip.isVisible());
  // Evidence only: the blurred stand-in over the content partway through.
  const midway = new Promise<void>(resolve => setTimeout(() => void overlay.webContents.capturePage()
    .then(image => writeFileSync(join(output, "appearance-midway.png"), image.toPNG())).finally(resolve), 90));
  const appearance = await settledMotion(pip);
  await midway;
  const rest = appearance.at(-1)!;
  check("a card grows from its resting corner while fading in", appearance[0].width < rest.width * 0.85 && appearance[0].opacity < 0.5
    && appearance.every(sample => sample.x + sample.width === rest.x + rest.width && sample.y + sample.height === rest.y + rest.height),
    { first: appearance[0], rest, frames: appearance.length });
  writeFileSync(join(output, "appearance-frames.json"), JSON.stringify(appearance, null, 2));
  check("watch-only preview never focuses its target", !browserViews[0].webContents.isFocused());
  let browserRequest = 0;
  async function browserAction(method: string, params: Record<string, JsonValue>) {
    const id = `input-${++browserRequest}`;
    await host!.handleServerRequest({ workdir, kind: "server-request", message: { id, method: "browser/cdp",
      params: { workdir, thread_id: threadID, tab_id: "browser-a", method, params } } });
    assert.equal(errors.get(id), undefined); assert(replies.has(id));
    return (replies.get(id) as { result: { nodes: { node_id: number; name: string }[] } }).result;
  }
  const browser = browserViews[0].webContents;
  const originalDPR = await browserViews[1].webContents.executeJavaScript("devicePixelRatio");
  async function checkBrowserInput(label: string, width = 1280, height = 800) {
    await browser.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    const metrics = await browser.executeJavaScript("({width:innerWidth,height:innerHeight,dpr:devicePixelRatio})");
    check(`${label}: preserves browser layout and device scale`, metrics.width === width && metrics.height === height && metrics.dpr === originalDPR, metrics);
    await browserAction("click", { x: 240, y: 200 });
    const hit = await browser.executeJavaScript("window.hit");
    check(`${label}: coordinate click reaches its target`, hit?.target === "target" && Math.abs(hit.x - 240) <= 1 && Math.abs(hit.y - 200) <= 1, hit);
    const observed = await browserAction("observe", {});
    const edge = observed.nodes.find(node => node.name === "Edge");
    assert(edge);
    await browserAction("click", { node_id: edge.node_id });
    const edgeHit = await browser.executeJavaScript("window.hit");
    check(`${label}: observed edge node remains clickable`, edgeHit?.target === "edge", edgeHit);
    await browser.executeJavaScript("window.wheel = null;window.scrollEnded = false;window.addEventListener('scrollend', () => { window.scrollEnded = true; }, { once: true })");
    await browserAction("scroll", { x: 240, y: 200, dx: 90, dy: 120 });
    await waitFor(`${label}: wheel delivered`, () => browser.executeJavaScript("window.wheel"));
    const wheel = await browser.executeJavaScript("window.wheel");
    check(`${label}: scroll preserves aim`, Math.abs(wheel.x - 240) <= 1 && Math.abs(wheel.y - 200) <= 1, wheel);
    await waitFor(`${label}: page scrolling settles`, () => browser.executeJavaScript("window.scrollEnded"));
    const scroll = await browser.executeJavaScript("({x:scrollX,y:scrollY})");
    check(`${label}: actual page scroll preserves CSS-pixel distance`, Math.abs(scroll.x - 90) <= 1 && Math.abs(scroll.y - 120) <= 1, { scroll, wheel });
    await browser.executeJavaScript("window.scrollTo({left:0,top:0,behavior:'instant'});window.scrollEnded = false;document.getElementById('scroller').addEventListener('scrollend', () => { window.scrollEnded = true; }, { once: true })");
    await browserAction("scroll", { x: 450, y: 350, dx: 70, dy: 110 });
    await waitFor(`${label}: nested scrolling settles`, () => browser.executeJavaScript("window.scrollEnded"));
    const nested = await browser.executeJavaScript("({x:document.getElementById('scroller').scrollLeft,y:document.getElementById('scroller').scrollTop,rootX:scrollX,rootY:scrollY})");
    check(`${label}: actual nested scroll preserves aim and distance`, Math.abs(nested.x - 70) <= 1 && Math.abs(nested.y - 110) <= 1 && nested.rootX === 0 && nested.rootY === 0, nested);
    await browser.executeJavaScript("document.getElementById('scroller').scrollTo({left:0,top:0,behavior:'instant'});window.blockWheel=true;window.wheel=null");
    await browserAction("scroll", { x: 450, y: 350, dx: 70, dy: 110 });
    await waitFor(`${label}: nested handler cancels wheel`, () => browser.executeJavaScript("window.wheel?.cancelled"));
    const cancelled = await browser.executeJavaScript("({x:document.getElementById('scroller').scrollLeft,y:document.getElementById('scroller').scrollTop,rootX:scrollX,rootY:scrollY})");
    check(`${label}: page wheel cancellation prevents scrolling`, cancelled.x === 0 && cancelled.y === 0 && cancelled.rootX === 0 && cancelled.rootY === 0, cancelled);
    await browser.executeJavaScript("window.blockWheel=false");
    const image = await browser.capturePage();
    const view = browserViews[0].getBounds();
    writeFileSync(join(output, `${label.replace(/[^a-z0-9]+/gi, "-")}.png`), image.toPNG());
    check(`${label}: capture contains the displayed browser surface`, image.getSize().width === Math.round(view.width * originalDPR) && image.getSize().height === Math.round(view.height * originalDPR), { image: image.getSize(), view });
  }
  check("default preview uses the compact card", pip.getBounds().width === 250 && pip.getBounds().height === 250, pip.getBounds());
  await checkBrowserInput("default 250px preview");
  await host.handleServerRequest({ workdir, kind: "server-request", message: { id: "takeover-before-panel-bounds", method: "browser/set_visibility", params: { workdir, thread_id: threadID, tab_id: "browser-a", visible: true } } });
  const handoff = await browser.executeJavaScript("({width:innerWidth,height:innerHeight,dpr:devicePixelRatio})");
  check("takeover before panel measurement restores original browser metrics", handoff.width === 1280 && handoff.height === 800 && handoff.dpr === originalDPR, handoff);
  await host.handleServerRequest({ workdir, kind: "server-request", message: { id: "return-before-panel-bounds", method: "browser/set_visibility", params: { workdir, thread_id: threadID, tab_id: "browser-a", visible: false } } });
  coordinator.refreshBrowserPresentation();
  await waitFor("preview restored after unmeasured panel", () => BrowserWindow.fromWebContents(browser) === pip);
  await checkBrowserInput("unmeasured panel round trip");
  for (const size of [{ width: 80, height: 64 }, { width: 160, height: 120 }, { width: 384, height: 240 }, { width: 250, height: 250 }]) {
    pip.setBounds({ x: 120, y: 140, ...size });
    await checkBrowserInput(`resized ${size.width}x${size.height} preview`);
  }
  await browserAction("navigate", { url: browser.getURL() });
  await checkBrowserInput("navigated compact preview");
  browser.debugger.detach();
  await checkBrowserInput("debugger reattached after turn end");
  host.reportBounds(workdir, "browser-a", main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 950, height: 620 }, 1, true);
  await checkBrowserInput("restored browser panel", 950, 620);
  // Returning to observation captures the panel's new viewport without leaving
  // its display scale or input transform active on the normal panel.
  await host.handleServerRequest({ workdir, kind: "server-request", message: { id: "hide-panel", method: "browser/set_visibility", params: { workdir, thread_id: threadID, tab_id: "browser-a", visible: false } } });
  coordinator.refreshBrowserPresentation();
  await waitFor("browser returns to compact preview", () => BrowserWindow.fromWebContents(browser) === pip);
  await checkBrowserInput("remounted compact preview", 950, 620);
  pip.setBounds({ x: 120, y: 140, width: 360, height: 240 });
  const bounds = pip.getBounds();
  const browserContext = contexts.at(-1)!.context;
  current = activity("cua", "native-a"); coordinator.update(current);
  const nativeView = pip.contentView.children.find(view => view !== overlay) as WebContentsView;
  await waitFor("native video page ready", () => nativeView.webContents.executeJavaScript("isSecureContext && typeof VideoDecoder === 'function' && !!document.querySelector('canvas')"));
  // Delay delivery of a real decoded VideoFrame, preserving decoder behavior
  // and the production ACK path while exercising a late output after reset.
  await nativeView.webContents.executeJavaScript(`(() => {
    const Decoder = VideoDecoder;
    window.VideoDecoder = class extends Decoder {
      constructor(init) { super({ ...init, output(video) {
        if (window.fixtureHoldDecode) {
          window.fixtureDecodeEntered = true;
          window.fixtureReleaseDecode = () => init.output(video);
        } else init.output(video);
      } }); }
    };
  })()`);
  await emitNative("native-a", frame(20, 100, 210));
  await waitFor("native pixels presented", () => overlay.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  check("browser to native keeps window and chrome identity", !pip.isDestroyed() && pip.id === pipID && overlay.webContents.id === overlayID && surfaceCount === 1);
  check("provider switch preserves user geometry", JSON.stringify(pip.getBounds()) === JSON.stringify(bounds), pip.getBounds());
  const nativeContext = contexts.at(-1)!.context;
  browserContext.onGone(); browserContext.onFailure("late old browser failure"); browserContext.setMounted(false);
  check("retired browser callbacks cannot close current native preview", !pip.isDestroyed() && pip.isVisible());
  await waitFor("first native frame acknowledged", () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").includes('"type":"ack"'));
  writeFileSync(join(output, "native-content.png"), (await nativeView.webContents.capturePage()).toPNG());
  const firstNativePixel = await nativeView.webContents.executeJavaScript("Array.from(document.querySelector('canvas').getContext('2d').getImageData(0,0,1,1).data)");
  check("AVC keyframe renders the expected pixels", Math.abs(firstNativePixel[0] - 20) < 8 && Math.abs(firstNativePixel[1] - 100) < 8 && Math.abs(firstNativePixel[2] - 210) < 8, firstNativePixel);
  coordinator.setActiveThread("different-thread");
  await waitFor("native preview hides on session switch", () => !pip.isVisible());
  current = activity("cua", "native-a"); coordinator.update(current);
  coordinator.refreshBrowserPresentation();
  check("native activity stays hidden and connected in the background", !pip.isVisible() && sources.has("native-a") && current.controller === "agent");
  coordinator.setActiveThread(threadID);
  await emitNative("native-a", frame(20, 100, 210));
  await waitFor("native preview returns to its session", () => pip.isVisible());
  check("native session switching retains its window and source", pip.id === pipID && surfaceCount === 1 && sources.has("native-a"));
  await nativeView.webContents.executeJavaScript("window.fixtureHoldDecode = true");
  await emitNative("native-a", frame(220, 30, 40));
  await waitFor("native delta frame is decoded", () => nativeView.webContents.executeJavaScript("window.fixtureDecodeEntered === true"));
  await emitNative("native-a", { event: "capture_status", status: "error", message: "Synthetic permission denied" });
  await waitFor("capture error clears live presentation", () => overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone')"));
  check("capture failure keeps its symbol and hide control available without raw diagnostics", await overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone') && document.getElementById('ph-icon').checkVisibility() && !document.getElementById('close').hidden && document.getElementById('expand').hidden && !document.body.innerText.includes('Synthetic permission denied')"));
  await nativeView.webContents.executeJavaScript("window.fixtureReleaseDecode()");
  await waitFor("invalidated frame is acknowledged after discard", () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line)).some(entry => entry.target === "native-a" && entry.command?.type === "ack" && entry.command.sequence === 3));
  check("a late decoded frame cannot overwrite pixels after capture failure", JSON.stringify(await nativeView.webContents.executeJavaScript("Array.from(document.querySelector('canvas').getContext('2d').getImageData(0,0,1,1).data)")) === JSON.stringify(firstNativePixel));
  check("discarding a late decode cannot restore live presentation", await overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone')"));
  coordinator.setAppearance(true, { reducedMotion: true, fontSize: 20, fontFamily: "system-ui", locale: "en-US", surface: "#20242c", text: "#f8f8f8", muted: "#adb5c2" });
  await waitFor("appearance applied", () => overlay.webContents.executeJavaScript("document.documentElement.classList.contains('dark') && document.documentElement.lang === 'en-US' && getComputedStyle(document.body).fontSize === '20px'"));
  await overlay.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  check("workbench appearance reaches the separate preview renderer", await overlay.webContents.executeJavaScript("document.documentElement.classList.contains('reduce-motion') && getComputedStyle(document.getElementById('ph')).backgroundColor === 'rgb(32, 36, 44)'"));
  writeFileSync(join(output, "native-permission-error-dark.png"), (await overlay.webContents.capturePage()).toPNG());
  current = activity("browser", "browser-b"); coordinator.update(current);
  await waitFor("browser restored", () => BrowserWindow.fromWebContents(browserViews[1].webContents) === pip);
  check("native to browser keeps the same shell", pip.id === pipID && surfaceCount === 1 && overlay.webContents.id === overlayID);
  nativeContext.onGone(); nativeContext.onFailure("late native failure"); nativeContext.setMounted(false);
  check("retired native events do not replace the browser", BrowserWindow.fromWebContents(browserViews[1].webContents) === pip && pip.isVisible());
  await waitFor("retired native subprocess exits", () => !sources.has("native-a"));

  await overlay.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor("explicit hide", () => !pip.isVisible());
  current = activity("browser", "browser-a"); coordinator.update(current);
  coordinator.refreshBrowserPresentation();
  check("a later action cannot undo explicit hide", !pip.isVisible());
  coordinator.setActiveThread("different-thread"); coordinator.setActiveThread(threadID);
  check("conversation switching preserves explicit hide", !pip.isVisible());
  // A hidden card returns only while its window has focus; another app may
  // hold focus on a machine in use.
  app.focus({ steal: true }); main.focus();
  await waitFor("main window focused for reopen", () => main!.isFocused());
  coordinator.showForThread(threadID);
  await waitFor("explicit reopen", () => pip.isVisible());
  check("show preview preserves the current activity", contexts.at(-1)!.activity.id === current.id && current.controller === "agent");
  check("hide and reopen preserve the same shell", pip.id === pipID && surfaceCount === 1);
  coordinator.setAutoShow(false);
  coordinator.setActiveThread("auto-off-thread");
  coordinator.update(activity("browser", "browser-b", { thread_id: "auto-off-thread" }));
  check("automatic preview can be disabled without changing task control", surfaceCount === 1);
  await waitFor("switching hides the previous session", () => !pip.isVisible());
  const cards = () => BrowserWindow.getAllWindows().filter(window => window.getParentWindow() === main);
  coordinator.showForThread("auto-off-thread");
  await waitFor("manual show while auto-show is disabled", () => surfaceCount === 2 && cards().length === 2 && cards().some(card => card.id !== pipID && card.isVisible()));
  check("explicit show works while auto-show is off", surfaceCount === 2 && !pip.isVisible());
  const second = cards().find(card => card.id !== pipID)!;
  const secondID = second.id;
  coordinator.setAutoShow(true);
  coordinator.setActiveThread(threadID);
  await waitFor("returning restores only the owning preview", () => pip.isVisible() && !second.isVisible());
  check("switching sessions retains both preview windows", pip.id === pipID && second.id === secondID && surfaceCount === 2);
  const backgroundActivity = activity("browser", "browser-b", { thread_id: "auto-off-thread" });
  coordinator.update(backgroundActivity);
  coordinator.refreshBrowserPresentation();
  check("background updates cannot reveal another session", pip.isVisible() && !second.isVisible());
  coordinator.setActiveThread(undefined);
  await waitFor("empty selection hides all previews", () => !pip.isVisible() && !second.isVisible());
  coordinator.setActiveThread("auto-off-thread");
  await waitFor("second session restores its own preview", () => second.isVisible() && !pip.isVisible());
  coordinator.handleServerEvent({ workdir, kind: "notification", message: {
    method: "turn/completed", params: { thread_id: threadID, turn: { status: "completed" } },
  } });
  coordinator.setActiveThread(threadID);
  await waitFor("offscreen completion stays retired on return", () => !pip.isVisible() && !second.isVisible());
  coordinator.handleServerEvent({ workdir, kind: "notification", message: {
    method: "turn/started", params: { thread_id: threadID },
  } });
  await waitFor("next turn restores the owning preview", () => pip.isVisible());
  writeFileSync(join(output, "session-visibility.json"), JSON.stringify({ firstWindow: pipID, secondWindow: secondID, backgroundUpdatesHidden: true, backgroundCompletionRetired: true, windowsRetained: surfaceCount === 2 }, null, 2));
  const otherWindow = new BrowserWindow({ width: 400, height: 300, show: false, frame: false });
  await otherWindow.loadURL("data:text/html,<body>Foreground fixture window</body>");
  // Explicitly blur because the headless platform can mark two windows focused.
  // Exercise real BrowserWindow state; native compositor ordering needs macOS QA.
  main.blur(); otherWindow.show(); otherWindow.focus();
  await waitFor("other fixture window focused", () => otherWindow.isFocused() && !main!.isFocused());
  check("an already-visible preview does not steal focus when its parent blurs", pip.isVisible() && otherWindow.isFocused());
  await overlay.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor("preview hidden with background parent", () => !pip.isVisible());
  main.blur(); otherWindow.focus();
  await waitFor("other fixture window remains foreground after hide", () => otherWindow.isFocused() && !main!.isFocused());
  coordinator.showForThread(threadID);
  check("a hidden preview waits while its parent is unfocused", !pip.isVisible() && otherWindow.isFocused(), { mainFocused: main.isFocused(), otherFocused: otherWindow.isFocused(), pipVisible: pip.isVisible(), pipFocused: pip.isFocused() });
  main.focus();
  await waitFor("parent focus restores preview", () => pip.isVisible());
  check("showing a preview preserves parent keyboard focus", main.isFocused() && !pip.isFocused());
  otherWindow.destroy();
  main.hide(); await waitFor("hidden parent hides preview", () => !pip.isVisible());
  coordinator.refreshBrowserPresentation();
  check("activity refresh cannot show hidden main window", !main.isVisible() && !pip.isVisible());
  main.show(); await waitFor("restore preview", () => pip.isVisible());
  pip.close(); await waitFor("native window close hides preview", () => !pip.isVisible());
  check("closing preview preserves main and browser targets", !main.isDestroyed() && browserViews.every(view => !view.webContents.isDestroyed()));
  coordinator.showForThread(threadID);
  await waitFor("reopen after native close", () => pip.isVisible());
  const oldContext = contexts.at(-1)!.context;
  current = activity("cua", "native-b"); coordinator.update(current);
  await emitNative("native-b", frame(210, 100, 20));
  await waitFor("second native target shown", () => overlay.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  oldContext.onGone();
  await emitNative("native-b", { event: "gone" });
  await waitFor("target disappearance stops its source", () => !sources.has("native-b"));
  check("target disappearance does not destroy main or browser apps", !main.isDestroyed() && browserViews.every(view => !view.webContents.isDestroyed()));
  const surfacesAfterGone = surfaceCount, reconciliationBeforeGone = reconciliationCount;
  snapshotActivities = [current];
  coordinator.handleServerEvent({ workdir, kind: "notification", message: { method: "turn/event", params: { thread_id: threadID } } });
  await waitFor("registry snapshot reconciles after target disappearance", () => reconciliationCount > reconciliationBeforeGone);
  check("unchanged registry snapshot cannot resurrect a disappeared target", surfaceCount === surfacesAfterGone && !sources.has("native-b"));
  snapshotActivities = [];
  // The disappeared target retired its card; the next target gets a new one
  // while the old card may still be leaving.
  const retiredCards = new Set(cards().map(card => card.id));
  current = activity("cua", "native-c"); coordinator.update(current);
  await emitNative("native-c", frame(120, 30, 170));
  await waitFor("card for the next target", () => cards().some(card => !retiredCards.has(card.id)));
  const finalPip = cards().find(card => !retiredCards.has(card.id))!;
  // Context loss may retire the old shell. Locate its chrome through the real
  // renderer hook instead of relying on child-view ordering.
  let controlOverlay: WebContentsView | undefined;
  for (const child of finalPip.contentView.children) {
    if (child instanceof WebContentsView && await child.webContents.executeJavaScript("typeof window.wuuPipState === 'function'")) controlOverlay = child;
  }
  assert(controlOverlay);
  await waitFor("final native target shown", () => controlOverlay!.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  await controlOverlay.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor("native preview hidden", () => !finalPip.isVisible());
  // A newer sibling observation must not prevent a retained hidden native
  // source from receiving the original activity's stop/restoration signal.
  coordinator.update(activity("browser", "browser-b"));
  const hiddenStop = activity("cua", "native-c", { state: "stopped", controller: "none" });
  coordinator.update(hiddenStop);
  await waitFor("hidden native receives stop authority", () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line)).some(entry => entry.target === "native-c" && entry.command?.type === "activity" && entry.command.state === "stopped"));
  check("hidden native stop propagates after a newer browser observation", !finalPip.isVisible() && !main.isDestroyed());
  current = activity("cua", "native-d", { execution_id: "fixture-run-2" });
  coordinator.update(current); coordinator.showForThread(threadID);
  await emitNative("native-d", frame(30, 160, 90));
  await waitFor("fresh native run shown", () => controlOverlay!.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  coordinator.update({ ...hiddenStop, updated_at: new Date(Date.now() + 10_000).toISOString() });
  check("late old-epoch stop cannot hide the new preview", finalPip.isVisible());
  await emitNative("native-d", { event: "capture_status", status: "stopped" });
  await waitFor("capture stop reaches the preview", () => events.some(entry => {
    const received = entry as { target: string; event?: { event: string; status?: string } };
    return received.target === "native-d" && received.event?.event === "capture_status" && received.event.status === "stopped";
  }));
  check("stopped capture retains its frame and hide control", await controlOverlay.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone') && !document.getElementById('close').hidden && document.getElementById('expand').hidden"));
  check("stopped capture never implies task completion", await controlOverlay.webContents.executeJavaScript("document.getElementById('completion').getAttribute('aria-hidden') === 'true'"));
  current = activity("cua", "native-d", { execution_id: "fixture-run-2", controller: "user", state: "user_controlled" });
  coordinator.update(current);
  await waitFor("native takeover reaches capture", () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line)).some(entry => entry.target === "native-d" && entry.command?.type === "activity" && entry.command.controller === "user"));
  check("external takeover preserves the native preview shell", finalPip.isVisible());
  current = activity("browser", "browser-b", { execution_id: "fixture-run-3" });
  coordinator.update(current);
  await waitFor("live browser preview restored", () => controlOverlay!.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  current = activity("browser", "browser-b", { execution_id: "fixture-run-3", state: "stopped", controller: "none" });
  coordinator.update(current);
  coordinator.handleServerEvent({ workdir, kind: "notification", message: { method: "turn/completed",
    params: { thread_id: threadID, turn: { status: "interrupted" } } } });
  await waitFor("authoritative task stop dismisses the preview", () => !finalPip.isVisible());
  check("external task stop preserves the browser page", !browserViews[1].webContents.isDestroyed());
  await coordinator.shutdown();
  check("shutdown destroys preview shell", finalPip.isDestroyed());
  host.destroyAll(); main.destroy();
  for (const source of sources.values()) source.end();
  server.close();
  writeFileSync(join(output, "results.json"), JSON.stringify({ platform: process.platform, nativeCaptureVerified: false, checks, events, windowID: pipID, overlayID, surfaceCount }, null, 2));
  console.log(`PASS: live activity shell and synthetic native transport (${checks.length} checks); evidence ${output}`);
  app.exit(0);
}).catch(async error => {
  writeFileSync(join(output, "failure.json"), JSON.stringify({ error: String(error), checks, events }, null, 2));
  console.error(error);
  await coordinator?.shutdown(); host?.destroyAll(); main?.destroy();
  for (const source of sources.values()) source.end();
  server.close(); app.exit(1);
});
