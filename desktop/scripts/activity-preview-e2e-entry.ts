// Production Electron preview shell and content adapters, isolated browser
// pages, and a real subprocess producing synthetic native frames. This does
// not establish native macOS capture, input, permissions, or packaged behavior.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
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
const events: { target?: string; event?: Parameters<ActivityPiPContentContext["onEvent"]>[0]; failure?: string; gone?: boolean }[] = [];
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
    return new ActivityPiPSurface({ activity: next, bounds: bounds(),
      sink: { ...sink, onEvent(event) { events.push({ target: next.target, event }); sink.onEvent(event); } },
      isPackaged: false, parent: () => main,
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
  let pip = BrowserWindow.getAllWindows().find(window => window.getParentWindow() === main)!;
  assert(pip);
  const pipID = pip.id;
  let overlay = pip.contentView.children.find(view => !browserViews.includes(view as WebContentsView)) as WebContentsView;
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
  const browserPip = pip;
  function card(target: string) {
    const context = contexts.find(item => item.activity.target === target)!.context;
    const window = context.window as unknown as BrowserWindow;
    // The production surface raises its chrome above the content on mount.
    const chrome = window.contentView.children.at(-1) as WebContentsView;
    return { window, chrome, context };
  }
  const cards = () => BrowserWindow.getAllWindows().filter(window => window.getParentWindow() === main);
  const visibleCards = () => cards().filter(window => window.isVisible());
  const transport = () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line));
  current = activity("cua", "native-a"); coordinator.update(current);
  pip = card("native-a").window; overlay = card("native-a").chrome;
  const nativeView = pip.contentView.children.find(view => view instanceof WebContentsView
    && view.webContents.id !== overlay.webContents.id) as WebContentsView;
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
  check("browser and native targets retain independent windows", browserPip.isVisible() && pip.isVisible() && browserPip.id !== pip.id && surfaceCount === 2);
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
  check("native session switching retains its window and source", pip.id !== pipID && surfaceCount === 2 && sources.has("native-a"));
  await nativeView.webContents.executeJavaScript("window.fixtureHoldDecode = true");
  await emitNative("native-a", frame(220, 30, 40));
  await waitFor("native delta frame is decoded", () => nativeView.webContents.executeJavaScript("window.fixtureDecodeEntered === true"));
  await emitNative("native-a", { event: "capture_status", status: "error", message: "Synthetic permission denied" });
  await waitFor("capture error clears live presentation", () => overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone')"));
  check("capture failure keeps its status, retry and hide controls available without raw diagnostics", await overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone') && document.getElementById('ph-status').checkVisibility() && !document.getElementById('retry').hidden && !document.getElementById('close').hidden && document.getElementById('expand').hidden && !document.body.innerText.includes('Synthetic permission denied')"));
  await nativeView.webContents.executeJavaScript("window.fixtureReleaseDecode()");
  await waitFor("invalidated frame is acknowledged after discard", () => readFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG!, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line)).some(entry => entry.target === "native-a" && entry.command?.type === "ack" && entry.command.sequence === 3));
  check("a late decoded frame cannot overwrite pixels after capture failure", JSON.stringify(await nativeView.webContents.executeJavaScript("Array.from(document.querySelector('canvas').getContext('2d').getImageData(0,0,1,1).data)")) === JSON.stringify(firstNativePixel));
  check("discarding a late decode cannot restore live presentation", await overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone')"));
  coordinator.setAppearance(true, { reducedMotion: true, fontSize: 20, fontFamily: "system-ui", locale: "en-US", surface: "#20242c", text: "#f8f8f8", muted: "#adb5c2" });
  await waitFor("appearance applied", () => overlay.webContents.executeJavaScript("document.documentElement.classList.contains('dark') && document.documentElement.lang === 'en-US' && getComputedStyle(document.body).fontSize === '20px'"));
  await overlay.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  check("workbench appearance reaches the separate preview renderer", await overlay.webContents.executeJavaScript("document.documentElement.classList.contains('reduce-motion') && getComputedStyle(document.getElementById('ph')).backgroundColor === 'rgb(32, 36, 44)'"));
  await settledMotion(pip);
  writeFileSync(join(output, "native-permission-error-dark.png"), (await overlay.webContents.capturePage()).toPNG());

  await nativeView.webContents.executeJavaScript("window.fixtureHoldDecode = false");
  await emitNative("native-a", { event: "capture_status", status: "healthy" });
  await emitNative("native-a", { event: "auto_frame", frame: frame(20, 100, 210) });
  await waitFor("native stream recovered for stack checks", () => overlay.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  current = activity("browser", "browser-b"); coordinator.update(current);
  await waitFor("second browser tab shown", () => card("browser-b").window.isVisible());
  check("one session retains browser and native targets", surfaceCount === 3 && visibleCards().length === 3);
  coordinator.update(activity("browser", "browser-a"));
  check("revisiting a tab reuses its window", surfaceCount === 3 && card("browser-a").window === browserPip);

  for (const [target, color] of [["browser-c", "#d5f0df"], ["browser-d", "#f0dbf4"], ["browser-e", "#f4eed6"]]) {
    await open(target, color);
    coordinator.update(activity("browser", target));
    check("stack never exposes more than five windows after " + target, visibleCards().length <= 5);
  }
  await waitFor("five retained cards visible", () => visibleCards().length === 5);
  check("six targets retain six independent surfaces", cards().length === 6 && surfaceCount === 6 && !browserPip.isVisible());
  check("overflow keeps the browser page alive", !browser.isDestroyed());

  async function screenshot(name: string) {
    // Keep the disposable fixture above unrelated desktop windows in evidence.
    main!.setAlwaysOnTop(true);
    main!.moveTop();
    await Promise.all(visibleCards().map(settledMotion));
    const bounds = main!.getBounds();
    if (process.platform === "darwin") {
      execFileSync("/usr/sbin/screencapture", ["-x", "-R" + [bounds.x, bounds.y, bounds.width, bounds.height].join(","), join(output, name + ".png")]);
    }
    writeFileSync(join(output, name + ".json"), JSON.stringify(visibleCards().map(window => ({ id: window.id, bounds: window.getBounds() })), null, 2));
    main!.setAlwaysOnTop(false);
  }
  function layout() {
    const { width, height } = main!.getContentBounds();
    coordinator!.setBrowserPiPHostLayout(main!.webContents.id, main!, { host: { x: 20, y: 20, width: width - 40, height: height - 40 }, obstacles: [] });
  }
  main.setBounds({ x: 80, y: 70, width: 1000, height: 760 }); layout();
  coordinator.setAppearance(false, { reducedMotion: false, fontSize: 15, fontFamily: "system-ui", locale: "en-US", surface: "#ffffff", text: "#222222", muted: "#666666" });
  await screenshot("five-cards-light");
  const promoted = card("browser-c");
  const point = promoted.window.getBounds();
  await promoted.chrome.webContents.executeJavaScript("location.href='wuu-pip://drag?phase=start&x=" + point.x + "&y=" + point.y + "&vx=0&vy=0'");
  await waitFor("trailing drag started", () => events.some(item => item.target === "browser-c" && item.event?.event === "drag_start"));
  check("grabbing a trailing card preserves its frame", JSON.stringify(promoted.window.getBounds()) === JSON.stringify(point));
  await promoted.chrome.webContents.executeJavaScript("location.href='wuu-pip://drag?phase=move&x=" + (point.x + 60) + "&y=" + (point.y - 40) + "&vx=0&vy=0'");
  await waitFor("trailing card follows its grab point", () => promoted.window.getBounds().x === point.x + 60 && promoted.window.getBounds().y === point.y - 40);
  await promoted.chrome.webContents.executeJavaScript("location.href='wuu-pip://drag?phase=end&x=" + (point.x + 60) + "&y=" + (point.y - 40) + "&vx=0&vy=0'");
  await waitFor("trailing card promoted on release", () => promoted.window.getBounds().y >= card("browser-e").window.getBounds().y);
  await screenshot("promoted-card");
  check("promotion preserves every target", surfaceCount === 6 && visibleCards().length === 5);
  main.setBounds({ x: 80, y: 70, width: 680, height: 600 }); layout();
  coordinator.setAppearance(true, { reducedMotion: true, fontSize: 20, fontFamily: "system-ui", locale: "en-US", surface: "#20242c", text: "#f8f8f8", muted: "#adb5c2" });
  await screenshot("five-cards-dark-large");
  const hostBounds = main.getContentBounds();
  check("five cards fit the narrow host", visibleCards().every(window => {
    const bounds = window.getBounds();
    return bounds.x >= hostBounds.x && bounds.y >= hostBounds.y
      && bounds.x + bounds.width <= hostBounds.x + hostBounds.width
      && bounds.y + bounds.height <= hostBounds.y + hostBounds.height;
  }));
  coordinator.setBrowserPiPHostLayout(main.webContents.id, main, null);
  check("foreground dialogs suppress the whole stack immediately", visibleCards().length === 0);
  layout();
  await waitFor("stack restores after dialog", () => visibleCards().length === 5);

  await promoted.chrome.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor("closing one card reveals overflow", () => !promoted.window.isVisible() && browserPip.isVisible() && visibleCards().length === 5);
  coordinator.update(activity("browser", "browser-c"));
  check("activity updates preserve individual dismissal", !promoted.window.isVisible() && visibleCards().length === 5);
  coordinator.setActiveThread("different-thread");
  check("session switch hides all six retained targets", visibleCards().length === 0 && cards().length === 6);
  coordinator.setActiveThread(threadID);
  await waitFor("session restores its own stack", () => visibleCards().length === 5);
  check("session switch preserves individual dismissal", !promoted.window.isVisible());

  coordinator.setAutoShow(false);
  check("auto-show preference hides every card", visibleCards().length === 0);
  coordinator.showForThread(threadID);
  await waitFor("manual show restores the capped stack", () => visibleCards().length === 5 && promoted.window.isVisible());
  check("manual show retains all target windows", surfaceCount === 6);
  main.hide();
  check("hidden parent suppresses every card", visibleCards().length === 0);
  coordinator.refreshBrowserPresentation();
  check("background refresh cannot reveal the parent", !main.isVisible() && visibleCards().length === 0);
  main.show();
  await waitFor("visible parent restores capped stack", () => visibleCards().length === 5);

  current = activity("cua", "native-b"); coordinator.update(current);
  await emitNative("native-b", { event: "auto_frame", frame: frame(210, 100, 20) });
  await waitFor("second native target renders", () => card("native-b").chrome.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone')"));
  check("two native targets share an activity without sharing a surface", card("native-a").window !== card("native-b").window && sources.has("native-a") && sources.has("native-b"));
  const takeover = activity("cua", "native-b", { controller: "user", state: "user_controlled" });
  const notifyActivity = (method: string, activity: ActivitySession) => coordinator!.handleServerEvent({ workdir, kind: "notification",
    message: { method, params: activity as unknown as JsonValue } });
  coordinator.update(takeover); // A snapshot can arrive before its notification.
  notifyActivity("activity/control_changed", takeover);
  await waitFor("takeover reaches both native targets", () => ["native-a", "native-b"].every(target => transport().some(entry => entry.target === target && entry.command?.type === "activity" && entry.command.controller === "user")));
  await waitFor("current target restoration is requested", () => transport().some(entry => entry.target === "native-b" && entry.restoration));
  check("takeover restores only the current native target", transport().filter(entry => entry.restoration).map(entry => entry.target).join() === "native-b");
  const nativeBSession = transport().filter(entry => entry.target === "native-b" && entry.command?.type === "open").at(-1)!.session;
  const beforeReplay = transport().filter(entry => entry.target === "native-b" && entry.command?.type === "activity").length;
  coordinator.update(takeover);
  notifyActivity("activity/control_changed", takeover);
  coordinator.refreshBrowserPresentation();
  await waitFor("snapshot replay reaches native target", () => transport().filter(entry => entry.target === "native-b" && entry.command?.type === "activity").length > beforeReplay);
  check("snapshot and duplicate notifications never repeat restoration", transport().filter(entry => entry.restoration).length === 1);
  card("native-b").context.onFailure("Synthetic recovery while user controlled");
  await card("native-b").chrome.webContents.executeJavaScript("document.getElementById('retry').click()");
  await waitFor("user-controlled helper restarts", () => transport().some(entry => entry.target === "native-b" && entry.session !== nativeBSession && entry.command?.type === "activity"));
  check("helper startup replays authority without restoring again", transport().filter(entry => entry.restoration).length === 1
    && transport().filter(entry => entry.target === "native-b" && entry.session !== nativeBSession && entry.command?.type === "activity").every(entry => entry.command.restore === false));
  contexts.filter(item => item.activity.target === "native-b").at(-1)!.context.onFailure("Synthetic failure before stop");
  const stopped = activity("cua", "native-b", { state: "stopped", controller: "none" });
  coordinator.update(stopped);
  notifyActivity("activity/stopped", stopped);
  await waitFor("stop reaches visible and overflow native targets", () => ["native-a", "native-b"].every(target => transport().some(entry => entry.target === target && entry.command?.type === "activity" && entry.command.state === "stopped")));
  await waitFor("current target stop restoration is requested", () => transport().filter(entry => entry.target === "native-b" && entry.restoration).length === 2);
  check("stop does not restore historical targets", transport().filter(entry => entry.restoration).map(entry => entry.target).join() === "native-b,native-b");
  check("current stop restoration survives capture recovery exactly once", transport().filter(entry => entry.target === "native-b" && entry.command?.type === "open").length === 3);
  current = activity("cua", "native-b", { execution_id: "fixture-run-2" });
  coordinator.update(current);
  await emitNative("native-b", { event: "auto_frame", frame: frame(210, 100, 20) });
  await waitFor("new execution resumes its own target", () => transport().filter(entry => entry.target === "native-b" && entry.command?.type === "activity").at(-1)?.command.controller === "agent");
  coordinator.update({ ...stopped, updated_at: new Date(Date.now() + 10_000).toISOString() });
  check("late retired execution cannot stop the new target", transport().filter(entry => entry.target === "native-b" && entry.command?.type === "activity").at(-1)?.command.controller === "agent");

  const retiringSession = transport().filter(entry => entry.target === "native-b" && entry.command?.type === "open").at(-1)!.session;
  contexts.filter(item => item.activity.target === "native-b").at(-1)!.context.onFailure("Synthetic failure before superseded restoration");
  notifyActivity("activity/stopped", activity("cua", "native-b", { execution_id: "fixture-run-2", state: "stopped", controller: "none" }));
  coordinator.update(activity("cua", "native-a", { execution_id: "fixture-run-3" }));
  await waitFor("historical helper restarts after the source moved on", () => {
    const log = transport(), latest = log.filter(entry => entry.target === "native-b" && entry.command?.type === "open").at(-1)?.session;
    return latest !== retiringSession && log.some(entry => entry.session === latest && entry.command?.type === "activity" && entry.command.state === "stopped");
  });
  check("superseding execution cancels a deferred historical restoration", transport().filter(entry => entry.restoration).length === 2);
  current = activity("cua", "native-b", { execution_id: "fixture-run-3" });
  coordinator.update(current);
  await waitFor("current target resumes after the deferred restoration check", () => transport().filter(entry => entry.target === "native-b" && entry.command?.type === "activity").at(-1)?.command.controller === "agent");

  const gone = card("native-b");
  await emitNative("native-b", { event: "gone" });
  await waitFor("target disappearance closes only its stream", () => !sources.has("native-b") && gone.window.isDestroyed());
  check("target disappearance preserves siblings", sources.has("native-a") && browserViews.every(view => !view.webContents.isDestroyed()), { nativeA: sources.has("native-a"), browserPages: browserViews.map(view => !view.webContents.isDestroyed()) });
  const afterGone = surfaceCount, beforeReconcile = reconciliationCount;
  snapshotActivities = [current];
  coordinator.handleServerEvent({ workdir, kind: "notification", message: { method: "turn/event", params: { thread_id: threadID } } });
  await waitFor("snapshot reconciles after disappearance", () => reconciliationCount > beforeReconcile);
  check("snapshot cannot resurrect disappeared target", surfaceCount === afterGone && !sources.has("native-b"));
  gone.context.onFailure("late failure from removed target");
  check("late callbacks cannot affect siblings", visibleCards().length === 5);

  coordinator.handleServerEvent({ workdir, kind: "notification", message: { method: "turn/completed",
    params: { thread_id: threadID, turn: { status: "interrupted" } } } });
  check("interrupted turn dismisses the whole session stack", visibleCards().length === 0);
  check("task interruption preserves browser pages", browserViews.every(view => !view.webContents.isDestroyed()));
  await coordinator.shutdown();
  check("shutdown destroys every preview shell", cards().length === 0);
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
