import { mountCalendarConnection } from '../src/connection-view.js';
import { ConnectedCalendarReader } from '../src/connected-calendar.js';
const calendar = { id: 'demo@example.invalid', summary: '个人日历', timeZone: 'Asia/Shanghai', primary: true };
let mode = 'connected';
let connection = { connection_id: 'fixture-connection', user_space_id: 'fixture-space', product_domain: 'personal', provider: 'google-calendar', account_id: 'fixture-sub', state: 'connected', selected_calendars: [calendar.id] };
const reader = new ConnectedCalendarReader({ status: async () => connection, authorize: async () => connection, eventsPage: async () => {
  if (mode === 'error') throw new Error('synthetic outage');
  if (mode === 'revoked-during-read') { connection = { ...connection, state: 'needs_reauth' }; throw new Error('synthetic authorization denied'); }
  if (mode === 'empty') return { items: [] };
  return { timeZone: 'Asia/Shanghai', items: [
    { id: 'day', status: 'confirmed', summary: '阅读日', start: { date: '2026-10-24' }, end: { date: '2026-10-25' } },
    { id: 'meeting', status: 'confirmed', summary: mode === 'long' ? '合成日程：与合作伙伴讨论下个季度产品研究、用户访谈和原型验证安排，确认每一项待办的责任人与预计完成时间' : '产品设计讨论', start: { dateTime: '2026-10-24T10:00:00+08:00' }, end: { dateTime: '2026-10-24T11:00:00+08:00' }, recurringEventId: 'weekly', originalStartTime: { dateTime: '2026-10-24T10:00:00+08:00' } },
    { id: 'cancelled', status: 'cancelled', recurringEventId: 'weekly', originalStartTime: { dateTime: '2026-10-24T15:00:00+08:00' } },
  ] };
} });
const view = mountCalendarConnection(document.getElementById('component')!, {
 status: async () => connection,
 calendars: async () => [calendar],
 select: async (_id, ids) => { connection = { ...connection, selected_calendars: ids }; },
 startAuthorization: async () => { connection = { ...connection, state: 'connected' }; },
 disconnect: async (_id, revoke) => { connection = { ...connection, state: 'disconnected', selected_calendars: [] }; return { local_deleted: true, revocation: revoke ? 'unconfirmed' : 'not_requested' }; },
 agenda: async query => ({ ...await reader.read('fixture-space', query, new AbortController().signal), freshness: 'fresh' }),
}, { now: () => new Date('2026-10-23T12:00:00+08:00'), timezone: 'Asia/Shanghai' });
document.getElementById('scenario')!.addEventListener('change', event => { mode = (event.target as HTMLSelectElement).value; connection = { ...connection, state: ['unconnected', 'needs_reauth'].includes(mode) ? mode : 'connected', selected_calendars: [calendar.id] }; void view.refresh(); });
document.getElementById('theme')!.addEventListener('click', () => document.documentElement.classList.toggle('dark'));
document.getElementById('size')!.addEventListener('click', () => document.documentElement.classList.toggle('large'));
