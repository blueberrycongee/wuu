// Exercise the shipping shell and isolated native ribbon over a live page.
// Synthetic tab models complement the full native workspace acceptance.
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const assert = require("node:assert/strict");
const root = path.resolve(__dirname, "..");
const output = process.env.WUU_RIBBON_E2E_OUTPUT || path.join(root, "out/e2e/browser-ribbon");
if (!process.versions.electron) {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-ribbon-e2e-"));
  try {
    const result = require("node:child_process").spawnSync(process.env.WUU_E2E_ELECTRON || String(require("electron")), [
      ...(process.platform === "linux" ? ["--no-sandbox", ...(!process.env.DISPLAY ? ["--ozone-platform=headless"] : [])] : []), __filename,
    ], { stdio: "inherit", env: { ...process.env, WUU_RIBBON_PROFILE: profile } });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally { fs.rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }); }
  return;
}
const { app, BrowserWindow, WebContentsView, ipcMain, protocol } = require("electron");
assert(process.env.WUU_RIBBON_PROFILE);
app.setPath("userData", process.env.WUU_RIBBON_PROFILE);
fs.mkdirSync(output, { recursive: true });
for (const name of ["failure.txt", "failure.png", "failure-events.json"]) fs.rmSync(path.join(output, name), { force: true });
const checks = [], screenshots = [], limitations = [];
const nativeEvents = [];
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let win, nativeHost;
require("esbuild").buildSync({ entryPoints: [path.join(root, "src/main/chromeOverlayHost.ts")], outfile: path.join(output, "native-host.cjs"),
  bundle: true, platform: "node", format: "cjs", external: ["electron"] });
const { ChromeOverlayHost, chromeOverlayScheme } = require(path.join(output, "native-host.cjs"));
protocol.registerSchemesAsPrivileged([chromeOverlayScheme]);
async function until(fn, label) {
  for (let i = 0; i < 150; i++) { if (await fn()) return; await delay(20); }
  throw new Error("Timed out: " + label);
}
const run = code => win.webContents.executeJavaScript(code, true);
function key(keyCode) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode });
}
(async () => {
  await app.whenReady();
  await require("esbuild").build({
    stdin: { resolveDir: root, loader: "tsx", contents: `
      import React,{useState} from "react";import{createRoot}from"react-dom/client";
      import{NavigationRibbon}from"./src/renderer/NavigationRibbon";import{Settings}from"./src/renderer/WuuIcons";import{startFocusModality}from"./src/renderer/FocusModality";
      import"./src/renderer/styles.css";startFocusModality();
      function Fixture(){const[tabs,setTabs]=useState(Array.from({length:48},(_,i)=>({id:"tab-"+i,title:i===0?"Playing audio · a very long page title that must not overlap controls":i===1?"Pinned and muted · 固定的标签页":i===2?"Loading the next page…":"Reference "+i+" · 浏览器标签页",url:"https://example.com/"+i,audible:i===0,muted:i===1,pinned:i===1,loading:i===2})));
      const[active,setActive]=useState("tab-0");window.fixture={tabs,active};
      return <><aside className="sidebar" style={{position:"absolute",inset:0,width:64}}><NavigationRibbon items={[]} websites={tabs} activeWebsiteId={active} onSelectWebsite={tab=>setActive(tab.id)}
        onCloseTab={id=>setTabs(tabs.filter(tab=>tab.id!==id))} onMuteTab={(id,muted)=>setTabs(tabs.map(tab=>tab.id===id?{...tab,muted}:tab))}
        onPinTab={(id,pinned)=>setTabs(tabs.map(tab=>tab.id===id?{...tab,pinned}:tab))} onNewTab={()=>{}}
        footer={<button className="sidebar-ribbon-button" aria-label="Settings"><Settings/><span className="sidebar-ribbon-label">Settings</span></button>}/></aside>
        <div style={{position:"absolute",top:0,left:64,right:0,height:"var(--web-workspace-toolbar-height)",background:"var(--workbench-canvas-bg)",display:"flex",alignItems:"center",gap:20,paddingLeft:40}}>
          <button aria-label="Browser back">Back</button><span>Search or enter address</span>
        </div><main style={{marginLeft:280,padding:80}}><h1>Browser ribbon acceptance</h1>
        <p>The production ribbon with 48 fixture tabs.</p><input aria-label="Page editor" placeholder="Page input keeps focus when a background tab closes"/></main></>}
      createRoot(document.getElementById("root")).render(<Fixture/>);` },
    outfile: path.join(output, "ribbon.js"), bundle: true, platform: "browser", format: "iife", define: { "import.meta.env.DEV": "false", "import.meta.env.MODE": '"production"' }, loader: { ".woff2": "file", ".png": "file", ".svg": "file" },
    plugins: [{ name: "raw-svg", setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: "raw-svg" }));
      build.onLoad({ filter: /.*/, namespace: "raw-svg" }, args => ({ contents: fs.readFileSync(args.path, "utf8"), loader: "text" }));
    } }],
  });
  fs.writeFileSync(path.join(output, "index.html"), `<!doctype html><html data-theme="light"><meta charset="utf-8">
    <link rel="stylesheet" href="ribbon.css"><style>:root{--sidebar-ribbon-width:64px;--workbench-top:0px;--conversation-message-font-size:15px}body{background:var(--workbench-canvas-bg);margin:0}main{font-size:var(--font-ui)}input{width:100%}</style>
    <div id="root"></div><script>window.wuu={};</script><script src="ribbon.js"></script></html>`);
  win = new BrowserWindow({ width: 1000, height: 760, webPreferences: { sandbox: true } });
  await win.loadFile(path.join(output, "index.html"));
  await until(() => run('document.querySelectorAll("[data-ribbon-id]").length===48'), "rendered tabs");
  await run('document.querySelector("[data-ribbon-id]").focus()');
  key("END");
  await until(() => run('document.activeElement.dataset.ribbonId==="tab-47"'), "End navigation");
  assert(await run('document.querySelector(".sidebar-ribbon-main").scrollTop>0'));
  checks.push("End focuses and scrolls to the last of 48 tabs");
  key("HOME");
  await until(() => run('document.activeElement.dataset.ribbonId==="tab-0"'), "Home navigation");
  key("DOWN");
  await until(() => run('document.activeElement.dataset.ribbonId==="tab-1"'), "Arrow navigation");
  assert.equal(await run('window.fixture.active'), "tab-0");
  checks.push("Arrow navigation moves focus without changing the active page");
  assert.equal(await run('document.querySelector("[data-ribbon-close-id=tab-1]")===null'), true);
  checks.push("Pinned tabs have no accidental hover-close target");
  await run('document.querySelector("[data-ribbon-audio-id=tab-0]").click()');
  await until(() => run('window.fixture.tabs[0].muted'), "mute click");
  assert.equal(await run('window.fixture.active'), "tab-0");
  checks.push("The audio control toggles mute without selecting another tab");
  key("HOME");
  for (const theme of ["light", "dark"]) for (const size of [15, 20]) {
    await run(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty("--conversation-message-font-size","${size}px");
      document.querySelector("[data-ribbon-id]").focus();`);
    key("HOME");
    await until(() => run('document.querySelector("nav").dataset.expanded==="true"'), "expanded keyboard rail");
    await run('document.querySelector(".sidebar-ribbon-footer button").focus()');
    await delay(250);
    const layout = await run(`(()=>{const row=document.querySelector(".sidebar-ribbon-tab-row");const label=row.querySelector(".sidebar-ribbon-label").getBoundingClientRect();
      return [...row.querySelectorAll(".sidebar-ribbon-audio,.sidebar-ribbon-close")].every(button=>label.right<=button.getBoundingClientRect().left)})()`);
    assert.equal(layout, true, `${theme}/${size}: title must not overlap audio or close`);
    const file = `${theme}-${size}px.png`;
    fs.writeFileSync(path.join(output, file), (await win.webContents.capturePage()).toPNG()); screenshots.push(file);
  }
  checks.push("Long titles do not overlap mute and close controls in light/dark at 15/20px");
  await run('document.querySelector("input").focus();document.querySelector("[data-ribbon-id=tab-2]").dispatchEvent(new MouseEvent("auxclick",{button:1,bubbles:true}))');
  win.webContents.sendInputEvent({ type: "mouseMove", x: 500, y: 400 });
  await until(() => run('!document.querySelector("[data-ribbon-id=tab-2]")'), "background close");
  assert.equal(await run('document.activeElement.tagName'), "INPUT");
  checks.push("Closing a background tab preserves the page editor focus");
  await until(() => run('document.querySelector("nav").dataset.expanded!=="true"'), "collapsed ribbon");
  await delay(250);
  assert.equal(await run(`(()=>{const row=document.querySelector("[data-ribbon-id=tab-1]").closest(".sidebar-ribbon-tab-row");
    return row.querySelector(".sidebar-ribbon-audio-badge").getBoundingClientRect().bottom<=row.querySelector(".sidebar-ribbon-pin").getBoundingClientRect().top})()`), true);
  checks.push("Collapsed audio and pinned indicators occupy separate corners");
  fs.writeFileSync(path.join(output, "dark-20px-collapsed.png"), (await win.webContents.capturePage()).toPNG());
  screenshots.push("dark-20px-collapsed.png");

  // Run the actual isolated native ribbon and its trusted IPC host over a live
  // animated WebContentsView. Record every native visibility/geometry/tree
  // mutation, not just the final frame.
  const esbuild = require("esbuild");
  await esbuild.build({ entryPoints: [path.join(root, "src/renderer/chromeOverlayMain.tsx")], outfile: path.join(output, "assets/native-ribbon.js"),
    bundle: true, platform: "browser", format: "iife", jsx: "automatic",
    define: { "import.meta.env.DEV": "false", "import.meta.env.MODE": '"production"' },
    loader: { ".woff2": "file", ".png": "file", ".svg": "file" },
    plugins: [{ name: "native-raw-svg", setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: "raw-svg" }));
      build.onLoad({ filter: /.*/, namespace: "raw-svg" }, args => ({ contents: fs.readFileSync(args.path, "utf8"), loader: "text" }));
    } }] });
  esbuild.buildSync({ entryPoints: [path.join(root, "src/main/chromeOverlayPreload.ts")], outfile: path.join(output, "native-preload.cjs"),
    bundle: true, platform: "node", format: "cjs", external: ["electron"] });
  fs.writeFileSync(path.join(output, "chrome-overlay.html"), '<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="wuu-chrome://ui/assets/native-ribbon.css"><div id="root"></div><script src="wuu-chrome://ui/assets/native-ribbon.js"></script>');
  const page = new WebContentsView({ webPreferences: { sandbox: true } });
  const pageTop = await run('Math.ceil(parseFloat(getComputedStyle(document.querySelector("nav")).paddingTop))');
  const pageBounds = { x: 64, y: pageTop, width: 936, height: 720 - pageTop };
  page.setBounds(pageBounds);
  win.contentView.addChildView(page);
  await page.webContents.loadURL('data:text/html,' + encodeURIComponent('<body style="background:#badced"><h1>Live browser page</h1><input placeholder="Page focus target"><script>window.framesSeen=[];function tick(t){framesSeen.push({t,visibility:document.visibilityState});if(framesSeen.length>400)framesSeen.shift();requestAnimationFrame(tick)}requestAnimationFrame(tick)</script>'));
  page.webContents.focus();
  const appearance = await run(`(()=>{const c=getComputedStyle(document.querySelector("nav"));return{theme:"dark",locale:"en-US",fontFamily:c.fontFamily,variables:Object.fromEntries([...c].filter(k=>k.startsWith("--")).map(k=>[k,c.getPropertyValue(k)]))}})()`);
  let request = { version: 1, token: "native-ribbon-fixture", generation: 1, bounds: { x: 0, y: 0, width: 248, height: 720 }, focus: true, appearance,
    model: { kind: "ribbon", collapsedWidth: 64, label: "Browser tabs", newTabLabel: "New tab", canCreateTab: true,
      closeTabLabel: "Close tab", pinTabLabel: "Pin tab", unpinTabLabel: "Unpin tab", muteTabLabel: "Mute tab", unmuteTabLabel: "Unmute tab", reorderHint: "Move tabs",
      paddingTop: 0, paddingBottom: 8, scrollTop: 600, focusTarget: { kind: "settings" }, footer: { settings: { id: "settings", title: "Settings", icon: { kind: "settings" } } },
      items: Array.from({ length: 48 }, (_, i) => ({ id: "native-" + i, title: "Native page " + i, active: i === 0, website: true, closable: true, canMute: true, audible: i === 0,
        icon: { kind: "website", url: "https://example.com/" + i, title: "Native page " + i } })) } };
  nativeHost = new ChromeOverlayHost((_owner, event) => {
    nativeEvents.push({ type: "command", command: event.command, at: performance.now() });
    if (event.command.type === "reveal") void run(`window.emitRibbon?.(${JSON.stringify(event)})`);
    const command = event.command;
    if (command.type === "mute-tab" || command.type === "pin-tab") {
      if (command.type === "mute-tab") page.webContents.setAudioMuted(command.muted);
      request = { ...request, generation: request.generation + 1, model: { ...request.model, items: request.model.items.map(item => item.id !== command.id ? item
        : { ...item, ...(command.type === "mute-tab" ? { muted: command.muted } : { pinned: command.pinned }) }) } };
      nativeHost.show(win, request);
    }
  }, output, path.join(output, "native-preload.cjs"));
  const readySurfaces = new Set();
  ipcMain.handle("wuu:chrome-overlay-ready", event => { readySurfaces.add(event.sender.id); return nativeHost.ready(event); });
  ipcMain.on("wuu:chrome-overlay-painted", (event, token, generation) => nativeHost.painted(event, token, generation));
  ipcMain.on("wuu:chrome-overlay-command", (event, message) => nativeHost.command(event, message));
  app.on("web-contents-created", (_event, contents) => {
    contents.on("console-message", (_event, details) => { console.error("Native renderer:", details); });
    contents.on("preload-error", (_event, _path, error) => console.error("Native preload:", error));
  });
  nativeHost.prepare(win);
  const mutations = [];
  for (const [target, method] of [[page, "setVisible"], [page, "setBounds"], [win.contentView, "addChildView"], [win.contentView, "removeChildView"]]) {
    const original = target[method].bind(target);
    target[method] = (...args) => { mutations.push({ method, page: target === page || args[0] === page, args: method === "setVisible" || method === "setBounds" ? args : [], at: performance.now() }); return original(...args); };
  }
  await until(() => win.contentView.children.filter(view => view.webContents?.getURL().startsWith("wuu-chrome:")).length === 2
    && win.contentView.children.filter(view => view.webContents?.getURL().startsWith("wuu-chrome:")).every(view => !view.webContents.isLoading()), "native surfaces preloaded");
  await until(() => readySurfaces.size === 2, "native renderers ready");
  // Hold the shell's opening request before forwarding it to the real host.
  // A cold native renderer must not expose a second expanded rail underneath
  // the webpage or intercept the toolbar while its first paint is pending.
  await run(`window.wuu.chromeOverlay={
    show:async request=>{window.pendingRibbon=request},
    hide:async()=>{},
    onEvent:callback=>{window.emitRibbon=callback;return()=>{}}
  };document.querySelector("[data-ribbon-audio-id=tab-1]").click();
  document.querySelector("[data-ribbon-id=tab-0]").focus()`);
  key("HOME");
  await until(() => run('Boolean(window.pendingRibbon)'), "shell native opening request");
  const pending = await run("window.pendingRibbon");
  const shellOpening = await run(`(()=>{
    const nav=document.querySelector("nav"),row=document.querySelector("[data-ribbon-id=tab-0]").closest(".sidebar-ribbon-tab-row");
    const collapsed=parseFloat(getComputedStyle(nav).getPropertyValue("--sidebar-ribbon-width"));
    return {width:nav.getBoundingClientRect().width,
      toolbarObscured:!!document.elementFromPoint(collapsed+60,4)?.closest("nav"),
      labelsVisible:[...nav.querySelectorAll(".sidebar-ribbon-label")].some(label=>getComputedStyle(label).visibility!=="hidden"),
      badgeVisible:getComputedStyle(row.querySelector(".sidebar-ribbon-audio-badge")).visibility==="visible"}
  })()`);
  assert.equal(shellOpening.toolbarObscured, false, "Pending native ribbon must not intercept toolbar outside the collapsed rail");
  assert.equal(shellOpening.labelsVisible, false, "Pending native ribbon must not expose duplicated expanded labels beneath the webpage");
  assert.equal(shellOpening.badgeVisible, true, "Collapsed audio status remains visible while native paint is pending");
  assert.equal(shellOpening.width, pending.bounds.width, "Suppressing shell paint must preserve the measured native width");
  assert.ok(pending.bounds.width > pending.model.collapsedWidth);
  nativeEvents.push({ type: "pending-shell", ...shellOpening, nativeBounds: pending.bounds });
  nativeHost.show(win, pending);
  const openingView = win.contentView.children.find(view => view.webContents?.getURL().includes("surface=ribbon"));
  await until(() => openingView.getVisible() && openingView.getBounds().width === Math.ceil(pending.bounds.width), "complete native opening");
  openingView.webContents.sendInputEvent({ type: "keyDown", keyCode: "Home" });
  openingView.webContents.sendInputEvent({ type: "keyUp", keyCode: "Home" });
  // setBounds updates the native view before Chromium receives its resize.
  // Wait for that renderer acknowledgement before calculating a click point.
  await until(async () => {
    const viewport = await openingView.webContents.executeJavaScript('new Promise(resolve=>requestAnimationFrame(()=>resolve({width:innerWidth,height:innerHeight})))');
    const bounds = openingView.getBounds(), zoom = openingView.webContents.getZoomFactor();
    nativeEvents.push({ type: "opening-viewport", bounds, zoom, viewport });
    return Math.abs(viewport.width * zoom - bounds.width) < 1 && Math.abs(viewport.height * zoom - bounds.height) < 1
      && bounds.width === Math.ceil(pending.bounds.width);
  }, "native renderer viewport matches expanded view");
  await until(() => run(`document.querySelector("nav").style.getPropertyValue("--ribbon-native-reveal")==="1"`), "native header reveal completes");
  await until(() => run(`Boolean(document.elementFromPoint(124, 4)?.closest("nav"))`), "expanded ribbon header covers the browser toolbar");
  const header = await run(`(()=>{const nav=document.querySelector("nav"),rect=nav.getBoundingClientRect();
    return {covered:nav.contains(document.elementFromPoint(124,4)),outside:nav.contains(document.elementFromPoint(rect.right+8,4))}})()`);
  assert.equal(header.covered, true, "Expanded browser ribbon covers its toolbar header");
  assert.equal(header.outside, false, "The rest of the browser toolbar stays reachable");
  fs.writeFileSync(path.join(output, "browser-ribbon-header-shell.png"), (await win.webContents.capturePage()).toPNG());
  screenshots.push("browser-ribbon-header-shell.png");
  checks.push("Native reveal extends the shell header over the toolbar without intercepting controls outside the ribbon");
  const hit = await openingView.webContents.executeJavaScript(`(()=>{
    const row=document.querySelector("[data-ribbon-id=tab-0]");row.scrollIntoView({block:"nearest"});
    const r=row.getBoundingClientRect(),x=Math.min(r.right-10,r.left+100),y=r.y+r.height/2;
    const target=document.elementFromPoint(x,y);
    return {x,y,clickable:row.contains(target),pinnedClose:!!document.querySelector("[data-ribbon-close-id=tab-1]"),
      viewport:{width:innerWidth,height:innerHeight},row:{x:r.x,y:r.y,width:r.width,height:r.height},hit:target?.outerHTML.slice(0,500)}
  })()`);
  nativeEvents.push({ type: "opening-hit", bounds: openingView.getBounds(), ...hit });
  assert.equal(hit.clickable, true, "Native expanded label remains clickable over the webpage");
  assert.equal(hit.pinnedClose, false);
  openingView.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, x: hit.x, y: hit.y });
  openingView.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, x: hit.x, y: hit.y });
  await until(() => nativeEvents.some(event => event.command?.type === "select" && event.command.id === "tab-0"), "native expanded row click");
  assert.equal(page.getVisible(), true);
  assert.deepEqual(page.getBounds(), pageBounds);
  nativeHost.hide(win, pending.token, pending.generation);
  await run(`window.emitRibbon({token:${JSON.stringify(pending.token)},generation:${pending.generation},command:{type:"dismiss",reason:"escape"}});
    delete window.wuu.chromeOverlay;document.querySelector("[data-ribbon-audio-id=tab-0]").click();document.querySelector("input").focus()`);
  checks.push("Pending native paint leaves toolbar hit targets and collapsed audio status intact; measured full-width native rail opens and its expanded row receives real clicks");
  win.focus();
  page.webContents.focus();
  await delay(0);
  nativeHost.show(win, request);
  let ribbon;
  await until(() => { ribbon = win.contentView.children.find(view => view.webContents?.getURL().includes("surface=ribbon")); return ribbon?.getVisible(); }, "native ribbon painted");
  const nativeRun = code => ribbon.webContents.executeJavaScript(code, true);
  await until(() => nativeRun('document.querySelectorAll("[data-ribbon-id]").length===48&&!!document.querySelector("[data-ribbon-id=native-0]")'), "native tab rows");
  await nativeRun('window.wuuChromeOverlay.ready().then(request=>window.wuuChromeOverlay.command({token:request.token,generation:request.generation,command:{type:"retain",active:true}}))');
  assert.equal(await nativeRun('document.querySelector(".sidebar-ribbon-main").scrollTop'), 600, "Opening the native ribbon preserves the user's saved scroll");
  checks.push("Native ribbon opening preserves manually scrolled long lists");
  ribbon.webContents.focus();
  await nativeRun('document.querySelector("[data-ribbon-id=native-0]").focus()');
  for (const pinned of [true, false, true]) {
    await nativeRun(`(()=>{const state=window.wuuChromeOverlay.ready();state.then(request=>window.wuuChromeOverlay.command({token:request.token,generation:request.generation,command:{type:"pin-tab",id:"native-0",pinned:${pinned}}}))})()`);
    await until(() => request.model.items[0].pinned === pinned, "native pin update");
    const muted = !request.model.items[0].muted;
    await nativeRun('document.querySelector("[data-ribbon-audio-id=native-0]").click()');
    await until(() => request.model.items[0].muted === muted, "native mute update");
    await until(() => nativeRun('document.querySelector("[data-ribbon-audio-id=native-0]").getAttribute("aria-pressed")===' + JSON.stringify(String(Boolean(request.model.items[0].muted)))), "native mute paint");
  }
  await delay(250);
  const frames = await page.webContents.executeJavaScript("framesSeen");
  assert.equal(page.getVisible(), true);
  assert.equal(mutations.some(event => event.page && (event.method === "setVisible" && event.args[0] === false || event.method === "addChildView" || event.method === "removeChildView")), false);
  assert(frames.length >= 10 && frames.every(frame => frame.visibility === "visible"));
  assert.equal(await nativeRun("document.activeElement.dataset.ribbonId"), "native-0");
  checks.push("Native pin/mute model updates keep the page visible, attached and animating without losing ribbon focus");
  nativeEvents.push({ type: "native-mutations", mutations, frames });
  // Electron's window capture omits WebContentsView children. On macOS capture
  // the compositor's window; elsewhere keep explicitly labelled view captures.
  await ribbon.webContents.capturePage();
  await page.webContents.capturePage();
  const capture = process.platform === "darwin" ? require("node:child_process").spawnSync("/usr/sbin/screencapture",
    ["-x", "-o", "-l", win.getMediaSourceId().split(":")[1], path.join(output, "native-ribbon-over-page.png")], { encoding: "utf8" }) : undefined;
  if (capture?.status === 0) screenshots.push("native-ribbon-over-page.png");
  else {
    limitations.push("Composited native window screenshot unavailable; separate view captures do not verify overlap: " + (capture?.stderr?.trim() || "non-macOS runner"));
    for (const [name, contents] of [["native-ribbon-view.png", ribbon.webContents], ["native-page-view.png", page.webContents]]) {
      fs.writeFileSync(path.join(output, name), (await contents.capturePage()).toPNG()); screenshots.push(name);
    }
  }
  request = { ...request, generation: request.generation + 1, model: { ...request.model, focusTarget: { kind: "item", id: "native-20" },
    items: request.model.items.map(item => ({ ...item, active: item.id === "native-20" })) } };
  nativeHost.show(win, request);
  await until(() => nativeRun('document.activeElement.dataset.ribbonId==="native-20"'), "selected row focus");
  assert(await nativeRun('document.querySelector(".sidebar-ribbon-main").scrollTop>0'));
  ribbon.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab", modifiers: ["shift"] });
  ribbon.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab", modifiers: ["shift"] });
  await until(() => nativeEvents.some(event => event.command?.type === "dismiss" && event.command.reason === "tab"), "Shift+Tab leaves the first tabbable row");
  assert.equal(page.webContents.isFocused(), true);
  checks.push("Changing the selected tab reveals it; Shift+Tab from the roving tab stop dismisses the ribbon and restores page focus");

  // A persistent editor must not disappear between owner resize notifications,
  // even if a microtask makes it visible again before the final screenshot.
  const capsuleRequest = { version: 1, token: "native-capsule-resize", generation: 1,
    bounds: { x: 280, y: 400, width: 416, height: 96 }, focus: true, appearance,
    model: { kind: "capsule", expanded: true, menuOpen: false, draft: "", busy: false,
      sourceTitle: "Live browser page", label: "Ask Wuu", placeholder: "Ask about this page",
      sendLabel: "Send", openLabel: "Open chat", collapseLabel: "Collapse and keep draft" } };
  nativeHost.show(win, capsuleRequest);
  let capsule;
  await until(() => {
    capsule = win.contentView.children.find(view => view.webContents?.getURL().includes("surface=capsule"));
    return capsule?.getVisible() && capsule.webContents.isFocused();
  }, "native capsule editor visible and focused");
  const capsuleRun = code => capsule.webContents.executeJavaScript(code, true);
  const editorPoint = await capsuleRun(`(()=>{const r=document.querySelector("[data-context-capsule] input").getBoundingClientRect();
    return{x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}})()`);
  capsule.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...editorPoint });
  capsule.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...editorPoint });
  await until(() => capsuleRun('document.activeElement?.matches("[data-context-capsule] input")'), "capsule input owns focus");
  const draft = "Keep this browser draft through window resizing";
  await capsule.webContents.insertText(draft);
  await until(() => capsuleRun(`document.querySelector("[data-context-capsule] input").value===${JSON.stringify(draft)}`), "native capsule accepts draft");
  await capsuleRun('document.querySelector("[data-context-capsule] input").setSelectionRange(5,22)');
  const resizeEvidence = { type: "capsule-resize", transitions: [], samples: [] };
  nativeEvents.push(resizeEvidence);
  const capsuleSetVisible = capsule.setVisible.bind(capsule);
  capsule.setVisible = visible => {
    const before = capsule.getVisible();
    const result = capsuleSetVisible(visible);
    const after = capsule.getVisible();
    if (before !== after) resizeEvidence.transitions.push({ visible: after, at: performance.now() });
    return result;
  };
  for (const [width, height] of [[940, 720], [1080, 800], [820, 660], [1140, 820], [900, 700], [1000, 760]]) {
    const resized = new Promise(resolve => win.once("resize", resolve));
    win.setSize(width, height);
    await resized;
    const editor = await capsuleRun(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>{
      const input=document.querySelector("[data-context-capsule] input");
      resolve({value:input.value,start:input.selectionStart,end:input.selectionEnd,active:document.activeElement===input});
    })))`);
    resizeEvidence.samples.push({ window: win.getBounds(), visible: capsule.getVisible(),
      focused: capsule.webContents.isFocused(), editor });
  }
  fs.writeFileSync(path.join(output, "capsule-resize.json"), JSON.stringify(resizeEvidence, null, 2));
  assert.equal(resizeEvidence.transitions.some(event => !event.visible), false, "Resizing the owner must never hide the persistent capsule");
  for (const sample of resizeEvidence.samples) {
    assert.equal(sample.visible, true);
    assert.equal(sample.focused, true, "Resizing preserves the native capsule keyboard owner");
    assert.deepEqual(sample.editor, { value: draft, start: 5, end: 22, active: true }, "Resizing preserves the capsule draft and selection");
  }
  fs.writeFileSync(path.join(output, "native-capsule-after-resize.png"), (await capsule.webContents.capturePage()).toPNG());
  screenshots.push("native-capsule-after-resize.png");
  checks.push("Repeated owner resize keeps the native capsule continuously visible and preserves editor draft, selection and focus");
  const paintEvidence = { type: "capsule-model-paint", samples: [] };
  nativeEvents.push(paintEvidence);
  let capsuleGeneration = capsuleRequest.generation;
  for (const focusOwner of ["capsule", "page"]) {
    if (focusOwner === "page") {
      await page.webContents.executeJavaScript('document.querySelector("input").focus()');
      page.webContents.focus();
      await until(() => page.webContents.isFocused(), "page owns native keyboard focus before chrome updates");
    }
    for (let step = 0; step < 4; step++) {
      const update = { ...capsuleRequest, generation: ++capsuleGeneration, focus: false,
        bounds: { ...capsuleRequest.bounds, x: 280 + step * 5, y: 400 + step * 3 },
        model: { ...capsuleRequest.model, expanded: focusOwner === "capsule", draft, sourceTitle: `Updated page ${step}` } };
      const painted = new Promise(resolve => {
        const observe = (_event, token, generation) => {
          if (token !== update.token || generation !== update.generation) return;
          ipcMain.removeListener("wuu:chrome-overlay-painted", observe);
          resolve();
        };
        ipcMain.on("wuu:chrome-overlay-painted", observe);
      });
      nativeHost.show(win, update);
      await painted;
      const editor = await capsuleRun(`(()=>{const input=document.querySelector("[data-context-capsule] input");
        return input?{value:input.value,start:input.selectionStart,end:input.selectionEnd,active:document.activeElement===input}:null})()`);
      paintEvidence.samples.push({ focusOwner, generation: update.generation, visible: capsule.getVisible(),
        capsuleFocused: capsule.webContents.isFocused(), pageFocused: page.webContents.isFocused(), editor });
    }
  }
  fs.writeFileSync(path.join(output, "capsule-model-paint.json"), JSON.stringify(paintEvidence, null, 2));
  for (const sample of paintEvidence.samples) {
    assert.equal(sample.visible, true);
    assert.equal(sample.focusOwner === "capsule" ? sample.capsuleFocused : sample.pageFocused, true, "Painting visible chrome preserves the current native keyboard owner");
    if (sample.focusOwner === "capsule") assert.deepEqual(sample.editor, { value: draft, start: 5, end: 22, active: true }, "Chrome model updates preserve the editor draft and selection");
  }
  checks.push("Visible capsule model and bounds updates preserve page or editor focus without disturbing the draft and selection");
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({ status: "passed", versions: process.versions, checks, screenshots, limitations,
    nativeEvents, scope: "Production React/CSS and native ChromeOverlayHost over a live webpage. Synthetic tab models; complete app navigation is covered by workspace acceptance." }, null, 2));
  console.log(checks.join("\n"));
  console.log("Evidence:", output);
})().catch(async error => {
  if (win) fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "failure-events.json"), JSON.stringify(nativeEvents, null, 2));
  console.error(error); fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack)); process.exitCode = 1;
}).finally(() => { if (win) nativeHost?.close(win); win?.destroy(); app.exit(process.exitCode || 0); });
setTimeout(() => app.exit(1), 60000).unref();
