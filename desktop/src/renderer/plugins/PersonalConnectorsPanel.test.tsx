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
 it('displays exact recipient/body and dispatches one digest-bound decision',async()=>{const s=service();await render(s);expect(container.textContent).toContain('reader@example.invalid');let finish:(x:{state:string;action_id:string})=>void=()=>{};s.decide.mockImplementation(()=>new Promise(resolve=>{finish=resolve}));await React.act(async()=>{approve().click();approve().click()});expect(s.decide).toHaveBeenCalledTimes(1);expect(s.decide).toHaveBeenCalledWith({action_id:'a',digest:'a'.repeat(64),approve:true});await React.act(async()=>finish({state:'completed',action_id:'a'}));});
 it('locks an ambiguous response until read-only refresh without replay',async()=>{const s=service();s.decide.mockRejectedValue(new Error('secret upstream error'));await render(s);await React.act(async()=>approve().click());expect(approve().disabled).toBe(true);expect(container.textContent).not.toContain('secret upstream');expect(container.querySelector('[role="alert"]')).not.toBeNull();expect(s.decide).toHaveBeenCalledTimes(1);});
 it('unknown and incomplete confirmation data cannot authorize',async()=>{const s=service();s.actions=async()=>({actions:[{...pending().actions[0],arguments_json:undefined}]});await render(s);expect(approve()?.disabled??true).toBe(true);});
 it('empty configuration is not presented as connected and empty conversation is quiet',async()=>{const s=service();s.actions=async()=>({actions:[]});await render(s,true);expect(container.textContent).toBe('');});
 it('closing and reopening reloads saved state without auto-confirming',async()=>{const s=service();await render(s,true);await React.act(async()=>root?.unmount());root=undefined;container.remove();await render(s,true);expect(s.decide).not.toHaveBeenCalled();expect(approve().disabled).toBe(false);});

});
