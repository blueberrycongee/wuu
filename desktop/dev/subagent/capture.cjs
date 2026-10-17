// Reproducible visual review of synthetic preview states; no model or app server.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const output = path.resolve(__dirname, "../../../artifacts/subagent-stream-preview");
const previewURL = process.env.WUU_SUBAGENT_PREVIEW_URL || "http://127.0.0.1:5238/dev/subagent/";
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(app.getPath("temp"), "wuu-subagent-preview-"));
app.setPath("userData", profile);
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1440, height: 1000, webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = [];
  const reports = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const run = code => win.webContents.executeJavaScript(code);
  const ready = () => run(`new Promise((resolve, reject) => { const end = performance.now()+10000; function frame() { if (document.querySelector('.preview-composer')) document.fonts.ready.then(() => requestAnimationFrame(() => requestAnimationFrame(resolve))); else if (performance.now() > end) reject(new Error('Preview did not render')); else requestAnimationFrame(frame); } frame(); })`);
  const settle = () => run("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  const capture = async name => {
    await run("Promise.all(document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).map(a => a.finished.catch(() => {})))");
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  };
  const click = async selector => {
    const point = await run(`(() => { const el=document.querySelector(${JSON.stringify(selector)}); if (!el) throw new Error('Missing click target'); el.scrollIntoView({block:'nearest'}); const r=el.getBoundingClientRect(); return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}; })()`);
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
    await settle();
  };
  const check = async (name, expression) => {
    const passed = await run(expression);
    reports.push({ interaction: name, passed });
    if (!passed) throw new Error(name);
  };
  for (const [name, width, height, query] of [
    ["01-inline", 1440, 1000, ""],
    ["02-overview", 1440, 1000, "panel=overview"],
    ["03-detail", 1440, 1000, "panel=routing"],
    ["04-result", 1440, 1000, "panel=protocol"],
    ["05-dark-many", 1440, 1000, "theme=dark&scenario=many&panel=overview"],
    ["06-failure", 1440, 1000, "scenario=failure&panel=regression"],
    ["07-completed", 1440, 1000, "scenario=done&panel=overview"],
    ["08-empty", 1000, 800, "scenario=empty"],
    ["09-narrow", 390, 844, "scenario=many"],
    ["10-large-dark", 1100, 1000, "theme=dark&font=20&scenario=many&panel=overview"],
    ["11-narrow-large", 390, 844, "font=20&scenario=many&panel=overview"],
    ["12-narrow-detail", 390, 844, "font=20&panel=protocol"],
    ["13-three", 1440, 1000, "scenario=three"],
  ]) {
    win.setContentSize(width, height);
    await win.loadURL(`${previewURL}?${query}`);
    await ready();
    await capture(name);
    const geometry = await run(`(() => { const rect = s => {const el=document.querySelector(s); if(!el) return null; const r=el.getBoundingClientRect(); return {x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height}}; return { viewport:[innerWidth,innerHeight],pageWidth:document.documentElement.scrollWidth,panel:rect('.agent-sidebar'),composer:rect('.preview-composer'), stream:rect('.preview-stream'),overview:rect('.agent-overview'), detail:rect('.agent-detail-scroll'), rows:document.querySelectorAll('.overview-agent').length }; })()`);
    reports.push({ name, ...geometry });
    if (geometry.pageWidth > width) throw new Error(`Page overflow in ${name}`);
  }
  win.setContentSize(1440, 1000);
  await win.loadURL(previewURL); await ready();
  await click("textarea");
  await win.webContents.insertText("请保留当前输入");
  await click(".inline-agent-name");
  await check("inline name opens selected child", "document.querySelector('.agent-detail-name')?.textContent === 'Atlas'");
  await check("panel receives keyboard focus", "document.activeElement === document.querySelector('.agent-sidebar-header h2')");
  await capture("14-click-detail");
  await click('[aria-label="返回全部子代理"]');
  await check("back opens overview", "document.querySelectorAll('.overview-agent').length === 5");
  await click('[aria-label="查看 Iris：核对消息协议"]');
  await check("completed child result", "document.querySelector('.detail-delivery')?.textContent.includes('结果已返回主会话')");
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Escape" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Escape" });
  await settle();
  await check("Escape closes and restores inline focus", "!document.querySelector('.agent-sidebar') && document.activeElement === document.querySelector('.inline-agent-name')");
  await check("draft survives panel navigation", "document.querySelector('textarea').value === '请保留当前输入'");
  await click(".inline-activity .inline-agent-name:last-of-type");
  await check("overflow name opens overview", "document.querySelectorAll('.overview-agent').length === 5");
  await click('[aria-label="关闭子代理侧栏"]');
  await check("close restores overflow focus", "!document.querySelector('.agent-sidebar') && document.activeElement.textContent === '另外 3 个子代理'");
  await win.loadURL(`${previewURL}?scenario=many&panel=overview&font=20`); await ready();
  await click(".show-more");
  await check("show all twelve tasks", "document.querySelectorAll('.overview-agent').length === 12");
  await run("document.querySelector('.agent-overview').scrollTop = 500"); await settle();
  await check("panel scroll is independent", "document.querySelector('.agent-overview').scrollTop > 0 && document.querySelector('.preview-stream').scrollTop === 0");
  await capture("15-scrolled-large");
  fs.writeFileSync(path.join(output, "review.json"), JSON.stringify({ errors, reports }, null, 2));
  console.log(JSON.stringify({ output, errors, screenshots: reports.filter(r => r.name).length, interactions: reports.filter(r => r.interaction) }, null, 2));
  if (errors.length) throw new Error("Renderer console errors");
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
app.on("will-quit", () => fs.rmSync(profile, { recursive: true, force: true }));
