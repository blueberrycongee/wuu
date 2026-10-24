import { RuntimeHostServiceError, type RuntimePlugin, type ServiceDescriptor } from '@wuu/plugin-sdk';

interface HistoryRecord {role: string; content: string; seq?: number; [field: string]: unknown}
interface Session {
  id: string; created_at: string; updated_at: string; title: string; entries: number;
  cwd: string; workspace_id?: string; engine_id?: string; archived_at?: string;
  forked_from_id?: string; forked_from_turn_id?: string; forked_from_item_id?: string;
  provider?: string; model?: string; variant?: string; effort?: string;
}
const methods = ['CreateWithMetadata', 'Find', 'List', 'ListForCWD', 'UpdateTitle',
  'UpdateArchived', 'Delete', 'SetEngine', 'SetWorkspaceID', 'SetRuntimeSelection',
  'WritePluginGenerationSnapshot', 'ReadPluginGenerationSnapshot', 'CreateInitialized',
  'AppendHistoryRecord', 'AppendHistoryRecordReturningSeq', 'AppendHistoryRecords', 'AppendHistoryRecordsReturningRange',
  'LoadHistoryRecords', 'LoadActiveHistoryRecords', 'LoadProviderHistorySnapshot', 'LatestHistoryCheckpoint'];
const descriptor: ServiceDescriptor = {
  name: 'session.domain', version: '1.0.0', methods: methods.map(name => ({
    name, input_schema: `session.domain.${name}.input.v1`, output_schema: `session.domain.${name}.output.v1`,
  })),
};

function checkedHistory(value: unknown): HistoryRecord[] {
  if (!Array.isArray(value) || value.some(record => !record || typeof record.role !== 'string' || typeof record.content !== 'string')) throw new Error('invalid history record batch');
  if (value.some(record => record.expected_held_message_bytes !== undefined)) throw new RuntimeHostServiceError({code: 'method_not_found', message: 'held-message admission is not supported'});
  return structuredClone(value);
}

/** Volatile author probe. Inference, compaction and recovery are deliberately unsupported. */
export function createSessionProvider(owner = 'independent-sessions'): RuntimePlugin {
  const sessions = new Map<string, Session>();
  const generations = new Map<string, unknown>();
  const histories = new Map<string, HistoryRecord[]>();
  let active = false;
  return {
    initialize() { return {protocol_version: 3, provided_services: [descriptor]}; },
    activate() { active = true; },
    invokeService({service, method, params}) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'session provider is not active'});
      if (service !== descriptor.name || !methods.includes(method)) {
        throw new RuntimeHostServiceError({code: 'method_not_found', message: `unsupported session operation: ${method}`});
      }
      const args = (params as {args?: unknown[]} | undefined)?.args;
      if (!Array.isArray(args)) throw new Error('args must be an array');
      const id = args[0];
      let values: unknown[];
      if (method === 'CreateInitialized') {
        const input = args[0] as Partial<Session>;
        if (!input || typeof input.id !== 'string' || !input.id.trim()) throw new Error('id is required');
        if (sessions.has(input.id)) throw new Error('session already exists');
        const records = checkedHistory(args[1] ?? []).map((record, index) => ({...record, seq: index + 1}));
        const now = new Date().toISOString();
        const created = {created_at: now, updated_at: now, title: owner, entries: records.length, cwd: '', ...structuredClone(input)} as Session;
        sessions.set(input.id, created); histories.set(input.id, records); values = [created];
      } else if (method === 'CreateWithMetadata') {
        if (typeof id !== 'string' || !id.trim() || typeof args[1] !== 'string') throw new Error('id and cwd are required');
        if (sessions.has(id)) throw new Error('session already exists');
        const now = new Date().toISOString();
        const created: Session = {id, cwd: args[1], title: owner, entries: 0, created_at: now, updated_at: now};
        sessions.set(id, created); histories.set(id, []); values = [created];
      } else if (method === 'List' || method === 'ListForCWD') {
        const limit = method === 'List' ? args[0] : args[2];
        if (typeof limit !== 'number' || !Number.isInteger(limit) || limit < 0) throw new Error('limit must be a nonnegative integer');
        let rows = [...sessions.values()].filter(row => method === 'List' || (args[1] ? row.workspace_id === args[1] : row.cwd === args[0]));
        rows.sort((a, b) => b.created_at.localeCompare(a.created_at) || a.id.localeCompare(b.id));
        if (limit) rows = rows.slice(0, limit);
        values = [rows];
      } else {
        if (typeof id !== 'string') throw new Error('id is required');
        const current = sessions.get(id);
        if (method === 'Find') return {values: structuredClone([current ?? {}, !!current])};
        if (!current) throw new RuntimeHostServiceError({code: 'session_not_found', message: 'session not found'});
        const history = histories.get(id)!;
        if (method === 'LoadHistoryRecords' || method === 'LoadActiveHistoryRecords') return {values: [structuredClone(history.filter(record => args[1] || record.role !== 'meta'))]};
        if (method === 'LoadProviderHistorySnapshot') return {values: [structuredClone({HeadSeq: history.length, Checkpoint: null, Records: history})]};
        if (method === 'LatestHistoryCheckpoint') return {values: [{}, false]};
        if (method.startsWith('AppendHistory')) {
          const batch = checkedHistory(method === 'AppendHistoryRecords' || method === 'AppendHistoryRecordsReturningRange' ? args[1] : [args[1]]);
          const start = history.length + 1;
          const appended = batch.map((record, index) => ({...record, seq: start + index}));
          histories.set(id, [...history, ...appended]);
          if (method === 'AppendHistoryRecordsReturningRange') return {values: batch.length ? [start, start + batch.length - 1] : [0, 0]};
          return {values: method === 'AppendHistoryRecordReturningSeq' ? [start] : []};
        }
        const next = {...current};
        switch (method) {
          case 'UpdateTitle':
            if (typeof args[1] !== 'string' || !args[1].trim()) throw new Error('title is required');
            next.title = args[1].trim(); break;
          case 'UpdateArchived':
            if (typeof args[1] !== 'boolean') throw new Error('archived must be a boolean');
            if (args[1]) next.archived_at = new Date().toISOString(); else delete next.archived_at;
            break;
          case 'SetEngine': case 'SetWorkspaceID':
            if (typeof args[1] !== 'string') throw new Error('value must be a string');
            if (method === 'SetEngine') next.engine_id = args[1]; else next.workspace_id = args[1];
            break;
          case 'SetRuntimeSelection': {
            const selection = args[1] as Record<string, unknown>;
            if (typeof selection?.Provider !== 'string' || typeof selection.Model !== 'string') throw new Error('provider and model are required');
            next.provider = selection.Provider; next.model = selection.Model;
            if (typeof selection.Variant === 'string') next.variant = selection.Variant;
            if (typeof selection.Effort === 'string') next.effort = selection.Effort;
            break;
          }
          case 'WritePluginGenerationSnapshot': generations.set(id, structuredClone(args[1])); return {values: []};
          case 'ReadPluginGenerationSnapshot': return {values: structuredClone([generations.get(id) ?? {}, generations.has(id)])};
          case 'Delete': sessions.delete(id); generations.delete(id); histories.delete(id); return {values: [structuredClone(current)]};
        }
        next.updated_at = new Date().toISOString(); sessions.set(id, next); values = [next];
      }
      return {values: structuredClone(values)};
    },
    shutdown() { active = false; sessions.clear(); generations.clear(); histories.clear(); },
  };
}

export default createSessionProvider();
