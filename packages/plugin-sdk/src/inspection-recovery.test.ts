import assert from 'node:assert/strict';
import { documentStore, documentStoreDescriptor, type StateInspectionResolution } from './persistence.js';
import { pluginSettingsStore, PLUGIN_SETTINGS_STORE_DESCRIPTOR } from './providers.js';
import { sessionDomain, sessionDomainDescriptor } from './session-service.js';
import type { RuntimeHost } from './index.js';

const calls: {params: unknown; options: unknown}[]=[];
const receipt={id:'guard',owner:'holder',namespaces:['authority'],outcome:'completed',reason:'verified terminal downstream receipt',reconciled_by:'operator'} as const;
const host={call:async (_method:string,params:unknown,options:unknown)=>{calls.push({params,options}); const request=params as {service:string;method:string};if(request.service==='session.domain')return {values:[request.method==='ListInspections'?{inspections:[receipt]}:receipt]};return request.method==='list-inspections'?{inspections:[receipt]}:receipt}} as unknown as RuntimeHost;
const resolution:StateInspectionResolution={id:'guard',namespaces:['authority'],expected_owner:'holder',outcome:'completed',reason:receipt.reason};
assert.deepEqual(await documentStore(host).listInspections(),{inspections:[receipt]});
assert.deepEqual(await documentStore(host).reconcileInspection(resolution),receipt);
await pluginSettingsStore(host).listInspections('user');
await pluginSettingsStore(host).reconcileInspection({id:'guard',subject:'plugin',scope:'user',expected_owner:'holder',outcome:'not-dispatched',reason:'confirmed no dispatch'});
await sessionDomain(host).call('ListInspections',[]);
await sessionDomain(host).call('ReconcileInspection',[resolution]);
assert.equal(calls.length,6);
for(const index of [1,3,5])assert.deepEqual(calls[index]?.options,{cleanup:true});
for(const descriptor of [documentStoreDescriptor({inspection:true}),PLUGIN_SETTINGS_STORE_DESCRIPTOR]) {
 for(const method of ['release-inspection','reconcile-inspection'])assert.equal(descriptor.methods?.find(item=>item.name===method)?.cleanup,true);
 assert.ok(descriptor.methods?.some(item=>item.name==='list-inspections'));
}
assert.equal(sessionDomainDescriptor(['ReleaseInspection','ReconcileInspection']).methods?.every(item=>item.cleanup),true);
await documentStore(host).claimNamespaces(['authority']);
assert.deepEqual(calls.at(-1)?.params,{service:'state.documents',method:'claim-namespaces',params:{namespaces:['authority']}});
assert.ok(documentStoreDescriptor({owned_namespaces:true}).methods?.some(method=>method.name==='claim-namespaces'));
await pluginSettingsStore(host).inspectionAuthority();
assert.deepEqual(calls.at(-1)?.params,{service:'state.plugin-settings',method:'inspection-authority',params:{}});
await sessionDomain(host).call('InspectionAuthority',[]);
assert.deepEqual(calls.at(-1)?.params,{service:'session.domain',method:'InspectionAuthority',params:{args:[]}});
console.log('inspection maintenance, ownership and topology transport contract passed');
