import {
  RuntimeHostServiceError, sessionDomainProvider, type RuntimePlugin,
  type SessionDomainHandlers, type SessionDomainSession as Session,
  type SessionDomainHistoryRecord as HistoryRecord,
} from '@wuu/plugin-sdk';

import {createJournal} from './journal.js';

function checkedHistory(value: readonly HistoryRecord[]): HistoryRecord[] {
  if (!Array.isArray(value) || value.some(record => !record || typeof record.role !== 'string' || typeof record.content !== 'string')) throw new Error('invalid history record batch');
  if (value.some(record => record.expected_held_message_bytes !== undefined)) throw new RuntimeHostServiceError({code: 'method_not_found', message: 'held-message admission is not supported'});
  return structuredClone(value);
}

/** Volatile author probe. Inference, compaction and recovery are deliberately unsupported. */
export function createSessionProvider(owner = 'independent-sessions'): RuntimePlugin {
  const sessions = new Map<string, Session>();
  const generations = new Map<string, Parameters<NonNullable<SessionDomainHandlers['WritePluginGenerationSnapshot']>>[0][1]>();
  const histories = new Map<string, readonly HistoryRecord[]>();
  let active = false;
  const journal = createJournal();
  const requireSession = (id: string) => {
    const current = sessions.get(id);
    if (!current) throw new RuntimeHostServiceError({code: 'session_not_found', message: 'session not found'});
    return current;
  };
  const create = (input: Partial<Session>, records: readonly HistoryRecord[] = []) => {
    if (!input || typeof input.id !== 'string' || !input.id.trim()) throw new Error('id is required');
    if (sessions.has(input.id)) throw new Error('session already exists');
    const history = checkedHistory(records).map((record, index) => ({...record, seq: index + 1}));
    const now = new Date().toISOString();
    const created: Session = {...structuredClone(input), id: input.id, created_at: input.created_at || now, updated_at: now, title: input.title || owner, entries: history.length};
    sessions.set(input.id, created); histories.set(input.id, history);
    return structuredClone(created);
  };
  const update = (id: string, fields: Partial<Session>) => {
    const next = {...requireSession(id), ...fields, updated_at: new Date().toISOString()};
    sessions.set(id, next); return structuredClone(next);
  };
  const list = (limit: number, matches: (row: Session) => boolean) => {
    if (!Number.isInteger(limit) || limit < 0) throw new Error('limit must be a nonnegative integer');
    const rows = [...sessions.values()].filter(matches).sort((a, b) => b.created_at.localeCompare(a.created_at) || a.id.localeCompare(b.id));
    return structuredClone(limit ? rows.slice(0, limit) : rows);
  };
  const readHistory = (id: string, includeMeta: boolean) => {
    requireSession(id);
    return structuredClone(histories.get(id)!.filter(record => includeMeta || record.role !== 'meta'));
  };
  const append = (id: string, records: readonly HistoryRecord[]): [number, number] => {
    requireSession(id);
    const batch = checkedHistory(records);
    const history = histories.get(id)!;
    const start = history.length + 1;
    histories.set(id, [...history, ...batch.map((record, index) => ({...record, seq: start + index}))]);
    return batch.length ? [start, start + batch.length - 1] : [0, 0];
  };
  const handlers: SessionDomainHandlers = {
    ...journal.handlers,
    CreateWithMetadata: ([id, cwd]) => {
      if (typeof cwd !== 'string') throw new Error('cwd is required');
      return [create({id, cwd})];
    },
    CreateInitialized: ([input, records]) => [create(input, records ?? [])],
    Find: ([id]) => [structuredClone(sessions.get(id) ?? {id: '', created_at: '0001-01-01T00:00:00Z', entries: 0}), sessions.has(id)],
    List: ([limit]) => [list(limit, () => true)],
    ListForCWD: ([cwd, workspaceID, limit]) => [list(limit, row => workspaceID ? row.workspace_id === workspaceID : row.cwd === cwd)],
    UpdateTitle: ([id, title]) => {
      requireSession(id);
      if (typeof title !== 'string' || !title.trim()) throw new Error('title is required');
      return [update(id, {title: title.trim()})];
    },
    UpdateArchived: ([id, archived]) => {
      if (typeof archived !== 'boolean') throw new Error('archived must be a boolean');
      return [update(id, {archived_at: archived ? new Date().toISOString() : undefined})];
    },
    Delete: ([id]) => {
      const current = requireSession(id);
      sessions.delete(id); generations.delete(id); histories.delete(id);
      return [structuredClone(current)];
    },
    SetEngine: ([id, engine]) => [update(id, {engine_id: engine})],
    SetWorkspaceID: ([id, workspaceID]) => [update(id, {workspace_id: workspaceID})],
    SetRuntimeSelection: ([id, selection]) => {
      if (!selection.Provider || !selection.Model) throw new Error('provider and model are required');
      return [update(id, {provider: selection.Provider, model: selection.Model, variant: selection.Variant, effort: selection.Effort,
        speed: selection.Speed, model_options: selection.ModelOptions ?? undefined, permission_mode: selection.PermissionMode, approve_for_me: selection.ApproveForMe})];
    },
    WritePluginGenerationSnapshot: ([id, snapshot]) => {requireSession(id); generations.set(id, structuredClone(snapshot)); return [];},
    ReadPluginGenerationSnapshot: ([id]) => [structuredClone(generations.get(id) ?? {version: 1, plugins: []}), generations.has(id)],
    AppendHistoryRecord: ([id, record]) => {append(id, [record]); return [];},
    AppendHistoryRecordReturningSeq: ([id, record]) => [append(id, [record])[0]],
    AppendHistoryRecords: ([id, records]) => {append(id, records ?? []); return [];},
    AppendHistoryRecordsReturningRange: ([id, records]) => append(id, records ?? []),
    LoadHistoryRecords: ([id, includeMeta]) => [readHistory(id, includeMeta)],
    LoadActiveHistoryRecords: ([id, includeMeta]) => [readHistory(id, includeMeta)],
    LoadProviderHistorySnapshot: ([id]) => {const records = readHistory(id, true); return [{HeadSeq: records.length, Checkpoint: null, Records: records}];},
    LatestHistoryCheckpoint: ([id]) => {requireSession(id); return [{version: 1, session_id: id, through_seq: 0, kind: '', created_at: '0001-01-01T00:00:00Z', replacement: []}, false];},
  };
  const domain = sessionDomainProvider(handlers);
  return {
    initialize() {return {protocol_version: 3, provided_services: [domain.descriptor]};},
    activate() {active = true;},
    invokeService(request) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'session provider is not active'});
      return domain.invoke(request);
    },
    shutdown() {active = false; sessions.clear(); generations.clear(); histories.clear(); journal.close();},
  };
}
export default createSessionProvider();
