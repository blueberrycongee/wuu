import type * as React from 'react';
import { createPersonalReviewPanel, personalReviewStyles } from './action-review-panel.js';
import { mountCalendarConnection } from '../../personal-calendar/src/connection-view.js';
import { decodeCalendarConnection, unwrapPersonalHost } from '../../personal-calendar/src/host-bridge.js';
import { type CalendarSnapshot, type CalendarEvent, type CalendarTime, calendarQuery } from '../../personal-calendar/src/google-calendar.js';
// @ts-expect-error The desktop entry builder loads scoped CSS as text.
import calendarStyles from '../../personal-calendar/src/connection-view.css';

type Invoke = (method: string, input?: unknown) => Promise<unknown>;
function object(value: unknown): Record<string,unknown> { if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid Calendar response'); return value as Record<string,unknown>; }
function text(value: unknown): string { if (typeof value !== 'string') throw new Error('Invalid Calendar text'); return value; }
function time(value: unknown): CalendarTime | undefined {
  if (value === undefined) return;
  const v=object(value); return { timezone:text(v.timezone), ...(v.date===undefined?{}:{date:text(v.date)}), ...(v.date_time===undefined?{}:{date_time:text(v.date_time)}) };
}
function events(value: unknown): CalendarEvent[] {
  if (!Array.isArray(value) || value.length>2000) throw new Error('Invalid Calendar events');
  return value.map(item => {const v=object(item); if (!['confirmed','tentative','cancelled'].includes(text(v.status))) throw new Error('Invalid event status'); return {id:text(v.id),status:v.status as CalendarEvent['status'],summary:v.summary===undefined?undefined:text(v.summary),start:time(v.start),end:time(v.end),all_day:v.all_day===true};});
}
function snapshot(raw: unknown): CalendarSnapshot & { freshness: 'fresh' | 'stale' } {
  const v=object(raw);
  if (!['ready','error'].includes(text(v.state)) || !['fresh','stale','empty'].includes(text(v.freshness)) || v.state==='error' && v.freshness==='fresh') throw new Error('Invalid Calendar result state');
  if (v.freshness==='empty') throw {code:typeof v.error_code==='string'?v.error_code:'provider_unavailable'};
  const source=object(v.source), query=calendarQuery(source);
  const freshness=v.freshness === 'fresh' ? 'fresh' : 'stale';
  return {freshness,source:{...query,provider:'google-calendar',read_only:true,user_space_id:text(source.user_space_id),account_id:text(source.account_id),fetched_at:text(source.fetched_at),connection_id:text(source.connection_id),selection_revision:text(source.selection_revision)},events:events(v.events),cancelled_events:events(v.cancelled_events)};
}
const pause = (signal: AbortSignal) => new Promise<void>(resolve => {
  const finish=()=>{clearTimeout(timer);signal.removeEventListener('abort',finish);resolve();};
  const timer=setTimeout(finish,500);signal.addEventListener('abort',finish,{once:true});if(signal.aborted)finish();
});

export function createSettingsPanels(react: typeof React, ui: Parameters<typeof createPersonalReviewPanel>[1], invoke: Invoke) {
  const h=react.createElement, ReviewPanel=createPersonalReviewPanel(react,ui);
  const host = async (method:string,input?:unknown) => unwrapPersonalHost(await invoke(method,input));
  const reviewService={list:()=>host('review.list'),resolve:(input:unknown)=>host('review.resolve',input),revoke:(input:unknown)=>host('review.revoke',input)};
  function Connections() {
    const root=react.useRef<HTMLDivElement>(null);
    react.useEffect(()=>{
      if (!root.current) return;
      const mounted=mountCalendarConnection(root.current,{
        status:async()=>decodeCalendarConnection(await host('connections.status')),
        calendars:async connectionID=>{const rows=await host('connections.calendars',{connection_id:connectionID});if(!Array.isArray(rows))throw new Error('Invalid calendar list');return rows.map(row=>{const v=object(row);return {id:text(v.id),summary:text(v.summary),timeZone:text(v.timeZone),primary:v.primary===true};});},
        select:async(connectionID,ids)=>{await host('connections.select',{connection_id:connectionID,calendar_ids:ids});},
        disconnect:async(connectionID,revoke)=>{const v=object(await host('connections.disconnect',{connection_id:connectionID,revoke}));if(typeof v.local_deleted!=='boolean')throw new Error('Invalid disconnect result');return {local_deleted:v.local_deleted,revocation:text(v.revocation)};},
        agenda:async query=>snapshot(await invoke('connections.agenda',query)),
        startAuthorization:async signal=>{
          const started=object(await host('connections.start')), attemptID=text(started.attempt_id);
          for (;;) {
            if(signal.aborted) { await host('connections.cancel',{attempt_id:attemptID}); return; }
            const attempt=object(await host('connections.attempt',{attempt_id:attemptID}));
            if(attempt.state==='connected')return;
            if(!['pending','exchanging'].includes(text(attempt.state)))throw {code:typeof attempt.error_code==='string'?attempt.error_code:'authorization_failed'};
            await pause(signal);
          }
        },
      });
      return ()=>mounted.destroy();
    },[]);
    return h('section',{'data-personal-connections':true},h('h2',null,'连接与工具'),h('div',{ref:root}),h('p',{className:'mosaic-eyebrow'},'Twin 的 MCP 工具尚未接入。'));
  }
  return { Connections, Review:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale}), styles:`${calendarStyles}\n${personalReviewStyles}\n.mosaic-settings>[data-personal-connections],.mosaic-settings>.personal-review{margin-top:var(--space-6);padding-top:var(--space-5);border-top:1px solid var(--hairline);}.mosaic-settings>[data-personal-connections]>h2{font:inherit;font-weight:600;margin:0 0 var(--space-5);}.mosaic-settings .gcal-view h2{font-size:1em;}` };
}
