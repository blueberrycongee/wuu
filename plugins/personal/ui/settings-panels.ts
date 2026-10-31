import { createShareCapturePanel, shareCaptureStyles } from './share-capture-panel.js';
import { createConnectorsPanel, connectorsStyles } from './connectors-panel.js';
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
  const h=react.createElement, ReviewPanel=createPersonalReviewPanel(react,ui), ConnectorsPanel=createConnectorsPanel(react,ui), ShareCapturePanel=createShareCapturePanel(react,ui,invoke);
  const host = async (method:string,input?:unknown) => unwrapPersonalHost(await invoke(method,input));
  const reviewService={list:()=>host('review.list'),resolve:(input:unknown)=>host('review.resolve',input),revoke:(input:unknown)=>host('review.revoke',input),settings:(input:{enabled:boolean;expected_revision:number})=>host('review.settings',input)};
  const connectorService={oauth:{start:(input:unknown)=>host('connectors.start',input),attempt:(input:{attempt_id:string})=>host('connectors.attempt',input),cancel:(input:{attempt_id:string})=>host('connectors.cancel',input)},list:()=>host('connectors.list'),actions:()=>host('connectors.actions'),decide:(input:{action_id:string;digest:string;approve:boolean})=>host('connectors.decide',input),disconnect:(input:{connection_id:string})=>host('connectors.disconnect',input)};
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
    return h('div',{'data-personal-connections':true},h('div',{ref:root}),h(ConnectorsPanel,{service:connectorService}),h(ShareCapturePanel));
  }
  return { Connections, ConnectorConversation:()=>h(ConnectorsPanel,{service:connectorService,conversation:true}), ReviewConversation:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale,variant:"conversation"}), Review:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale}), styles:`${shareCaptureStyles}\n${connectorsStyles}\n${calendarStyles}\n${personalReviewStyles}\n
.mosaic-settings>[data-personal-connections]{display:flex;flex-direction:column;gap:var(--section-gap);min-width:0;}
.mosaic-settings .gcal-view{max-width:none;margin:0;font-family:inherit;}
.mosaic-settings .gcal-heading{margin-bottom:var(--section-heading-gap);gap:var(--space-3);}
.mosaic-settings .gcal-icon{display:none;}
.mosaic-settings .gcal-view h2{font-size:var(--font-title);font-weight:var(--weight-medium);margin:0 0 var(--space-1);}
.mosaic-settings .gcal-connection{padding:var(--settings-row-pad-block) var(--card-padding);background:var(--settings-card);border:1px solid var(--hairline-soft);border-radius:var(--settings-group-radius);gap:var(--space-4);flex-wrap:wrap;}
.mosaic-settings .gcal-button{min-height:var(--control-field-height);padding:var(--control-padding-block) var(--control-padding-inline);border:0;border-radius:var(--wuu-radius-control,var(--radius-sm));font-weight:var(--weight-medium);box-shadow:var(--shadow-soft);}
.mosaic-settings .gcal-notice{padding:var(--space-3) 0 0;}
.mosaic-settings .personal-connectors{margin:0;display:flex;flex-direction:column;gap:var(--section-heading-gap);}
.mosaic-settings .personal-connectors-heading{align-items:flex-start;flex-wrap:nowrap;}
.mosaic-settings .personal-connectors-heading>.plugin-ui-section-header{min-width:0;}
.mosaic-settings .personal-connectors-heading>.plugin-ui-button{flex:none;}
.mosaic-settings .personal-connectors-heading .plugin-ui-section-header p{margin:0;}
.mosaic-settings .personal-connectors-heading h3{font-size:var(--font-title);font-weight:var(--weight-medium);}
.mosaic-settings .personal-connectors-list{grid-template-columns:minmax(0,1fr);gap:0;margin:0;padding:0 var(--card-padding);background:var(--settings-card);border:1px solid var(--hairline-soft);border-radius:var(--settings-group-radius);}
.mosaic-settings .personal-connector-provider{padding:var(--settings-row-pad-block) 0;border-top:0;border-bottom:1px solid var(--hairline-soft);}
.mosaic-settings .personal-connector-provider:last-child{border-bottom:0;}
.mosaic-settings .personal-connector-title h4{font-weight:var(--weight-regular);}
.mosaic-settings .personal-connector-title>span{font-size:var(--font-sm);color:var(--ink-soft);}
.mosaic-settings .personal-connector-provider p{margin:var(--space-1) 0;}
.mosaic-settings .personal-connector-provider details{margin-top:var(--space-3);}
.mosaic-settings .personal-review-overview{padding:var(--card-padding);background:var(--settings-card);border:1px solid var(--hairline-soft);border-radius:var(--settings-group-radius);}
.mosaic-settings .personal-review-heading h3{font-weight:var(--weight-regular);}
.mosaic-settings .personal-review-overview>[role=switch]{justify-self:end;}
@container settings-page (max-width:32em){.mosaic-settings .gcal-agenda-form{grid-template-columns:minmax(0,1fr);}.mosaic-settings .gcal-event{grid-template-columns:minmax(0,1fr);}}
` };
}
