import assert from 'node:assert/strict';
import { createInterface } from 'node:readline';

const engine = process.argv[2];
const args = process.argv.slice(3);
const permissionMode = args[args.indexOf('--permission-mode') + 1];
const allowedIndex = args.indexOf('--allowedTools');
const allowedTools = allowedIndex < 0 ? [] : args[allowedIndex + 1].split(',');
const send = message => process.stdout.write(`${JSON.stringify(message)}\n`);
const reply = (id, result) => send({ id, result });
const notify = (method, params) => send({ method, params });
let endpoint;
let active;
let subscribed = false;
let sequence = 0;
const nativeThread = `fixture-${engine}-${process.pid}`;

if (args.includes('--version')) {
  console.log(engine === 'claude' ? '2.1.226 (fixture)' : 'codex-cli 0.134.0');
  process.exit(0);
}
if (engine === 'claude') {
  const index = args.indexOf('--mcp-config');
  const servers = index >= 0 ? JSON.parse(args[index + 1]).mcpServers : {};
  endpoint = Object.values(servers).find(server => server.url)?.url;
  send({ type: 'system', subtype: 'init', session_id: nativeThread, claude_code_version: '2.1.226', model: 'claude-sonnet-4' });
}

async function mcp(method, params, state) {
  if (engine === 'claude' && method === 'tools/call' && permissionMode === 'dontAsk' && !allowedTools.includes('mcp__wuu_host__*')) {
    return { status: 200, result: { isError: true, content: [{ type: 'text', text: 'Claude dontAsk denied the MCP tool before host execution.' }] } };
  }
  const id = method.startsWith('notifications/') ? undefined : ++sequence;
  const response = await fetch(endpoint, {
    method: 'POST', signal: state.abort.signal,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
      ...(state.session ? { 'Mcp-Session-Id': state.session } : {}),
      ...(state.protocol ? { 'MCP-Protocol-Version': state.protocol } : {}) },
    body: JSON.stringify({ jsonrpc: '2.0', id, method, params }),
  });
  state.session = response.headers.get('mcp-session-id') ?? state.session;
  const body = await response.text();
  if (!response.ok) return { status: response.status, error: body };
  if (id === undefined) return { status: response.status };
  const message = response.headers.get('content-type')?.includes('text/event-stream')
    ? body.split('\n').filter(line => line.startsWith('data:')).map(line => JSON.parse(line.slice(5))).find(value => value.id === id)
    : JSON.parse(body);
  assert(message, `MCP ${method} returned its JSON-RPC response`);
  return { status: response.status, ...message };
}

async function control(path, body, state) {
  const response = await fetch(`${process.env.WUU_CAPABILITY_CONTROL}/${path}`, {
    method: 'POST', signal: state.abort.signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  });
  assert(response.ok, `Fixture control ${path} returned ${response.status}`);
  return response.status === 204 ? undefined : response.json();
}

function finish(state, error, interrupted = false) {
  if (state.finished) return;
  state.finished = true;
  const text = error ? String(error) : 'Host capability acceptance completed.';
  if (engine === 'codex') {
    notify('item/completed', { threadId: nativeThread, turnId: state.turn,
      item: { id: `message-${state.turn}`, type: 'agentMessage', text } });
    notify('turn/completed', { threadId: nativeThread,
      turn: { id: state.turn, status: interrupted ? 'interrupted' : error ? 'failed' : 'completed', ...(error ? { error: { message: text } } : {}) } });
  } else {
    send({ type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text }] } });
    send({ type: 'result', subtype: error ? 'error_during_execution' : 'success', is_error: Boolean(error),
      session_id: nativeThread, num_turns: 1, result: text, usage: { input_tokens: 10, output_tokens: 5 } });
  }
}

async function run(prompt, state) {
  try {
    const marker = /WUU_CAPABILITY_SCENARIO=([a-z0-9_-]+)/.exec(prompt)?.[1];
    assert(marker, 'The engine receives its acceptance scenario');
    assert(endpoint, `${engine} receives a host MCP endpoint in native configuration`);
    if (engine === 'claude') assert(allowedTools.every(rule => rule === 'mcp__wuu_host__*'), 'The host does not broadly allow native or unrelated MCP tools');
    const initialized = await mcp('initialize', { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: `fixture-${engine}`, version: '1' } }, state);
    assert(initialized.result, JSON.stringify(initialized));
    state.protocol = initialized.result.protocolVersion;
    await mcp('notifications/initialized', {}, state);
    const catalog = await mcp('tools/list', {}, state);
    await control('register', { marker, engine, endpoint, session: state.session, protocol: state.protocol, catalog, permissionMode, allowedTools }, state);
    for (;;) {
      const command = await control('next', { marker }, state);
      if (command.finish) { finish(state); return; }
      const response = await mcp(command.method ?? 'tools/call', command.params, state);
      await control('result', { marker, id: command.id, response }, state);
    }
  } catch (error) {
    if (!state.abort.signal.aborted) finish(state, error);
  }
}

const input = createInterface({ input: process.stdin });
input.on('line', line => {
  const message = JSON.parse(line);
  if (engine === 'codex') {
    if (message.method === 'initialize') reply(message.id, { userAgent: 'codex-cli/0.134.0 (fixture)', platformFamily: 'test', platformOs: process.platform });
    else if (message.method === 'model/list') reply(message.id, { data: [{ id: 'gpt-6-astra', model: 'gpt-6-astra', displayName: 'Fixture', isDefault: true, defaultReasoningEffort: 'high', supportedReasoningEfforts: [{ reasoningEffort: 'high' }] }] });
    else if (message.method === 'config/read') reply(message.id, { config: {} });
    else if (['thread/start', 'thread/resume'].includes(message.method)) {
      if (message.method === 'thread/start' || !subscribed) {
        endpoint = Object.values(message.params.config?.mcp_servers ?? {}).find(server => server.url)?.url;
      }
      subscribed = true;
      reply(message.id, { thread: { id: nativeThread, status: { type: 'idle' } }, model: 'gpt-6-astra', cwd: process.cwd() });
    } else if (message.method === 'thread/unsubscribe') {
      subscribed = false;
      reply(message.id, { status: 'unsubscribed' });
    } else if (message.method === 'turn/start') {
      active = { abort: new AbortController(), turn: `turn-${++sequence}` };
      reply(message.id, { turn: { id: active.turn } });
      notify('turn/started', { threadId: nativeThread, turn: { id: active.turn } });
      void run(message.params.input.filter(item => item.type === 'text').map(item => item.text).join('\n'), active);
    } else if (message.method === 'turn/interrupt') {
      reply(message.id, {});
      if (active) { active.abort.abort(); finish(active, undefined, true); }
    } else if (message.id !== undefined) reply(message.id, {});
  } else if (message.type === 'control_request') {
    send({ type: 'control_response', response: { request_id: message.request_id, subtype: 'success',
      response: message.request.subtype === 'get_settings' ? { applied: { model: 'claude-sonnet-4' } }
        : { models: [{ value: 'claude-sonnet-4', displayName: 'Fixture', description: 'Scripted acceptance engine' }] } } });
  } else if (message.type === 'user') {
    active = { abort: new AbortController() };
    const content = message.message.content;
    void run(typeof content === 'string' ? content : content.filter(item => item.type === 'text').map(item => item.text).join('\n'), active);
  }
});
input.on('close', () => { active?.abort.abort(); process.exit(0); });
