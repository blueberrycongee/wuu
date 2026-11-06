import {createTaskModel} from './model.mjs';

/** Mount with the public @wuu/plugin-sdk ConnectDesktopSessionV1 connector.
 * The caller supplies an explicitly selected authenticated remote route.
 * This module has no local execution fallback or durable runtime dependency. */
export async function mountDurableTasks(root, {connectDesktopSession, route, sessionId}) {
  if (route?.kind !== 'remote' || !sessionId) throw new Error('An explicit remote route and session are required');
  const doc = root.ownerDocument;
  let disposed = false, model;
  const connection = await connectDesktopSession({version: 1, route, hooks: {
    onAttach() { void model?.attach(); }, onDetach() { model?.detach(); },
    onNotification() {}, onState() {},
    async onServerRequest() { return {error: {code: 'unsupported', message: 'Task view cannot execute host requests'}}; },
  }});
  root.classList.add('durable-tasks');
  const make = (tag, text, className) => {
    const el = doc.createElement(tag); if (text != null) el.textContent = String(text);
    if (className) el.className = className; return el;
  };
  function button(label, action, disabled = false) {
    const el = make('button', label); el.type = 'button'; el.disabled = disabled;
    el.addEventListener('click', action); return el;
  }
  function render(state) {
    if (disposed) return;
    const focused = doc.activeElement?.dataset.action;
    const expanded = new Set([...root.querySelectorAll('details[open]')].map(el => el.dataset.task));
    const heading = make('header');
    heading.append(make('h1', 'Cloud tasks'), make('span', state.connection === 'connected' ? 'Connected' : state.connection === 'connecting' ? 'Connecting…' : 'Disconnected · last snapshot', 'connection'));
    const refresh = button(state.loading ? 'Refreshing…' : 'Refresh', () => void model.refresh(), state.loading || state.connection === 'disconnected');
    refresh.dataset.action = 'refresh'; heading.append(refresh);
    const nodes = [heading, make('p', `Session ${sessionId}`, 'scope')];
    if (state.error) {
      const error = make('div', 'Could not load cloud state. Decisions are disabled. ', 'load-error');
      error.setAttribute('role', 'alert'); error.append(button('Retry', () => void model.refresh())); nodes.push(error);
    }
    if (state.revision != null) nodes.push(make('p', `Host revision ${state.revision}`, 'scope'));
    const approval = make('section'); approval.setAttribute('aria-label', 'Approvals'); approval.append(make('h2', 'Approvals'));
    if (!state.waits.length) approval.append(make('p', state.loading ? 'Loading approvals…' : 'No approval requests.', 'empty'));
    for (const wait of state.waits) {
      const row = make('article'); row.append(make('h3', wait.id));
      const uncertain = state.uncertain.includes(wait.id);
      row.append(make('p', uncertain ? 'Decision outcome unknown. Waiting for host confirmation.' : wait.status === 'pending' ? 'Waiting for your decision' : wait.status, 'status'));
      if (wait.expires_at) row.append(make('p', `Expires ${wait.expires_at}`, 'scope'));
      if (wait.effect_state === 'unknown') row.append(make('p', 'External effect outcome unknown.', 'status'));
      if (wait.status === 'pending' && (wait.kind === 'approval' || (!wait.kind && wait.batch_id))) {
        row.append(make('p', 'Review this request in an authorized approval surface before deciding. This metadata view cannot show the action details.', 'scope'));
        const actions = make('div', null, 'actions');
        // Metadata alone is insufficient for informed approval. Keep both
        // decisions disabled until a separate authorized review surface exists.
        actions.append(button('Approve', () => {}, true), button('Deny', () => {}, true));
        row.append(actions);
      }
      approval.append(row);
    }
    nodes.push(approval);
    const tasks = make('section'); tasks.setAttribute('aria-label', 'Tasks'); tasks.append(make('h2', 'Tasks'));
    if (!state.tasks.length) tasks.append(make('p', state.loading ? 'Loading tasks…' : 'No tasks in this session.', 'empty'));
    for (const task of state.tasks) {
      const row = make('article'); row.append(make('h3', task.id));
      row.append(make('p', [task.kind, task.status, task.phase].filter(Boolean).join(' · '), 'status'));
      const details = [task.parent_id && `Parent ${task.parent_id}`, task.attempt != null && `Attempt ${task.attempt}`,
        task.duration_ms != null && `${Math.round(task.duration_ms / 1000)}s`, task.error_code && `Error ${task.error_code}`].filter(Boolean);
      if (details.length) row.append(make('p', details.join(' · '), 'scope'));
      if (task.usage_status === 'unavailable') row.append(make('p', 'Host usage unavailable after recovery.', 'scope'));
      else if (task.usage) row.append(make('p', `Host usage${task.usage_status === 'incomplete' ? ' (incomplete)' : ''} · ${task.usage.input_tokens} input / ${task.usage.output_tokens} output tokens`, 'scope'));
      const metadata = [task.owner && `Owner ${task.owner}`, task.updated_at && `Updated ${task.updated_at}`,
        task.checkpoint_ref && `Checkpoint ${task.checkpoint_ref}`, task.turn_ref && `Turn ${task.turn_ref}`].filter(Boolean);
      if (metadata.length) {
        const disclosure = make('details'); disclosure.dataset.task = task.id; disclosure.open = expanded.has(task.id); disclosure.append(make('summary', 'Task details'), make('p', metadata.join(' · '), 'scope')); row.append(disclosure);
      }
      tasks.append(row);
    }
    nodes.push(tasks); root.replaceChildren(...nodes);
    if (focused) [...root.querySelectorAll('button')].find(el => el.dataset.action === focused)?.focus({preventScroll: true});
  }
  model = createTaskModel(connection, sessionId, render);
  render(model.getSnapshot()); connection.start();
  // The connector owns authentication, reconnect and its host cursor. Poll only
  // while this view is mounted; every successful read replaces the full snapshot.
  const timer = setInterval(() => {
    const state = model.getSnapshot();
    if (state.connection === 'connected' && !state.loading) void model.refresh();
  }, 5000);
  return async () => { disposed = true; clearInterval(timer); model.stop(); await connection.stop(); root.replaceChildren(); };
}
