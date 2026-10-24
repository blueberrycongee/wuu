import {
  RuntimeHostServiceError, type SessionDomainHandlers, type SessionDomainSession,
  type SessionDomainControl as Control, type SessionDomainHistoryRecord as HistoryRecord,
  type SessionDomainDriverBinding as Binding, type SessionDomainDriverCheckpointRecord as Checkpoint,
  type SessionDomainHeldUserWork as Held, type SessionDomainModelInputReceiptRecord as Receipt,
} from '@wuu/plugin-sdk';

export function createTurnState(requireSession: (id: string) => SessionDomainSession, append: (id: string, records: readonly HistoryRecord[]) => [number, number]) {
  const controls = new Map<string, Control>();
  const bindings = new Map<string, Binding>();
  const checkpoints = new Map<string, Checkpoint>();
  const held = new Map<string, Map<string, {entry: Held; state: 'pending' | 'dispatching' | 'settled'}>>();
  const heldReceipts = new Map<string, Set<string>>();
  const receipts = new Map<string, Map<string, Receipt>>();
  const resets = new Map<string, number>();
  const controlError = () => new RuntimeHostServiceError({code: 'control_changed', message: 'session control changed'});
  const validate = (expected: Control) => {
    const current = controls.get(expected.session_id);
    if (!current || current.manager_id !== expected.manager_id || current.revision !== expected.revision || current.state !== 'active') throw controlError();
  };
  const heldRows = (id: string) => {
    requireSession(id);
    let rows = held.get(id); if (!rows) {rows = new Map(); held.set(id, rows);} return rows;
  };
  const validateHeld = (id: string, entry: Held) => {
    if (!entry.ID || !entry.MessageJSON || !entry.RuntimeJSON || !entry.Origin) throw new Error('complete held input is required');
    if (entry.SessionID && entry.SessionID !== id) throw new Error('held input session mismatch');
  };
  const matchesHeld = (a: Held, b: Held) => a.ID === b.ID && a.MessageJSON === b.MessageJSON && a.RuntimeJSON === b.RuntimeJSON && a.Origin === b.Origin;
  const handlers: SessionDomainHandlers = {
    ReadControl: ([id]) => [structuredClone(controls.get(id) ?? {session_id: '', manager_id: '', revision: 0, state: ''}), controls.has(id)],
    ListControls: () => [structuredClone(Object.fromEntries(controls))],
    ChangeControl: ([id, manager, state, revision]) => {
      requireSession(id);
      if (!manager || !['active', 'paused', 'taken_over', 'released'].includes(state)) throw new Error('invalid control');
      const current = controls.get(id);
      if ((current?.revision ?? 0) !== revision || current && current.manager_id !== manager && current.state !== 'released') throw controlError();
      const next = {session_id: id, manager_id: manager, revision: revision + 1, state}; controls.set(id, next); return [structuredClone(next)];
    },
    ValidateControl: ([control]) => {validate(control); return [];},
    AppendControlledHistoryRecord: ([id, record, control]) => {
      if (control) {if (control.session_id !== id) throw controlError(); validate(control);}
      return [append(id, [record])[0]];
    },
    AdmitThreadExecution: ([id, limit]) => {
      requireSession(id);
      if (limit !== 0) throw new RuntimeHostServiceError({code: 'method_not_found', message: 'project capacity is unsupported'});
      return [resets.get(id) ?? 0];
    },
    RequestThreadExecutionResetRecord: ([id]) => {requireSession(id); resets.set(id, (resets.get(id) ?? 0) + 1); return [];},
    ThreadExecutionResetRequested: ([id, generation]) => [((resets.get(id) ?? 0) > generation)],
    LoadDriverBinding: ([id]) => {requireSession(id); return [structuredClone(bindings.get(id) ?? {contract_version: 0, driver_id: '', driver_version: '', profile: '', provider_id: ''}), bindings.has(id)];},
    BindDriver: ([id, binding]) => {
      requireSession(id);
      if (binding.contract_version < 1 || !binding.driver_id || !binding.driver_version || !binding.profile || !binding.provider_id) throw new Error('complete driver identity is required');
      const old = bindings.get(id);
      if (old && JSON.stringify(old) !== JSON.stringify(binding)) throw new Error('driver binding conflict');
      const checkpoint = checkpoints.get(id);
      if (checkpoint && (checkpoint.DriverID !== binding.driver_id || checkpoint.DriverVersion !== binding.driver_version || checkpoint.ContractVersion !== binding.contract_version)) throw new Error('driver checkpoint conflict');
      bindings.set(id, structuredClone(binding)); return [];
    },
    LoadDriverCheckpoint: ([id]) => {requireSession(id); return [structuredClone(checkpoints.get(id) ?? {ContractVersion: 0, DriverID: '', DriverVersion: '', State: null, UpdatedAt: '0001-01-01T00:00:00Z'}), checkpoints.has(id)];},
    SaveDriverCheckpoint: ([id, checkpoint]) => {
      requireSession(id);
      const binding = bindings.get(id);
      if (!checkpoint.DriverID || !checkpoint.DriverVersion || checkpoint.ContractVersion < 1 || checkpoint.State == null) throw new Error('complete driver checkpoint is required');
      if (binding && (checkpoint.DriverID !== binding.driver_id || checkpoint.DriverVersion !== binding.driver_version || checkpoint.ContractVersion !== binding.contract_version)) throw new Error('driver checkpoint conflict');
      checkpoints.set(id, structuredClone(checkpoint)); return [];
    },
    SaveModelInputReceipt: ([id, receipt]) => {
      requireSession(id);
      if (!receipt.OperationID || !receipt.Payload || !receipt.DriverID || !receipt.DriverVersion || receipt.ContractVersion < 1) throw new Error('complete receipt identity is required');
      let rows = receipts.get(id); if (!rows) {rows = new Map(); receipts.set(id, rows);}
      const prior = rows.get(receipt.OperationID);
      if (prior && (prior.Payload !== receipt.Payload || prior.DriverID !== receipt.DriverID || prior.DriverVersion !== receipt.DriverVersion || prior.ContractVersion !== receipt.ContractVersion)) throw new Error('model receipt conflict');
      if (!prior) rows.set(receipt.OperationID, structuredClone(receipt)); return [];
    },
    LoadModelInputReceipts: ([id]) => {requireSession(id); return [structuredClone([...(receipts.get(id)?.values() ?? [])])];},
    AcceptHeldUserWork: ([id, entry]) => {
      validateHeld(id, entry); const rows = heldRows(id); const current = rows.get(entry.ID);
      if (current?.state === 'settled' || heldReceipts.get(id)?.has(entry.ID)) throw new RuntimeHostServiceError({code: 'held_work_settled', message: 'held input settled'});
      if (current?.state === 'dispatching') throw new RuntimeHostServiceError({code: 'held_work_unconfirmed', message: 'held input dispatch is unconfirmed'});
      if (current) return [false];
      rows.set(entry.ID, {entry: structuredClone({...entry, SessionID: id, Position: rows.size, DeliveryUnconfirmed: false}), state: 'pending'}); return [true];
    },
    ReplaceHeldUserWork: ([id, entries]) => {
      requireSession(id); const rows = new Map<string, {entry: Held; state: 'pending'}>();
      for (const entry of entries ?? []) {validateHeld(id, entry); if (rows.has(entry.ID)) throw new Error('duplicate held input'); rows.set(entry.ID, {entry: structuredClone({...entry, SessionID: id, Position: rows.size, DeliveryUnconfirmed: false}), state: 'pending'});}
      held.set(id, rows); return [];
    },
    LoadHeldUserWork: ([id]) => [structuredClone([...heldRows(id).values()].filter(row => row.state !== 'settled').map(row => ({...row.entry, DeliveryUnconfirmed: row.state === 'dispatching'})))],
    HasHeldUserWorkReceipt: ([id, inputID]) => [heldReceipts.get(id)?.has(inputID) ?? false],
    ClaimHeldUserWork: ([id, entry]) => {const row = heldRows(id).get(entry.ID); if (!row || row.state !== 'pending' || !matchesHeld(row.entry, entry)) throw new Error('held input changed'); row.state = 'dispatching'; return [];},
    ReleaseHeldUserWork: ([id, inputID]) => {const row = heldRows(id).get(inputID); if (row?.state === 'dispatching') row.state = 'pending'; return [];},
    DeleteHeldUserWork: ([id, inputID]) => [heldRows(id).delete(inputID)],
    AppendAcceptedUserHistoryRecord: ([id, record, entry]) => {
      const row = heldRows(id).get(entry.ID);
      if (!row || row.state !== 'pending') throw new RuntimeHostServiceError({code: 'held_work_settled', message: 'held input settled'});
      if (record.client_id !== entry.ID || record.role !== 'user' || !matchesHeld(row.entry, entry)) throw new Error('held input changed');
      const {expected_held_message_bytes: fence, ...stored} = record;
      if (fence !== undefined && fence !== entry.MessageJSON) throw new Error('held input changed');
      const seq = append(id, [stored])[0]; row.state = 'settled';
      let settled = heldReceipts.get(id); if (!settled) {settled = new Set(); heldReceipts.set(id, settled);} settled.add(entry.ID); return [seq];
    },
  };
  return {handlers,
    delete(id: string) {controls.delete(id); bindings.delete(id); checkpoints.delete(id); held.delete(id); heldReceipts.delete(id); receipts.delete(id); resets.delete(id);},
    close() {controls.clear(); bindings.clear(); checkpoints.clear(); held.clear(); heldReceipts.clear(); receipts.clear(); resets.clear();},
  };
}
