const fs = require("node:fs");
const path = require("node:path");
const browserPreview = process.argv.includes("--browser-preview");
const { app, BrowserWindow, ipcMain } = browserPreview ? {} : require("electron");

// npx electron-vite build && npx electron scripts/response-selection-e2e.cjs
const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_SELECTION_E2E_OUTPUT || path.join(desktop, "out/response-selection-e2e");
fs.mkdirSync(output, { recursive: true });
if (app) app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_SELECTION_E2E_CWD = path.dirname(desktop);
const report = { scene: "response-selection-v2", boundary: "Real Electron renderer; synthetic preload transport. No Go/provider execution.", cases: [], calls: [], screenshots: [], measurements: [], errors: [] };
if (ipcMain) ipcMain.on("selection:bridge-call", (_event, call) => report.calls.push(call));
let win;
const surface = '[data-thread-id="selection-main"] article[data-response-item-id="selection-main-answer"][data-response-settled="true"] .agent-text';
const card = '[data-main-conversation-composer] .composer-selection-chip';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function evaluate(fn, ...args) {
  const result = await win.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})}}catch(e){return {error:String(e.stack||e)}}})()`, true);
  if (result.error) throw new Error(result.error);
  return result.value;
}
async function until(fn, label, ...args) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const result = await evaluate(fn, ...args);
    if (result) return result;
    await sleep(25);
  }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector) {
  await until(selector => !!document.querySelector(selector), selector, selector);
  await evaluate(selector => document.querySelector(selector).click(), selector);
}
async function input(selector, value) {
  await evaluate((selector, value) => {
    const node = document.querySelector(selector);
    window.dispatchEvent(new CustomEvent("selection-fixture-step", { detail: { step: "before-input-focus", selector, active: document.activeElement?.className } }));
    node.focus();
    window.dispatchEvent(new CustomEvent("selection-fixture-step", { detail: { step: "after-input-focus", selector, active: document.activeElement?.className } }));
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(node, value);
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }, selector, value);
  // The embedded-browser adapter invokes functions in one JS task, unlike
  // separate Electron IPC evaluations. Let React commit the controlled draft
  // before a subsequent submit click reads it.
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
// Entrances and tray lifts run on the compositor; capture the settled UI, not
// a frame from the middle of a fade.
async function settle() {
  await evaluate(() => Promise.race([
    Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => undefined))),
    new Promise(resolve => setTimeout(resolve, 1500)),
  ]).then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))));
}
async function screenshot(name) {
  await settle();
  const file = path.join(output, `${name}.png`);
  fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(file);
  report.measurements.push({ name, ...await evaluate(measureGeometry) });
}
function measureGeometry() {
  const selectors = {
    toolbar: ".response-selection-toolbar", card: "[data-main-conversation-composer] .composer-selection-chip",
    frame: "[data-main-conversation-composer] .composer-frame",
    popover: ".composer-selection-panel", sourceComment: ".response-selection-toolbar .selection-action-comment-input",
  };
  const regions = {};
  for (const [name, selector] of Object.entries(selectors)) {
    regions[name] = [...document.querySelectorAll(selector)].filter(node => node.getBoundingClientRect().width).map(node => {
      const rect = node.getBoundingClientRect(), css = getComputedStyle(node);
      return { selector, left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height,
        fontSize: css.fontSize, fontFamily: css.fontFamily, lineHeight: css.lineHeight,
        scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight,
        insideComposer: name === "card" ? !!node.closest(".composer") : null,
        contained: rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth + 1 && rect.bottom <= innerHeight + 1 };
    });
  }
  const frame = window.frameElement?.getBoundingClientRect();
  return { unit: "CSS px", boundary: "DOM layout boxes, not glyph/ink or aesthetic measurements", viewport: { width: innerWidth, height: innerHeight, devicePixelRatio, visualScale: visualViewport?.scale },
    parentFrame: frame ? { left: frame.left, top: frame.top, width: frame.width, height: frame.height } : null,
    theme: document.documentElement.dataset.theme, userAgent: navigator.userAgent, regions };
}
async function select(text, last = false, physical = false) {
  await evaluate(selector => {
    const root = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().width);
    document.activeElement?.blur();
    root.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerId: 1 }));
    root.scrollIntoView({ block: "center", behavior: "instant" });
  }, surface);
  // Source navigation and centering deliver scroll asynchronously. A user starts
  // the next drag after that movement; don't create a toolbar destined to be
  // dismissed by the previous operation's queued scroll event.
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const selected = await evaluate((selector, text, last) => {
    const root = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().width);
    const source = root.textContent;
    const start = last ? source.lastIndexOf(text) : source.indexOf(text);
    if (start < 0) throw new Error(`Missing ${text} in ${source}`);
    const end = start + text.length;
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let offset = 0, node, startNode, endNode, startOffset, endOffset;
    while ((node = walker.nextNode())) {
      if (!startNode && start < offset + node.length) { startNode = node; startOffset = start - offset; }
      if (!endNode && end <= offset + node.length) { endNode = node; endOffset = end - offset; }
      offset += node.length;
    }
    const range = document.createRange();
    range.setStart(startNode, startOffset); range.setEnd(endNode, endOffset);
    const first = range.cloneRange(); first.collapse(true);
    const final = range.cloneRange(); final.collapse(false);
    const a = first.getBoundingClientRect(), b = final.getBoundingClientRect();
    const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
    document.dispatchEvent(new Event("selectionchange"));
    root.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerId: 1 }));
    return { text, start, end, source, from: { x: Math.round(a.x), y: Math.round(a.y + a.height / 2) }, to: { x: Math.round(b.x), y: Math.round(b.y + b.height / 2) } };
  }, surface, text, last);
  if (physical) {
    await evaluate(() => window.getSelection().removeAllRanges());
    win.webContents.focus();
    win.webContents.sendInputEvent({ type: "mouseDown", ...selected.from, button: "left", clickCount: 1 });
    for (let step = 1; step <= 10; step++) {
      win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(selected.from.x + (selected.to.x - selected.from.x) * step / 10), y: selected.from.y, button: "left" });
    }
    win.webContents.sendInputEvent({ type: "mouseUp", ...selected.to, button: "left", clickCount: 1 });
    await until(text => window.getSelection()?.toString() === text, "physical drag selected exact text", text);
  }
  await until(() => !!document.querySelector(".response-selection-toolbar button"), "native selection toolbar");
  return selected;
}
async function add(text, last = false, physical = false, comment = "") {
  const result = await select(text, last, physical);
  if (comment) {
    await click(".response-selection-toolbar .selection-action-comment-toggle");
    await input(".response-selection-toolbar .selection-action-comment-input", comment);
  }
  await click(comment ? ".response-selection-toolbar .selection-action-comment-submit" : ".response-selection-toolbar .selection-action-menu-controls > button:first-child");
  await until(selector => !!document.querySelector(selector), "quote card", card);
  return result;
}
async function closeQuotePanel() {
  await evaluate(() => document.querySelector(".composer-selection-panel").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  await until(() => !document.querySelector(".composer-selection-panel"), "quote panel closed");
}
async function checkSource(expected) {
  await settle();
  await click(card);
  await click(".composer-selection-quote");
  const actual = await until(() => {
    const highlight = CSS.highlights.get("wuu-response-source");
    const range = highlight && [...highlight][0];
    if (!range) return null;
    const root = range.startContainer.parentElement.closest(".agent-text");
    const prefix = document.createRange(); prefix.selectNodeContents(root); prefix.setEnd(range.startContainer, range.startOffset);
    return { text: range.toString(), start: prefix.toString().length };
  }, "exact source highlight");
  if (actual.text !== expected.text || actual.start !== expected.start) {
    throw new Error(`Source highlight mismatch: ${JSON.stringify({ actual, expected })}`);
  }
}
async function send(expected, comment, prompt) {
  const before = report.calls.length;
  await input('[data-main-conversation-composer] .composer textarea', prompt);
  await click('[data-main-conversation-composer] .composer-send-button');
  await until(() => !document.querySelector(card), "submitted quote clears");
  for (let retry = 0; report.calls.length === before && retry < 100; retry++) await sleep(20);
  if (report.calls.length !== before + 1) throw new Error("Expected exactly one bridge call");
  const call = report.calls.at(-1);
  validatePayload(call, expected, comment, prompt);
}
function validatePayload(call, expected, comment, prompt) {
  if (call.method !== "startTurn" || call.args[0] !== "selection-main") throw new Error("Wrong send route");
  const parts = call.args[6];
  const quote = `Quoted assistant response (JSON):\n${JSON.stringify({ text: expected.text, comment })}\n`;
  if (parts[0].type !== "response_selection" || parts[0].text !== quote || parts[0].selection.text !== expected.text || (parts[0].selection.comment || "") !== comment) throw new Error("Quote/comment content parts mismatch");
  const source = parts[0].selection.source;
  if (source.thread_id !== "selection-main" || source.turn_id !== "selection-main-turn" || source.item_id !== "selection-main-answer" || source.start_offset !== expected.start || source.end_offset !== expected.end || (source.range_text ?? expected.text) !== expected.source.slice(expected.start, expected.end) || !parts[0].selection.id) throw new Error("Rich selection source metadata mismatch");
  if (prompt && (parts.at(-1).type !== "text" || parts.at(-1).text !== prompt)) throw new Error("Prompt part mismatch");
  if (!call.args[1].includes(quote.trimEnd()) || (prompt && !call.args[1].includes(prompt))) throw new Error("Flattened prompt lost quote/comment or prompt");
}
async function run() {
  win = new BrowserWindow({ width: 1200, height: 820, show: process.env.WUU_E2E_VISIBLE === "true", webPreferences: { preload: path.join(__dirname, "response-selection-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  win.webContents.on("console-message", ({ level, message }) => { if (level >= 3) report.errors.push(message); });
  // The synthetic bridge never needs remote resources or a live provider.
  win.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
    report.errors.push(`Unexpected network request: ${details.url}`);
    callback({ cancel: true });
  });
  await win.loadFile(process.env.WUU_E2E_RENDERER || path.join(desktop, "out/renderer/index.html"));
  await until(selector => !!document.querySelector(selector), "settled response", surface);
  await evaluate(() => {
    for (const toggle of document.querySelectorAll('.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]')) toggle.click();
    if (!document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) document.querySelector(".sidebar-toggle-button").click();
  });
  const drag = await add("Native drag selection", false, true);
  await checkSource(drag);
  await screenshot("physical-drag-source");
  await click(card);
  await click(".composer-selection-actions button:last-child");
  report.cases.push("physical mouse drag -> toolbar -> card -> exact source -> remove");

  const comment = 'Explain "this" 😀\n第二行';
  const repeated = await select("Repeated 😀 café 中文 target.", true);
  await screenshot("toolbar-actions");
  await click(".response-selection-toolbar .selection-action-comment-toggle");
  // Real Chromium input insertion, not a React setter, protects the restored-Range
  // focus contract: typing must enter the comment rather than replace the quote.
  await win.webContents.insertText(comment);
  await until(value => document.querySelector('.response-selection-toolbar .selection-action-comment-input')?.value === value, "native source comment typing", comment);
  await screenshot("comment-expanded");
  await click(".response-selection-toolbar .selection-action-comment-submit");
  await until(selector => !!document.querySelector(selector), "source comment added", card);
  await checkSource(repeated);
  await send(repeated, comment, "Please explain the selected passage.");
  report.cases.push("repeated Unicode last occurrence, UTF16 offsets, exact highlight, escaped comment + prompt payload");

  const quote = await add("Repeated 😀 café 中文 target.");
  await send(quote, "", "");
  report.cases.push("quote-only submission retains rich metadata and flattened quote");

  await add("Repeated 😀 café 中文 target.", true);
  for (const [width, height, font, theme] of [[1200, 820, 14, "light"], [1200, 820, 20, "dark"], [390, 820, 14, "dark"], [390, 820, 20, "light"]]) {
    win.setContentSize(width, height);
    await evaluate((font, theme) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
      document.documentElement.style.setProperty("--ui-font-size", `${font}px`);
    }, font, theme);
    await settle();
    await click(card);
    await until(() => {
      const node = document.querySelector(".composer-selection-panel");
      if (!node) return false;
      const rect = node.getBoundingClientRect();
      return rect.width > 0 && rect.left >= 0 && rect.right <= innerWidth + 1 && rect.top >= 0 && rect.bottom <= innerHeight + 1;
    }, "quote editor within viewport");
    await screenshot(`${theme}-${width}-${font}`);
    await closeQuotePanel();
  }
  report.cases.push("light/dark, default/large font, wide/narrow quote card and editor geometry");
  if (report.errors.length) throw new Error(`Unexpected renderer or network errors: ${JSON.stringify(report.errors)}`);
  report.status = "passed";
}
if (browserPreview) servePreview();
else app.whenReady().then(run).catch(async error => {
  report.status = "failed"; report.failure = String(error.stack || error);
  console.error(error);
  if (win && !win.isDestroyed()) await screenshot("failure").catch(() => {});
}).finally(() => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  app.exit(report.status === "passed" ? 0 : 1);
});

// Explicit alternate runtime when Electron cannot launch under host restrictions.
// This serves the same built renderer and mock bridge, never the production IPC.
function servePreview() {
  const http = require("node:http");
  const root = path.join(desktop, "out/renderer");
  const preload = fs.readFileSync(path.join(__dirname, "response-selection-e2e-preload.cjs"), "utf8").replace('const { contextBridge, ipcRenderer } = require("electron");', "");
  const shim = `const process={env:{WUU_SELECTION_E2E_CWD:${JSON.stringify(path.dirname(desktop))}}};
    window.selectionCalls=[];
    const contextBridge={exposeInMainWorld:(name,api)=>window[name]=api};
    const ipcRenderer={on:()=>{},send:(_channel,call)=>window.selectionCalls.push(call)};\n${preload}`;
  const harness = `const report={scene:'response-selection-v2',calls:window.selectionCalls,cases:[],measurements:[],boundary:"Built React renderer in embedded browser; mock transport; NOT Electron E2E"};
    const surface=${JSON.stringify(surface)},card=${JSON.stringify(card)};
    const evaluate=async(fn,...args)=>fn(...args);
    ${[sleep, until, click, input, select, add, settle, closeQuotePanel, checkSource, send, validatePayload, measureGeometry].map(fn => `const ${fn.name || "sleep"}=${fn.toString()};`).join("\n")}
    const params=new URLSearchParams(location.search);
    report.nativeInput=[];
    report.eventTrace=[];
    function trace(event){const node=event.target;report.eventTrace.push({time:performance.now(),type:event.type,target:node?.className||node?.nodeName||'window',active:document.activeElement?.className,toolbar:!!document.querySelector('.response-selection-toolbar'),commenting:!!document.querySelector('.response-selection-commenting'),selection:window.getSelection()?.toString(),scrollTop:node?.scrollTop,visibility:document.visibilityState,viewport:{width:innerWidth,height:innerHeight,dpr:devicePixelRatio},detail:event.detail&&typeof event.detail==='object'?event.detail:undefined});if(report.eventTrace.length>500)report.eventTrace.shift()}
    for(const type of ['scroll','blur','focusin','focusout','pointerdown','pointerup','input','selectionchange','visibilitychange'])document.addEventListener(type,trace,true);
    for(const type of ['blur','resize','selection-fixture-step'])window.addEventListener(type,trace);
    document.addEventListener('input',event=>{if(event.target.matches('.response-selection-toolbar .selection-action-comment-input'))report.nativeInput.push({type:event.type,isTrusted:event.isTrusted,value:event.target.value,active:document.activeElement?.className,selection:window.getSelection()?.toString()})});
    document.addEventListener('keyup',event=>{if(event.key==='Escape')requestAnimationFrame(()=>{report.escapeFocus=document.activeElement?.className})});
    const style=document.createElement('style'); style.textContent='#selection-fixture-controls{position:fixed;top:36px;right:8px;max-width:calc(100% - 16px);z-index:2147483647;display:flex;flex-wrap:wrap;gap:4px;font:11px sans-serif;background:#eee;color:#111;padding:4px}';document.head.append(style);
    const controls=document.createElement('div');controls.id='selection-fixture-controls';document.body.append(controls);
    function control(label,action){const button=document.createElement('button');button.textContent=label;button.onclick=()=>Promise.resolve(action()).catch(error=>{report.failure=String(error);save()});controls.append(button)}
    control('Select repeated',()=>select('Repeated 😀 café 中文 target.',true));
    control('Select long',async()=>{const text=document.querySelector(surface).textContent;await select(text)});
    control('Toggle theme',()=>document.documentElement.dataset.theme=document.documentElement.dataset.theme==='dark'?'light':'dark');
    control('Toggle font',()=>{const root=document.documentElement;const size=root.dataset.fixtureLarge?'14':'20';root.dataset.fixtureLarge=size==='20'?'1':'';root.style.setProperty('--conversation-message-font-size',size+'px');root.style.setProperty('--ui-font-size',size+'px')});
    control('Hide fixture controls',()=>controls.remove());
    if(params.get('controls')==='0')controls.remove();
    control('Measure geometry',async()=>{report.measurements.push({name:'manual',...measureGeometry()});await save()});
    async function save(){report.geometry=measureGeometry();await fetch('/report'+location.search,{method:'POST',body:JSON.stringify(report,null,2)})}
    (async()=>{try{
      await until(selector=>!!document.querySelector(selector),'settled response',surface);
      const sourceRoot=document.querySelector(surface);
      const observer=new MutationObserver(records=>{for(const record of records)trace({type:'source-observer-mutation',target:record.target,detail:{kind:record.type,attribute:record.attributeName,oldValue:record.oldValue,newValue:record.attributeName?record.target.getAttribute(record.attributeName):undefined}})});
      observer.observe(sourceRoot,{subtree:true,childList:true,characterData:true});
      for(let node=sourceRoot.closest('article');node;node=node.parentElement)observer.observe(node,{attributes:true,attributeOldValue:true,attributeFilter:['hidden','inert','aria-hidden','style','class','data-thread-id','data-response-settled']});
      new MutationObserver(records=>{for(const record of records)for(const node of [...record.addedNodes,...record.removedNodes])if(node.nodeType===1&&node.matches('.response-selection-toolbar'))trace({type:'toolbar-lifecycle',target:node,detail:{connected:node.isConnected}})}).observe(document.body,{childList:true});
      for(const toggle of document.querySelectorAll('.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]'))toggle.click();
      if(!document.querySelector('.app-shell').classList.contains('sidebar-collapsed'))document.querySelector('.sidebar-toggle-button').click();
      if(params.has('theme'))document.documentElement.dataset.theme=params.get('theme');
      if(params.has('font')){document.documentElement.style.setProperty('--conversation-message-font-size',params.get('font')+'px');document.documentElement.style.setProperty('--ui-font-size',params.get('font')+'px')}
      // The embedded host initially loads pages hidden, then makes them visible
      // on observation. The product intentionally dismisses selection UI on
      // visibilitychange. Start user gestures only after that host transition;
      // never suppress the product's lifecycle handler to make a test pass.
      if(document.visibilityState!=='visible'){
        report.status='waiting-for-visible-host';await save();
        await new Promise(resolve=>{const visible=()=>{if(document.visibilityState==='visible'){document.removeEventListener('visibilitychange',visible);resolve()}};document.addEventListener('visibilitychange',visible);visible()});
      }
      await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
      const scene=params.get('state')||'manager';
      const text=params.get('long')==='1'?document.querySelector(surface).textContent:'Repeated 😀 café 中文 target.';
      if(scene==='send'){
        const comment='Explain "this" 😀\\n第二行';
        const selected=await add(text,true,false,comment);await checkSource(selected);await send(selected,comment,'Please explain.');
        report.cases.push('source-local Comment + Add + send wire payload');
        const quote=await add(text,true);await send(quote,'','');report.cases.push('quote-only wire payload');
      }else if(scene==='toolbar'||scene==='comment'||scene==='typing'){
        await select(text,true);
        if(scene==='comment'||scene==='typing'){await click('.response-selection-toolbar .selection-action-comment-toggle');if(scene==='comment')await input('.response-selection-toolbar .selection-action-comment-input','Explain "this" 😀\\n第二行')}
      }else{
        const expected=await add(text,true,false,'Explain "this" 😀\\n第二行');
        if(params.get('long')!=='1'){await checkSource(expected);report.cases.push({name:'repeated Unicode exact source highlight',expected})}
        const count=Math.max(1,Math.min(12,Number(params.get('count'))||(scene==='aggregate'?2:1)));
        for(let index=1;index<count;index++)await add('Native drag selection',false,false,'Comment '+(index+1));
        if(scene==='manager'||scene==='aggregate')await click(card);
        if(scene==='aggregate'){
          const entries=()=>[...document.querySelectorAll('.composer-selection-panel .composer-selection-entry')];
          await until(()=>entries().length===2,'two selections in the chip');
          if(!document.querySelector(card)?.closest('.composer'))throw new Error('Selection chip rendered outside the composer');
          entries()[1].querySelector('.composer-selection-actions button:last-child').click();
          await until(()=>entries().length===1,'remove only the second selection');
          await closeQuotePanel();
          await checkSource(expected);await click(card);report.cases.push('two selections in one chip; removing the second preserves the first exact source');
        }
      }
      report.measurements.push({name:scene,...measureGeometry()});
      report.status='ready-for-visual-review';
    }catch(error){report.status='failed';report.failure=String(error.stack||error)}finally{await save()}})();
    setInterval(save,1000);`;
  http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");
    if (url.pathname === "/report" && request.method === "POST") {
      let body = ""; request.on("data", chunk => body += chunk); request.on("end", () => {
        const scene = [...url.searchParams].map(([key, value]) => `${key}-${value}`).join("-").replace(/[^a-z0-9-]/gi, "").slice(0, 160) || "default";
        fs.writeFileSync(path.join(output, "browser-report.json"), body);
        fs.writeFileSync(path.join(output, `browser-report-${scene}.json`), body); response.end("ok");
      }); return;
    }
    if (url.pathname === "/" && url.searchParams.has("width")) {
      const width = Math.max(320, Math.min(1800, Number(url.searchParams.get("width")) || 390));
      const height = Math.max(400, Math.min(1400, Number(url.searchParams.get("height")) || 820));
      const source = `/index.html?${url.searchParams}`.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
      response.setHeader("Content-Type", "text/html");
      response.end(`<!doctype html><html><head><title>Selection viewport fixture</title></head><body style="margin:0;background:#777"><iframe title="Real renderer ${width} by ${height}" src="${source}" width="${width}" height="${height}" style="display:block;border:0"></iframe></body></html>`);
      return;
    }
    if (request.url.startsWith("/fixture.js")) { response.setHeader("Content-Type", "text/javascript"); response.end(shim); return; }
    if (request.url.startsWith("/harness.js")) { response.setHeader("Content-Type", "text/javascript"); response.end(harness); return; }
    const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
    const file = path.resolve(root, `.${pathname === "/" ? "/index.html" : pathname}`);
    if (!file.startsWith(`${root}${path.sep}`) || !fs.existsSync(file)) { response.writeHead(404); response.end(); return; }
    const ext = path.extname(file);
    response.setHeader("Content-Type", { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[ext] || "application/octet-stream");
    if (ext === ".html") {
      response.end(fs.readFileSync(file, "utf8").replace("<head>", '<head><script src="/fixture.js"></script>').replace("</body>", '<script src="/harness.js"></script></body>'));
    } else response.end(fs.readFileSync(file));
  }).listen(4179, "127.0.0.1", () => console.log("Selection browser preview: http://127.0.0.1:4179"));
}
