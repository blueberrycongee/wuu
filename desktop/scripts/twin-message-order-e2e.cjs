// Full desktop + Go host + loopback provider. Restart the Electron process with
// the same profile while an ID-less provider response is still streaming.
// Prerequisites: npm run build:core && npm run build:renderer, or a running dev
// renderer supplied as WUU_TEST_RENDERER_URL. No live account or user data.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn, execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '../..');
const testReplies = process.argv.includes('--stream-replies');
const output = path.resolve(process.env.WUU_ORDER_OUTPUT || path.join(root, testReplies ? 'desktop/out/twin-stream-reply-e2e' : 'desktop/out/twin-message-order-e2e'));
fs.mkdirSync(output, { recursive: true });
const run = fs.mkdtempSync(path.join(output, 'run-'));
const home = path.join(run, 'home'); fs.mkdirSync(home);
const branch = execFileSync('git', ['branch', '--show-current'], { cwd: root, encoding: 'utf8' }).trim();
const report = { scope: 'Full Wuu desktop, real Personal Go host, synthetic loopback model, isolated data and profile', branch, cases: [], pids: [] };
const user1 = '第一条请求：请回复一段说明。';
const user2 = '第二条请求：这条消息应一直在后续 Twin 回复上方。';
const answer1 = '这条 Twin 回复在第二条用户消息之后出现，仍在生成。';
const answer2 = '第二条回复已收到。';
const user3 = '重启后再次引用这条过程消息。';
const expected = testReplies ? [user1, answer1, user2] : [user1, user2, answer1];
let modelCount = 0, firstResponse, child, connection;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await fn()) return; await delay(50); }
  throw new Error(`Timed out: ${label}`);
}
const chunk = (content, finish = null) => `data: ${JSON.stringify({ id: 'synthetic', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta: content ? { role: 'assistant', content } : {}, finish_reason: finish }] })}\n\n`;
const provider = http.createServer(async (req, res) => {
  try {
    let raw = ''; for await (const part of req) raw += part;
    const body = JSON.parse(raw); fs.appendFileSync(path.join(run, 'model-requests.jsonl'), JSON.stringify(body) + '\n');
    modelCount++;
    if (body.stream) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      if (modelCount === 1) { firstResponse = res; res.write(': awaiting test gate\n\n'); }
      else res.end(chunk(answer2) + chunk('', 'stop') + 'data: [DONE]\n\n');
    } else { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: answer2 }, finish_reason: 'stop' }] })); }
  } catch (error) { res.destroy(error); }
});
async function connect(port) {
  let page;
  await until(async () => {
    try { const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); page = pages.find(p => p.type === 'page' && !p.url.startsWith('devtools:')); return !!page; }
    catch { return false; }
  }, 'desktop debug endpoint');
  const ws = new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
  let serial = 0; const pending = new Map();
  ws.onmessage = event => { const frame = JSON.parse(event.data); const call = pending.get(frame.id); if (call) { pending.delete(frame.id); frame.error ? call.reject(Error(JSON.stringify(frame.error))) : call.resolve(frame.result); } };
  ws.onclose = () => { for (const call of pending.values()) call.reject(Error('Desktop connection closed')); pending.clear(); };
  const call = (method, params = {}) => new Promise((resolve, reject) => { const id = ++serial; pending.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })); });
  return { ws, call, eval: async expression => { const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (result.exceptionDetails) throw Error(JSON.stringify(result.exceptionDetails)); return result.result.value; } };
}
async function launch() {
  const executable = process.env.WUU_TEST_ELECTRON || path.join(root, 'desktop/build/dev-host/Wuu Dev.app/Contents/MacOS/Electron');
  const debugPort = Number(process.env.WUU_ORDER_DEBUG_PORT || 9453);
  const log = fs.openSync(path.join(run, `client-${report.pids.length}.log`), 'a');
  child = spawn(executable, [path.join(root, 'desktop'), `--user-data-dir=${path.join(run, 'profile')}`, `--remote-debugging-port=${debugPort}`], { cwd: root, stdio: ['ignore', log, log], env: { ...process.env, NODE_ENV: 'development', WUU_HOME: home, WUU_SOURCE_ROOT: root, WUU_DESKTOP_CORE: path.join(root, 'desktop/build/bin/wuu-core'), ELECTRON_RENDERER_URL: process.env.WUU_TEST_RENDERER_URL || '' } });
  fs.closeSync(log); report.pids.push(child.pid);
  connection = await connect(debugPort);
  await connection.call('Page.bringToFront');
  await connection.call('Emulation.setEmulatedMedia', {features:[{name:'prefers-reduced-motion',value:'reduce'}]});
  await until(() => connection.eval(`!!document.querySelector('[data-wuu-component="plugin-navigation-item"][aria-label="twin"]')`), 'Twin navigation');
  await connection.eval(`document.querySelector('[data-wuu-component="plugin-navigation-item"][aria-label="twin"]').click()`);
  await until(() => connection.eval(`!!document.querySelector('.mosaic-main [data-wuu-component="composer-input"]')`), 'Twin composer');
  await connection.eval(`window.__orderEvents=[];window.wuu.onServerEvent(event=>{if(event.user_scope?.product_domain==='personal')window.__orderEvents.push(event)});(()=>{const label=document.createElement('div');label.id='order-source';label.textContent=${JSON.stringify(`Wuu 源码：${branch} · 本机合成模型 · 独立验收数据`)};label.style='position:fixed;top:0;left:20%;z-index:999999;background:#eee;color:#222;padding:4px 12px;font:12px system-ui;pointer-events:none';document.body.append(label)})()`);
}
async function close() {
  const process = child;
  const exited = new Promise(resolve => process.once('exit', resolve));
  await connection.call('Browser.close').catch(() => {}); connection.ws.close(); connection = undefined;
  await Promise.race([exited, delay(20000).then(() => { throw Error('Desktop did not exit'); })]);
  assert.equal(process.exitCode, 0); child = undefined;
}
async function send(text, scope = '.mosaic-main') {
  await connection.eval(`(()=>{const node=document.querySelector('${scope} [data-wuu-component="composer-input"]');node.focus();if(node.tagName==='TEXTAREA'){Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(node,${JSON.stringify(text)});node.dispatchEvent(new Event('input',{bubbles:true}))}else document.execCommand('insertText',false,${JSON.stringify(text)})})()`);
  await until(() => connection.eval(`!document.querySelector('${scope} [data-wuu-component="composer-send"]').disabled`), 'send enabled');
  await connection.eval(`document.querySelector('${scope} [data-wuu-component="composer-send"]').click()`);
}
async function rows() { return connection.eval(`[...document.querySelectorAll('.mosaic-main [data-role]')].map(node=>node.innerText)`); }
async function verify(name, expected) {
  await until(async () => { const actual = await rows(); return actual.length === expected.length && expected.every((text, index) => actual[index].includes(text)); }, name + ' messages visible');
  const actual = await rows(); expected.forEach((text, i) => assert(actual[i].includes(text), `${name}: ${JSON.stringify(actual)}`));
  const screenshot = await connection.call('Page.captureScreenshot', { format: 'png' }); fs.writeFileSync(path.join(run, name + '.png'), Buffer.from(screenshot.data, 'base64'));
  report.cases.push({ name, rows: actual, passed: true }); fs.writeFileSync(path.join(run, name + '.json'), JSON.stringify(actual, null, 2));
}
async function openReply(name) {
  const rect = await connection.eval(`(()=>{const row=[...document.querySelectorAll('.mosaic-main [data-role="assistant"]')].find(n=>n.innerText.includes(${JSON.stringify(answer1)}));row.scrollIntoView({block:'center'});const r=row.querySelector('.bubble-message-body').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await connection.call('Input.dispatchMouseEvent', {type:'mouseMoved', ...rect});
  await until(()=>connection.eval(`(()=>{const row=[...document.querySelectorAll('.mosaic-main [data-role="assistant"]')].find(n=>n.innerText.includes(${JSON.stringify(answer1)}));const b=row.querySelector('[data-action="reply-message"]');return b && Number(getComputedStyle(b).opacity)>0})()`),'hover reply visible');
  let button;
  await until(async () => {
    button = await connection.eval(`(()=>{const row=[...document.querySelectorAll('.mosaic-main [data-role="assistant"]')].find(n=>n.innerText.includes(${JSON.stringify(answer1)}));const r=row.querySelector('[data-action="reply-message"]').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await connection.call('Input.dispatchMouseEvent', {type:'mouseMoved', ...button});
    return connection.eval(`!!document.elementFromPoint(${button.x},${button.y})?.closest('[data-action="reply-message"]')`);
  }, 'reply button receives pointer');
  fs.writeFileSync(path.join(run,name+'-hover.png'),Buffer.from((await connection.call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  fs.writeFileSync(path.join(run,name+'-hit.json'),JSON.stringify(button,null,2));
  await connection.call('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...button});
  await connection.call('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...button});
  await until(()=>connection.eval(`document.querySelector('.mosaic-replies')?.innerText.includes(${JSON.stringify(answer1)})`),'reply panel source');
  fs.writeFileSync(path.join(run,name+'.png'),Buffer.from((await connection.call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  report.cases.push({name,passed:true});
}
async function closeReply(){await connection.eval(`document.querySelector('[data-action="close-replies"]').click()`);await until(()=>connection.eval(`!document.querySelector('.mosaic-replies')`),'reply closed');}
(async () => {
  await new Promise(resolve => provider.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${provider.address().port}`, api_key: 'synthetic-unused', model: 'fixture', models: { fixture: { context_window: 131072 } } } }, agent: { max_steps: 2 }, skills: { enabled: false } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 99, language: 'zh-CN', theme: 'light', phone_access_enabled: false }));
  await launch(); await send(user1); await until(() => !!firstResponse, 'model waiting');
  if (testReplies) {
    firstResponse.write(chunk(answer1)); await verify('01-streaming', [user1, answer1]);
    await openReply('01-stream-reply');
    firstResponse.write(chunk(' 后续文本仍在更新。'));
    await until(() => connection.eval(`document.querySelector('.mosaic-replies')?.innerText.includes('后续文本仍在更新。')`), 'quoted source continues streaming');
    await send(user2, '.mosaic-replies');
    await until(() => connection.eval(`!!document.querySelector('.mosaic-replies [data-receipt-state="queued"]')`), 'reply accepted while source is streaming');
    await closeReply();
    await verify('01-reply-queued', expected);
  } else {
    await send(user2);
    await until(() => connection.eval(`!!document.querySelector('.mosaic-main [data-receipt-state="queued"]')`), 'second input durably queued');
    firstResponse.write(chunk(answer1)); await verify('01-streaming', expected);
  }
  const events = await connection.eval('window.__orderEvents'); fs.writeFileSync(path.join(run, 'events-before-exit.json'), JSON.stringify(events, null, 2));
  const started = events.find(e => e.message?.method === 'item/started' && e.message.params.item?.type === 'agent_message');
  assert(started, 'missing real streamed item'); assert(!started.message.params.item.seq && !started.message.params.item.source_id, 'test must exercise no provider identity and no history sequence');
  assert(!events.some(e => e.message?.method === 'turn/completed'), 'reply must still be running at exit');
  await close(); await launch(); await verify('02-restart-during-stream', expected);
  if (testReplies) { await openReply('02-recovered-reply'); await closeReply(); }
  await until(() => connection.eval(`!!document.querySelector('.mosaic-main [data-action="retry-message"]')`), 'held input recovery');
  await connection.eval(`document.querySelector('.mosaic-main [data-action="retry-message"]').click()`);
  await verify('03-history-settled', [...expected, answer2]);
  await close(); await launch(); await verify('04-restart-after-settlement', [...expected, answer2]);
  if (testReplies) {
    await openReply('04-persisted-reply');
    assert((await connection.eval(`document.querySelector('.mosaic-replies').innerText`)).includes(user2));
    await send(user3, '.mosaic-replies');
    await until(() => modelCount >= 3, 'reply after restart reaches model');
    await until(() => connection.eval(`document.querySelectorAll('.mosaic-replies [data-role="assistant"]').length >= 3`), 'reply after restart answered');
    await closeReply();
    expected.push(answer2, user3);
    await verify('04-replied-after-restart', [...expected, answer2]);
    const requests = fs.readFileSync(path.join(run, 'model-requests.jsonl'), 'utf8').trim().split('\n').map(JSON.parse);
    for (const request of requests.slice(1)) {
      const marker = 'Explicit user message reference: ';
      const block = request.messages.findLast(message => typeof message.content === 'string' && message.content.includes(marker))?.content;
      assert(block, 'model must receive explicit quote context');
      const envelope = JSON.parse(block.split(marker).at(-1).split('. These are targets')[0]);
      assert.equal(envelope.quoted_message.source.display_id, `${started.message.params.turn_id}:${started.message.params.item.id}`);
      assert(envelope.quoted_message.content.includes(answer1));
      assert(envelope.quoted_message.content.includes('后续文本仍在更新。'));
    }
    report.modelContextVerified = true;
  }
  await connection.eval(`Promise.all([window.wuu.setThemePreference('dark'),window.wuu.setMessageFlowFontSize(20)])`);
  await close(); await launch();
  assert.equal(await connection.eval(`getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size').trim()`), '20px');
  await connection.call('Emulation.setDeviceMetricsOverride', { width: 680, height: 900, deviceScaleFactor: 1, mobile: false });
  await verify('05-dark-narrow-large', [...expected, answer2]);
  if (testReplies) { await openReply('05-dark-reply'); await closeReply(); }
  report.layout = await connection.eval(`({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,fontSize:getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size').trim(),theme:document.documentElement.dataset.theme,rows:[...document.querySelectorAll('.mosaic-main [data-role]')].map(n=>{const r=n.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom}})})`);
  assert(report.layout.scrollWidth <= report.layout.width); assert(report.layout.rows.every(r => r.left >= 0 && r.right <= report.layout.width));
  report.passed = true;
})().catch(error => { report.passed = false; report.error = error.stack; process.exitCode = 1; console.error(error); }).finally(async () => {
  if (child) { try { await close(); } catch { child?.kill('SIGTERM'); } }
  provider.closeAllConnections(); provider.close();
  fs.writeFileSync(path.join(run, 'report.json'), JSON.stringify(report, null, 2)); console.log(path.join(run, 'report.json'));
});
