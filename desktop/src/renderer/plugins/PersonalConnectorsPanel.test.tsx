import * as React from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {afterEach,describe,it,expect,vi} from 'vitest';
import {createPluginUIKit} from '../../shared/workbench';
import {createConnectorsPanel,type ConnectorService} from '../../../../plugins/personal/ui/connectors-panel';
// Owner-control contracts: exact digest confirmation, double clicks, lost ACK,
// forged/unknown states and unmount must never imply a successful external write.
let root:Root|undefined,container:HTMLDivElement;
afterEach(()=>{if(root)React.act(()=>root?.unmount());container?.remove();root=undefined;});
const pending=()=>({actions:[{state:'awaiting_confirmation',action_id:'a',digest:'a'.repeat(64),connection_id:'mail',operation:'gmail.send',summary:'Send one note',arguments_json:'{"to":"reader@example.invalid","body":"Synthetic note"}',account_id:'owner@example.invalid'}]});
async function render(service:ConnectorService,conversation=false){container=document.createElement('div');document.body.append(container);root=createRoot(container);const Panel=createConnectorsPanel(React,createPluginUIKit(React));await React.act(async()=>root?.render(<Panel service={service} conversation={conversation}/>));}
const service=()=>({list:async()=>({connections:[]}),actions:async():Promise<unknown>=>pending(),decide:vi.fn(async()=>({state:'completed',action_id:'a'})),disconnect:vi.fn(async()=>({state:'disconnected'}))});
const approve=()=>container.querySelector<HTMLButtonElement>('[data-connector-approve]')!;
describe('Personal connector owner surface',()=>{
 it('previews an exact connector rule and saves it before dispatching once',async()=>{
  const calls:string[]=[];const base=service();
  const s={...base,rulePreview:vi.fn(async()=>({schema:'personal-review-rules.v1',revision:3,rules:[],preview:{description:'Only this exact account, recipient and message',operation:'gmail.send',target:'reader@example.invalid',decision:'allow'}})),ruleSave:vi.fn(async()=>{calls.push('save');return {schema:'personal-review-rules.v1',revision:4,rules:[]}}),decide:vi.fn(async()=>{calls.push('decide');return {state:'completed',action_id:'a'}})};
  await render(s,true);await React.act(async()=>container.querySelector<HTMLButtonElement>('[data-connector-remember]')!.click());
  expect(s.ruleSave).not.toHaveBeenCalled();expect(s.decide).not.toHaveBeenCalled();
  await React.act(async()=>container.querySelector<HTMLButtonElement>('[data-connector-save-rule]')!.click());
  expect(calls).toEqual(['save','decide']);expect(s.ruleSave).toHaveBeenCalledWith({action_id:'a',digest:'a'.repeat(64),expected_revision:3,decision:'allow'});
 });
 it('never dispatches after a rule save fails',async()=>{
  const base=service();const s={...base,rulePreview:async()=>({schema:'personal-review-rules.v1',revision:3,rules:[],preview:{description:'Exact message',operation:'gmail.send',target:'reader@example.invalid',decision:'allow'}}),ruleSave:async()=>{throw new Error('lost save response')}};
  await render(s,true);await React.act(async()=>container.querySelector<HTMLButtonElement>('[data-connector-remember]')!.click());
  await React.act(async()=>container.querySelector<HTMLButtonElement>('[data-connector-save-rule]')!.click());
  expect(s.decide).not.toHaveBeenCalled();expect(approve().disabled).toBe(true);
 });
 it('displays exact recipient/body and dispatches one digest-bound decision',async()=>{const s=service();await render(s);expect(container.textContent).toContain('reader@example.invalid');let finish:(x:{state:string;action_id:string})=>void=()=>{};s.decide.mockImplementation(()=>new Promise(resolve=>{finish=resolve}));await React.act(async()=>{approve().click();approve().click()});expect(s.decide).toHaveBeenCalledTimes(1);expect(s.decide).toHaveBeenCalledWith({action_id:'a',digest:'a'.repeat(64),approve:true});await React.act(async()=>finish({state:'completed',action_id:'a'}));});
 it('locks an ambiguous response until read-only refresh without replay',async()=>{const s=service();s.decide.mockRejectedValue(new Error('secret upstream error'));await render(s);await React.act(async()=>approve().click());expect(approve().disabled).toBe(true);expect(container.textContent).not.toContain('secret upstream');expect(container.querySelector('[role="alert"]')).not.toBeNull();expect(s.decide).toHaveBeenCalledTimes(1);});
 it('unknown and incomplete confirmation data cannot authorize',async()=>{const s=service();s.actions=async()=>({actions:[{...pending().actions[0],arguments_json:undefined}]});await render(s);expect(approve()?.disabled??true).toBe(true);});
 it('ignores a late decision response after the Personal service changes',async()=>{
  const s=service();let finish:()=>void=()=>{};s.decide.mockImplementation(()=>new Promise(resolve=>{finish=()=>resolve({state:'completed',action_id:'a'})}));
  container=document.createElement('div');document.body.append(container);root=createRoot(container);const Panel=createConnectorsPanel(React,createPluginUIKit(React));
  await React.act(async()=>root?.render(<Panel service={s} conversation/>));React.act(()=>approve().click());
  const next=service();next.actions=async()=>({actions:[]});await React.act(async()=>root?.render(<Panel service={next} conversation/>));
  await React.act(async()=>finish());expect(container.querySelector('[data-connector-approve]')).toBeNull();
 });
 it('does not unlock a new space decision when an old rule save returns',async()=>{
  let saved:(value:unknown)=>void=()=>{};
  const old={...service(),rulePreview:async()=>({schema:'personal-review-rules.v1',revision:3,rules:[],preview:{description:'Exact message',operation:'gmail.send',target:'reader@example.invalid',decision:'allow'}}),ruleSave:()=>new Promise(resolve=>{saved=resolve})};
  container=document.createElement('div');document.body.append(container);root=createRoot(container);const Panel=createConnectorsPanel(React,createPluginUIKit(React));
  await React.act(async()=>root?.render(<Panel service={old} conversation/>));
  await React.act(async()=>container.querySelector<HTMLButtonElement>('[data-connector-remember]')!.click());React.act(()=>container.querySelector<HTMLButtonElement>('[data-connector-save-rule]')!.click());
  const next=service();next.decide.mockImplementation(()=>new Promise(()=>{}));await React.act(async()=>root?.render(<Panel service={next} conversation/>));React.act(()=>approve().click());
  await React.act(async()=>saved({schema:'personal-review-rules.v1',revision:4,rules:[]}));
  expect(approve().disabled).toBe(true);expect(old.decide).not.toHaveBeenCalled();expect(next.decide).toHaveBeenCalledTimes(1);
 });
 it('empty configuration is not presented as connected and empty conversation is quiet',async()=>{const s=service();s.actions=async()=>({actions:[]});await render(s,true);expect(container.textContent).toBe('');});
 it('closing and reopening reloads saved state without auto-confirming',async()=>{const s=service();await render(s,true);await React.act(async()=>root?.unmount());root=undefined;container.remove();await render(s,true);expect(s.decide).not.toHaveBeenCalled();expect(approve().disabled).toBe(false);});

});
