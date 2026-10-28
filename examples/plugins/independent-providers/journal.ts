import {
  RuntimeHostServiceError, type SessionDomainHandlers, type SessionDomainContext,
  type SessionDomainInferenceOperationJournalRecord as OperationRecord,
  type SessionDomainInferenceAttemptJournalRecord as AttemptRecord,
  type SessionDomainInferenceSubmissionJournalRecord as SubmissionRecord,
  type SessionDomainInferenceCrashRecovery as Recovery,
} from '@wuu/plugin-sdk';

/** Volatile journal. Closed unfinished work is blocked, never automatically replayed. */
export function createJournal() {
  let sequence = 0;
  const resources = new Map<string, {caller: string; scope: string; closed: boolean}>();
  const operations = new Map<string, {handle: string; owner: string; record: OperationRecord; outcome?: string}>();
  const attempts = new Map<string, {record: AttemptRecord; outcome?: string; firstEvent?: string}>();
  const submissions = new Map<string, SubmissionRecord>();
  const workflows = new Map<string, {handle: string; owner: string; outcome?: string; completedAt?: string}>();
  const resource = (handle: string, context: SessionDomainContext, allowClosed = false) => {
    const current = resources.get(handle);
    if (!current || current.caller !== context.caller || (!allowClosed && current.closed)) throw new RuntimeHostServiceError({code: 'resource_unavailable', message: 'journal handle is absent, closed or belongs to another caller'});
    return current;
  };
  const operation = (handle: string, owner: string, id: string, context: SessionDomainContext) => {
    resource(handle, context);
    const current = operations.get(id);
    if (!current || current.handle !== handle || current.owner !== owner) throw new Error('operation is absent or belongs to another owner');
    return current;
  };
  const attempt = (operationID: string, attemptID: string) => {
    const current = attempts.get(attemptID);
    if (!current || current.record.OperationID !== operationID) throw new Error('attempt is absent or belongs to another operation');
    return current;
  };
  const prepareAttempt = (handle: string, owner: string, record: AttemptRecord, context: SessionDomainContext) => {
    const op = operation(handle, owner, record.OperationID, context);
    if (op.outcome) throw new Error('operation is already terminal');
    if (!record.AttemptID || record.WorkflowID !== op.record.Workflow.ID) throw new Error('invalid attempt identity');
    const old = attempts.get(record.AttemptID);
    if (old && JSON.stringify(old.record) !== JSON.stringify(record)) throw new Error('attempt conflict');
    if (!old) attempts.set(record.AttemptID, {record: structuredClone(record)});
  };
  const handlers: SessionDomainHandlers = {
    NewInferenceJournalRuntime: ([scope], context) => {
      const handle = `journal-${++sequence}`;
      resources.set(handle, {caller: context.caller, scope, closed: false});
      return [{handle, runtime_id: handle}];
    },
    'Journal.Close': ([handle], context) => {resource(handle, context, true).closed = true; return [];},
    'Journal.PrepareOperation': ([handle, owner, record], context) => {
      resource(handle, context);
      if (!record.Operation.ID || !record.Workflow.ID || record.Operation.WorkflowID !== record.Workflow.ID) throw new Error('invalid operation identity');
      const old = operations.get(record.Operation.ID);
      if (old && (old.handle !== handle || old.owner !== owner || JSON.stringify(old.record) !== JSON.stringify(record))) throw new Error('operation conflict');
      const workflow = workflows.get(record.Workflow.ID);
      if (workflow && (workflow.handle !== handle || workflow.owner !== owner || workflow.outcome)) throw new Error('workflow conflict');
      if (!old) operations.set(record.Operation.ID, {handle, owner, record: structuredClone(record)});
      if (!workflow) workflows.set(record.Workflow.ID, {handle, owner});
      return [];
    },
    'Journal.PrepareAttempt': ([handle, owner, record], context) => {prepareAttempt(handle, owner, record, context); return [];},
    'Journal.PrepareRecoveryAttempt': ([handle, owner, record], context) => {
      operation(handle, owner, record.Recovery.OperationID, context);
      attempt(record.Recovery.OperationID, record.Recovery.AttemptID);
      if (record.NextAttempt.OperationID !== record.Recovery.OperationID) throw new Error('recovery operation mismatch');
      prepareAttempt(handle, owner, record.NextAttempt, context); return [];
    },
    'Journal.UpsertSubmission': ([handle, owner, record], context) => {
      operation(handle, owner, record.OperationID, context); attempt(record.OperationID, record.AttemptID);
      const old = submissions.get(record.ID);
      if (!record.ID || old && (old.OperationID !== record.OperationID || old.AttemptID !== record.AttemptID)) throw new Error('submission conflict');
      submissions.set(record.ID, structuredClone(record)); return [];
    },
    'Journal.MarkAttemptFirstEvent': ([handle, owner, operationID, attemptID, submissionID, at], context) => {
      operation(handle, owner, operationID, context);
      const current = attempt(operationID, attemptID);
      const submission = submissions.get(submissionID);
      if (!submission || submission.AttemptID !== attemptID) throw new Error('submission is absent');
      current.firstEvent ??= at; return [];
    },
    'Journal.CompleteAttempt': ([handle, owner, record], context) => {
      operation(handle, owner, record.OperationID, context);
      const current = attempt(record.OperationID, record.AttemptID);
      if (current.outcome && current.outcome !== record.Outcome) throw new Error('attempt terminal conflict');
      current.outcome = record.Outcome; return [];
    },
    'Journal.CompleteOperation': ([handle, owner, record], context) => {
      const current = operation(handle, owner, record.OperationID, context);
      if (current.outcome && current.outcome !== record.Outcome) throw new Error('operation terminal conflict');
      current.outcome = record.Outcome; return [];
    },
    'Journal.CompleteWorkflow': ([handle, owner, record], context) => {
      resource(handle, context);
      const current = workflows.get(record.WorkflowID);
      if (!current || current.handle !== handle || current.owner !== owner) throw new Error('workflow is absent or belongs to another owner');
      if (current.outcome && current.outcome !== record.Outcome) throw new Error('workflow terminal conflict');
      current.outcome = record.Outcome; current.completedAt = record.At; return [];
    },
    'Journal.ReconcileOrphans': ([handle], context) => {
      const current = resource(handle, context);
      const results: Recovery[] = [];
      for (const [id, op] of operations) {
        const owner = resources.get(op.handle)!;
        if (owner.scope !== current.scope || owner.caller !== current.caller || !owner.closed || op.outcome) continue;
        op.outcome = 'blocked';
        results.push({OperationID: id, OwnerID: op.owner, Kind: op.record.Operation.Kind, Profile: op.record.Operation.WorkloadProfile,
          AttemptID: '', PriorPhase: '', PriorOutcome: '', PriorRecovery: '', Outcome: 'blocked', Action: 'block_ambiguous'});
      }
      return [results];
    },
    'Journal.Prune': ([handle, now], context) => {
      const current = resource(handle, context);
      const cutoff = Date.parse(now) - 86400000;
      if (!Number.isFinite(cutoff)) throw new Error('invalid prune time');
      let count = 0;
      for (const [id, workflow] of workflows) {
        const owner = resources.get(workflow.handle)!;
        if (owner.scope !== current.scope || owner.caller !== current.caller) continue;
        if (!workflow.outcome || !workflow.completedAt || Date.parse(workflow.completedAt) >= cutoff) continue;
        for (const [operationID, op] of operations) if (op.record.Workflow.ID === id) {
          operations.delete(operationID);
          for (const [attemptID, value] of attempts) if (value.record.OperationID === operationID) attempts.delete(attemptID);
          for (const [submissionID, value] of submissions) if (value.OperationID === operationID) submissions.delete(submissionID);
        }
        workflows.delete(id); count++;
      }
      return [count];
    },
  };
  return {handlers, close() {resources.clear(); operations.clear(); attempts.clear(); submissions.clear(); workflows.clear();}};
}
