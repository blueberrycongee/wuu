// Production Electron preview shell and content adapters, isolated browser
// pages, and a real subprocess producing synthetic native frames. This does
// not establish native macOS capture, input, permissions, or compositor support.
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type ServerResponse } from "node:http";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { app, BrowserWindow, WebContentsView, screen } from "electron";
import type { ActivitySession, JsonValue } from "../src/shared/protocol";
import { registerRenderableFileProtocol, registerRenderableFileScheme } from "../src/main/renderableFileProtocol";
import { renderableFileURL } from "../src/main/renderableFileURLs";
import { CodexPetWindowManager } from "../src/main/codexPetWindow";
import { browserPiPOverlayHTML } from "../src/main/activityPiPChrome";
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
for (const name of ["results.json", "failure.json"]) rmSync(join(output, name), { force: true });
app.setPath("userData", join(fixture, "profile"));
registerRenderableFileScheme();
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
let pointer = { x: -100, y: -100 };
let surfaceCount = 0;
let reconciliationCount = 0;
let snapshotActivities: ActivitySession[] = [];

app.whenReady().then(async () => {
  registerRenderableFileProtocol(fixture!);
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
    return new ActivityPiPSurface({ activity: next, bounds: bounds(), sink, isPackaged: false, parent: () => main, cursorPosition: () => pointer,
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
    await waitFor(`${label}: browser viewport settles`, () => browser.executeJavaScript(`innerWidth === ${width} && innerHeight === ${height}`));
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
  // Exercise the real placement coordinator, preview shell and Wuu pet host.
  const petManager = new CodexPetWindowManager({ onClose() {}, onJump() {},
    onSubmit: async () => ({ ok: true }), onShowApp() {},
    onPreviewHostChanged: () => coordinator!.refreshPetHost(), isPackaged: false });
  coordinator.setPetHost(() => petManager.previewHost(), active => petManager.setPreviewActive(active), () => petManager.requestPreviewHost());
  coordinator.setReturnHomeHandler(next => { main!.show(); coordinator!.setActiveThread(next.thread_id); });
  const face = `data:image/png;base64,${readFileSync(join(desktop, "src/renderer/assets/mascot-face.png")).toString("base64")}`;
  const sheet = await main.webContents.executeJavaScript(`(async () => {
    const image = new Image(); image.src = ${JSON.stringify(face)}; await image.decode();
    const canvas = document.createElement('canvas'); canvas.width = 192 * 8; canvas.height = 208 * 9;
    const ctx = canvas.getContext('2d');
    for(let row=0;row<9;row++)for(let col=0;col<8;col++)ctx.drawImage(image,col*192+8,row*208+16,176,176);
    return canvas.toDataURL();
  })()`);
  const sheetPath = join(fixture!, "wuu-pet.png");
  writeFileSync(sheetPath, Buffer.from(sheet.split(",")[1], "base64"));
  const petSnapshot = { home: fixture!, enabled: true, selected_id: "wuu-fixture", errors: [], pets: [{
    id: "wuu-fixture", display_name: "Wuu", description: "Wuu artwork acceptance fixture", manifest_path: "", spritesheet_path: sheetPath, spritesheet_url: renderableFileURL(sheetPath),
  }] };
  petManager.sync({ ...petSnapshot, enabled: false });
  check("desktop handoff is available with the pet shape disabled", coordinator.presentationForThread(threadID)?.petAvailable);
  coordinator.setPresentation(threadID, "pet");
  check("pending desktop host keeps the original parent and placement", pip.getParentWindow() === main && coordinator.presentationForThread(threadID)?.placement === "home");
  await waitFor("shape-free desktop host accepts the existing preview", () => petManager.previewHost()?.ready && pip.getParentWindow() === petManager.previewHost()?.window);
  await settledMotion(pip);
  const shapeFree = petManager.previewHost()!.window;
  check("disabled pet is a tiny unfocused host without visible pet artwork", shapeFree.getBounds().width === 1 && shapeFree.getBounds().height === 1 && !shapeFree.isFocused() && pip.id === pipID && BrowserWindow.fromWebContents(browser) === pip, shapeFree.getBounds());
  await host.handleServerRequest({ workdir, kind: "server-request", message: { id: "shape-free-panel-takeover", method: "browser/set_visibility", params: { workdir, thread_id: threadID, tab_id: "browser-a", visible: true } } });
  host.reportBounds(workdir, "browser-a", main as unknown as BrowserHostWindowHandle, { x: 0, y: 0, width: 950, height: 620 }, 1, true);
  coordinator.refreshBrowserPresentation();
  await waitFor("panel takeover hides its desktop preview", () => !pip.isVisible());
  check("temporary panel takeover retains shape-free desktop ownership", !shapeFree.isDestroyed() && coordinator.presentationForThread(threadID)?.placement === "pet");
  await host.handleServerRequest({ workdir, kind: "server-request", message: { id: "shape-free-panel-return", method: "browser/set_visibility", params: { workdir, thread_id: threadID, tab_id: "browser-a", visible: false } } });
  coordinator.refreshBrowserPresentation();
  await waitFor("agent control restores the same desktop parent", () => pip.isVisible() && pip.getParentWindow() === shapeFree && BrowserWindow.fromWebContents(browser) === pip);
  coordinator.setPresentation(threadID, "home");
  await waitFor("last preview releases the shape-free desktop host", () => shapeFree.isDestroyed() && pip.getParentWindow() === main && pip.isVisible());
  coordinator.setPresentation(threadID, "pet");
  coordinator.setPresentation(threadID, "hide");
  await waitFor("hide cancels a pending desktop request", () => !petManager.previewHost() && !pip.isVisible());
  check("pending cancellation preserves the browser task", !browser.isDestroyed() && current.controller === "agent");
  coordinator.setPresentation(threadID, "show");
  petManager.sync(petSnapshot);
  await waitFor("Wuu pet renderer shown", () => petManager.previewHost()?.ready);
  coordinator.setBrowserPiPHostLayout(main.webContents.id, main, { host: { x: 0, y: 0, width: 1000, height: 720 }, obstacles: [] });
  coordinator.setAppearance(false, { reducedMotion: true, fontSize: 15, fontFamily: "system-ui", locale: "en-US", surface: "#f8f8fa", text: "#131314", muted: "#5f5f61" });
  await settledMotion(pip);
  await waitFor("home controls updated", () => overlay.webContents.executeJavaScript("!document.getElementById('pet').hidden"));
  check("home has exactly two icon controls", await overlay.webContents.executeJavaScript("[...document.querySelectorAll('#actions button')].filter(b=>!b.hidden).length===2 && !document.getElementById('close').textContent.trim() && !!document.getElementById('close').querySelector('svg')"));
  check("live preview chrome stays hidden at rest", await overlay.webContents.executeJavaScript("getComputedStyle(document.getElementById('actions')).opacity==='0'"));

  // These are exact production renderer captures. Linux cannot capture native
  // sibling-view composition; the review page layers their captured pixels.
  const review = new BrowserWindow({ show: false, frame: false, autoHideMenuBar: true, width: 360, height: 240 });
  async function capturePresentation(name: string, placement: "home" | "floating" | "pet", dark: boolean, fontSize: number) {
    const b = pip.getBounds(); pointer = { x: b.x + 12, y: b.y + 12 };
    await waitFor("local control backdrop captured", () => overlay.webContents.executeJavaScript("!document.getElementById('control-backdrop').hidden"));
    await browser.executeJavaScript(`document.body.style.background=${JSON.stringify(dark ? "#253144" : "#dcecff")};document.body.style.color=${JSON.stringify(dark ? "#eef3fb" : "#131314")}`);
    const content = await browser.capturePage();
    const chrome = await overlay.webContents.capturePage();
    writeFileSync(join(output, `${name}-content.png`), content.toPNG());
    writeFileSync(join(output, `${name}-chrome.png`), chrome.toPNG());
    review.setBounds({ x: 10, y: 10, width: b.width, height: b.height });
    await review.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(browserPiPOverlayHTML("Wuu workspace"))}`);
    await review.webContents.executeJavaScript(`(() => {
      const underlay=document.createElement('img'); underlay.src=${JSON.stringify(content.toDataURL())};
      Object.assign(underlay.style,{position:'absolute',inset:'0',width:'100%',height:'100%',objectFit:'contain'});
      document.body.prepend(underlay);
      window.wuuPipMount({mounted:true});window.wuuPipState({kind:'browser',state:'active',controller:'agent'});
      window.wuuPipPlacement({placement:${JSON.stringify(placement)},petAvailable:true});
      window.wuuPipAppearance({dark:${dark},fontSize:${fontSize},locale:'en-US',surface:${JSON.stringify(dark ? "#242426" : "#f8f8fa")},text:${JSON.stringify(dark ? "#f4f4f5" : "#131314")}});
      window.wuuPipControlBackdrop({src:${JSON.stringify(content.toDataURL())},rect:{x:0,y:0,width:${b.width},height:${b.height}}});
      window.wuuPipHover({x:12,y:12});
      return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
    })()`);
    await review.webContents.executeJavaScript("Promise.all(document.getAnimations().filter(a=>Number.isFinite(a.effect.getComputedTiming().endTime)).map(a=>a.finished))");
    writeFileSync(join(output, `${name}-renderer.png`), (await review.webContents.capturePage()).toPNG());
    pointer = { x: -100, y: -100 };
    await waitFor("controls hide again", () => overlay.webContents.executeJavaScript("getComputedStyle(document.getElementById('actions')).opacity==='0'"));
  }
  await capturePresentation("home-light", "home", false, 15);
  coordinator.setPresentation(threadID, "floating");
  await settledMotion(pip);
  check("floating moves the same shell off the main parent", pip.getParentWindow() === null && pip.id === pipID);
  coordinator.setActiveThread("different-thread"); main.hide();
  check("floating remains owned by its task while main is hidden or changes conversation", pip.isVisible() && BrowserWindow.fromWebContents(browser) === pip && current.controller === "agent");
  await capturePresentation("floating-light", "floating", false, 15);
  await overlay.webContents.executeJavaScript("document.getElementById('pet').click()");
  await waitFor("preview attaches to Wuu pet", () => pip.getParentWindow() === petManager.previewHost()?.window);
  await settledMotion(pip);
  check("pet has only the return control", await overlay.webContents.executeJavaScript("document.getElementById('pet').hidden && !document.getElementById('close').textContent.trim() && !!document.getElementById('close').querySelector('svg')"));
  const petWindow = petManager.previewHost()!.window;
  const beforePetMove = pip.getBounds(), petBounds = petWindow.getBounds();
  petWindow.setBounds({ ...petBounds, x: petBounds.x - 70, y: petBounds.y - 40 });
  await waitFor("preview follows the pet geometry", () => pip.getBounds().x === beforePetMove.x - 70 && pip.getBounds().y === beforePetMove.y - 40);
  check("moving the pet preserves the task and preview identity", pip.id === pipID && current.controller === "agent");
  const beforeResize = pip.getBounds();
  for (const [phase, x, y] of [["start", 100, 100], ["move", 150, 135], ["end", 150, 135]] as const) {
    await overlay.webContents.executeJavaScript(`window.location.href='wuu-pip://resize?phase=${phase}&edge=se&x=${x}&y=${y}'`);
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  check("pet preview can grow without losing its anchor", pip.getBounds().width > beforeResize.width && pip.getParentWindow() === petWindow);
  coordinator.setAppearance(true, { reducedMotion: true, fontSize: 20, fontFamily: "system-ui", locale: "en-US", surface: "#242426", text: "#f4f4f5", muted: "#b5b5b7" });
  await capturePresentation("pet-dark-large", "pet", true, 20);
  await petWindow.webContents.executeJavaScript(`new Promise((resolve,reject)=>{const image=new Image();image.onload=resolve;image.onerror=reject;image.src=${JSON.stringify(renderableFileURL(sheetPath))};})`);
  writeFileSync(join(output, "wuu-pet-renderer.png"), (await petWindow.webContents.capturePage()).toPNG());
  petManager.refreshLocale();
  check("loading pet replacement retains placement without advertising readiness", coordinator.presentationForThread(threadID)?.placement === "pet" && petManager.previewHost()?.ready === false);
  await waitFor("locale refresh recreates pet without changing placement", () => Boolean(petManager.previewHost()?.ready) && pip.getParentWindow() === petManager.previewHost()?.window);
  check("locale refresh retains pet placement", coordinator.presentationForThread(threadID)?.placement === "pet");
  await overlay.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor("return control restores owning conversation", () => main!.isVisible() && pip.getParentWindow() === main && coordinator!.presentationForThread(threadID)?.placement === "home");
  check("returning preview does not revoke task control", current.controller === "agent" && pip.id === pipID);
  coordinator.setPresentation(threadID, "pet"); coordinator.setActiveThread("different-thread");
  petManager.sync({ ...petSnapshot, enabled: false });
  await waitFor("disabling the shape retains the desktop preview", () => petManager.previewHost()?.ready && pip.isVisible() && petManager.previewHost()!.window.getBounds().width === 1);
  const retainedDesktopHost = petManager.previewHost()!.window;
  check("disabling the pet never changes preview ownership or task control", coordinator.presentationForThread(threadID)?.placement === "pet" && pip.getParentWindow() === retainedDesktopHost && current.controller === "agent");
  petManager.sync(petSnapshot);
  await waitFor("enabling the shape reuses its preview host", () => petManager.previewHost()?.ready && petManager.previewHost()!.window.getBounds().width > 1);
  check("pet shape reappears without rebuilding the content or host", petManager.previewHost()!.window === retainedDesktopHost && pip.id === pipID && BrowserWindow.fromWebContents(browser) === pip);
  coordinator.setPresentation(threadID, "home");
  coordinator.setActiveThread(threadID);
  await waitFor("home preview restores its owner", () => pip.isVisible());
  review.destroy(); petManager.destroy();
  coordinator.setBrowserPiPHostLayout(main.webContents.id, main, { host: { x: 0, y: 0, width: 1000, height: 720 }, obstacles: [] });
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
  check("capture failure keeps its symbol and hide control available without raw diagnostics", await overlay.webContents.executeJavaScript("!document.getElementById('ph').classList.contains('gone') && document.getElementById('ph-icon').checkVisibility() && !document.getElementById('close').hidden && !document.getElementById('pet').hidden && !document.body.innerText.includes('Synthetic permission denied')"));
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
  // Restore explicitly without changing the task or recreating its surface.
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
  await waitFor("preview can reappear without focusing its parent", () => pip.isVisible());
  check("reopening a preview preserves the foreground app", pip.isVisible() && otherWindow.isFocused() && !pip.isFocused(), { mainFocused: main.isFocused(), otherFocused: otherWindow.isFocused(), pipVisible: pip.isVisible(), pipFocused: pip.isFocused() });
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
  const oldContext = contexts.findLast(entry => entry.activity.thread_id === threadID)!.context;
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
  check("stopped capture retains its frame and hide control", await controlOverlay.webContents.executeJavaScript("document.getElementById('ph').classList.contains('gone') && !document.getElementById('close').hidden && !document.getElementById('pet').hidden"));
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
  // Two genuine live surfaces keep different hosts and owning conversations.
  coordinator.update(activity("browser", "browser-a", { execution_id: "fixture-multi-a" }));
  coordinator.setPresentation(threadID, "floating");
  coordinator.update(activity("browser", "browser-b", { thread_id: "auto-off-thread", execution_id: "fixture-multi-b" }));
  coordinator.setActiveThread("auto-off-thread"); coordinator.showForThread("auto-off-thread");
  await waitFor("independent live surfaces are visible", () => finalPip.isVisible() && second.isVisible());
  check("desktop and home surfaces retain separate parents and targets", finalPip.getParentWindow() === null && second.getParentWindow() === main
    && BrowserWindow.fromWebContents(browserViews[0].webContents) === finalPip && BrowserWindow.fromWebContents(browserViews[1].webContents) === second);
  petManager.sync(petSnapshot);
  await waitFor("pet available for simultaneous previews", () => petManager.previewHost()?.ready);
  coordinator.setPresentation(threadID, "pet");
  await waitFor("pet and home surfaces remain visible", () => finalPip.getParentWindow() === petManager.previewHost()?.window && finalPip.isVisible() && second.isVisible());
  await settledMotion(finalPip); await settledMotion(second);
  const homeBefore = second.getBounds(), externalBefore = finalPip.getBounds(), multiPet = petManager.previewHost()!.window;
  multiPet.setBounds({ ...multiPet.getBounds(), x: multiPet.getBounds().x - 50 });
  await waitFor("external preview follows only its pet", () => finalPip.getBounds().x === externalBefore.x - 50);
  check("pet geometry cannot move another conversation's home preview", JSON.stringify(second.getBounds()) === JSON.stringify(homeBefore));
  const overlaps = (a: Electron.Rectangle, b: Electron.Rectangle) =>
    a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
  multiPet.setBounds({ ...multiPet.getBounds(), x: 600, y: 8 });
  await settledMotion(finalPip);
  check("top-edge fallback leaves the pet dock unobstructed", !overlaps(finalPip.getBounds(), multiPet.getBounds()));
  coordinator.setPresentation("auto-off-thread", "pet");
  await waitFor("both conversations share the desktop host", () => second.getParentWindow() === multiPet && finalPip.isVisible() && second.isVisible());
  await settledMotion(finalPip); await settledMotion(second);
  check("trailing pet cards cannot fan back over the dock", !overlaps(finalPip.getBounds(), multiPet.getBounds()) && !overlaps(second.getBounds(), multiPet.getBounds()), { first: finalPip.getBounds(), second: second.getBounds(), pet: multiPet.getBounds() });
  coordinator.setPresentation("auto-off-thread", "home");
  await settledMotion(finalPip); await settledMotion(second);
  petManager.setHints([{ thread_id: "auto-off-thread", title: "Other task", status: "running", preview: "Still working", attention: false, updated_at: Date.now() }]);
  await multiPet.webContents.executeJavaScript("window.location.href='wuu-pet://action/panel?open=1'");
  await waitFor("pet composer expands", () => multiPet.getBounds().width >= 280);
  await settledMotion(finalPip);
  check("expanded pet composer and activity cards remain unobstructed", !overlaps(finalPip.getBounds(), multiPet.getBounds()) && second.isVisible(), { pet: multiPet.getBounds(), preview: finalPip.getBounds() });
  await multiPet.webContents.executeJavaScript("window.location.href='wuu-pet://action/panel?open=0'");
  petManager.setHints([]);
  multiPet.setBounds({ ...multiPet.getBounds(), x: 1200, y: 800 });
  await settledMotion(finalPip);
  for (const [phase, x, y] of [["start", 100, 100], ["move", 800, 700], ["end", 800, 700]] as const) {
    await controlOverlay.webContents.executeJavaScript(`window.location.href='wuu-pip://resize?phase=${phase}&edge=se&x=${x}&y=${y}'`);
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  await settledMotion(finalPip);
  check("large preview is established before changing displays", finalPip.getBounds().width > 800);
  // Inject a smaller display work area while retaining real native windows,
  // renderer, coordinator and layout. Physical multi-display behavior needs Mac QA.
  const nearestDisplay = screen.getDisplayNearestPoint;
  try {
    screen.getDisplayNearestPoint = point => ({ ...nearestDisplay(point), workArea: { x: 0, y: 0, width: 800, height: 600 } });
    multiPet.setBounds({ ...multiPet.getBounds(), x: 660, y: 400 });
    coordinator.refreshPetHost();
    await settledMotion(finalPip);
    const bounded = finalPip.getBounds();
    check("moving to a smaller display fits the preview and avoids the pet", bounded.x >= 0 && bounded.y >= 0 && bounded.x + bounded.width <= 800 && bounded.y + bounded.height <= 600 && !overlaps(bounded, multiPet.getBounds()), { pet: multiPet.getBounds(), preview: bounded });
    coordinator.setPresentation("auto-off-thread", "pet");
    await settledMotion(finalPip); await settledMotion(second);
    const frames = [finalPip.getBounds(), second.getBounds()];
    check("mixed-size desktop cards stay in the smaller display safe region", frames.every(rect => rect.x >= 0 && rect.y >= 0 && rect.x + rect.width <= 800 && rect.y + rect.height <= 600 && !overlaps(rect, multiPet.getBounds())), frames);
    screen.getDisplayNearestPoint = point => ({ ...nearestDisplay(point), workArea: { x: 0, y: 0, width: 80, height: 64 } });
    coordinator.refreshPetHost();
    await settledMotion(finalPip); await settledMotion(second);
    check("aspect fitting at the native shell floor settles without recursive geometry", [finalPip, second].every(win => { const b = win.getBounds(); return b.x === 0 && b.y === 0 && b.width === 80 && b.height === 64; }));
    coordinator.setPresentation("auto-off-thread", "home");
  } finally { screen.getDisplayNearestPoint = nearestDisplay; }
  const brokenPet = { ...petSnapshot.pets[0], spritesheet_url: renderableFileURL(join(fixture!, "missing-sheet.png")) };
  petManager.sync({ ...petSnapshot, pets: [brokenPet] });
  check("loading a replacement sheet does not prematurely return home", coordinator.presentationForThread(threadID)?.placement === "pet" && petManager.previewHost()?.ready === false);
  await waitFor("broken pet asset returns preview to its owning home", () => !petManager.previewHost() && coordinator!.presentationForThread(threadID)?.placement === "home" && !finalPip.isVisible());
  check("broken pet leaves the selected conversation and task untouched", second.isVisible() && !browserViews[0].webContents.isDestroyed());
  petManager.sync(petSnapshot);
  await waitFor("repaired pet asset becomes ready", () => petManager.previewHost()?.ready);
  coordinator.setPresentation(threadID, "pet");
  await waitFor("preview returns to repaired pet", () => finalPip.isVisible() && finalPip.getParentWindow() === petManager.previewHost()?.window);
  petManager.previewHost()!.window.webContents.forcefullyCrashRenderer();
  await waitFor("crashed pet renderer releases its preview", () => !petManager.previewHost() && coordinator!.presentationForThread(threadID)?.placement === "home" && !finalPip.isVisible());
  check("pet renderer crash does not stop or expose another task", second.isVisible() && !browserViews[0].webContents.isDestroyed());
  petManager.sync(petSnapshot);
  await waitFor("explicit pet sync recovers its renderer", () => petManager.previewHost()?.ready);
  coordinator.setPresentation(threadID, "pet");
  await waitFor("preview attaches after pet recovery", () => finalPip.isVisible());
  const recoveredPet = petManager.previewHost()!.window;
  recoveredPet.hide();
  await waitFor("hidden pet is not a usable host", () => !petManager.previewHost() && coordinator!.presentationForThread(threadID)?.placement === "home" && !finalPip.isVisible());
  recoveredPet.showInactive();
  await waitFor("shown pet is available again", () => petManager.previewHost()?.ready);
  check("restoring pet does not silently move a returned preview", coordinator.presentationForThread(threadID)?.placement === "home" && second.isVisible());
  coordinator.setPresentation(threadID, "pet");
  petManager.sync({ ...petSnapshot, enabled: false });
  await waitFor("shape disable preserves both independently owned previews", () => petManager.previewHost()?.ready && finalPip.isVisible() && second.isVisible());
  const finalDesktopHost = petManager.previewHost()!.window;
  check("shape-disabled host stays tiny while carrying its own preview", finalDesktopHost.getBounds().width === 1 && finalPip.getParentWindow() === finalDesktopHost && second.getParentWindow() === main);
  coordinator.setPresentation(threadID, "hide");
  await waitFor("hiding the last desktop preview releases only its host", () => finalDesktopHost.isDestroyed() && second.isVisible());
  coordinator.setPresentation(threadID, "pet");
  coordinator.setPresentation("auto-off-thread", "pet");
  coordinator.setPresentation(threadID, "hide");
  await waitFor("one cancelled request cannot cancel another conversation's handoff", () => petManager.previewHost()?.ready && second.getParentWindow() === petManager.previewHost()?.window && second.isVisible() && !finalPip.isVisible());
  const sharedDesktopHost = petManager.previewHost()!.window;
  check("concurrent handoffs keep the cancelled task alive and the other content attached", !browserViews[0].webContents.isDestroyed() && BrowserWindow.fromWebContents(browserViews[1].webContents) === second && coordinator.presentationForThread("auto-off-thread")?.placement === "pet");
  coordinator.setPresentation(threadID, "pet");
  await waitFor("cancelled conversation can explicitly rejoin without swapping targets", () => finalPip.isVisible() && BrowserWindow.fromWebContents(browserViews[0].webContents) === finalPip && BrowserWindow.fromWebContents(browserViews[1].webContents) === second);
  check("rejoining keeps both preview shells and their desktop parent", finalPip.getParentWindow() === sharedDesktopHost && second.getParentWindow() === sharedDesktopHost);
  await coordinator.shutdown();
  await waitFor("desktop host closes after shutdown", () => sharedDesktopHost.isDestroyed());
  check("shutdown releases the unneeded desktop host", sharedDesktopHost.isDestroyed());
  check("shutdown destroys preview shell", finalPip.isDestroyed());
  host.destroyAll(); main.destroy();
  for (const source of sources.values()) source.end();
  server.close();
  writeFileSync(join(output, "results.json"), JSON.stringify({ platform: process.platform, nativeCaptureVerified: false, checks, events, windowID: pipID, overlayID, surfaceCount }, null, 2));
  console.log(`PASS: live activity shell and synthetic native transport (${checks.length} checks); evidence ${output}`);
  app.exit(0);
}).catch(async error => {
  writeFileSync(join(output, "failure.json"), JSON.stringify({ error: String(error), checks, events,
    windows: BrowserWindow.getAllWindows().map(win => ({ id: win.id, visible: win.isVisible(), parent: win.getParentWindow()?.id, bounds: win.getBounds() })),
    presentations: [coordinator?.presentationForThread(threadID), coordinator?.presentationForThread("auto-off-thread")],
    contexts: contexts.map(entry => ({ thread: entry.activity.thread_id, target: entry.activity.target, epoch: entry.activity.execution_id })),
  }, null, 2));
  console.error(error);
  await coordinator?.shutdown(); host?.destroyAll(); main?.destroy();
  for (const source of sources.values()) source.end();
  server.close(); app.exit(1);
});
