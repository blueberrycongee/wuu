/** Remote display model. All connection/cursor recovery belongs to the host connector. */
export function createTaskModel(connection, sessionId, publish) {
  let state = {connection: 'connecting', tasks: [], waits: [], revision: null, loading: false, error: false, pending: [], uncertain: []};
  let stopped = false, serial = 0;
  const emit = patch => { if (!stopped) { state = {...state, ...patch}; publish(state); } };
  const scoped = record => record.session_id === sessionId;
  async function refresh() {
    const rpc = connection.rpc();
    if (stopped || state.connection === 'disconnected' || !rpc || rpc.isClosed()) return;
    const request = ++serial;
    emit({loading: true});
    try {
      const [tasks, waits] = await Promise.all([
        rpc.call('durable/tasks/list', {session_id: sessionId}),
        rpc.call('durable/waits/list', {session_id: sessionId}),
      ]);
      if (stopped || request !== serial || rpc !== connection.rpc()) return;
      if (tasks.version !== 1 || !Array.isArray(tasks.tasks) || tasks.revision == null || !tasks.tasks.every(scoped)
          || waits.version !== 1 || !Array.isArray(waits.waits) || !waits.waits.every(wait => wait.owner?.session_id === sessionId)) {
        throw new Error('Invalid scoped snapshot');
      }
      // Deliberately omit payloads, free-form errors, questions and unknown fields.
      emit({connection: 'connected', loading: false, error: false, revision: tasks.revision,
        tasks: tasks.tasks.map(t => ({id: t.id, parent_id: t.parent_id, kind: t.kind, status: t.status,
          phase: t.phase, attempt: t.attempt, duration_ms: t.duration_ms, error_code: t.error_code,
          owner: t.owner, created_at: t.created_at, updated_at: t.updated_at, checkpoint_ref: t.checkpoint_ref, turn_ref: t.turn_ref,
          usage_status: ['available', 'unavailable', 'incomplete'].includes(t.usage_status) ? t.usage_status : undefined,
          usage: t.usage ? {input_tokens: t.usage.input_tokens, output_tokens: t.usage.output_tokens} : undefined})),
        waits: waits.waits.map(w => ({id: w.id, status: w.status, kind: w.kind, batch_id: w.batch_id, expires_at: w.expires_at, effect_state: w.effect_state})),
        uncertain: state.uncertain.filter(id => waits.waits.some(w => w.id === id && w.status === 'pending'))});
    } catch {
      if (!stopped && request === serial) emit({loading: false, error: true});
    }
  }
  return {
    getSnapshot: () => state,
    refresh,
    attach() { emit({connection: 'connecting'}); return refresh(); },
    detach() { ++serial; emit({connection: 'disconnected', loading: false}); },
    async decide(id, decision) {
      const wait = state.waits.find(w => w.id === id);
      const rpc = connection.rpc();
      if (stopped || state.connection !== 'connected' || state.loading || state.error || !rpc || rpc.isClosed()
          || wait?.status !== 'pending' || !(wait.kind === 'approval' || (!wait.kind && wait.batch_id)) || state.pending.includes(id) || state.uncertain.includes(id)
          || !['approve', 'deny'].includes(decision)) return;
      emit({pending: [...state.pending, id]});
      try {
        await rpc.call('durable/waits/decide', {session_id: sessionId, wait_id: id, decision});
      } catch {
        // Admission may have succeeded. Never replay a decision after disconnect.
        emit({uncertain: [...state.uncertain, id]});
      } finally {
        emit({pending: state.pending.filter(value => value !== id)});
        await refresh();
      }
    },
    stop() { stopped = true; ++serial; },
  };
}
