import type {SessionProgressSnapshotV1} from '@wuu/plugin-sdk';

/** A bot edits one existing card with this replace-all view. It must not append
 * each snapshot to a transcript or claim that a missing item never happened. */
export function botStatusCard(progress: SessionProgressSnapshotV1) {
  if (!progress.available) return {available: false as const, reason: progress.reason};
  const latest = progress.items.findLast(item => item.type === 'assistant');
  return {available: true as const, session_id: progress.session_id,
    request_id: progress.request_id, turn_id: progress.turn_id,
    text: latest?.text ?? '', truncated: progress.truncated};
}

/** A fleet board shares the same observation boundary without receiving tool
 * arguments/results or control authority. Replace the row on every read. */
export function fleetProgress(progress: SessionProgressSnapshotV1) {
  if (!progress.available) return {available: false as const, reason: progress.reason};
  return {available: true as const, session_id: progress.session_id,
    request_id: progress.request_id, turn_id: progress.turn_id,
    activeTools: progress.items.filter(item => item.type === 'tool' && item.status === 'in_progress').map(item => item.name ?? 'tool'),
    completedItems: progress.items.filter(item => item.status === 'completed').length,
    failedItems: progress.items.filter(item => item.status === 'failed').length,
    truncated: progress.truncated};
}
