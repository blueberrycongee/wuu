import assert from 'node:assert/strict';
import { spawn, type ChildProcess } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { createServer, request, type ServerResponse } from 'node:http';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { app, BrowserWindow, WebContentsView, nativeImage } from 'electron';
import { AppServerClientPool } from '../src/main/appServerClients';
import { createBrowserView } from '../src/main/browserView';
import { BrowserHostCoordinator, defaultBrowserHostDeps, type BrowserHostWindowHandle, type BrowserViewHandle } from '../src/main/browserHostWindows';
import { createObservationPiPFactory } from '../src/main/browserPiPWindow';
import { ObservationCoordinator } from '../src/main/cuaActivityWindows';
import type { WindowRegistry } from '../src/main/windowRegistry';
import { APP_SERVER_PROTOCOL_VERSION, BROWSER_REVERSE_RPC_METHODS, type ActivitySession, type RuntimeContext, type ServerEvent, type Thread } from '../src/shared/protocol';

const desktop = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const fixture = process.env.WUU_EXTERNAL_CAPABILITIES_FIXTURE!;
assert(fixture, 'Run through external-capabilities-e2e.cjs');
const output = resolve(process.env.WUU_EXTERNAL_CAPABILITIES_OUTPUT ?? join(desktop, 'out/e2e/external-capabilities'));
const home = join(fixture, 'home');
const projects = [join(fixture, 'project-a'), join(fixture, 'project-b')];
const cua = process.env.WUU_EXTERNAL_CAPABILITIES_CUA === '1';
for (const directory of [output, home, ...projects]) mkdirSync(directory, { recursive: true });
app.setPath('userData', join(fixture, 'profile'));
app.disableHardwareAcceleration();
app.on('window-all-closed', () => {});
Object.assign(process.env, { HOME: home, WUU_HOME: home, WUU_SAFE_MODE: cua ? '0' : '1', WUU_ENABLE_BROWSER: '1', WUU_ENABLE_CUA_LINUX: cua ? '1' : '0',
  WUU_DESKTOP_CORE: resolve(process.env.WUU_EXTERNAL_CAPABILITIES_CORE ?? join(desktop, 'build/bin/wuu-core')),
  WUU_SOURCE_ROOT: resolve(desktop, '..'), CODEX_HOME: join(home, '.codex'), CLAUDE_CONFIG_DIR: join(home, '.claude') });
delete process.env.ELECTRON_RENDERER_URL;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function bounded<T>(label: string, promise: Promise<T>, timeout = 30_000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try { return await Promise.race([promise, new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`Timed out waiting for ${label}`)), timeout);
  })]); } finally { clearTimeout(timer); }
}
async function waitFor(label: string, predicate: () => unknown | Promise<unknown>) {
  await bounded(label, (async () => {
    while (!await predicate()) await new Promise(resolve => setTimeout(resolve, 25));
  })());
}
type MCPResponse = { status: number; error?: unknown; result?: { isError?: boolean; tools?: { name: string }[]; content?: { type: string; text?: string; data?: string; mimeType?: string }[] } };
type Scenario = { marker: string; engine: string; context: RuntimeContext; thread: string; endpoint: string;
  session?: string; protocol?: string; tools: string[]; registered: ReturnType<typeof deferred<void>>;
  next: ReturnType<typeof deferred<ServerResponse>>; completed: ReturnType<typeof deferred<void>> };
const scenarios = new Map<string, Scenario>();
const commands = new Map<number, ReturnType<typeof deferred<MCPResponse>>>();
const activities = new Map<string, ActivitySession>();
const terminal = new Map<string, { status?: string; error?: unknown }>();
const checks: string[] = [];
const transcript: unknown[] = [];
const events: unknown[] = [];
const views: WebContentsView[] = [];
const browserRequests: { id: string; method: string; params: Record<string, unknown> }[] = [];
const browserReplies = new Map<string, { result?: unknown; error?: string }>();
let serial = 0;
let pool: AppServerClientPool;
let host: BrowserHostCoordinator;
let coordinator: ObservationCoordinator;
let nativeTarget: ChildProcess | undefined;
let captureGate: { entered: ReturnType<typeof deferred<void>>; resume: ReturnType<typeof deferred<void>> } | undefined;
function check(name: string, condition: unknown, detail?: unknown) {
  assert(condition, `${name}${detail === undefined ? '' : `: ${JSON.stringify(detail)}`}`);
  checks.push(name);
  console.log(`PASS ${name}`);
}
function json(response: ServerResponse, value: unknown) {
  response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(value));
}
const control = createServer((request, response) => {
  if (request.method === 'GET') {
    response.writeHead(200, { 'Content-Type': 'text/html' }).end(`<!doctype html><title>${request.url}</title>
      <body style="margin:0;background:#f4f7fa;color:#173454;font:18px system-ui"><h1>${request.url}</h1>
      <button id="target" style="position:absolute;left:100px;top:100px;width:150px;height:55px;background:#cce4ff">Target</button>
      <script>window.clicks=0;document.querySelector('button').onclick=()=>{window.clicks++;document.querySelector('button').textContent='Clicks: '+window.clicks};</script>`);
    return;
  }
  let body = '';
  request.on('data', data => { body += data; });
  request.on('end', () => {
    try {
      const value = JSON.parse(body);
      if (request.url?.startsWith('/v1/')) {
        const message = { role: 'assistant', content: 'Capability acceptance' };
        if (value.stream) {
          response.writeHead(200, { 'Content-Type': 'text/event-stream' });
          response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: message, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
        } else json(response, { choices: [{ message, finish_reason: 'stop' }] });
        return;
      }
      const scenario = scenarios.get(value.marker);
      assert(scenario, `Known scenario ${value.marker}`);
      if (request.url === '/register') {
        assert(value.catalog.result?.tools, JSON.stringify(value.catalog));
        Object.assign(scenario, { endpoint: value.endpoint, session: value.session, protocol: value.protocol,
          tools: value.catalog.result.tools.map((tool: { name: string }) => tool.name) });
        transcript.push({ marker: value.marker, engine: value.engine, tools: scenario.tools,
          permissionMode: value.permissionMode, allowedTools: value.allowedTools,
          endpointSHA256: createHash('sha256').update(value.endpoint).digest('hex') });
        scenario.registered.resolve();
        response.writeHead(204).end();
      } else if (request.url === '/next') scenario.next.resolve(response);
      else if (request.url === '/result') {
        const result = commands.get(value.id);
        assert(result, `Known tool command ${value.id}`);
        commands.delete(value.id);
        result.resolve(value.response);
        response.writeHead(204).end();
      } else throw new Error(`Unknown control path ${request.url}`);
    } catch (error) { response.writeHead(500).end(String(error)); }
  });
});
function notify(event: ServerEvent) {
  events.push(event);
  host?.handleServerEvent(event);
  if (event.kind === 'server-request') {
    if (event.message.method.startsWith('browser/')) {
      browserRequests.push({ id: String(event.message.id), method: event.message.method, params: event.message.params as Record<string, unknown> });
      void host.handleServerRequest(event);
    }
    else pool.rejectServerRequest(String(event.message.id), `Unexpected fixture request ${event.message.method}`);
    return;
  }
  if (event.kind !== 'notification') return;
  if (event.message.method.startsWith('activity/')) {
    const activity = event.message.params as unknown as ActivitySession;
    activities.set(activity.thread_id, activity);
    host.updateActivity(activity, event.message.method);
  }
  coordinator?.handleServerEvent(event);
  if (['turn/completed', 'turn/error'].includes(event.message.method)) {
    const params = event.message.params as unknown as { thread_id: string; turn?: { status?: string }; error?: unknown };
    terminal.set(params.thread_id, { status: params.turn?.status, error: params.error });
    for (const scenario of scenarios.values()) if (scenario.thread === params.thread_id) scenario.completed.resolve();
  }
}
async function start(engine: string, marker: string, project = 0, existingThread?: string, permission = 'unconfined') {
  const context: RuntimeContext = { kind: 'no_project', cwd: projects[project] };
  const thread = existingThread ?? (await pool.requestInContext<{ thread: Thread }>(context, 'thread/start', {
    engine, model: engine === 'codex' ? 'gpt-6-astra' : 'claude-sonnet-4', permission_mode: permission,
  })).thread.id;
  const scenario: Scenario = { marker, engine, context, thread, endpoint: '', tools: [],
    registered: deferred<void>(), next: deferred<ServerResponse>(), completed: deferred<void>() };
  scenarios.set(marker, scenario);
  terminal.delete(thread);
  coordinator.setActiveThread(thread);
  await pool.requestInContext(context, 'turn/start', { thread_id: thread, prompt: `WUU_CAPABILITY_SCENARIO=${marker}` });
  await bounded(`${engine} native MCP registration`, scenario.registered.promise);
  check(`${marker} receives the browser tool through native MCP configuration`, scenario.tools.includes('wuu_browser'), scenario.tools);
  if (!cua) check(`${marker} does not expose disabled CUA plugins`, scenario.tools.every(name => !/computer|cua/.test(name)), scenario.tools);
  else check(`${marker} exposes the enabled native CUA helper from its first catalog`, scenario.tools.some(name => /cua_linux.*computer/.test(name)), scenario.tools);
  return scenario;
}
async function tool(scenario: Scenario, args: Record<string, unknown>, name = 'wuu_browser', expectedError = false) {
  const next = await bounded(`${scenario.marker} engine command poll`, scenario.next.promise);
  scenario.next = deferred<ServerResponse>();
  const id = ++serial;
  const result = deferred<MCPResponse>();
  commands.set(id, result);
  json(next, { id, params: { name, arguments: args } });
  const response = await bounded(`${scenario.marker} ${name} ${args.action}`, result.promise);
  transcript.push({ marker: scenario.marker, name, args, response: { ...response, result: response.result && { ...response.result,
    content: response.result.content?.map(part => part.type === 'image' ? { type: part.type, mimeType: part.mimeType, bytes: Buffer.from(part.data!, 'base64').length } : part) } } });
  assert.equal(Boolean(response.error || response.result?.isError), expectedError, JSON.stringify(response));
  return response;
}
function text(response: MCPResponse) { return response.result?.content?.filter(part => part.type === 'text').map(part => part.text).join('\n') ?? ''; }
function saveImage(response: MCPResponse, filename: string) {
  const image = response.result?.content?.find(part => part.type === 'image');
  assert(image?.data && image.mimeType?.startsWith('image/'), 'MCP returns image content, rather than only a local path');
  const bytes = Buffer.from(image.data, 'base64');
  const decoded = nativeImage.createFromBuffer(bytes);
  const size = decoded.getSize();
  assert(!decoded.isEmpty() && size.width > 100 && size.height > 100, 'The returned image decodes to a rendered screenshot');
  const colors = new Set<number>();
  const bitmap = decoded.toBitmap();
  for (let offset = 0; offset < bitmap.length; offset += 16) colors.add(bitmap.readUInt32LE(offset));
  assert(colors.size > 20, `The returned screenshot contains rendered content, colors=${colors.size}`);
  writeFileSync(join(output, filename), decoded.toPNG());
  return size;
}
async function staleCall(scenario: Scenario, args: Record<string, unknown>) {
  try {
    const response = await fetch(scenario.endpoint, { method: 'POST', signal: AbortSignal.timeout(5000),
      headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
        ...(scenario.session ? { 'Mcp-Session-Id': scenario.session } : {}), ...(scenario.protocol ? { 'MCP-Protocol-Version': scenario.protocol } : {}) },
      body: JSON.stringify({ jsonrpc: '2.0', id: ++serial, method: 'tools/call', params: { name: 'wuu_browser', arguments: args } }) });
    const body = await response.text();
    return [404, 410].includes(response.status) || response.ok && /"isError"\s*:\s*true|"error"\s*:/.test(body);
  } catch (error) {
    const code = (error as { cause?: { code?: string } }).cause?.code;
    if (code && ['ECONNREFUSED', 'ECONNRESET', 'UND_ERR_SOCKET'].includes(code)) return true;
    throw error;
  }
}
async function finish(scenario: Scenario) {
  json(await bounded('engine finish command', scenario.next.promise), { finish: true });
  await bounded('authoritative external turn completion', scenario.completed.promise);
  check(`${scenario.marker} completes through its external adapter`, terminal.get(scenario.thread)?.status === 'completed', terminal.get(scenario.thread));
  check(`${scenario.marker} rejects late calls after completion`, await staleCall(scenario, { action: 'navigate', url: 'about:blank' }));
}
function tab(scenario: Scenario) {
  const activity = activities.get(scenario.thread);
  assert(activity?.target && activity.workdir === scenario.context.cwd && activity.controller === 'agent', JSON.stringify(activity));
  const meta = host.tabSurfaceMeta(scenario.context.cwd, activity.target);
  const view = views.find(view => view.webContents && !view.webContents.isDestroyed() && view.webContents.getURL() === meta?.url);
  assert(view, 'The actual activity identifies a real Chromium tab');
  return { id: activity.target, view };
}
async function closePreview(native = false) {
  let overlay: WebContentsView | undefined;
  let pip: BrowserWindow | undefined;
  await waitFor('visible production PiP', async () => {
    for (const win of BrowserWindow.getAllWindows().filter(win => win.isVisible())) {
      for (const child of win.contentView.children) {
        if (child instanceof WebContentsView && await child.webContents.executeJavaScript("Boolean(document.getElementById('close'))")) {
          overlay = child; pip = win; return true;
        }
      }
    }
    return false;
  });
  if (native) {
    let content: WebContentsView | undefined;
    await waitFor('real native PiP video frames', async () => {
      for (const child of pip!.contentView.children) {
        if (child instanceof WebContentsView && await child.webContents.executeJavaScript("(() => { const video = document.querySelector('video'); return Boolean(video && video.readyState >= 2 && video.videoWidth > 0 && video.currentTime > 0); })()")) {
          content = child; return true;
        }
      }
      return false;
    });
    writeFileSync(join(output, 'native-pip-live.png'), (await content!.webContents.capturePage()).toPNG());
    check('The external CUA activity presents real native PiP video frames', true);
  } else {
    let content: WebContentsView | undefined;
    await waitFor('live browser content mounted in PiP', async () => {
      content = views.find(view => pip!.contentView.children.includes(view));
      return content && await content.webContents.executeJavaScript("Boolean(document.getElementById('target'))");
    });
    writeFileSync(join(output, 'browser-pip-live.png'), (await content!.webContents.capturePage()).toPNG());
    check('The external browser activity mounts its actual live page in PiP', true);
  }
  await waitFor('PiP placeholder dismissed', () => overlay!.webContents.executeJavaScript("Number(getComputedStyle(document.getElementById('ph')).opacity) === 0"));
  writeFileSync(join(output, `pip-${serial}.png`), (await overlay!.webContents.capturePage()).toPNG());
  await overlay!.webContents.executeJavaScript("document.getElementById('close').click()");
  await waitFor('PiP dismissed', () => pip!.isDestroyed() || !pip!.isVisible());
}
async function run() {
  await new Promise<void>(resolve => control.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${(control.address() as { port: number }).port}`;
  process.env.WUU_CAPABILITY_CONTROL = base;
  const engines: Record<string, unknown> = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  for (const engine of ['codex', 'claude']) {
    const binary = join(fixture, engine);
    const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
    writeFileSync(binary, `#!/bin/sh\nexec ${quote(process.env.WUU_EXTERNAL_CAPABILITIES_NODE!)} ${quote(join(desktop, 'scripts/external-capabilities-fixture.mjs'))} ${engine} "$@"\n`, { mode: 0o755 });
    engines[engine] = { enabled: true, binary_path: binary };
  }
  const disabled = Object.fromEntries(readdirSync(join(desktop, '../internal/plugin/bundled'), { withFileTypes: true })
    .filter(entry => entry.isDirectory() && (!cua || entry.name !== 'cua-linux')).map(entry => [`plugin:bundled:${entry.name}`, true]));
  writeFileSync(join(home, 'config.json'), JSON.stringify({ engines, extensions: { disabled }, ptc: { enabled: false },
    default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', model: 'fixture', base_url: `${base}/v1`, api_key: 'fixture-only' } } }));
  await app.whenReady();
  const main = new BrowserWindow({ width: 1000, height: 720, show: false });
  await main.loadURL('data:text/html,<body style="font:18px system-ui;background:%23edf2f8"><h1>External engine capability acceptance</h1><p>Real host browser and PiP. Scripted external protocols.</p>');
  const registry = { mainWindow: () => main } as unknown as WindowRegistry;
  pool = new AppServerClientPool(() => ({ kind: 'no_project', cwd: projects[0] }), () => projects[0], notify, undefined, () => ({
    protocol_version: APP_SERVER_PROTOCOL_VERSION, client: { name: 'external-capabilities-e2e', version: 'fixture' },
    capabilities: { reverse_rpc: { methods: [...new Set([...BROWSER_REVERSE_RPC_METHODS, 'browser/finalize', 'browser/turn_ended'])] } },
  }));
  host = new BrowserHostCoordinator(registry, {
    respond: (id, result) => { browserReplies.set(id, { result }); pool.respondToServerRequest(id, result); },
    reject: (id, error) => { browserReplies.set(id, { error }); pool.rejectServerRequest(id, error); },
  }, defaultBrowserHostDeps(
    () => new BrowserWindow({ show: false, frame: false, skipTaskbar: true, webPreferences: { sandbox: true } }) as unknown as BrowserHostWindowHandle,
    options => {
      const view = createBrowserView(options);
      const execute = view.webContents.executeJavaScript.bind(view.webContents);
      view.webContents.executeJavaScript = async (code, userGesture) => {
        const result = await execute(code, userGesture);
        if (captureGate && code.includes('requestAnimationFrame(() => requestAnimationFrame(resolve))')) {
          const gate = captureGate; captureGate = undefined; gate.entered.resolve(); await gate.resume.promise;
        }
        return result;
      };
      views.push(view);
      return view as unknown as BrowserViewHandle;
    },
  ), () => undefined);
  const factory = createObservationPiPFactory({ browserHost: host, isPackaged: false, parent: () => main });
  coordinator = new ObservationCoordinator(registry, undefined, factory);
  coordinator.setBrowserInPanel(activity => host.isInPanel(activity.workdir, activity.target!));
  host.setRendererSink({ surface() {}, userInput() {}, adopted() {}, presented: () => coordinator.refreshBrowserPresentation() });
  pool.setClientTorndownHandler(cwd => host.onClientTorndown(cwd));
  main.show();
  main.focus();

  const a = await start('codex', 'codex-a');
  for (const id of [0, 'string-request-id']) {
    const response = await fetch(a.endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' },
      body: JSON.stringify({ jsonrpc: '2.0', id, method: 'tools/list', params: {} }) });
    const body = await response.json() as { id: string | number };
    check(`MCP preserves the ${typeof id} request ID`, body.id === id);
  }
  const rejectedOrigin = await fetch(a.endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: 'https://untrusted.example' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} }) });
  check('Browser origins cannot borrow the local turn capability', rejectedOrigin.status === 403);
  const rejectedHost = await bounded('foreign Host rejection', new Promise<number | undefined>((resolve, reject) => {
    const probe = request(a.endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json', Host: 'untrusted.example' } }, response => {
      response.resume();
      response.on('end', () => resolve(response.statusCode));
    });
    probe.on('error', reject);
    probe.end(JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} }));
  }));
  check('The capability endpoint rejects a foreign Host header', rejectedHost === 403, rejectedHost);
  await tool(a, {}, 'shell', true);
  check('The MCP endpoint cannot invoke tools outside its capability catalog', true);
  await tool(a, { action: 'navigate', url: `${base}/codex-a` });
  const aTab = tab(a);
  const screenshot = await tool(a, { action: 'screenshot', tab_id: aTab.id });
  saveImage(screenshot, 'codex-browser.png');
  check('Codex receives screenshot image bytes from the real host browser', true);
  await closePreview();
  check('Closing PiP keeps the Codex turn running', !terminal.has(a.thread) && pool.runningThreadsSnapshot().some(item => item.thread_id === a.thread));
  await tool(a, { action: 'click', tab_id: aTab.id, x: 140, y: 125 });
  check('Codex input still reaches the real page after closing PiP', await aTab.view.webContents.executeJavaScript('window.clicks') === 1);

  const b = await start('claude', 'claude-b');
  await tool(b, { action: 'navigate', url: `${base}/claude-b` });
  const bTab = tab(b);
  await tool(b, { action: 'click', tab_id: bTab.id, x: 140, y: 125 });
  saveImage(await tool(b, { action: 'screenshot', tab_id: bTab.id }), 'claude-browser.png');
  check('Claude input and image return cross its real stream-json adapter', await bTab.view.webContents.executeJavaScript('window.clicks') === 1);
  await tool(b, { action: 'click', tab_id: aTab.id, x: 140, y: 125 }, 'wuu_browser', true);
  check('A second thread cannot control the first thread tab', await aTab.view.webContents.executeJavaScript('window.clicks') === 1);
  const bTabs = await tool(b, { action: 'tabs' });
  check('A second thread cannot list the first thread tab', !text(bTabs).includes(aTab.id), text(bTabs));

  const c = await start('codex', 'codex-workspace-b', 1);
  await tool(c, { action: 'navigate', url: `${base}/workspace-b` });
  const cTab = tab(c);
  await tool(c, { action: 'click', tab_id: bTab.id, x: 140, y: 125 }, 'wuu_browser', true);
  check('Another workspace cannot control a foreign tab', await bTab.view.webContents.executeJavaScript('window.clicks') === 1);

  for (const [scenario, action] of [[a, 'takeover'], [b, 'stop']] as const) {
    const activity = activities.get(scenario.thread)!;
    await pool.requestInContext(scenario.context, `activity/${action}`, { thread_id: scenario.thread, activity_id: activity.id, execution_id: activity.execution_id });
    await bounded(`${action} interrupts its external engine`, scenario.completed.promise);
    const settled = terminal.get(scenario.thread);
    check(`${action} settles its external turn as interrupted`, settled?.status === 'interrupted' &&
      (!settled.error || String(settled.error).split('\n').every(line => line === 'context canceled')), settled);
    check(`${action} revokes the old MCP turn capability`, await staleCall(scenario, { action: 'click', tab_id: activity.target, x: 140, y: 125 }));
  }
  check('Takeover and stop do not affect the sibling workspace turn', !terminal.has(c.thread));

  const gate = { entered: deferred<void>(), resume: deferred<void>() };
  captureGate = gate;
  const pendingCapture = tool(c, { action: 'screenshot', tab_id: cTab.id }).catch(error => error);
  await bounded('real screenshot frame readiness', gate.entered.promise);
  const capture = browserRequests.filter(request => request.method === 'browser/screenshot' && request.params.thread_id === c.thread).at(-1)!;
  assert(capture?.params.dest_path, 'Pending capture has an actual host destination');
  const destination = String(capture.params.dest_path);
  mkdirSync(dirname(destination), { recursive: true });
  writeFileSync(destination, 'previous-history-preview');
  await pool.requestInContext(c.context, 'turn/interrupt', { thread_id: c.thread });
  gate.resume.resolve();
  await bounded('interrupted external engine settles', c.completed.promise);
  check('The interrupted external turn settles as interrupted', terminal.get(c.thread)?.status === 'interrupted', terminal.get(c.thread));
  await waitFor('cancelled real host request settles', () => browserReplies.has(capture.id));
  check('Cancellation rejects the pending browser screenshot', Boolean(browserReplies.get(capture.id)?.error));
  check('Late screenshot completion cannot overwrite an existing preview', readFileSync(destination, 'utf8') === 'previous-history-preview');
  check('Interruption revokes MCP while a real host request is pending', await staleCall(c, { action: 'click', tab_id: cTab.id, x: 140, y: 125 }));
  void pendingCapture;

  const resumed = await start('codex', 'codex-resumed', 0, a.thread);
  check('A new turn gets a fresh MCP capability', resumed.endpoint !== a.endpoint);
  await tool(resumed, { action: 'navigate', url: `${base}/resumed` });
  check('A new turn does not revive the prior turn capability', await staleCall(a, { action: 'navigate', url: `${base}/stale` }));
  await finish(resumed);

  const readOnly = await start('claude', 'claude-read-only', 0, undefined, 'read_only');
  const priorOpenRequests = browserRequests.filter(request => request.method === 'browser/open_tab').length;
  await tool(readOnly, { action: 'navigate', url: `${base}/read-only-must-not-open` }, 'wuu_browser', true);
  check('Host permissions reject read-only navigation before opening a page', browserRequests.filter(request => request.method === 'browser/open_tab').length === priorOpenRequests);
  await tool(readOnly, { action: 'tabs' });
  await finish(readOnly);

  const standard = await start('claude', 'claude-standard', 0, undefined, 'standard');
  await tool(standard, { action: 'navigate', url: `${base}/standard` });
  check('Claude standard mode permits only the scoped host MCP capability', Boolean(tab(standard)));
  await finish(standard);

  if (cua) {
    mkdirSync(join(fixture, 'native-target'), { recursive: true });
    const targetReady = deferred<{ processID: number; windowID: number }>();
    const targetReplies = new Map<number, ReturnType<typeof deferred<{ clicks: number }>>>();
    nativeTarget = spawn(process.execPath, ['--no-sandbox', join(desktop, 'out/main/linux-cua-e2e.mjs'), '--linux-cua-fixture'], {
      env: { ...process.env, WUU_LINUX_CUA_PROFILE: join(fixture, 'native-target') }, stdio: ['ignore', 'inherit', 'inherit', 'ipc'],
    });
    nativeTarget.on('message', (message: { ready?: boolean; processID: number; windowID: number; id: number; result: { clicks: number } }) => {
      if (message.ready) targetReady.resolve(message); else targetReplies.get(message.id)?.resolve(message.result);
    });
    const target = await bounded('existing real Linux CUA fixture', targetReady.promise);
    main.focus();
    const linux = await start('claude', 'claude-linux-cua');
    const computer = linux.tools.find(name => /cua_linux.*computer/.test(name));
    assert(computer, `Enabled native CUA tool is exposed: ${linux.tools}`);
    const listed = await tool(linux, { action: 'list_apps', app: `linux:${target.processID}:${target.windowID}` }, computer);
    const canonical = new RegExp(`linux:${target.processID}:${target.windowID}:\\d+`).exec(text(listed))?.[0];
    assert(canonical, text(listed));
    const observed = await tool(linux, { action: 'observe', app: canonical, mode: 'vision' }, computer);
    saveImage(observed, 'claude-linux-cua.png');
    const snapshot = /"snapshot_id"\s*:\s*"([^"]+)"/.exec(text(observed))?.[1];
    assert(snapshot, text(observed));
    await closePreview(true);
    check('Closing native CUA PiP leaves its external engine running', !terminal.has(linux.thread));
    await tool(linux, { action: 'click', app: canonical, snapshot_id: snapshot, coordinate_space: 'screenshot', x: 130, y: 180, foreground_policy: 'allow', after: 'vision' }, computer);
    const id = ++serial;
    const state = deferred<{ clicks: number }>();
    targetReplies.set(id, state);
    nativeTarget.send!({ id, command: 'state' });
    check('Claude MCP call delivers real native X11 input', (await bounded('native fixture state', state.promise)).clicks === 1);
    await finish(linux);
  }
  writeFileSync(join(output, 'results.json'), JSON.stringify({ status: 'passed', scope: 'Scripted Codex and Claude protocol fixtures; production Go MCP/tools/activity and Electron Chromium/PiP. No upstream CLI or paid model invocation.', nativeCUA: cua,
    coreSHA256: createHash('sha256').update(readFileSync(process.env.WUU_DESKTOP_CORE!)).digest('hex'), checks, transcript, events }, null, 2));
  await cleanup();
  console.log(`PASS external engine host capabilities. Evidence ${output}`);
  app.exit(0);
}
async function cleanup() {
  nativeTarget?.kill();
  await coordinator?.shutdown();
  await pool?.shutdown();
  host?.destroyAll();
  control.closeAllConnections();
  control.close();
}
run().catch(async error => {
  writeFileSync(join(output, 'failure.json'), JSON.stringify({ error: String(error), checks, transcript, events }, null, 2));
  console.error(error);
  await cleanup().catch(console.error);
  app.exit(1);
});
