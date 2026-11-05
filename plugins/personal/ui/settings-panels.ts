import { createConnectorsPanel, connectorsStyles } from './connectors-panel.js';
import type * as React from 'react';
import { createPersonalReviewPanel, personalReviewStyles } from './action-review-panel.js';
import { unwrapPersonalHost } from '../../personal-calendar/src/host-bridge.js';

type Invoke = (method: string, input?: unknown) => Promise<unknown>;

export function createSettingsPanels(react: typeof React, ui: Parameters<typeof createPersonalReviewPanel>[1], invoke: Invoke) {
  const h=react.createElement, ReviewPanel=createPersonalReviewPanel(react,ui), ConnectorsPanel=createConnectorsPanel(react,ui);
  const host = async (method:string,input?:unknown) => unwrapPersonalHost(await invoke(method,input));
  const reviewService={list:()=>host('review.list'),resolve:(input:unknown)=>host('review.resolve',input),revoke:(input:unknown)=>host('review.revoke',input),settings:(input:{enabled:boolean;expected_revision:number})=>host('review.settings',input)};
  const connectorService={oauth:{start:(input:unknown)=>host('connectors.start',input),attempt:(input:{attempt_id:string})=>host('connectors.attempt',input),cancel:(input:{attempt_id:string})=>host('connectors.cancel',input)},list:()=>host('connectors.list'),actions:()=>host('connectors.actions'),decide:(input:{action_id:string;digest:string;approve:boolean})=>host('connectors.decide',input),disconnect:(input:{connection_id:string})=>host('connectors.disconnect',input)};
  function Connections() {
    return h('section',{'data-personal-connections':true},h('h2',null,'连接与工具'),h(ConnectorsPanel,{service:connectorService}));
  }
  return { Connections, ConnectorConversation:()=>h(ConnectorsPanel,{service:connectorService,conversation:true}), ReviewConversation:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale,variant:"conversation"}), Review:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale}), styles:`${connectorsStyles}\n${personalReviewStyles}\n.mosaic-settings>[data-personal-connections],.mosaic-settings>.personal-review{margin-top:var(--space-6);padding-top:var(--space-5);border-top:1px solid var(--hairline);}.mosaic-settings>[data-personal-connections]>h2{font:inherit;font-weight:600;margin:0 0 var(--space-5);}` };
}
