import { createHash } from 'node:crypto';
import type { CalendarEvent, CalendarQuery, CalendarSnapshot, CalendarSource } from './google-calendar.js';
export interface CalendarChange {
  event_id: string; calendar_event_id: string; kind: 'created' | 'updated' | 'cancelled' | 'removed';
  before?: CalendarEvent; after?: CalendarEvent; occurred_at: string; source: CalendarSource;
}
export interface CalendarFeed {
  generation: string; sequence: number; account_id: string; scope: string;
  changes: Array<{ sequence: number; change: CalendarChange }>;
}
const hash = (value: unknown) => createHash('sha256').update(JSON.stringify(value)).digest('hex');
export const calendarWindowKey = (query: CalendarQuery) => hash([query.calendar_id, query.time_min, query.time_max, query.timezone]);
function meaningful(event: CalendarEvent, includeAttendees = true) {
  const time = (value: CalendarEvent['start']) => value?.date ? { date: value.date, timezone: value.timezone } : value ? { instant: Date.parse(value.date_time!), timezone: value.timezone } : undefined;
  return { id: event.id, summary: event.summary ?? '', location: event.location ?? '', status: event.status, start: time(event.start), end: time(event.end), recurring_event_id: event.recurring_event_id, original_start_time: time(event.original_start_time), ...(includeAttendees ? { attendees: event.attendees ?? [] } : {}) };
}
/** A full, successful, identical-window snapshot is the only way to advance.
 * The initial snapshot creates a baseline, never historical notifications. */
export function advanceCalendarFeed(previous: CalendarFeed | undefined, before: CalendarSnapshot | undefined, snapshot: CalendarSnapshot, epoch: string): { feed: CalendarFeed; snapshot: CalendarSnapshot } {
  const same = previous && before && previous.account_id === snapshot.source.account_id && previous.scope === calendarWindowKey(snapshot.source) && before.source.generation === previous.generation && epoch === previous.generation;
  // Connection generation is shared by independent windows. The cursor and
  // delivery identity bind the window separately, so Today cannot reset a feed.
  const generation = epoch;
  const source = { ...snapshot.source, generation };
  const current: CalendarSnapshot = { ...snapshot, source };
  const feed: CalendarFeed = same ? { ...previous, changes: [...previous.changes] } : { generation, sequence: 0, account_id: source.account_id, scope: calendarWindowKey(source), changes: [] };
  if (!same) return { feed, snapshot: current };
  const old = new Map(before!.events.map(event => [event.id, event])), next = new Map(current.events.map(event => [event.id, event])), tombstones = new Map(current.cancelled_events.map(event => [event.id, event]));
  for (const id of [...new Set([...old.keys(), ...next.keys()])].sort()) {
    const from = old.get(id), to = next.get(id); let kind: CalendarChange['kind'];
    if (!from) kind = 'created';
    else if (!to) kind = tombstones.has(id) ? 'cancelled' : 'removed';
    else { const includeAttendees = !from.attendees_omitted && !to.attendees_omitted; if (hash(meaningful(from, includeAttendees)) === hash(meaningful(to, includeAttendees))) continue; kind = 'updated'; }
    feed.sequence++;
    const change: CalendarChange = { event_id: `calendar-change-${hash([feed.generation, feed.scope, feed.sequence, id, kind])}`, calendar_event_id: id, kind, ...(from ? { before: from } : {}), ...(to || tombstones.has(id) ? { after: to ?? tombstones.get(id)! } : {}), occurred_at: source.fetched_at, source };
    feed.changes.push({ sequence: feed.sequence, change });
  }
  // Both count and bytes are bounded. Expired cursors are reported explicitly.
  while (feed.changes.length > 128 || Buffer.byteLength(JSON.stringify(feed.changes)) > 256 * 1024) feed.changes.shift();
  return { feed, snapshot: current };
}
interface Cursor { v: 1; generation: string; sequence: number; account_id: string; scope: string; user_space: string }
function encode(feed: CalendarFeed, source: CalendarSource): string { return Buffer.from(JSON.stringify({ v: 1, generation: feed.generation, sequence: feed.sequence, account_id: feed.account_id, scope: feed.scope, user_space: hash(source.user_space_id) })).toString('base64url'); }
export function calendarChanges(feed: CalendarFeed | undefined, snapshot: CalendarSnapshot | undefined, query: CalendarQuery, input: Record<string, unknown>, state: string, fresh: boolean) {
  const blocked = (reason: string, reset_required = false) => ({ state, available: false, reason, reset_required, baseline: false, changes: [] as CalendarChange[], ...(snapshot ? { source: snapshot.source } : {}) });
  if (snapshot && input.account_id !== undefined && input.account_id !== snapshot.source.account_id) return blocked('account_scope_mismatch', true);
  if (!feed || !snapshot || !fresh || feed.scope !== calendarWindowKey(query)) return blocked(state === 'unconnected' || state === 'reconnect_required' ? state : 'refresh_required');
  if (input.generation !== undefined && input.generation !== feed.generation) return blocked('source_generation_changed', true);
  const cursor = encode(feed, snapshot.source);
  if (input.cursor === undefined || input.cursor === '') return { state, available: true, reset_required: false, baseline: true, cursor, source: snapshot.source, changes: [] as CalendarChange[] };
  let previous: Cursor;
  try {
    if (typeof input.cursor !== 'string' || input.cursor.length > 4096) throw new Error();
    previous = JSON.parse(Buffer.from(input.cursor, 'base64url').toString('utf8')) as Cursor;
    if (previous.v !== 1 || !Number.isSafeInteger(previous.sequence) || previous.sequence < 0) throw new Error();
  } catch { return blocked('invalid_cursor', true); }
  if (previous.generation !== feed.generation || previous.account_id !== feed.account_id || previous.scope !== feed.scope || previous.user_space !== hash(snapshot.source.user_space_id)) return blocked('source_generation_changed', true);
  const earliest = feed.changes[0]?.sequence ?? feed.sequence + 1;
  if (previous.sequence < earliest - 1 || previous.sequence > feed.sequence) return blocked('cursor_expired', true);
  return { state, available: true, reset_required: false, baseline: false, cursor, source: snapshot.source, changes: feed.changes.filter(entry => entry.sequence > previous.sequence).map(entry => entry.change) };
}
