/** Run with `bun run probe:surface`; CHROME can override the executable. */
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const candidates = [process.env.CHROME, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "chromium", "google-chrome"].filter(Boolean) as string[];
const chrome = candidates.find((bin) => {
  try { return Bun.spawnSync([bin, "--version"], { stdout: "ignore", stderr: "ignore" }).success; }
  catch { return false; }
});
if (!chrome) throw new Error("Chrome is required for this probe; set CHROME to its executable.");
const built = await Bun.build({ entrypoints: ["scripts/probe/surface.tsx"], target: "browser", format: "esm" });
if (!built.success) throw new Error(built.logs.join("\n"));
const js = built.outputs.find((file) => file.path.endsWith(".js"))!;
const css = built.outputs.find((file) => file.path.endsWith(".css"))!;
const directory = mkdtempSync(join(tmpdir(), "wuu-surface-probe-"));
const server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch(req) {
  const path = new URL(req.url).pathname;
  if (path === "/surface.js") return new Response(js);
  if (path === "/surface.css") return new Response(css);
  return new Response(`<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/surface.css">
    <style>body{font:15px system-ui;background:#f6f4f0;color:#303937;margin:0}main{max-width:920px;margin:28px auto}h1{font-size:23px}.surface-workbench{background:white;padding:24px;border-radius:20px}.control-grid{display:flex;gap:32px;align-items:center}label{display:flex;flex-direction:column;gap:8px}input[type=range]{width:180px}.mascot-size-grid{display:flex;align-items:center;justify-content:space-around;text-align:center}figure{margin:16px}figcaption{color:#747571;margin-top:12px}p{color:#747571}#product{display:flex;align-items:center;justify-content:center;margin-top:20px}</style>
    </head><body><div id="root"></div><script type="module" src="/surface.js"></script></body></html>`, { headers: { "content-type": "text/html" } });
}});
const browser = Bun.spawn([chrome, "--headless=new", "--no-sandbox", "--no-first-run", "--disable-background-timer-throttling", "--disable-renderer-backgrounding", "--remote-debugging-port=0", `--user-data-dir=${directory}`, "about:blank"], { stdout: "ignore", stderr: "ignore" });
let ws: WebSocket | undefined;
const pending = new Map<number, { resolve: (value: any) => void; reject: (reason: unknown) => void }>();
let id = 0;
const timeout = setTimeout(() => { console.error("Surface probe timed out"); browser.kill(); ws?.close(); server.stop(true); process.exitCode = 1; }, 30000);
function call(method: string, params: object = {}): Promise<any> {
  return new Promise((resolve, reject) => { const next = ++id; pending.set(next, { resolve, reject }); ws!.send(JSON.stringify({ id: next, method, params })); });
}
async function evaluate(expression: string) {
  const result = await call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result.value;
}
const results: string[] = [];
const check = (name: string, value: unknown) => { if (!value) throw new Error(name); results.push(name); };
try {
  let port: string | undefined;
  for (let i = 0; i < 100 && !port; i++) {
    try { port = readFileSync(join(directory, "DevToolsActivePort"), "utf8").split("\n")[0]; }
    catch { await Bun.sleep(50); }
  }
  if (!port) throw new Error("Chrome did not open its debugging endpoint");
  const tabs = await (await fetch(`http://127.0.0.1:${port}/json`)).json() as { type: string; webSocketDebuggerUrl: string }[];
  ws = new WebSocket(tabs.find((tab) => tab.type === "page")!.webSocketDebuggerUrl);
  await new Promise<void>((resolve, reject) => { ws!.onopen = () => resolve(); ws!.onerror = reject; });
  ws.onmessage = (event) => {
    const message = JSON.parse(String(event.data));
    const task = pending.get(message.id);
    if (task) { pending.delete(message.id); message.error ? task.reject(message.error) : task.resolve(message.result); }
  };
  ws.onclose = () => { for (const task of pending.values()) task.reject(new Error("Browser disconnected")); pending.clear(); };
  await call("Emulation.setDeviceMetricsOverride", { width: 1000, height: 920, deviceScaleFactor: 1, mobile: false });
  await call("Page.navigate", { url: server.url.href });
  for (let i = 0; i < 100; i++) {
    if (await evaluate(`!!document.querySelector('.surface-workbench .mo-eye path')`)) break;
    await Bun.sleep(50);
  }
  // DOM-native input events drive the existing React workbench controls.
  await evaluate(`window.probe = {
   svg:document.querySelector('svg[aria-hidden]') || document.querySelector('.surface-workbench svg'),
   set:(label,value)=>{const el=document.querySelector('[aria-label="'+label+'"]');const prototype=el.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(el,String(value));el.dispatchEvent(new Event(el.tagName==='SELECT'?'change':'input',{bubbles:true}));},
   wait:ms=>new Promise(r=>setTimeout(r,ms)),
  }; probe.svg=document.querySelector('.surface-workbench svg');
  probe.nodes=[...probe.svg.querySelectorAll('.mo-eye path')];
  probe.paths=()=>probe.nodes.map(p=>p.getAttribute('d'));
  probe.expected=()=>{const img=document.querySelector('.surface-workbench img');const xml=decodeURIComponent(img.src.split(',')[1]);const doc=new DOMParser().parseFromString(xml,'image/svg+xml');return [...([...doc.querySelectorAll('g[fill]')].at(-1)).querySelectorAll('path')].map(p=>p.getAttribute('d'));};`);
  check('initial static/live contour equality',await evaluate(`JSON.stringify(probe.paths())===JSON.stringify(probe.expected())`));
  for (const expression of ['happy','wink','sleepy','idle']) {
   await evaluate(`probe.set('Expression','${expression}');probe.set('Yaw',-34);probe.set('Pitch',23);probe.wait(750)`);
   check(`${expression}: posed contour matches static export`,await evaluate(`JSON.stringify(probe.paths())===JSON.stringify(probe.expected())`));
  }
  await evaluate(`probe.before=probe.paths();probe.set('Yaw',40);probe.wait(130)`);
  const mid=await evaluate(`({paths:probe.paths(),expected:probe.expected(),sameNodes:probe.nodes.every((n,i)=>n===probe.svg.querySelectorAll('.mo-eye path')[i])})`);
  check('camera morph preserves DOM',mid.sameNodes);
  check('camera morph has an intermediate contour',await evaluate(`JSON.stringify(probe.paths())!==JSON.stringify(probe.before)&&JSON.stringify(probe.paths())!==JSON.stringify(probe.expected())`));
  await evaluate(`probe.wait(700)`);
  check('camera morph reaches the shared projection',await evaluate(`JSON.stringify(probe.paths())===JSON.stringify(probe.expected())`));
  // Pause seeded motion at deterministic phases; keep only blink active.
  await evaluate(`probe.root=probe.svg.querySelector('.mo-root');probe.eyes=probe.svg.querySelector('.mo-eyes');probe.root.style.setProperty('--mo-amp','1');probe.root.style.setProperty('--mo-look-x','0');probe.root.style.setProperty('--mo-look-y','0');probe.root.style.setProperty('--mo-morph','0ms');probe.eyes.getAnimations().forEach(a=>{a.pause();a.currentTime=0;});probe.wait(450)`);
  const open=await evaluate(`probe.nodes.map(p=>p.getBBox().height)`);
  await evaluate(`probe.eyes.getAnimations().forEach(a=>{if(a.animationName==='mo-surface-blink'){const t=a.effect.getTiming();a.currentTime=Number(t.delay)+Number(t.duration)*.98;}});probe.wait(50)`);
  const shut=await evaluate(`probe.nodes.map(p=>p.getBBox().height)`);
  check('blink deforms the surface contour',shut.every((h:number,i:number)=>h<open[i]*.35));
  await evaluate(`probe.product=document.querySelector('#product svg');probe.portal=probe.product.querySelector('.wuu-mascot-layer-front');probe.productEye=probe.product.querySelector('.mo-eye path');probe.productBefore=probe.productEye.getAttribute('d');document.querySelector('#product button').click();probe.wait(800)`);
  check('product activity keeps face and accessory nodes',await evaluate(`probe.product.querySelector('.mo-eye path')===probe.productEye && probe.product.querySelector('.wuu-mascot-layer-front')===probe.portal && probe.productEye.getAttribute('d')!==probe.productBefore`));
  await evaluate(`probe.pointerBefore=probe.productEye.getAttribute('d');window.dispatchEvent(new PointerEvent('pointermove',{clientX:20,clientY:20,pointerType:'mouse'}));probe.wait(250)`);
  check('pointer attention updates the surface camera', await evaluate(`probe.productEye.getAttribute('d')!==probe.pointerBefore && Number(probe.product.style.getPropertyValue('--mo-pointer-yaw'))<0`));
  await call('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'reduce'}]});
  await evaluate(`probe.wait(100);`);
  await evaluate(`probe.set('Expression','wink');probe.set('Yaw',-25);probe.wait(40)`);
  check('reduced motion applies the final expression immediately',await evaluate(`JSON.stringify(probe.paths())===JSON.stringify(probe.expected())`));
  await evaluate(`probe.still=probe.paths();probe.wait(180)`);
  check('reduced motion has no ambient geometry changes',await evaluate(`JSON.stringify(probe.paths())===JSON.stringify(probe.still)`));
  check('reduced motion clears pointer attention', await evaluate(`Number(probe.product.style.getPropertyValue('--mo-pointer-yaw'))===0`));
  await evaluate(`probe.set('Expression','idle');probe.set('Yaw',30);probe.set('Pitch',12);probe.wait(100)`);
  if (process.env.SURFACE_SCREENSHOT) {
    const image = await call("Page.captureScreenshot", { format: "png" });
    await Bun.write(process.env.SURFACE_SCREENSHOT, Buffer.from(image.data, "base64"));
  }
  console.log(`${results.length} browser checks passed:\n${results.map((name) => `  ✓ ${name}`).join("\n")}`);
} finally {
  clearTimeout(timeout);
  ws?.close();
  browser.kill();
  await browser.exited;
  server.stop(true);
  rmSync(directory, { recursive: true, force: true });
}
