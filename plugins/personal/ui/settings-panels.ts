import type * as React from 'react';
import { createPersonalReviewPanel, personalReviewStyles } from './action-review-panel.js';
import { unwrapPersonalHost } from '../src/host-bridge.js';

type Invoke = (method: string, input?: unknown) => Promise<unknown>;

export function createSettingsPanels(react: typeof React, ui: Parameters<typeof createPersonalReviewPanel>[1], invoke: Invoke) {
  const h=react.createElement, ReviewPanel=createPersonalReviewPanel(react,ui);
  const host = async (method:string,input?:unknown) => unwrapPersonalHost(await invoke(method,input));
  const reviewService={list:()=>host('review.list'),rules:(input?:import('./action-review-panel.js').ReviewRuleCommand)=>host('review.rules',input),previewRule:(input:import('./action-review-panel.js').ReviewBinding)=>host('review.rule.preview',input),resolve:(input:unknown)=>host('review.resolve',input),revoke:(input:unknown)=>host('review.revoke',input),settings:(input:{enabled:boolean;expected_revision:number})=>host('review.settings',input)};
  return { ReviewConversation:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale,variant:"conversation"}), Review:({locale}:{locale?:string})=>h(ReviewPanel,{service:reviewService,locale}), styles:`${personalReviewStyles}\n.mosaic-settings>.personal-review{margin-top:var(--space-6);padding-top:var(--space-5);border-top:1px solid var(--hairline);}` };
}
