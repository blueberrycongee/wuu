/** Read-only Google Calendar v3 adapter. Authorization stays in an injected,
 * user-space-bound credential provider; it is never accepted from tool input. */
export interface CalendarQuery { calendar_id: string; time_min: string; time_max: string; timezone: string; account_id?: string }
export interface CalendarTime { date?: string; date_time?: string; timezone: string }
export interface CalendarEvent {
  id: string; etag?: string; status: 'confirmed' | 'tentative' | 'cancelled'; summary?: string; location?: string;
  all_day?: boolean; start?: CalendarTime; end?: CalendarTime; recurring_event_id?: string;
  original_start_time?: CalendarTime; updated_at?: string;
  attendees?: Array<{ email: string; optional: boolean; response_status?: string }>; attendees_omitted?: boolean;
}
export interface CalendarSource extends CalendarQuery {
  provider: 'google-calendar'; user_space_id: string; account_id: string; fetched_at: string; read_only: true; generation?: string; connection_id?: string; selection_revision?: string;
}
export interface CalendarSnapshot { source: CalendarSource; events: CalendarEvent[]; cancelled_events: CalendarEvent[] }
export interface GoogleAuthorization {
  user_space_id: string; account_id: string; access_token: string; expires_at: string; scopes: readonly string[];
}
export interface CalendarReader {
 read(userSpace: string, query: CalendarQuery, signal: AbortSignal): Promise<CalendarSnapshot>;
 /** Revalidate retained data before any cache/status/change-feed delivery. */
 validateCache?(userSpace: string, sources: readonly CalendarSource[], signal: AbortSignal): Promise<void>;
}
export interface GoogleCalendarReaderOptions {
  /** Returns an existing, authorized account grant. Must not initiate consent. */
  authorize?: (userSpace: string, signal: AbortSignal) => Promise<GoogleAuthorization | null>;
  fetch?: typeof globalThis.fetch;
  clock?: () => Date;
}
export class CalendarReadError extends Error {
  constructor(readonly state: 'unconnected' | 'reconnect_required' | 'error', readonly code: string, readonly account_id?: string) { super(code); }
}
const failure = (code: string, account?: string) => new CalendarReadError('error', code, account);
const record = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw failure('invalid_response');
  return value as Record<string, unknown>;
};
const timestamp = (value: unknown): value is string => typeof value === 'string' && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(value) && Number.isFinite(Date.parse(value));
function timezone(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 100) throw failure('invalid_timezone');
  try { new Intl.DateTimeFormat('en', { timeZone: value }); } catch { throw failure('invalid_timezone'); }
  return value;
}
export function calendarQuery(raw: unknown): CalendarQuery {
  const value = record(raw);
  const id = value.calendar_id;
  if (typeof id !== 'string' || !id || id.length > 512 || /[\x00-\x20/\\:]/.test(id) || id === '.' || id === '..') throw failure('invalid_calendar_id');
  if (!timestamp(value.time_min) || !timestamp(value.time_max)) throw failure('invalid_time_range');
  const min = Date.parse(value.time_min), max = Date.parse(value.time_max);
  if (max <= min || max - min > 31 * 86_400_000) throw failure('invalid_time_range');
  if (value.account_id !== undefined && (typeof value.account_id !== 'string' || !value.account_id || value.account_id.length > 512)) throw failure('invalid_account_id');
  return { calendar_id: id, time_min: new Date(min).toISOString(), time_max: new Date(max).toISOString(), timezone: timezone(value.timezone), ...(value.account_id === undefined ? {} : { account_id: value.account_id as string }) };
}
function text(value: unknown, required = false): string | undefined {
  if (value === undefined && !required) return undefined;
  if (typeof value !== 'string' || value.length > 8192 || required && !value) throw failure('invalid_response');
  return value;
}
function eventTime(raw: unknown, defaultTimezone: string): CalendarTime {
  const value = record(raw), zone = value.timeZone === undefined ? defaultTimezone : timezone(value.timeZone);
  if (typeof value.date === 'string' && /^\d{4}-\d\d-\d\d$/.test(value.date) && new Date(`${value.date}T00:00:00Z`).toISOString().slice(0, 10) === value.date && value.dateTime === undefined) return { date: value.date, timezone: zone };
  if (timestamp(value.dateTime) && value.date === undefined) return { date_time: value.dateTime, timezone: zone };
  throw failure('invalid_response');
}
export function normalizeEvent(raw: unknown, zone: string): CalendarEvent {
  const value = record(raw), id = text(value.id, true)!;
  if (!['confirmed', 'tentative', 'cancelled'].includes(String(value.status))) throw failure('invalid_response');
  const status = value.status as CalendarEvent['status'];
  const event: CalendarEvent = { id, etag: text(value.etag), status, summary: text(value.summary), location: text(value.location), recurring_event_id: text(value.recurringEventId) };
  if (value.originalStartTime !== undefined) event.original_start_time = eventTime(value.originalStartTime, zone);
  if (value.updated !== undefined) { if (!timestamp(value.updated)) throw failure('invalid_response'); event.updated_at = value.updated; }
  // Deleted recurring instances may contain only identity and original start.
  if (status === 'cancelled') return event;
  if (value.attendees !== undefined && !Array.isArray(value.attendees)) throw failure('invalid_response');
  event.attendees = ((value.attendees ?? []) as unknown[]).map(raw => {
    const attendee = record(raw), email = text(attendee.email, true)!;
    if (attendee.responseStatus !== undefined && !['needsAction', 'declined', 'tentative', 'accepted'].includes(String(attendee.responseStatus))) throw failure('invalid_response');
    return { email: email.toLowerCase(), optional: attendee.optional === true, ...(attendee.responseStatus === undefined ? {} : { response_status: String(attendee.responseStatus) }) };
  }).sort((a, b) => a.email.localeCompare(b.email));
  event.attendees_omitted = value.attendeesOmitted === true;
  event.start = eventTime(value.start, zone); event.end = eventTime(value.end, zone); event.all_day = event.start.date !== undefined;
  if (event.all_day !== Boolean(event.end.date)) throw failure('invalid_response');
  if (event.all_day ? event.end.date! <= event.start.date! : Date.parse(event.end.date_time!) <= Date.parse(event.start.date_time!)) throw failure('invalid_response');
  return event;
}
export async function readGoogleJSON(response: Response): Promise<unknown> {
  if (!response.body) throw failure('invalid_response');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read(); if (done) break;
      size += value.byteLength;
      if (size > 2 * 1024 * 1024) { await reader.cancel(); throw failure('response_too_large'); }
      chunks.push(value);
    }
  } finally { reader.releaseLock(); }
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw failure('invalid_response'); }
}

/** Only GET /calendar/v3/calendars/{id}/events can leave this adapter. It follows
 * opaque page tokens, never upstream links; redirects cannot receive the token. */
export class GoogleCalendarReader implements CalendarReader {
  private fetcher: typeof globalThis.fetch;
  private clock: () => Date;
  constructor(private options: GoogleCalendarReaderOptions = {}) { this.fetcher = options.fetch ?? globalThis.fetch; this.clock = options.clock ?? (() => new Date()); }
  async read(userSpace: string, input: CalendarQuery, signal: AbortSignal): Promise<CalendarSnapshot> {
    const query = calendarQuery(input);
    if (!this.options.authorize) throw new CalendarReadError('unconnected', 'authorization_not_configured');
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(15_000)]);
    let account: string | undefined;
    try {
      bounded.throwIfAborted();
      const grant = await this.options.authorize(userSpace, bounded);
      bounded.throwIfAborted();
      if (!grant) throw new CalendarReadError('unconnected', 'account_not_connected');
      if (grant.user_space_id !== userSpace || typeof grant.account_id !== 'string' || !grant.account_id || grant.account_id.length > 512) throw new CalendarReadError('reconnect_required', 'account_scope_mismatch');
      account = grant.account_id;
      if (query.account_id !== undefined && query.account_id !== account) throw new CalendarReadError('reconnect_required', 'account_scope_mismatch', account);
      if (!timestamp(grant.expires_at) || Date.parse(grant.expires_at) <= this.clock().getTime() + 30_000 || typeof grant.access_token !== 'string' || !grant.access_token || /[\r\n]/.test(grant.access_token)) throw new CalendarReadError('reconnect_required', 'authorization_expired', account);
      if (!Array.isArray(grant.scopes) || !grant.scopes.some(scope => ['https://www.googleapis.com/auth/calendar.events.owned.readonly', 'https://www.googleapis.com/auth/calendar.readonly', 'https://www.googleapis.com/auth/calendar.events.readonly', 'https://www.googleapis.com/auth/calendar', 'https://www.googleapis.com/auth/calendar.events'].includes(scope))) throw new CalendarReadError('reconnect_required', 'read_scope_required', account);
      const events = new Map<string, CalendarEvent>(); const seen = new Set<string>(); let pageToken = '';
      for (let page = 0; page < 10; page++) {
        bounded.throwIfAborted();
        const url = new URL(`https://www.googleapis.com/calendar/v3/calendars/${encodeURIComponent(query.calendar_id)}/events`);
        for (const [key, value] of Object.entries({ timeMin: query.time_min, timeMax: query.time_max, timeZone: query.timezone, singleEvents: 'true', showDeleted: 'true', maxResults: '250', orderBy: 'startTime' })) url.searchParams.set(key, value);
        if (pageToken) url.searchParams.set('pageToken', pageToken);
        const response = await this.fetcher(url, { method: 'GET', redirect: 'error', cache: 'no-store', headers: { Authorization: `Bearer ${grant.access_token}`, Accept: 'application/json' }, signal: bounded });
        bounded.throwIfAborted();
        if (response.status === 401 || response.status === 403) { await response.body?.cancel(); throw new CalendarReadError('reconnect_required', 'authorization_denied', account); }
        if (!response.ok) { await response.body?.cancel(); throw failure(response.status === 429 ? 'rate_limited' : 'provider_unavailable', account); }
        const body = record(await readGoogleJSON(response)); bounded.throwIfAborted();
        const zone = body.timeZone === undefined ? query.timezone : timezone(body.timeZone);
        if (body.items !== undefined && !Array.isArray(body.items)) throw failure('invalid_response', account);
        for (const item of (body.items ?? []) as unknown[]) { const event = normalizeEvent(item, zone); events.set(event.id, event); if (events.size > 2000) throw failure('result_limit', account); }
        if (body.nextPageToken === undefined || body.nextPageToken === '') {
          const records = [...events.values()];
          return { source: { provider: 'google-calendar', user_space_id: userSpace, account_id: account, ...query, fetched_at: this.clock().toISOString(), read_only: true }, events: records.filter(event => event.status !== 'cancelled'), cancelled_events: records.filter(event => event.status === 'cancelled') };
        }
        if (typeof body.nextPageToken !== 'string' || body.nextPageToken.length > 4096 || seen.has(body.nextPageToken)) throw failure('invalid_response', account);
        pageToken = body.nextPageToken; seen.add(pageToken);
      }
      throw failure('page_limit', account);
    } catch (error) {
      if (error instanceof CalendarReadError) throw new CalendarReadError(error.state, error.code, error.account_id ?? account);
      // Provider/transport exceptions can contain request headers or tokens.
      throw failure(bounded.aborted ? 'request_cancelled' : 'provider_unavailable', account);
    }
  }
}
