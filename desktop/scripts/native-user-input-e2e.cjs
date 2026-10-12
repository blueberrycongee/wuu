// Real renderer/preload/main/core and a local native protocol peer. No account
// or inference. Build both artifacts first; WUU_INPUT_CORE selects the core.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_INPUT_OUTPUT || path.join(desktop, 'out/native-user-input-e2e'));
fs.mkdirSync(output, { recursive: true });
const fixture = fs.mkdtempSync(path.join(output, 'run-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
fs.mkdirSync(home); fs.mkdirSync(project);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.HOME = fixture;
process.env.CODEX_HOME = path.join(fixture, 'native-home');
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_INPUT_CORE || path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const nativeLog = path.join(fixture, 'native.jsonl');
fs.writeFileSync(nativeLog, '');
process.env.WUU_INPUT_NATIVE_LOG = nativeLog;
const nativeConfig = path.join(fixture, 'native-config.json');
fs.writeFileSync(nativeConfig, '{}');
process.env.WUU_INPUT_NATIVE_CONFIG = nativeConfig;
const quote = value => "'" + value.replaceAll("'", "'\\''") + "'";
const fake = path.join(fixture, 'native-fixture');
fs.writeFileSync(fake, `#!/bin/sh\nexec ${quote(process.env.WUU_NODE_EXECUTABLE || 'node')} ${quote(path.join(desktop, '../scripts/fixtures/native-user-input.mjs'))} "$@"\n`, { mode: 0o755 });
process.env.WUU_CODEX_BINARY = fake;
const engines = Object.fromEntries(['claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
engines.codex = { enabled: true, binary_path: fake };
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture', engines,
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic-only', model: 'fixture', models: { fixture: {} } } },
  agent: { permission_mode: 'standard' }, ptc: { enabled: false },
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'User input fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
const results = { status: 'failed', checks: [], fixture, artifacts: {}, limitations: ['Local synthetic native protocol peer; no official engine inference, macOS acceptance, or secret input UI.'] };
const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
for (const [name, file] of Object.entries({ core: process.env.WUU_DESKTOP_CORE, main: path.join(desktop, 'out/main/index.js'), preload: path.join(desktop, 'out/preload/index.cjs'), renderer: path.join(desktop, 'out/renderer/index.html'), native: path.join(desktop, '../scripts/fixtures/native-user-input.mjs') })) results.artifacts[name] = { file, sha256: hash(file) };
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let main, threadID, trustedBeforeReload = [];
app.on('browser-window-created', (_event, win) => { main ||= win; });
const evaluate = (fn, arg) => main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(fn, label, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const value = await fn(); if (value) return value; await delay(25); }
  throw new Error('Timeout: ' + label);
}
const native = () => fs.readFileSync(nativeLog, 'utf8').split('\n').filter(Boolean).map(JSON.parse);
function check(name, fn) { fn(); results.checks.push(name); console.log('PASS', name); }
async function click(selector, index = 0) {
  const rect = await evaluate(async ({ selector, index }) => {
    const node = document.querySelectorAll(selector)[index]; node.scrollIntoView({ block: 'center', behavior: 'instant' });
    // New cards can still move while the conversation's auto-scroll settles.
    // Wait for a stable hit target rather than sending a click to stale pixels.
    let previous = '', stable = 0;
    for (let frame = 0; frame < 120; frame++) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      const r = node.getBoundingClientRect(), current = `${r.x},${r.y},${r.width},${r.height}`;
      stable = current === previous ? stable + 1 : 0; previous = current;
      if (stable >= 6) return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
    }
    throw new Error('Click target did not settle: ' + selector);
  }, { selector, index });
  main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...rect });
  main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...rect });
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
async function type(selector, text) { await click(selector); await until(() => evaluate(selector => document.activeElement?.matches(selector), selector), 'focused ' + selector); await main.webContents.insertText(text); await until(() => evaluate(({ selector, text }) => document.querySelector(selector)?.value === text, { selector, text }), 'typed ' + selector); }
async function capture(name) { fs.writeFileSync(path.join(output, name + '.png'), (await main.webContents.capturePage()).toPNG()); }
const questions = async id => (await evaluate(id => window.wuu.listUserQuestions(id), id || threadID)).questions;
async function begin(scenario, id = threadID) {
  const before = new Set(native().filter(row => row.started).map(row => row.started));
  const { turn } = await evaluate(({ id, scenario }) => window.wuu.startTurn(id, scenario), { id, scenario });
  const started = await until(() => native().find(row => row.started && !before.has(row.started)), 'native turn ' + scenario);
  return { ...started, wuuTurn: turn.id, wuuThread: id };
}
async function response(started) { return until(() => native().find(row => row.response && row.turnID === started.started), 'native response ' + started.scenario); }
async function clear(started) {
  await until(async () => !(await questions(started.wuuThread)).length, 'pending question cleared');
  await until(() => evaluate(turn => window.__inputEvents.some(event => ['turn/completed', 'turn/error'].includes(event.message?.method) && event.message.params?.turn?.id === turn), started.wuuTurn), 'turn terminal event');
  const resumed = await evaluate(id => window.wuu.resumeThread(id), started.wuuThread);
  assert.equal(resumed.pending_user_messages?.length || 0, 0, 'No extra user message is queued');
  assert.equal(resumed.held_user_messages?.length || 0, 0, 'No extra user message is held');
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
async function control(started, action) {
  const port = native().find(row => row.pid === started.pid && row.control)?.control;
  assert(port); await fetch(`http://127.0.0.1:${port}`, { method: 'POST', body: JSON.stringify({ action, turnID: started.started }) });
}
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 180000);
async function run() {
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', project], { env: process.env, input: '{"id":1,"method":"thread/start","params":{"engine":"codex","model":"fixture","effort":"high","speed":"standard","permission_mode":"standard"}}\n', encoding: 'utf8', timeout: 30000 });
  assert.equal(boot.status, 0, boot.stderr);
  threadID = boot.stdout.split('\n').filter(Boolean).map(JSON.parse).find(row => row.id === 1)?.result?.thread?.id;
  assert(threadID, boot.stdout);
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => main, 'main window'); main.setSize(1180, 920);
  await until(() => evaluate(() => document.querySelector('.composer textarea')), 'composer');
  main.webContents.debugger.attach('1.3');
  await main.webContents.debugger.sendCommand('Emulation.setFocusEmulationEnabled', { enabled: true });
  await evaluate(async id => { await window.wuu.resumeThread(id); await window.wuu.renameThread(id, 'Native question journey'); }, threadID);
  await evaluate(() => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await until(() => evaluate(() => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Native question journey'))), 'thread row');
  await evaluate(() => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Native question journey')).querySelector('.thread-row-main').click());
  await until(() => evaluate(() => document.querySelector('.thread-row[aria-current="page"]')?.textContent.includes('Native question journey')), 'selected thread');
  await evaluate(() => {
    window.__inputEvents = []; window.__trustedQuestionEvents = [];
    window.wuu.onServerEvent(event => window.__inputEvents.push(event));
    for (const type of ['click', 'input']) document.addEventListener(type, event => {
      if (event.target.closest('.user-question-card')) window.__trustedQuestionEvents.push({ type, trusted: event.isTrusted, target: event.target.className });
    }, true);
  });

  const blocking = await begin('blocking');
  const q = await until(async () => (await questions())[0], 'blocking question');
  await until(() => evaluate(() => document.querySelectorAll('.user-question-field').length === 2), 'blocking card');
  const inventory = await evaluate(() => window.wuu.listEngines());
  check('new native thread receives the default-mode question feature', () => assert.equal(native().find(row => row.loaded === blocking.threadID).overrides['features.default_mode_request_user_input'], true));
  check('enabled engine advertises user input capability', () => assert(inventory.engines.find(engine => engine.id === 'codex').capabilities.includes('user-input-requests')));
  check('native request retains Wuu thread/turn and native item ownership', () => { assert.equal(q.thread_id, threadID); assert.equal(q.turn_id, blocking.wuuTurn); assert.equal(q.call_id, blocking.request.params.itemId); assert.equal(q.mode, 'ask'); });
  const untouched = await evaluate(() => ({ selected: document.querySelectorAll('.user-question-card [aria-checked="true"]').length, disabled: document.querySelector('.user-question-submit').disabled, prompt: document.querySelector('.user-question-prompt').textContent }));
  check('no answer or approval is selected automatically', () => { assert.equal(untouched.selected, 0); assert(untouched.disabled); assert(untouched.prompt.includes('Which approach')); });
  const invalid = await evaluate(async q => { try { await window.wuu.answerUserQuestion(q.request_id, { answers: [] }); return false; } catch { return true; } }, q);
  check('invalid partial response leaves the question pending', () => assert(invalid));
  await capture('blocking-unanswered');
  await click('.user-question-card [role="radio"]');
  await type('.user-question-custom', 'with careful contrast');
  await click('.user-question-custom', 1); await main.webContents.insertText('Preserve existing work');
  await until(() => evaluate(() => document.querySelectorAll('.user-question-custom')[1]?.value === 'Preserve existing work' && !document.querySelector('.user-question-submit').disabled), 'complete question form');
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await capture('blocking-ready');
  await click('.user-question-submit');
  const answered = await response(blocking); await clear(blocking);
  check('GUI choices and free text answer the original native request', () => assert.deepEqual(answered.response.result, { answers: { 'approval.command_execution': { answers: ['Blue', 'with careful contrast'] }, details: { answers: ['Preserve existing work'] } } }));
  const duplicate = await evaluate(async q => { try { await window.wuu.cancelUserQuestion(q.request_id); return false; } catch { return true; } }, q);
  check('late duplicate reply cannot reuse a settled question', () => assert(duplicate));

  for (const scenario of ['optional', 'optional-custom', 'optional-text-only', 'optional-closed', 'text-only', 'legacy', 'early', 'early-wait-for-answer', 'cancel', 'optional-skip']) {
    const started = await begin(scenario), pending = await until(async () => (await questions())[0], scenario);
    await until(() => evaluate(() => document.querySelector('.user-question-card')), 'rendered ' + scenario);
    if (scenario === 'optional-custom' || scenario === 'optional-text-only') {
      await click('.user-question-offer-custom');
      await until(() => evaluate(() => document.activeElement?.matches('.user-question-offer-input')), 'custom action focuses its input');
      await type('.user-question-offer-input', 'Use the custom approach');
      await until(async () => !(await questions())[0]?.expires_at || (await questions())[0]?.expires_at.startsWith('0001-'), 'offer held');
      await click('.user-question-submit');
    } else if (scenario === 'text-only') { await type('.user-question-custom', 'A free-text answer'); await click('.user-question-submit'); }
    else if (scenario === 'cancel') await click('.user-question-cancel');
    else if (scenario === 'optional-skip') await click('.user-question-skip');
    else {
      if (scenario === 'optional-closed') check('native option-only request has no unrequested free-text action', () => assert.equal(pending.questions[0].allow_custom, undefined));
      if (scenario === 'optional-closed') assert.equal(await evaluate(() => Boolean(document.querySelector('.user-question-offer-custom'))), false);
      await click('.user-question-card [role="radio"]', 1);
      if (scenario.startsWith('early')) {
        await until(() => evaluate(() => document.querySelectorAll('.user-question-card [role="radio"]')[1]?.getAttribute('aria-checked') === 'true'), 'early option selected');
        await click('.user-question-custom', 1); await main.webContents.insertText('Early reply preserved');
        await until(() => evaluate(() => !document.querySelector('.user-question-submit').disabled), 'early form complete');
      }
      if (!scenario.includes('optional')) await click('.user-question-submit');
    }
    const got = await response(started); await clear(started);
    check(scenario + ' returns one native reply without another user turn', () => {
      assert(!got.response.error, JSON.stringify(got.response));
      if (['cancel', 'optional-skip'].includes(scenario)) assert.deepEqual(got.response.result, { answers: {} });
      if (scenario === 'optional-custom' || scenario === 'optional-text-only') assert.deepEqual(got.response.result.answers['approval.command_execution'].answers, ['Use the custom approach']);
      assert.equal(native().filter(row => row.started && row.threadID === started.threadID).at(-1).started, started.started);
      if (scenario === 'legacy') assert.equal(pending.mode, 'ask');
      if (scenario === 'early-wait-for-answer') {
        const records = native();
        assert(records.findIndex(row => row.response && row.turnID === started.started) < records.findIndex(row => row.startResponse === started.started), 'question must be answered before the native start response exists');
      }
    });
  }
  for (const scenario of ['secret', 'stale', 'wrong-thread', 'duplicate']) {
    const started = await begin(scenario), got = await response(started); await clear(started);
    check(scenario + ' fails safely without a question card', () => { assert(got.response.error); if (scenario === 'secret') assert.equal(got.response.error.message, 'secret user input is not supported'); if (scenario === 'stale') assert.equal(got.response.error.code, -32602); });
  }
  const concurrent = await begin('concurrent-primary');
  const primaryQuestion = await until(async () => (await questions())[0], 'primary concurrent question');
  const secondary = await evaluate(() => window.wuu.startThread({ engine: 'codex', model: 'fixture', effort: 'high', speed: 'standard', permission_mode: 'standard' }));
  const parallel = await begin('concurrent-secondary', secondary.thread.id);
  const secondaryQuestion = await until(async () => (await questions(secondary.thread.id))[0], 'secondary concurrent question');
  check('simultaneous requests stay isolated on one native process', () => {
    assert.equal(concurrent.pid, parallel.pid); assert.notEqual(concurrent.started, parallel.started);
    assert.equal(primaryQuestion.thread_id, threadID); assert.equal(secondaryQuestion.thread_id, secondary.thread.id);
    assert.equal(secondaryQuestion.turn_id, parallel.wuuTurn);
  });
  await evaluate(id => window.wuu.cancelUserQuestion(id), secondaryQuestion.request_id);
  const secondaryResponse = await response(parallel); await clear(parallel);
  check('cancelling another thread cannot consume the visible question', () => assert.deepEqual(secondaryResponse.response.result, { answers: {} }));
  assert.equal((await questions())[0].request_id, primaryQuestion.request_id);
  await until(() => evaluate(() => document.querySelector('.user-question-card [role="radio"]')), 'primary remains visible');
  await click('.user-question-card [role="radio"]'); await click('.user-question-submit');
  await response(concurrent); await clear(concurrent);

  const reload = await begin('renderer-reload');
  const beforeReload = await until(async () => (await questions())[0], 'question before reload');
  trustedBeforeReload = await evaluate(() => window.__trustedQuestionEvents);
  main.webContents.reload();
  await until(() => evaluate(() => Boolean(document.querySelector('.user-question-card [role="radio"]'))).catch(() => false), 'pending question restored after renderer reload');
  await evaluate(() => {
    window.__inputEvents = []; window.__trustedQuestionEvents = [];
    window.wuu.onServerEvent(event => window.__inputEvents.push(event));
    for (const type of ['click', 'input']) document.addEventListener(type, event => {
      if (event.target.closest('.user-question-card')) window.__trustedQuestionEvents.push({ type, trusted: event.isTrusted, target: event.target.className });
    }, true);
  });
  check('renderer reload restores the same live request without a default selection', () => assert(beforeReload.request_id));
  assert.equal((await questions())[0].request_id, beforeReload.request_id);
  assert.equal(await evaluate(() => document.querySelectorAll('.user-question-card [aria-checked="true"]').length), 0);
  await click('.user-question-card [role="radio"]'); await click('.user-question-submit');
  await response(reload); await clear(reload);

  for (const scenario of ['interrupt', 'finish-pending', 'finish-disconnect', 'disconnect']) {
    const started = await begin(scenario), pending = await until(async () => (await questions())[0], scenario);
    await until(() => evaluate(() => document.querySelector('.user-question-card')), 'rendered ' + scenario);
    if (scenario === 'interrupt') await evaluate(id => window.wuu.interruptTurn(id), threadID);
    else await control(started, scenario.includes('disconnect') ? scenario : 'finish');
    await clear(started);
    if (scenario === 'finish-disconnect') {
      const terminal = await evaluate(turnID => window.__inputEvents.find(event => event.message?.method === 'turn/completed' && event.message.params?.turn?.id === turnID), started.wuuTurn);
      check('terminal notification wins over subsequent native EOF', () => assert.equal(terminal.message.params.turn.status, 'completed'));
    }
    await until(() => evaluate(() => !document.querySelector('.user-question-card')), 'retired card ' + scenario);
    const late = await evaluate(async requestID => { try { await window.wuu.answerUserQuestion(requestID, { answers: [{ id: 'input.0', selected: ['Blue'] }] }); return false; } catch { return true; } }, pending.request_id);
    check(scenario + ' removes the pending question and rejects late answers', () => assert(late));
  }
  const recovered = await begin('optional-after-disconnect');
  await until(async () => (await questions())[0], 'recovered question');
  await until(() => evaluate(() => document.querySelector('.user-question-card [role="radio"]')), 'recovered card');
  await click('.user-question-card [role="radio"]'); await response(recovered); await clear(recovered);
  check('new process after disconnect can ask and receive input again', () => assert(recovered.pid !== native().find(row => row.scenario === 'disconnect' && row.started).pid));
  for (const disabled of [{ feature: false }, { tool: false }]) {
    fs.writeFileSync(nativeConfig, JSON.stringify(disabled));
    const disabledThread = await evaluate(() => window.wuu.startThread({ engine: 'codex', model: 'fixture', effort: 'high', speed: 'standard', permission_mode: 'standard' }));
    const disabledTurn = await begin('explicit-disabled', disabledThread.thread.id);
    await clear(disabledTurn);
    check('native ' + Object.keys(disabled)[0] + ' opt-out stays disabled on a new thread', () => {
      assert(disabledTurn.gated); assert.equal(disabledTurn.availability[disabled.feature === false ? 'defaultMode' : 'toolEnabled'], false);
      assert(!native().some(row => row.response && row.turnID === disabledTurn.started));
    });
    const load = native().find(row => row.loaded === disabledTurn.threadID);
    if (disabled.feature === false) assert.equal(load.overrides['features.default_mode_request_user_input'], false);
    assert(!Object.keys(load.overrides).some(key => key.startsWith('tools.')), 'No override of native tool opt-out');
  }
  fs.writeFileSync(nativeConfig, '{}');
  check('warm resumes keep the feature enabled at their first native load', () => {
    const warm = native().filter(row => row.warmResume);
    assert(warm.length > 0); assert(warm.every(row => row.retained.defaultMode));
  });
  const heldReload = await begin('optional-held-reload');
  const heldQuestion = await until(async () => (await questions())[0], 'offer before hold and reload');
  await until(() => evaluate(() => document.querySelector('.user-question-offer-custom')), 'offer custom action');
  await click('.user-question-offer-custom');
  await until(async () => !Object.hasOwn((await questions())[0] || {}, 'expires_at'), 'held offer omits expiry');
  trustedBeforeReload.push(...await evaluate(() => window.__trustedQuestionEvents));
  main.webContents.reload();
  await until(() => evaluate(() => Boolean(document.querySelector('.user-question-card [role="radio"]'))).catch(() => false), 'held offer restored');
  await evaluate(() => {
    window.__inputEvents = []; window.__trustedQuestionEvents = [];
    window.wuu.onServerEvent(event => window.__inputEvents.push(event));
    for (const type of ['click', 'input']) document.addEventListener(type, event => {
      if (event.target.closest('.user-question-card')) window.__trustedQuestionEvents.push({ type, trusted: event.isTrusted, target: event.target.className });
    }, true);
  });
  const restoredHeld = (await questions())[0];
  check('held optional offer remains answerable after renderer reload', () => { assert.equal(restoredHeld.request_id, heldQuestion.request_id); assert(!Object.hasOwn(restoredHeld, 'expires_at')); });
  await click('.user-question-card [role="radio"]'); await response(heldReload); await clear(heldReload);
  const expiry = await begin('optional-expiry'); await until(async () => (await questions())[0], 'expiring offer');
  await until(() => evaluate(() => Boolean(document.querySelector('.user-question-card-offer [role="radio"]'))), 'visible expiring offer');
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await capture('optional-unanswered');
  const expired = await response(expiry); await clear(expiry);
  check('unanswered optional request returns an empty map without selecting an option', () => assert.deepEqual(expired.response.result, { answers: {} }));
  const trusted = [...trustedBeforeReload, ...await evaluate(() => window.__trustedQuestionEvents)];
  check('question controls receive native trusted clicks and input', () => { assert(trusted.some(event => event.type === 'click')); assert(trusted.some(event => event.type === 'input')); assert(trusted.every(event => event.trusted)); });
  fs.writeFileSync(path.join(output, 'trusted-interactions.json'), JSON.stringify(trusted, null, 2));
  results.status = 'passed';
  fs.writeFileSync(path.join(output, 'events.json'), JSON.stringify(await evaluate(() => window.__inputEvents), null, 2));
  await capture('completed');
}
run().catch(async error => { results.error = error.stack; console.error(error); if (main) { await capture('failure').catch(() => {}); fs.writeFileSync(path.join(output, 'failure.html'), await evaluate(() => document.body.innerHTML).catch(() => '')); } }).finally(() => {
  clearTimeout(timeout); fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(results, null, 2)); app.exit(results.status === 'passed' ? 0 : 1);
});
