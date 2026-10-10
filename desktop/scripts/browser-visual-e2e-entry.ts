// Real Go tool loop, Electron browser host, Chromium pixels and local synthetic
// provider. This never uses a real model, account, key, or browser profile.
import assert from "node:assert/strict";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { createServer, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { app, BrowserWindow, WebContentsView, nativeImage } from "electron";
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from "../src/main/browserHostWindows";
import type { WindowRegistry } from "../src/main/windowRegistry";

const output = resolve(process.env.WUU_BROWSER_VISUAL_OUTPUT ?? "out/e2e/browser-visual");
const fixture = mkdtempSync(join(tmpdir(), "wuu-browser-visual-"));
const corePath = resolve(process.env.WUU_DESKTOP_CORE ?? "build/bin/wuu-core");
app.setPath("userData", join(fixture, "electron-profile"));
app.on("window-all-closed", () => { /* The next isolated scenario creates its own windows. */ });
mkdirSync(output, { recursive: true });
const evidence: Record<string, unknown>[] = [];
const browserMethods = ["browser/cdp", "browser/screenshot", "browser/open_tab", "browser/close_tab", "browser/set_visibility", "browser/list_tabs"];
const page = `<!doctype html><meta charset="utf-8"><title>Visual evidence fixture</title>
<style>html{overflow:scroll}body{margin:0;min-width:calc(100vw + 160px);min-height:calc(100vh + 160px)}::-webkit-scrollbar{width:24px;height:24px}canvas{position:absolute;inset:0;width:100vw;height:100vh}p,input{position:relative;z-index:1}p{margin:12px;font:20px sans-serif}</style>
<canvas></canvas><p id="status">Waiting for canvas input</p><input type="password" value="fixture-password-secret">
<script>
const canvas=document.querySelector('canvas');
function paint(){canvas.width=innerWidth;canvas.height=innerHeight;const c=canvas.getContext('2d');c.fillStyle='#f0f4fa';c.fillRect(0,0,canvas.width,canvas.height);c.fillStyle='#ff0000';c.fillRect(canvas.width*.68-7,canvas.height*.44-7,14,14)}
addEventListener('resize',paint);paint();
canvas.onclick=e=>{if(Math.abs(e.clientX-innerWidth*.68)<=7&&Math.abs(e.clientY-innerHeight*.44)<=7)document.querySelector('#status').textContent='Canvas target clicked';else document.querySelector('#status').textContent='Wrong canvas point'};
history.replaceState({},'',location.pathname+'?code=fixture-url-secret');
</script>`;

type Message = { role: string; content: string | { type: string; text?: string; image_url?: { url: string } }[] };
const textOf = (message: Message | undefined): string => typeof message?.content === "string" ? message.content : (message?.content ?? []).filter(p => p.type === "text").map(p => p.text ?? "").join("\n");
const imagesOf = (messages: Message[]): string[] => messages.flatMap(m => Array.isArray(m.content) ? m.content.flatMap(p => p.image_url ? [p.image_url.url] : []) : []);
const latestTool = (messages: Message[]): string => textOf([...messages].reverse().find(m => m.role === "tool"));
function objects(text: string): Record<string, any>[] {
  return text.split("\n").flatMap(line => { try { const value = JSON.parse(line); return value && typeof value === "object" ? [value] : []; } catch { return []; } });
}
function reply(res: ServerResponse, delta: Record<string, unknown>, finish: string): void {
  res.writeHead(200, { "Content-Type": "text/event-stream" });
  for (const [part, reason] of [[delta, null], [{}, finish]]) {
    res.write(`data: ${JSON.stringify({ id: "visual-fixture", object: "chat.completion.chunk", choices: [{ index: 0, delta: part, finish_reason: reason }] })}\n\n`);
  }
  res.end("data: [DONE]\n\n");
}
function decodeObservation(messages: Message[], name: string): { x: number; y: number } {
  const uri = imagesOf(messages).at(-1)!;
  assert.ok(uri?.startsWith("data:image/"), "Provider receives a native image attachment");
  const data = Buffer.from(uri.split(",")[1], "base64");
  assert.ok(data.length <= 2 * 1024 * 1024, "Image obeys inline attachment limit");
  const image = nativeImage.createFromBuffer(data);
  const { width, height } = image.getSize();
  assert.ok(width > 0 && height > 0 && Math.max(width, height) <= 2048);
  const metadata = objects(latestTool(messages));
  const pixels = metadata.find(value => value.action === "read_image");
  const viewport = metadata.find(value => value.coordinate_space === "css");
  assert.ok(pixels && viewport, `Capture geometry is present: ${latestTool(messages)}`);
  assert.equal(pixels.width, width);
  assert.equal(pixels.height, height);
  assert.ok(pixels.source_width > width, "Fixture exercises actual image downscaling");
  assert.ok(Math.abs(viewport.viewport_width - width) > 10, "CSS and image dimensions deliberately differ");
  const bitmap = image.toBitmap();
  let count = 0, sumX = 0, sumY = 0;
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
    const i = (y * width + x) * 4;
    if (bitmap[i + 2] > 230 && bitmap[i + 1] < 25 && bitmap[i] < 25) { count++; sumX += x; sumY += y; }
  }
  assert.ok(count > 100, "The model-bound image contains the actual canvas target");
  writeFileSync(join(output, `${name}.png`), image.toPNG());
  const point = { x: (sumX / count) * viewport.viewport_width / width, y: (sumY / count) * viewport.viewport_height / height };
  evidence.push({ name, source: { width: pixels.source_width, height: pixels.source_height }, delivered: { width, height, bytes: data.length, sha256: createHash("sha256").update(data).digest("hex") }, viewport, point });
  return point;
}

async function scenario(name: string, vision: boolean, ptc: boolean, takeover = false, capabilityKnown = true): Promise<void> {
  const root = join(fixture, name), home = join(root, "home"), workdir = join(root, "workspace");
  mkdirSync(home, { recursive: true }); mkdirSync(workdir, { recursive: true });
  let child: ChildProcessWithoutNullStreams;
  let host: BrowserHostCoordinator;
  let step = 0, tab = "", thread = "", activity = "", bridgeObservations = 0;
  let failure: Error | undefined;
  let lateFrameDelivered: Promise<void> | undefined;
  let resolveDone!: () => void, rejectDone!: (error: Error) => void;
  const done = new Promise<void>((resolve, reject) => { resolveDone = resolve; rejectDone = reject; });
  void done.catch(() => undefined); // The turn can fail before its start RPC resolves.
  const views: WebContentsView[] = [];
  const pending = new Map<string, { resolve: (value: any) => void; reject: (error: Error) => void }>();
  let sequence = 0;
  const send = (message: unknown) => child.stdin.write(JSON.stringify(message) + "\n");
  const rpc = (method: string, params: unknown): Promise<any> => new Promise((resolve, reject) => {
    const id = `e2e-${++sequence}`; pending.set(id, { resolve, reject }); send({ id, method, params });
  });
  const tool = (res: ServerResponse, args: Record<string, unknown>) => {
    const call = ptc ? { name: "run_code", arguments: JSON.stringify({ input: `const r = await tools.wuu_browser(${JSON.stringify(args)}); for (const part of r.content ?? []) if (part.type === "text") console.log(part.text);` }) }
      : { name: "wuu_browser", arguments: JSON.stringify(args) };
    reply(res, { tool_calls: [{ index: 0, id: `${name}-${step}`, type: "function", function: call }] }, "tool_calls");
  };
  const server = createServer((req, res) => {
    if (req.method === "GET" && req.url?.startsWith("/page")) { res.writeHead(200, { "Content-Type": "text/html" }); res.end(page); return; }
    let body = ""; req.on("data", part => { body += part; });
    req.on("end", async () => {
      try {
        const request = JSON.parse(body || "{}");
        // Auxiliary title/summary requests cannot advance the tool scenario.
        if (!request.tools?.length) {
          if (request.stream) reply(res, { content: "Visual fixture" }, "stop");
          else { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify({ choices: [{ message: { role: "assistant", content: "Visual fixture" }, finish_reason: "stop" }] })); }
          return;
        }
        const messages: Message[] = request.messages;
        const text = latestTool(messages);
        const current = step++;
        if (current === 0) return tool(res, { action: "navigate", url: `http://127.0.0.1:${(server.address() as any).port}/page` });
        if (current === 1) {
          tab = /"tab_id":"([^"]+)"/.exec(text)?.[1] ?? ""; assert.ok(tab, `No tab in ${text}`);
          // A large surface at fractional page zoom and device scale exercises
          // capture pixels, provider normalization, and CSS input independently.
          views[0].setBounds({ x: 0, y: 0, width: 2500, height: 1600 });
          views[0].webContents.setZoomFactor(1.25);
          views[0].setVisible(true);
          views[0].webContents.setBackgroundThrottling(false);
          try {
            await views[0].webContents.executeJavaScript("new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))");
            const geometry = await views[0].webContents.executeJavaScript("({width:innerWidth,height:innerHeight,scrollbarX:innerWidth-document.documentElement.clientWidth,scrollbarY:innerHeight-document.documentElement.clientHeight})");
            assert.ok(geometry.scrollbarX >= 20 && geometry.scrollbarY >= 20, "Fixture has real classic scrollbars on both axes");
            evidence.push({ name, geometry });
          } finally {
            views[0].webContents.setBackgroundThrottling(true);
            views[0].setVisible(false);
          }
          return tool(res, { action: "observe", tab_id: tab, ...(takeover || !vision ? { include_image: true } : {}) });
        }
        if (takeover) throw new Error("Provider continued after takeover");
        if (!vision) {
          if (current === 2) { assert.match(text, /image_input_unsupported/); assert.equal(bridgeObservations, 0); assert.equal(imagesOf(messages).length, 0); return tool(res, { action: "observe", tab_id: tab }); }
          if (current === 3) { assert.match(text, /Waiting for canvas input/); return tool(res, { action: "finalize", keep: [] }); }
          assert.equal(current, 4); reply(res, { content: "Visual fixture complete" }, "stop"); return;
        }
        if (current === 2) {
          assert.match(text, /Waiting for canvas input/); assert.ok(!text.includes("fixture-password-secret") && !text.includes("fixture-url-secret"));
          assert.equal(imagesOf(messages).length, 0, "DOM observations do not transmit pixels");
          return tool(res, { action: "screenshot", tab_id: tab });
        }
        if (current === 3) { assert.equal(imagesOf(messages).length, 0, "Preview-only screenshots do not transmit pixels"); return tool(res, { action: "sequence", tab_id: tab, steps: [{ action: "observe", include_image: true, risk: "safe" }, { action: "wait_for", timeout_ms: 1, risk: "safe" }] }); }
        if (current === 4) { assert.equal(imagesOf(messages).length, 1); const point = decodeObservation(messages, `${name}-observe`); return tool(res, { action: "click", tab_id: tab, ...point }); }
        if (current === 5) return tool(res, { action: "observe", tab_id: tab });
        if (current === 6) { assert.match(text, /Canvas target clicked/, "Pixel-derived coordinates activate the real canvas target"); return tool(res, { action: "screenshot", tab_id: tab, include_image: true }); }
        if (current === 7) {
          assert.equal(imagesOf(messages).length, 2); decodeObservation(messages, `${name}-screenshot`);
          await views[0].webContents.debugger.sendCommand("Emulation.setPageScaleFactor", { pageScaleFactor: 1.5 });
          return tool(res, { action: "observe", tab_id: tab, include_image: true });
        }
        if (current === 8) {
          assert.match(text, /unsupported_pinch_zoom/);
          await views[0].webContents.debugger.sendCommand("Emulation.setPageScaleFactor", { pageScaleFactor: 1 });
          return tool(res, { action: "finalize", keep: [] });
        }
        assert.equal(current, 9); reply(res, { content: "Visual fixture complete" }, "stop");
      } catch (error) { failure = error as Error; res.writeHead(500).end(failure.message); rejectDone(failure); }
    });
  });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  writeFileSync(join(home, "config.json"), JSON.stringify({ default_provider: "fixture", ptc: { enabled: ptc }, agent: { tool_loading: "flat", max_steps: 20 }, providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${(server.address() as any).port}/v1`, api_key: "fixture-only", model: "fixture", models: { fixture: { tool_call: true, ...(capabilityKnown ? { modalities: { input: vision ? ["text", "image"] : ["text"], output: ["text"] } } : {}), limit: { context: 200000, output: 8000 } } } } } }));
  child = spawn(corePath, ["app-server", "--safe-mode", "--workdir", workdir], { env: { ...process.env, HOME: home, WUU_HOME: home, CODEX_HOME: join(home, ".codex"), GROK_HOME: join(home, ".grok"), WUU_ENABLE_BROWSER: "1", WUU_ENABLE_CUA_MAC: "0" }, stdio: ["pipe", "pipe", "pipe"] });
  let log = ""; child.stderr.on("data", chunk => { log += chunk; });
  const main = new BrowserWindow({ show: false, webPreferences: { sandbox: true } });
  host = new BrowserHostCoordinator({ mainWindow: () => main } as unknown as WindowRegistry, {
    respond: (id, result) => {
      if (takeover && (result as any)?.result?.screenshot_path) {
        // Hold an already captured frame at the real reverse-RPC boundary,
        // revoke the activity, then deliver the late response deterministically.
        lateFrameDelivered = rpc("activity/takeover", { thread_id: thread, activity_id: activity }).then(() => { send({ id, result }); });
        void lateFrameDelivered.catch(rejectDone);
      } else send({ id, result });
    },
    reject: (id, message) => send({ id, error: { code: "browser", message } }),
  }, defaultBrowserHostDeps(
    () => new BrowserWindow({ show: false, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
    () => { const view = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } }); views.push(view); return view as unknown as BrowserViewHandle; },
  ), () => undefined);
  const reader = createInterface({ input: child.stdout });
  reader.on("line", line => {
    try {
      const message = JSON.parse(line);
      if (message.method?.startsWith("browser/") && message.id) {
        if (message.params?.method === "observe") bridgeObservations++;
        void host.handleServerRequest({ workdir, kind: "server-request", message });
      } else if (message.method === "browser/request_cancelled") {
        host.handleServerEvent({ workdir, kind: "notification", message });
      } else if (message.id && pending.has(String(message.id))) {
        const waiter = pending.get(String(message.id))!; pending.delete(String(message.id));
        if (message.error) waiter.reject(new Error(JSON.stringify(message.error))); else waiter.resolve(message.result);
      } else if (message.method?.startsWith("activity/")) {
        activity = message.params.id; host.updateActivity(message.params, message.method);
      } else if (message.method === "turn/error") {
        if (takeover && message.params.turn?.status === "interrupted") resolveDone();
        else rejectDone(new Error(JSON.stringify(message.params)));
      }
      else if (message.method === "turn/completed") resolveDone();
    } catch (error) { rejectDone(error as Error); }
  });
  child.on("error", rejectDone);
  const timer = setTimeout(() => {
    const error = new Error(`${name} timed out at step ${step}\n${log}`);
    for (const waiter of pending.values()) waiter.reject(error);
    pending.clear(); rejectDone(error);
  }, 60000);
  try {
    await rpc("initialize", { client: { name: "browser-visual-e2e" }, capabilities: { reverse_rpc: { methods: browserMethods } } });
    const started = await rpc("thread/start", { engine: "wuu", approve_for_me: false }); thread = started.thread.id;
    await rpc("turn/start", { thread_id: thread, prompt: "Run the isolated browser visual evidence fixture." });
    await done;
    if (takeover) { assert.ok(lateFrameDelivered); await lateFrameDelivered; }
    assert.equal(failure, undefined);
    assert.equal(step, takeover ? 2 : vision ? 10 : 5);
    if (takeover) assert.equal(await views[0].webContents.executeJavaScript("document.querySelector('#status').textContent"), "Waiting for canvas input");
    evidence.push({ scenario: name, status: "passed", providerRequests: step, bridgeObservations, vision, ptc, takeover, capabilityKnown });
  } finally {
    clearTimeout(timer); reader.close(); child.stdin.end();
    await Promise.race([once(child, "exit"), new Promise(resolve => setTimeout(resolve, 3000))]);
    if (child.exitCode === null) child.kill("SIGKILL");
    writeFileSync(join(output, `${name}-core.log`), log);
    host.destroyAll(); main.destroy(); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
  }
}

app.whenReady().then(async () => {
  try {
    await scenario("direct-vision", true, false);
    await scenario("ptc-vision", true, true);
    await scenario("text-only", false, false);
    await scenario("unknown-vision", true, false, false, false);
    await scenario("takeover", true, false, true);
    writeFileSync(join(output, "results.json"), JSON.stringify({ status: "passed", scope: "Production Go app-server and tool loop, real Chromium capture/input, local synthetic provider; no external model inference or physical macOS validation.", versions: process.versions, evidence }, null, 2));
    console.log(`Browser visual E2E passed: ${output}`);
    rmSync(fixture, { recursive: true, force: true }); app.exit(0);
  } catch (error) {
    writeFileSync(join(output, "results.json"), JSON.stringify({ status: "failed", error: String(error), evidence }, null, 2));
    console.error(error); app.exit(1);
  }
});
