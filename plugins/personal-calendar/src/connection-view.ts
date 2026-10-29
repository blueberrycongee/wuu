import { calendarHostFailure, tomorrowQuery, type CalendarConnection } from './connected-calendar.js';
import type { CalendarQuery, CalendarSnapshot } from './google-calendar.js';

export interface CalendarChoice { id: string; summary: string; timeZone: string; primary?: boolean }
/** Trusted UI-only actions. startAuthorization opens the system browser in the
 * host; no token, authorization URL, or arbitrary host RPC reaches this view. */
export interface ConnectionViewHost {
  status(): Promise<CalendarConnection>;
  calendars(connectionID: string): Promise<CalendarChoice[]>;
  select(connectionID: string, ids: string[]): Promise<void>;
  startAuthorization(signal: AbortSignal): Promise<void>;
  disconnect(connectionID: string, revoke: boolean): Promise<{ local_deleted: boolean; revocation: string }>;
  agenda(query: CalendarQuery): Promise<CalendarSnapshot & { freshness: 'fresh' | 'stale' }>;
}
const labels: Record<string, string> = { unconnected: '尚未连接', connected: '已连接 · 只读', needs_reauth: '需要重新授权', disconnected: '已断开', unavailable: '连接状态暂时不可用' };
/** Reusable actual view, mounted independently of the Coding page. All provider
 * text uses textContent; event titles are never markup or executable prompts. */
export function mountCalendarConnection(root: HTMLElement, host: ConnectionViewHost, options: { now?: () => Date; timezone?: string } = {}) {
  const lifetime = new AbortController();
  let authorization: AbortController | undefined;
  let connection: CalendarConnection, choices: CalendarChoice[] = [], busy = false, generation = 0, notice = '', snapshot: (CalendarSnapshot & { freshness: 'fresh' | 'stale' }) | undefined;
  let snapshotOwner: string | undefined;
  let timezone = options.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone;
  const node = <K extends keyof HTMLElementTagNameMap>(tag: K, text = '', className = '') => { const element = document.createElement(tag); element.textContent = text; element.className = className; return element; };
  const current = (version: number) => version === generation && !lifetime.signal.aborted;
  const binding = (c?: CalendarConnection) => c?.state === 'connected' ? JSON.stringify([c.product_domain, c.provider, c.user_space_id, c.account_id, c.connection_id, c.selection_revision, [...c.selected_calendars].sort()]) : undefined;
  function accept(next: CalendarConnection, version: number) {
    if (!current(version)) return false;
    if (!binding(next) || binding(next) !== binding(connection)) { snapshot = undefined; snapshotOwner = undefined; choices = []; }
    connection = { ...next, selected_calendars: [...next.selected_calendars] };
    return true;
  }
  function button(text: string, action: (version: number) => Promise<void>, primary = false) { const b = node('button', text, primary ? 'gcal-button primary' : 'gcal-button'); b.type = 'button'; b.disabled = busy; b.addEventListener('click', () => void run(action), { signal: lifetime.signal }); return b; }
  async function reload(version: number) {
    const next = await host.status();
    if (!accept(next, version)) return;
    render();
    if (next.state !== 'connected' || !next.connection_id) return;
    const owner = binding(next), fetched = await host.calendars(next.connection_id);
    if (!current(version) || binding(connection) !== owner) return;
    // Calendar listing can outlive a selection/account change, even when the
    // host transport ignores cancellation. Verify ownership before publishing.
    const checked = await host.status();
    if (!accept(checked, version) || binding(checked) !== owner) return;
    choices = fetched;
  }
  async function run(action: (version: number) => Promise<void>, supersede = false) {
    if (lifetime.signal.aborted || busy && !supersede) return;
    const version = ++generation; busy = true; notice = ''; render();
    try { await action(version); }
    catch (error) {
      if (!current(version)) return;
      const owner = binding(connection), authorization = calendarHostFailure(error);
      if (snapshot) snapshot = { ...snapshot, freshness: 'stale' };
      if (authorization) { snapshot = undefined; snapshotOwner = undefined; choices = []; }
      try {
        let next = await host.status();
        if (authorization && binding(next) === owner) next = { ...next, state: authorization.state === 'reconnect_required' ? 'needs_reauth' : 'unavailable', error_code: authorization.code };
        if (!accept(next, version)) return;
      } catch {
        if (!accept({ product_domain: 'personal', provider: 'google-calendar', state: 'unavailable', selected_calendars: [] }, version)) return;
      }
      const code = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
      notice = code === 'authorization_already_completed' ? '授权已经完成，取消未生效。可以断开连接。' : code === 'credential_cleanup_failed' || code === 'cleanup_failed' || error && typeof error === 'object' && 'persistence_failed' in error && error.persistence_failed === true ? '这次操作的本地凭据清理未完成。请重试清理。' : connection.state === 'needs_reauth' ? '' : snapshot ? '暂时无法更新，以下是上次结果，可能已过期。' : '暂时无法完成，请重试。';
    } finally { if (current(version)) { busy = false; render(); } }
  }
  function render() {
    if (lifetime.signal.aborted) return;
    root.replaceChildren(); root.classList.add('gcal-view');
    const title = node('div', '', 'gcal-heading'), icon = node('span', '31', 'gcal-icon'); icon.setAttribute('aria-hidden', 'true');
    const text = node('div'); text.append(node('h2', 'Google Calendar'), node('p', '按需查看你选择的日历，不修改日程。', 'gcal-muted')); title.append(icon, text); root.append(title);
    const row = node('div', '', 'gcal-connection'); const status = node('span', labels[connection?.state] ?? '正在读取连接状态', 'gcal-status'); status.setAttribute('role', 'status'); row.append(status);
    if (connection?.state === 'unavailable') row.append(button('重试', reload));
    else if (connection?.state !== 'connected') row.append(button(connection?.state === 'needs_reauth' ? '重新授权' : '连接 Google Calendar', async version => { authorization = new AbortController(); render(); try { await host.startAuthorization(AbortSignal.any([lifetime.signal, authorization.signal])); if (current(version)) await reload(version); } finally { authorization = undefined; } }, true));
    else row.append(button('刷新状态', reload));
    if (authorization) { const cancel = node('button', '取消授权', 'gcal-button'); cancel.type = 'button'; cancel.addEventListener('click', () => authorization?.abort(), { signal: lifetime.signal }); row.append(cancel); }
    root.append(row);
    if (connection?.error_code === 'oauth_client_not_configured') root.append(node('p', '尚未配置经你批准的 Google OAuth 客户端，当前不能连接账号。', 'gcal-muted'));
    if (connection?.error_code === 'authorization_already_completed') { const p=node('p','授权已经完成，自动取消未生效。可使用下方断开连接。','gcal-notice');p.setAttribute('role','alert');root.append(p); }
    if (['credential_cleanup_failed','cleanup_failed'].includes(connection?.error_code ?? '')) { const p=node('p','这次操作的本地凭据清理未完成。请重试清理。','gcal-notice');p.setAttribute('role','alert');root.append(p); }
    if (connection?.authorization_diagnostics?.length) {
      const history = node('details', '', 'gcal-authorization-history'); history.open = true;
      history.append(node('summary', '之前的授权尝试'));
      for (const diagnostic of connection.authorization_diagnostics) {
        const message = diagnostic.error_code === 'credential_cleanup_failed' || diagnostic.persistence_failed ? '先前一次授权取消时，本地凭据清理失败，尚未确认清理完成。' : diagnostic.error_code === 'authorization_already_completed' ? '先前一次授权在取消前已完成。' : '先前一次授权的自动取消未完成。';
        const item = node('p', message, 'gcal-notice');
        item.setAttribute('role', 'alert'); item.setAttribute('data-calendar-attempt-diagnostic', diagnostic.attempt_id); history.append(item);
      }
      root.append(history);
    }
    if (connection?.state === 'needs_reauth') root.append(node('p', 'Google 授权已失效。重新授权后才能继续读取；之前的日程不会作为最新结果展示。', 'gcal-muted'));
    if (connection?.state === 'connected' && connection.connection_id) {
      const fieldset = node('fieldset'), legend = node('legend', '选择日历'); fieldset.append(legend);
      if (!choices.length) fieldset.append(node('p', '没有可选择的自有日历。', 'gcal-muted'));
      for (const calendar of choices) {
        const label = node('label', '', 'gcal-choice'), input = node('input'); input.type = 'checkbox'; input.checked = connection.selected_calendars.includes(calendar.id); input.disabled = busy;
        const owner = binding(connection), connectionID = connection.connection_id;
        input.addEventListener('change', () => void run(async version => { if (binding(connection) !== owner) return; const selected = new Set(connection.selected_calendars); input.checked ? selected.add(calendar.id) : selected.delete(calendar.id); await host.select(connectionID, [...selected]); if (current(version)) { snapshot = undefined; snapshotOwner = undefined; await reload(version); } }), { signal: lifetime.signal });
        const text = node('span'); text.append(node('span', calendar.summary || calendar.id), node('small', `${calendar.primary ? '主日历 · ' : ''}${calendar.id}`, 'gcal-muted')); label.append(input, text); fieldset.append(label);
      }
      root.append(fieldset);
      const form = node('form', '', 'gcal-agenda-form'); const zoneLabel = node('label', '日程时区', 'gcal-zone'); const zone = node('input'); zone.value = timezone; zone.name = 'timezone'; zone.disabled = busy; zoneLabel.append(zone); form.append(zoneLabel);
      const ask = node('input'); ask.name = 'request'; ask.value = '查看明天日程'; ask.setAttribute('aria-label', '日程请求'); ask.readOnly = true; form.append(ask);
      const submit = node('button', busy ? '正在读取…' : '查看明天', 'gcal-button primary'); submit.disabled = busy || !connection.selected_calendars.length; form.append(submit);
      form.addEventListener('submit', e => { e.preventDefault(); const desiredZone = zone.value; void run(async version => {
        const owner = binding(connection), calendarID = connection.selected_calendars[0];
        const query = tomorrowQuery(calendarID, desiredZone, options.now?.());
        const result = await host.agenda(query);
        if (!current(version)) return;
        const checked = await host.status();
        if (!accept(checked, version) || owner !== binding(checked)) return;
        if (result.source.user_space_id !== checked.user_space_id || result.source.account_id !== checked.account_id || result.source.calendar_id !== calendarID) throw new Error('Calendar result owner changed');
        timezone = desiredZone; snapshot = result; snapshotOwner = owner;
      }); }, { signal: lifetime.signal }); root.append(form);
      if (connection.selected_calendars.length > 1) root.append(node('p', '此预览先查看第一个已选日历；正式工具按指定日历查询。', 'gcal-muted'));
      if (snapshot && snapshotOwner === binding(connection)) {
        const results = node('section', '', 'gcal-results'); results.setAttribute('aria-live', 'polite'); results.append(node('h3', '明天的日程'), node('p', `${snapshot.source.timezone} · ${snapshot.events.length} 项`, 'gcal-muted'));
        if (!snapshot.events.length) results.append(node('p', '这个时间范围内没有日程。'));
        for (const event of snapshot.events) {
          const row = node('div', '', 'gcal-event'); const stamp = (value?: string) => value ? new Intl.DateTimeFormat('zh-CN', { timeZone: snapshot!.source.timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(value)) : '';
          row.append(node('span', event.all_day ? '全天' : `${stamp(event.start?.date_time)}–${stamp(event.end?.date_time)}`, 'gcal-time'), node('span', event.summary || '未命名日程')); results.append(row);
        }
        if (snapshot.cancelled_events.length) results.append(node('p', `${snapshot.cancelled_events.length} 项已取消，未计入日程。`, 'gcal-muted')); root.append(results);
      }
      const details = node('details', '', 'gcal-disconnect'); details.append(node('summary', '断开连接'));
      const label = node('label', '', 'gcal-choice'), revoke = node('input'); revoke.type = 'checkbox'; revoke.disabled = busy; label.append(revoke, node('span', '同时向 Google 撤销授权')); details.append(label, node('p', '撤销可能影响同一 Google Cloud 项目的其他授权。仅断开会停止 Twin 读取并删除本地凭据及日程缓存。', 'gcal-muted'));
      details.append(button('断开并删除本地凭据', async version => { const result = await host.disconnect(connection.connection_id!, revoke.checked); if (!current(version)) return; snapshot = undefined; snapshotOwner = undefined; await reload(version); if (!current(version)) return; notice = !result.local_deleted ? '本地清理未完成，请重试。' : result.revocation === 'unconfirmed' ? '本地凭据已删除；Google 撤销尚未确认，请到 Google 账号权限页检查。' : '已断开，本地凭据及日程缓存已清除。'; })); root.append(details);
    }
    const visibleNotice = notice || (snapshot?.freshness === 'stale' ? '暂时无法更新，以下是上次结果，可能已过期。' : '');
    if (visibleNotice) { const p = node('p', visibleNotice, 'gcal-notice'); p.setAttribute('role', 'alert'); root.append(p); }
  }
  render(); void run(reload);
  return { refresh: () => run(reload, true), destroy: () => { lifetime.abort(); generation++; root.replaceChildren(); } };
}
