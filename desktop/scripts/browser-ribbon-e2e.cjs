// Exercise the shipping ribbon in Chromium with a long synthetic tab list.
// This renderer fixture complements the full native workspace acceptance.
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
const { app, BrowserWindow } = require("electron");
assert(process.env.WUU_RIBBON_PROFILE);
app.setPath("userData", process.env.WUU_RIBBON_PROFILE);
fs.mkdirSync(output, { recursive: true });
for (const name of ["failure.txt", "failure.png"]) fs.rmSync(path.join(output, name), { force: true });
const checks = [], screenshots = [];
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let win;
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
      import{NavigationRibbon}from"./src/renderer/NavigationRibbon";import{startFocusModality}from"./src/renderer/FocusModality";
      import"./src/renderer/styles.css";startFocusModality();
      function Fixture(){const[tabs,setTabs]=useState(Array.from({length:48},(_,i)=>({id:"tab-"+i,title:i===0?"Playing audio · a very long page title that must not overlap controls":i===1?"Pinned and muted · 固定的标签页":i===2?"Loading the next page…":"Reference "+i+" · 浏览器标签页",url:"https://example.com/"+i,audible:i===0,muted:i===1,pinned:i===1,loading:i===2})));
      const[active,setActive]=useState("tab-0");window.fixture={tabs,active};
      return <><aside className="sidebar" style={{position:"absolute",inset:0,width:64}}><NavigationRibbon items={[]} websites={tabs} activeWebsiteId={active} onSelectWebsite={tab=>setActive(tab.id)}
        onCloseTab={id=>setTabs(tabs.filter(tab=>tab.id!==id))} onMuteTab={(id,muted)=>setTabs(tabs.map(tab=>tab.id===id?{...tab,muted}:tab))}
        onPinTab={(id,pinned)=>setTabs(tabs.map(tab=>tab.id===id?{...tab,pinned}:tab))} onNewTab={()=>{}}
        footer={<button>Settings</button>}/></aside><main style={{marginLeft:280,padding:32}}><h1>Browser ribbon acceptance</h1>
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
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({ status: "passed", versions: process.versions, checks, screenshots,
    scope: "Production React/CSS rendered in Chromium. Synthetic tabs; native overlay transport and full app navigation are covered by workspace acceptance." }, null, 2));
  console.log(checks.join("\n"));
  console.log("Evidence:", output);
})().catch(async error => {
  if (win) fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG());
  console.error(error); fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack)); process.exitCode = 1;
}).finally(() => { win?.destroy(); app.exit(process.exitCode || 0); });
setTimeout(() => app.exit(1), 60000).unref();
