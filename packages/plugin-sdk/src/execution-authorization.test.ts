import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { executionAuthorization, EXECUTION_AUTHORIZATION_DESCRIPTOR, type ExecutionAuthorizationCommit, type ExecutionAuthorizationCommitGuard } from './execution-authorization.js';
import type { RuntimeHost, RuntimeHostCallOptions, ServiceInvokeParams } from './index.js';

const request: ExecutionAuthorizationCommit = {
 id:'operation', operation_digest:'a'.repeat(64),
 authorization:{reference:{provider:'provider',id:'grant'},scope:{kind:'exact-run',authority:{service:'tasks',major:1,provider:'task-provider',subject:'subject',fingerprint:'fingerprint'},bounds:{model_providers:['provider'],models:['model'],permission_modes:['normal'],max_depth:1},depth:0,consumer:'consumer',user_space_id:'space',product_domain:'personal',task_id:'task',run_id:'run',session_id:'session',context_digest:'<source>&\u2028',revision:1},action:'memory/publish',resources:[{kind:'task',id:'task'}]},
};
const authorizationDigest = createHash('sha256').update(JSON.stringify(request.authorization).replace(/[<>&\u2028\u2029]/g, character => `\\u${character.charCodeAt(0).toString(16).padStart(4,'0')}`)).digest('hex');
const guard: ExecutionAuthorizationCommitGuard = {id:request.id,operation_digest:request.operation_digest,authorization_digest:authorizationDigest,principal:{kind:'user',issuer:'local',tenant:'installation',subject:'owner'},expires_at:'2026-10-29T00:00:00Z'};

// These helpers cross a durable authority boundary, not a convenience API: an
// absent/mismatched response must never look like admission or trigger replay.
test('execution commit helpers bind exact admission and retain cleanup routing', async()=>{
 const calls:Array<{method:string;params:unknown;cleanup?:boolean}>=[];
 const host={call:async(_method:string,input:ServiceInvokeParams,options?:RuntimeHostCallOptions)=>{
  calls.push({method:input.method,params:input.params,cleanup:options?.cleanup});
  switch(input.method){
   case'acquire-commit':return guard;
   case'release-commit':case'reconcile-commit':return {released:true};
   case'inspect-commit':return {guard,caller:'consumer'};
   case'list-commits':return {commits:[{request,guard,caller:'consumer'}]};
   default:throw new Error('unexpected method');
  }
 }} as unknown as RuntimeHost;
 const client=executionAuthorization(host);
 assert.equal(authorizationDigest, 'db361ca5ceec9aab50bb85dfc3d041c762305ab552eaa4f3bdbd7415c5d0c34e');
 assert.deepEqual(await client.acquireCommit({...request,authorization:{...request.authorization,reference:{id:'grant',provider:'provider'}}}),guard);
 assert.deepEqual(await client.inspectCommit(request),{guard,caller:'consumer'});
 assert.equal((await client.listCommits()).commits.length,1);
 const release={...request,outcome:'committed' as const,receipt_digest:'b'.repeat(64)};
 assert.deepEqual(await client.releaseCommit(release,{cleanup:false}),{released:true});
 assert.deepEqual(await client.reconcileCommit({...release,expected_caller:'consumer',reason:'stopped writer and inspected receipt'}),{released:true});
 assert.deepEqual(calls.map(({method,cleanup})=>({method,cleanup})),[
  {method:'acquire-commit',cleanup:undefined},{method:'inspect-commit',cleanup:undefined},{method:'list-commits',cleanup:undefined},{method:'release-commit',cleanup:true},{method:'reconcile-commit',cleanup:undefined},
 ]);
 assert.equal(EXECUTION_AUTHORIZATION_DESCRIPTOR.methods.find(method=>method.name==='release-commit')?.cleanup,true);
});

test('execution commit helpers reject unknown and changed responses without automatic cleanup or retries',async()=>{
 const malformedPrincipals = [
  {kind:'user',issuer:123,tenant:[],subject:{}},
  {...guard.principal,issuer:'   '}, {...guard.principal,tenant:'with\0nul'},
  {...guard.principal,subject:'界'.repeat(1366)},
 ];
 const malformedExpiries = [123, '2026-10-29', 'October 29, 2026', '2026-02-30T00:00:00Z', '2026-10-29T24:00:00Z', '2026-10-29T00:00:00+24:00', '0001-01-01T00:00:00Z'];
 for(const response of [null,{}, {...guard,id:'different'},{...guard,operation_digest:'c'.repeat(64)},{...guard,authorization_digest:'c'.repeat(64)},{...guard,principal:{}},{...guard,expires_at:'invalid'},...malformedPrincipals.map(principal=>({...guard,principal})),...malformedExpiries.map(expires_at=>({...guard,expires_at}))]){
  let calls=0;
  const host={call:async()=>{calls++;return response;}} as unknown as RuntimeHost;
  await assert.rejects(executionAuthorization(host).acquireCommit(request));
  assert.equal(calls,1);
 }
 for (const caller of [123, {}, [], '   ', 'a'.repeat(1025), '界'.repeat(342), 'with\0nul', 'with\nnewline', '*']) {
  const status = {guard,caller};
  const host={call:async(_method:string,input:ServiceInvokeParams)=>input.method==='list-commits'?{commits:[{...status,request}]}:status} as unknown as RuntimeHost;
  await assert.rejects(executionAuthorization(host).inspectCommit(request));
  await assert.rejects(executionAuthorization(host).listCommits());
 }
 for (const receipt_digest of [123, ['b'.repeat(64)]]) {
  const host={call:async()=>({guard,caller:'consumer',outcome:'committed',receipt_digest})} as unknown as RuntimeHost;
  await assert.rejects(executionAuthorization(host).inspectCommit(request));
 }
 let calls=0;const failure=Object.assign(new Error('lost response'),{code:'outcome_unknown'});
 const host={call:async()=>{calls++;throw failure;}} as unknown as RuntimeHost;
 await assert.rejects(executionAuthorization(host).acquireCommit(request),error=>error===failure);
 assert.equal(calls,1);
 for(const response of [{},{released:false},null]){
  const host={call:async()=>response} as unknown as RuntimeHost;
  await assert.rejects(executionAuthorization(host).releaseCommit({...request,outcome:'not-committed'}));
 }
});
