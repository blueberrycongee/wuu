import { createHash } from 'node:crypto';
import { calendarQuery, readGoogleJSON, type CalendarTime, type GoogleAuthorization, type GoogleCalendarReaderOptions } from './google-calendar.js';
export interface EventFields { summary: string; location: string; start: CalendarTime; end: CalendarTime; attendees: Array<{ email: string; optional: boolean }>; send_updates: 'all' | 'externalOnly' | 'none' }
export interface WriteIntent { operation_id: string; kind: 'create' | 'reschedule'; account_id: string; calendar_id: string; task_id: string; requirements_revision: number; fields: EventFields; event_id?: string; original_event_etag?: string }
export interface WriteTarget { user_space_id: string; agent_id: string; session_id: string }
export interface PreparedWrite extends WriteIntent, WriteTarget {
  event_id: string; availability: { checked_at: string; calendar_id: string; conflicting_event_ids: string[]; scope: 'selected_calendar_only' };
  original_event?: { id: string; etag: string; fields: Omit<EventFields, 'send_updates'> }; preview_digest: string; notification_notice: string;
}
export interface WriteProof {
  event: { id: string; etag: string; status: string; fields: Omit<EventFields, 'send_updates'> };
  requested_send_updates: EventFields['send_updates']; notification_delivery: 'unverified';
  source: { provider: 'google-calendar'; user_space_id: string; account_id: string; calendar_id: string; operation_id: string; verified_at: string; verified_by: 'events.get' };
}
export interface CalendarWriter {
  prepare(intent: WriteIntent, target: WriteTarget, signal: AbortSignal): Promise<PreparedWrite>;
  execute(operation: PreparedWrite, beforeMutation: () => Promise<void>, signal: AbortSignal): Promise<WriteProof>;
  reconcile(operation: PreparedWrite, signal: AbortSignal): Promise<WriteProof | null>;
}
export class CalendarWriteError extends Error {
  constructor(readonly code: string, readonly outcome: 'failed' | 'uncertain' = 'failed') { super(code); }
}
export const operationDigest = (value: unknown) => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const fail = (code: string): never => { throw new CalendarWriteError(code); };
function object(value: unknown): Record<string, any> { if (!value || typeof value !== 'object' || Array.isArray(value)) fail('invalid_operation'); return value as Record<string, any>; }
function text(value: unknown, field: string, limit = 512): string { if (typeof value !== 'string' || !value.trim() || value.length > limit || /[\x00-\x1f]/.test(value)) fail(`invalid_${field}`); return value as string; }
function time(raw: unknown): CalendarTime {
  const value = object(raw); const zone = text(value.timezone, 'timezone', 100); let timezone: string;
  try { timezone = new Intl.DateTimeFormat('en', { timeZone: zone }).resolvedOptions().timeZone; } catch { return fail('invalid_timezone'); }
  if (value.date !== undefined && value.date_time === undefined && typeof value.date === 'string' && /^\d{4}-\d\d-\d\d$/.test(value.date) && Number.isFinite(Date.parse(`${value.date}T00:00:00Z`)) && new Date(`${value.date}T00:00:00Z`).toISOString().slice(0, 10) === value.date) return { date: value.date, timezone };
  if (value.date === undefined && typeof value.date_time === 'string') {
    calendarQuery({ calendar_id: 'primary', time_min: value.date_time, time_max: new Date(Date.parse(value.date_time) + 1000).toISOString(), timezone });
    return { date_time: value.date_time, timezone };
  }
  return fail('invalid_event_time');
}
function fields(raw: unknown): EventFields {
  const value = object(raw);
  if (Object.keys(value).some(key => !['summary', 'location', 'start', 'end', 'attendees', 'send_updates'].includes(key))) fail('unsupported_event_field');
  const start = time(value.start), end = time(value.end);
  if (Boolean(start.date) !== Boolean(end.date) || start.timezone !== end.timezone) fail('invalid_event_time');
  const duration = Date.parse(end.date ? `${end.date}T00:00:00Z` : end.date_time!) - Date.parse(start.date ? `${start.date}T00:00:00Z` : start.date_time!);
  if (duration <= 0 || duration > 31 * 86_400_000) fail('invalid_event_time_range');
  if (!Array.isArray(value.attendees) || value.attendees.length > 50) fail('invalid_attendees');
  const attendees = value.attendees.map((entry: unknown) => { const row = object(entry), email = text(row.email, 'attendee_email', 254).toLowerCase(); if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || row.optional !== undefined && typeof row.optional !== 'boolean' || Object.keys(row).some(key => !['email', 'optional'].includes(key))) fail('invalid_attendees'); return { email, optional: row.optional === true }; }).sort((a: { email: string }, b: { email: string }) => a.email.localeCompare(b.email));
  if (new Set(attendees.map((attendee: { email: string }) => attendee.email)).size !== attendees.length) fail('duplicate_attendees');
  if (!['all', 'externalOnly', 'none'].includes(value.send_updates)) fail('explicit_send_updates_required');
  if (value.location !== undefined && (typeof value.location !== 'string' || value.location.length > 2048)) fail('invalid_location');
  return { summary: text(value.summary, 'summary', 2048), location: value.location ?? '', start, end, attendees, send_updates: value.send_updates };
}
export function writeIntent(raw: unknown): WriteIntent {
  const value = object(raw), kind = value.kind;
  if (!['create', 'reschedule'].includes(kind)) fail('unsupported_operation');
  const account_id = text(value.account_id, 'account_id'), calendar_id = text(value.calendar_id, 'calendar_id');
  calendarQuery({ calendar_id, time_min: '2026-01-01T00:00:00Z', time_max: '2026-01-02T00:00:00Z', timezone: 'UTC' });
  const operation_id = text(value.operation_id, 'operation_id', 128); if (!/^[A-Za-z0-9._-]+$/.test(operation_id)) fail('invalid_operation_id');
  if (!Number.isInteger(value.requirements_revision) || value.requirements_revision < 1) fail('invalid_requirements_revision');
  const intent: WriteIntent = { operation_id, kind, account_id, calendar_id, task_id: text(value.task_id, 'task_id'), requirements_revision: value.requirements_revision, fields: fields(value.fields) };
  if (kind === 'reschedule') { intent.event_id = text(value.event_id, 'event_id', 1024); if (/[\/\\:]/.test(intent.event_id) || ['.', '..'].includes(intent.event_id)) fail('invalid_event_id'); intent.original_event_etag = text(value.original_event_etag, 'event_etag', 512); }
  else if (value.event_id !== undefined || value.original_event_etag !== undefined) fail('create_event_identity_is_derived');
  return intent;
}
const googleTime = (value: CalendarTime) => value.date ? { date: value.date } : { dateTime: value.date_time, timeZone: value.timezone };
function eventFields(event: Record<string, any>, expected: EventFields): EventFields {
  if (event.attendeesOmitted === true) return fail('event_attendees_incomplete');
  const convert = (raw: unknown) => { const value = object(raw); return value.date ? { date: value.date, timezone: expected.start.timezone } : { date_time: value.dateTime, timezone: value.timeZone ?? expected.start.timezone }; };
  return fields({ summary: event.summary, location: event.location ?? '', start: convert(event.start), end: convert(event.end), attendees: (event.attendees ?? []).map((item: any) => ({ email: item.email, optional: item.optional === true })), send_updates: expected.send_updates });
}
function equalFields(a: EventFields, b: EventFields, ignoreTime = false): boolean {
  const comparable = (value: EventFields) => ({ summary: value.summary, location: value.location, attendees: value.attendees, ...(ignoreTime ? {} : { start: value.start.date ?? Date.parse(value.start.date_time!), end: value.end.date ?? Date.parse(value.end.date_time!), timezone: value.start.timezone }) });
  return operationDigest(comparable(a)) === operationDigest(comparable(b));
}

/** Authenticated write adapter with no implicit grants or arbitrary endpoints.
 * Every mutation is guarded by the ledger, then independently read back. */
export class GoogleCalendarWriter implements CalendarWriter {
  private fetcher: typeof fetch; private clock: () => Date;
  constructor(private options: GoogleCalendarReaderOptions = {}) { this.fetcher = options.fetch ?? globalThis.fetch; this.clock = options.clock ?? (() => new Date()); }
  private async grant(target: WriteTarget & { account_id: string }, signal: AbortSignal): Promise<GoogleAuthorization> {
    signal.throwIfAborted(); if (!this.options.authorize) return fail('authorization_not_configured');
    let grant: GoogleAuthorization | null;
    try { grant = await this.options.authorize(target.user_space_id, signal); } catch { return fail('authorization_unavailable'); }
    signal.throwIfAborted();
    if (!grant || grant.user_space_id !== target.user_space_id || grant.account_id !== target.account_id) return fail('account_scope_mismatch');
    if (typeof grant.access_token !== 'string' || !grant.access_token || /[\r\n]/.test(grant.access_token) || !Number.isFinite(Date.parse(grant.expires_at)) || Date.parse(grant.expires_at) <= this.clock().getTime() + 30_000) return fail('authorization_expired');
    if (!Array.isArray(grant.scopes) || !grant.scopes.some(scope => ['https://www.googleapis.com/auth/calendar.events', 'https://www.googleapis.com/auth/calendar'].includes(scope))) return fail('write_scope_required');
    return grant;
  }
  private url(calendarID: string, eventID?: string): URL { return new URL(`https://www.googleapis.com/calendar/v3/calendars/${encodeURIComponent(calendarID)}/events${eventID ? `/${encodeURIComponent(eventID)}` : ''}`); }
  private async get(grant: GoogleAuthorization, operation: WriteIntent & { event_id: string }, signal: AbortSignal): Promise<Record<string, any> | null> {
    const response = await this.fetcher(this.url(operation.calendar_id, operation.event_id), { method: 'GET', redirect: 'error', cache: 'no-store', headers: { Authorization: `Bearer ${grant.access_token}`, Accept: 'application/json' }, signal }); signal.throwIfAborted();
    if (response.status === 404 || response.status === 410) { await response.body?.cancel(); return null; }
    if (!response.ok) { await response.body?.cancel(); return fail(response.status === 401 || response.status === 403 ? 'authorization_denied' : 'provider_unavailable'); }
    const value = object(await readGoogleJSON(response)); if (value.id !== operation.event_id) return fail('provider_event_mismatch'); return value;
  }
  private async availability(grant: GoogleAuthorization, intent: WriteIntent, signal: AbortSignal): Promise<PreparedWrite['availability']> {
    const url = this.url(intent.calendar_id), start = intent.fields.start, end = intent.fields.end;
    // The buffer covers every UTC offset for date-only events. It may report
    // adjacent conflicts conservatively; it must never silently miss a day.
    const min = start.date ? new Date(Date.parse(`${start.date}T00:00:00Z`) - 14 * 3600_000).toISOString() : start.date_time!;
    const max = end.date ? new Date(Date.parse(`${end.date}T00:00:00Z`) + 14 * 3600_000).toISOString() : end.date_time!;
    for (const [key, value] of Object.entries({ timeMin: min, timeMax: max, timeZone: start.timezone, singleEvents: 'true', showDeleted: 'false', maxResults: '2500' })) url.searchParams.set(key, value);
    const response = await this.fetcher(url, { method: 'GET', redirect: 'error', cache: 'no-store', headers: { Authorization: `Bearer ${grant.access_token}`, Accept: 'application/json' }, signal }); signal.throwIfAborted();
    if (!response.ok) { await response.body?.cancel(); return fail('availability_unavailable'); }
    const body = object(await readGoogleJSON(response)); if (body.nextPageToken || body.items !== undefined && !Array.isArray(body.items)) return fail('availability_incomplete');
    const ids = (body.items ?? []).filter((event: any) => event.id !== intent.event_id && event.status !== 'cancelled' && event.transparency !== 'transparent').map((event: any) => text(event.id, 'event_id', 1024)).sort();
    return { checked_at: this.clock().toISOString(), calendar_id: intent.calendar_id, conflicting_event_ids: [...new Set<string>(ids)], scope: 'selected_calendar_only' };
  }
  async prepare(intent: WriteIntent, target: WriteTarget, signal: AbortSignal): Promise<PreparedWrite> {
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(15_000)]);
    try {
      const grant = await this.grant({ ...target, account_id: intent.account_id }, bounded);
      const event_id = intent.event_id ?? `mosaic${operationDigest([target.user_space_id, intent.account_id, intent.calendar_id, intent.operation_id])}`;
      let original_event: PreparedWrite['original_event'];
      if (intent.kind === 'reschedule') {
        const event = await this.get(grant, { ...intent, event_id }, bounded);
        if (!event || event.status === 'cancelled' || event.etag !== intent.original_event_etag) return fail('original_event_etag_conflict');
        if (event.recurrence?.length) return fail('recurring_series_reschedule_unsupported');
        const original = eventFields(event, intent.fields);
        if (!equalFields(original, intent.fields, true)) return fail('reschedule_cannot_change_attendees_or_details');
        const { send_updates: _requestedUpdates, ...observed } = original;
        original_event = { id: event_id, etag: text(event.etag, 'event_etag'), fields: observed };
      }
      const availability = await this.availability(grant, { ...intent, event_id }, bounded);
      const prepared = { ...intent, ...target, event_id, availability, ...(original_event ? { original_event } : {}), notification_notice: 'Google Calendar may email attendees even when send_updates is none. The selected notification setting and all recipients must be shown before confirmation.' };
      return { ...prepared, preview_digest: operationDigest(prepared) };
    } catch (error) { if (error instanceof CalendarWriteError) throw error; throw new CalendarWriteError(bounded.aborted ? 'request_cancelled' : 'provider_unavailable'); }
  }
  private proof(event: Record<string, any> | null, operation: PreparedWrite): WriteProof | null {
    if (!event || event.id !== operation.event_id || !['confirmed', 'tentative'].includes(event.status) || !event.etag || event.extendedProperties?.private?.mosaicOperation !== operation.preview_digest) return null;
    const actual = eventFields(event, operation.fields); if (!equalFields(actual, operation.fields)) return null;
    const { send_updates, ...verifiedFields } = actual;
    return { event: { id: event.id, etag: text(event.etag, 'event_etag'), status: event.status, fields: verifiedFields }, requested_send_updates: send_updates, notification_delivery: 'unverified', source: { provider: 'google-calendar', user_space_id: operation.user_space_id, account_id: operation.account_id, calendar_id: operation.calendar_id, operation_id: operation.operation_id, verified_at: this.clock().toISOString(), verified_by: 'events.get' } };
  }
  async execute(operation: PreparedWrite, beforeMutation: () => Promise<void>, signal: AbortSignal): Promise<WriteProof> {
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(20_000)]); let attempted = false;
    try {
      const grant = await this.grant(operation, bounded); const current = await this.get(grant, operation, bounded);
      const existing = this.proof(current, operation); if (existing) return existing;
      if (operation.kind === 'create' && current) return fail('event_id_conflict');
      if (operation.kind === 'reschedule' && (!current || current.etag !== operation.original_event_etag || current.status === 'cancelled')) return fail('original_event_etag_conflict');
      const availability = await this.availability(grant, operation, bounded);
      if (operationDigest(availability.conflicting_event_ids) !== operationDigest(operation.availability.conflicting_event_ids)) return fail('availability_changed');
      const patch = operation.kind === 'reschedule'; const url = this.url(operation.calendar_id, patch ? operation.event_id : undefined); url.searchParams.set('sendUpdates', operation.fields.send_updates);
      const body = { ...(patch ? {} : { id: operation.event_id, summary: operation.fields.summary, location: operation.fields.location, attendees: operation.fields.attendees }), start: googleTime(operation.fields.start), end: googleTime(operation.fields.end), extendedProperties: { private: { ...(patch ? current!.extendedProperties?.private ?? {} : {}), mosaicOperation: operation.preview_digest } } };
      await beforeMutation(); bounded.throwIfAborted(); attempted = true;
      const response = await this.fetcher(url, { method: patch ? 'PATCH' : 'POST', redirect: 'error', cache: 'no-store', headers: { Authorization: `Bearer ${grant.access_token}`, Accept: 'application/json', 'Content-Type': 'application/json', ...(patch ? { 'If-Match': operation.original_event_etag! } : {}) }, body: JSON.stringify(body), signal: bounded });
      bounded.throwIfAborted(); await response.body?.cancel();
      if (response.status === 412) throw new CalendarWriteError('original_event_etag_conflict');
      if (!response.ok && response.status !== 409) throw new CalendarWriteError(response.status === 401 || response.status === 403 ? 'authorization_denied' : 'provider_write_failed', response.status >= 500 || response.status === 408 || response.status === 429 ? 'uncertain' : 'failed');
      let proof: WriteProof | null;
      try { proof = this.proof(await this.get(grant, operation, bounded), operation); }
      catch { throw new CalendarWriteError('write_verification_unavailable', 'uncertain'); }
      if (!proof) throw new CalendarWriteError('write_not_verified', 'uncertain');
      return proof;
    } catch (error) { if (error instanceof CalendarWriteError) throw error; throw new CalendarWriteError(bounded.aborted ? 'write_cancelled' : 'provider_unavailable', attempted ? 'uncertain' : 'failed'); }
  }
  async reconcile(operation: PreparedWrite, signal: AbortSignal): Promise<WriteProof | null> {
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(15_000)]);
    try { const grant = await this.grant(operation, bounded); return this.proof(await this.get(grant, operation, bounded), operation); }
    catch { throw new CalendarWriteError(bounded.aborted ? 'reconciliation_cancelled' : 'reconciliation_unavailable', 'uncertain'); }
  }
}
