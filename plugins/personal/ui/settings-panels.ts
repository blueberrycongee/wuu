import { createShareCapturePanel, shareCaptureStyles } from './share-capture-panel.js';
import { createConnectorsPanel, connectorsStyles } from './connectors-panel.js';
import type * as React from 'react';
import type { ApplicationServiceV1 } from '@wuu/plugin-sdk';
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

export function createSettingsPanels(react: typeof React, ui: Parameters<typeof createPersonalReviewPanel>[1], invoke: Invoke, application: ApplicationServiceV1) {
  const h=react.createElement, ReviewPanel=createPersonalReviewPanel(react,ui), ConnectorsPanel=createConnectorsPanel(react,ui), ShareCapturePanel=createShareCapturePanel(react,ui,invoke);
  const host = async (method:string,input?:unknown) => unwrapPersonalHost(await invoke(method,input));
  const reviewService={list:()=>host('review.list'),resolve:(input:unknown)=>host('review.resolve',input),revoke:(input:unknown)=>host('review.revoke',input),settings:(input:{enabled:boolean;expected_revision:number})=>host('review.settings',input)};
  const connectorService={list:()=>host('connectors.list'),actions:()=>host('connectors.actions'),decide:(input:{action_id:string;digest:string;approve:boolean})=>host('connectors.decide',input),disconnect:(input:{connection_id:string})=>host('connectors.disconnect',input)};
  function Connections() {
    const root=react.useRef<HTMLDivElement>(null);
    const [navigationError,setNavigationError]=react.useState('');
    const openConnectors=async()=>{
      try {
        const record=application.catalog.getSnapshot().inventory.find(item=>item.kind==='plugin'&&item.provenance.plugin_id==='account-connectors');
        await application.invoke('catalog.select-section',{section:'plugins'});
        await application.invoke('catalog.select-plugin',{pluginId:record?.id??''});
        await application.invoke('navigation.show-native',{});
        await application.invoke('navigation.show-section',{section:'catalog'});
      } catch { setNavigationError('无法打开连接器，请从插件目录进入账号连接器。'); }
    };
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
      },{legacyOnly:true});
      return ()=>mounted.destroy();
    },[]);
    return h('section',{'data-personal-connections':true},h('h2',null,'连接与工具'),h('p',null,'账号在连接器目录统一管理，连接后 twin 和 Coding 共用授权。外部写入仍需在这里确认。'),h(ui.Button,{onClick:()=>void openConnectors()},'管理共享连接器'),navigationError?h('p',{role:'alert'},navigationError):null,h('details',null,h('summary',null,'已有授权与历史回执'),h('p',null,'旧授权继续保留。MCP 的授权对象和权限可能不同，请从共享连接器重新授权；旧任务和回执不会迁移或自动重发。'),h('div',{ref:root}),h(ConnectorsPanel,{service:connectorService})),h(ShareCapturePanel));
  }
  return { Connections, ConnectorConversation:()=>h(ConnectorsPanel,{service:connectorService,conversation:true}), ReviewConversation:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale,variant:"conversation"}), Review:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale}), styles:`${shareCaptureStyles}\n${connectorsStyles}\n${calendarStyles}\n${personalReviewStyles}\n.mosaic-settings .mosaic-quiet-hours-row{display:flex;flex-wrap:nowrap;justify-content:flex-end;align-items:center;gap:var(--space-3);padding:var(--settings-row-pad-block,12px) 0;border-bottom:1px solid var(--hairline-soft);}\n.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-field{display:inline-flex;align-items:center;gap:var(--space-2);padding:0;border:0;width:auto;}\n.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-field:first-child{flex:1;min-width:0;justify-content:space-between;}\n.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-input{width:128px;}\n@container settings-page (max-width:32em){.mosaic-settings .mosaic-quiet-hours-row{flex-direction:column;align-items:stretch;}.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-field{width:100%;justify-content:space-between;}.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-field:first-child{flex:none;}.mosaic-settings .mosaic-quiet-hours-row .plugin-ui-input{flex:0 0 128px;max-width:100%;}}\n.mosaic-settings .mosaic-settings-save-row{justify-content:flex-end;}\n.mosaic-settings [data-personal-connections]{display:flex;flex-direction:column;gap:var(--space-4);}\n.mosaic-settings [data-personal-connections]>h2{font-size:var(--font-title,16px);font-weight:var(--weight-semibold,600);letter-spacing:-0.012em;color:var(--ink);margin:0;}\n.mosaic-settings .gcal-view{margin:0;max-width:none;}\n.mosaic-settings .gcal-heading{margin-bottom:var(--space-2);gap:var(--space-3);}\n.mosaic-settings .gcal-icon{width:34px;height:34px;border-radius:var(--radius-sm);font-size:var(--font-sm);}\n.mosaic-settings .gcal-view h2{font-size:var(--font-ui,15px);font-weight:var(--weight-medium,500);}\n.mosaic-settings .gcal-muted{font-size:var(--font-sm,14px);color:var(--ink-tertiary);}\n.mosaic-settings .gcal-connection{padding:var(--settings-row-pad-block,12px) 0;border-top:1px solid var(--hairline-soft);border-bottom:1px solid var(--hairline-soft);}\n.mosaic-settings .gcal-button{min-height:var(--control-field-height,32px);padding:0 var(--control-padding-inline,12px);border-radius:var(--radius-sm,6px);font-size:var(--font-sm,14px);font-weight:var(--weight-medium,500);}\n.mosaic-settings .gcal-view fieldset{padding:var(--space-3) 0;}\n.mosaic-settings .gcal-view legend{padding:var(--space-2) 0 0;font-size:var(--font-sm,14px);color:var(--ink-soft);}\n.mosaic-settings .gcal-agenda-form{padding:var(--space-3) 0;border-top:1px solid var(--hairline-soft);}\n.mosaic-settings .gcal-view input:not([type=checkbox]){min-height:var(--control-field-height,32px);padding:0 var(--control-padding-inline,12px);border:1px solid var(--field-border);border-radius:var(--radius-sm,6px);font-size:var(--font-sm,14px);}\n.mosaic-settings .gcal-disconnect{padding:var(--space-3) 0;border-top:1px solid var(--hairline-soft);margin-top:var(--space-2);}` };
}
