#!/usr/bin/env node
// npm-packed public SDK/provider -> real cloud app-server -> existing encrypted
// remote core/relay -> delegated-only receiving app-server -> native session.
// Synthetic model and fresh identities only. Evidence never contains device seeds.
import assert from 'node:assert/strict';
import {spawn, spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {EventEmitter, once} from 'node:events';
import {mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {join, resolve, dirname} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath, pathToFileURL} from 'node:url';

const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const binary=resolve(process.env.WUU_E2E_CORE || join(root,'bin/wuu'));
const output=resolve(process.env.WUU_FEDERATION_E2E_OUTPUT || join(root,'.tmp/e2e/session-federation'));
await mkdir(output,{recursive:true});
const scratch=await mkdtemp(join(output,'run-'));
process.env.npm_config_cache ||= join(scratch,'npm-cache');
const fixture=join(scratch,'independent-provider');await mkdir(fixture);
const report={status:'failed',checks:[],scratch,limitations:['Loopback processes and synthetic inference; physical hosts and rendered desktop acceptance are separate.']};
const children=[];const logs=[];
const hash=value=>createHash('sha256').update(value).digest('hex');
function command(program,args,cwd=root,env=process.env){
 const result=spawnSync(program,args,{cwd,env,encoding:'utf8',timeout:120000,maxBuffer:32*1024*1024});
 logs.push({program,args,status:result.status,stdout:result.stdout,stderr:result.stderr});
 assert.equal(result.status,0,result.stderr||result.stdout);return result.stdout;
}
function launch(name,args,env){
 const child=spawn(binary,args,{env,stdio:['pipe','pipe','pipe']});children.push(child);
 const messages=[],events=new EventEmitter();let stderr='',sequence=0;
 child.stderr.setEncoding('utf8').on('data',text=>{stderr+=text});
 createInterface({input:child.stdout}).on('line',line=>{let value;try{value=JSON.parse(line)}catch{value=line};messages.push(value);events.emit('line',value)});
 const wait=(predicate,label,timeout=30000)=>{
  const old=messages.find(predicate);if(old!==undefined)return Promise.resolve(old);
  return new Promise((yes,no)=>{const timer=setTimeout(()=>{events.off('line',listener);no(Error(`${name} ${label}: ${stderr}`))},timeout);function listener(value){if(predicate(value)){clearTimeout(timer);events.off('line',listener);yes(value)}}events.on('line',listener)});
 };
 async function rpcRaw(method,params={}){const id=`${name}-${++sequence}`;const pending=wait(v=>v?.id===id,method,60000);child.stdin.write(JSON.stringify({id,method,params})+'\n');return pending}
 async function rpc(method,params){const response=await rpcRaw(method,params);assert(!response.error,JSON.stringify(response.error));return response.result}
 const dump=()=>({name,args,stderr,messages:messages.map(value=>typeof value==='string'&&value.includes('wuu://pair')?'[synthetic pairing URI redacted]':value)});
 return {child,wait,rpc,rpcRaw,dump};
}
async function stop(child){if(child.exitCode!==null||child.signalCode!==null)return;const done=once(child,'exit');child.stdin.end();child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),5000);try{await done}finally{clearTimeout(timer)}}
let model,relay,receiver,cloud,remote;
try{
 report.core_sha256=hash(await readFile(binary));report.source_sha256=hash(await readFile(fileURLToPath(import.meta.url)));
 const packed=JSON.parse(command('npm',['pack','--json','--pack-destination',fixture],join(root,'packages/plugin-sdk')))[0];
 const remotePacked=JSON.parse(command('npm',['pack','--json','--pack-destination',fixture],join(root,'clients/core')))[0];
 await writeFile(join(fixture,'package.json'),JSON.stringify({name:'independent-session-federation',private:true,type:'module',dependencies:{'@wuu/plugin-sdk':`file:${join(fixture,packed.filename)}`,'@wuu/remote-core':`file:${join(fixture,remotePacked.filename)}`}}));
 command('npm',['install','--ignore-scripts','--no-audit','--no-fund'],fixture);
 const entry=join(fixture,'remote-entry.ts'),remoteBundle=join(fixture,'remote-core.mjs');
 await writeFile(entry,`export {RemoteClient,pair,Identity,b64encode,b64decode} from '@wuu/remote-core';`);
 command(join(fixture,'node_modules/esbuild/bin/esbuild'),[entry,'--bundle','--platform=node','--format=esm',`--outfile=${remoteBundle}`],fixture);
 remote=await import(pathToFileURL(remoteBundle));
 const home=join(scratch,'local-home'),cloudHome=join(scratch,'cloud-home'),workspace=join(scratch,'local-workspace'),cloudWorkspace=join(scratch,'cloud-workspace');
 await Promise.all([home,cloudHome,workspace,cloudWorkspace].map(path=>mkdir(path)));
 const env=home=>({PATH:process.env.PATH,HOME:scratch,WUU_HOME:home,WUU_NODE_EXECUTABLE:process.execPath});
 const modelCalls=[];let heldReached;const heldArrived=new Promise(resolve=>{heldReached=resolve});
 model=createServer(async(req,res)=>{let data='';for await(const chunk of req)data+=chunk;const body=JSON.parse(data);modelCalls.push(body);
  if(JSON.stringify(body.messages).includes('hold-fixture')){heldReached();await new Promise(resolve=>res.on('close',resolve));}
  if(res.destroyed)return;
  res.writeHead(200,{'content-type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{delta:{content:'federated fixture result'},finish_reason:null}]})}\n\ndata: ${JSON.stringify({choices:[{delta:{},finish_reason:'stop'}]})}\n\ndata: [DONE]\n\n`);
 });await new Promise(resolve=>model.listen(0,'127.0.0.1',resolve));
 const engines=Object.fromEntries(['codex','claude','cursor','devin','dsh','grok','hermes','pi','opencode','antigravity'].map(name=>[name,{enabled:false}]));
 const base={default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:`http://127.0.0.1:${model.address().port}/v1`,api_key:'synthetic',model:'fixture-model'}},engines:{default_engine:'wuu',...engines},skills:{enabled:false},ptc:{enabled:false}};
 const policySource=join(fixture,'delegation-policy.ts'),grantFile=join(scratch,'grant.json');
 const policyID='local-'+hash(policySource).slice(0,24);
 await writeFile(grantFile,JSON.stringify({enabled:true,workspace,sessions:[],subject:''}));
 await writeFile(policySource,`import {readFile} from 'node:fs/promises';
export default {initialize(){return {protocol_version:3,provided_services:[{name:'resource.authorization',version:'1.0.0',methods:[{name:'authorize',input_schema:'resource.authorization.request.v1',output_schema:'resource.authorization.decision.v1'}]}]}},async invokeService({params:r}){
 if(r.owner)return {allow:true};const grant=JSON.parse(await readFile(${JSON.stringify(grantFile)},'utf8'));
 const action=r.action;const allowed=grant.enabled&&r.principal.kind==='device'&&r.principal.subject===grant.subject&&action.startsWith('session/federation.')&&r.resources.every(resource=>resource.kind==='workspace'?resource.id===grant.workspace:resource.kind==='session'&&grant.sessions.includes(resource.id));
 return {allow:allowed,reason:allowed?'fixture-grant':'fixture-denied'};
}};`);
 const localConfig={...base,composition:{version:3,profile:'headless',patches:[{modules:{[policyID]:true,'wuu.credentials.os':false,'wuu.credentials.file':true,'wuu.title.default':false,'wuu.resource-access':true,'wuu.resource-policy.default':false},services:[{service:'resource.authorization',major:1,provider:policyID}]}]},session_federation:{enabled:true,host_id:'local-fixture',storage_host:'local-fixture-store',models:{fixture:['fixture-model']},tool_allow:['read_file']}};
 await writeFile(join(home,'config.json'),JSON.stringify(localConfig));
 command(binary,['plugin','dev','--watch=false',policySource],workspace,env(home));
 relay=launch('relay',['relay','--addr','127.0.0.1:0','--state',join(scratch,'relay-state.json')],env(join(scratch,'relay-home')));
 const connectLine=await relay.wait(value=>typeof value==='string'&&value.startsWith('connect url:'),'relay readiness');
 const relayURL=connectLine.slice('connect url:'.length).trim();
 receiver=launch('receiver',['remote','host','--workdir',workspace,'--relay',relayURL,'--pair','--delegate-only','--ready'],env(home));
 const uri=await receiver.wait(value=>typeof value==='string'&&value.startsWith('wuu://pair'),'pairing readiness');
 const creds=await remote.pair(uri,'cloud-delegate');
 const subject=remote.b64encode(remote.Identity.fromSeed(remote.b64decode(creds.device_seed)).public_());
 await writeFile(grantFile,JSON.stringify({enabled:true,workspace,sessions:[],subject}));
 const credentialsPath=join(fixture,'synthetic-credentials.json');await writeFile(credentialsPath,JSON.stringify(creds),{mode:0o600});
 const cloudSource=join(fixture,'cloud-delegate.ts'),cloudID='local-'+hash(cloudSource).slice(0,24);
 const transportSource=join(fixture,'remote-transport.ts'),transportBundled=join(fixture,'remote-transport-bundled.ts'),transportID='local-'+hash(transportBundled).slice(0,24);
 await writeFile(transportSource,`import {readFile} from 'node:fs/promises';
import {SESSION_TRANSPORT_DESCRIPTOR,remoteSessionTransport,sessionRouter} from '@wuu/plugin-sdk';
import {RemoteClient} from './remote-core.mjs';
let client;const transport=remoteSessionTransport(host=>host==='local-fixture'?client:undefined,{timeoutMs:30000});
export default {concurrentCapabilities:['plugin.client.request'],concurrentServices:['session.transport'],initialize(){return {protocol_version:3,provided_services:[SESSION_TRANSPORT_DESCRIPTOR],capabilities:[{id:'plugin.client.request',kind:'decision',version:1}]}},async activate(){client=new RemoteClient(JSON.parse(await readFile(${JSON.stringify(credentialsPath)},'utf8')),{reconnectMinMs:10});client.start();await client.waitAttached(20000)},async shutdown(){await client?.stop()},async invokeService({params},host,execution){return transport.dispatch(params,execution.signal)},async invokeCapability({input:{method,input}},host){
 if(method==='disconnect'){await client.stop();return {output:{result:{disconnected:true}}}}
 if(method==='reconnect'){client.start();await client.waitAttached(20000);return {output:{result:{connected:true}}}}
 if(method==='raw-control'){try{await client.call('thread/start',{},5000);return {output:{result:{denied:false}}}}catch(error){return {output:{result:{denied:true}}}}}
 throw Error('unknown fixture control');
}};`);
 await writeFile(cloudSource,`import {sessionRouter} from '@wuu/plugin-sdk';
 export default {concurrentCapabilities:['plugin.client.request'],initialize(){return {protocol_version:3,required_services:[{name:'host.session.route',major_version:1,required:true},{name:'session.transport',major_version:1,required:true}],capabilities:[{id:'plugin.client.request',kind:'decision',version:1}]}},async invokeCapability({input:{input}},host){return {output:{result:await sessionRouter(host).dispatch(input)}}}};`);
 const cloudConfig={...base,composition:{version:3,profile:'headless',patches:[{modules:{[cloudID]:true,[transportID]:true,'wuu.credentials.os':false,'wuu.credentials.file':true,'wuu.title.default':false,'wuu.resource-access':true,'wuu.resource-policy.default':true}}]}};
 await writeFile(join(cloudHome,'config.json'),JSON.stringify(cloudConfig));
 command(join(fixture,'node_modules/esbuild/bin/esbuild'),[transportSource,'--bundle','--platform=node','--format=esm',`--outfile=${transportBundled}`],fixture);
 command(binary,['plugin','dev','--watch=false',transportBundled],cloudWorkspace,env(cloudHome));
 command(binary,['plugin','dev','--watch=false',cloudSource],cloudWorkspace,env(cloudHome));
 cloud=launch('cloud',['app-server','--workdir',cloudWorkspace,'--config',join(cloudHome,'config.json')],env(cloudHome));
 const initialized=await cloud.rpc('initialize',{protocol_version:'wuu-app-server/v0.1',client:{name:'federation-e2e'}});
 const plugin=initialized.extension_inventory.find(item=>item.name==='cloud-delegate');assert.equal(plugin?.runtime_state,'active',JSON.stringify(initialized.extension_inventory));
 const transportPlugin=initialized.extension_inventory.find(item=>item.name==='remote-transport-bundled');assert.equal(transportPlugin?.runtime_state,'active',JSON.stringify(transportPlugin));
 const generations=new Set(initialized.extension_inventory.filter(item=>item.graph_generation).map(item=>item.graph_generation));assert.equal(generations.size,1);const graphGeneration=[...generations][0];
 let operation=0;
 const invokeRaw=(method,input)=>{const target=method==='dispatch'?plugin:transportPlugin;return cloud.rpcRaw('plugin/client/request',{id:target.id,fingerprint:target.fingerprint,graph_generation:graphGeneration,request_id:`fixture-${++operation}`,method,input})};
 const invoke=async(method,input)=>{const reply=await invokeRaw(method,input);assert(!reply.error,JSON.stringify(reply.error));return reply.result.result};
 const dispatch=request=>invoke('dispatch',request);
 const assertCompleted=receipt=>{assert.equal(receipt.status,'completed',JSON.stringify(receipt));assert(!receipt.error,JSON.stringify(receipt));return receipt};
 const localDefault=await cloud.rpc('thread/start',{});assert(localDefault.thread.id);
 assert.equal((await invoke('raw-control',{})).denied,true);report.checks.push('delegated paired key cannot call raw owner thread/start');
 const create={version:1,operation:'create',operation_id:'stable-create',locator:{version:1,execution_host:'local-fixture'},params:{visibility:'user',context_source:'fresh',provider:'fixture',model:'fixture-model'}};
 const created=assertCompleted(await dispatch(create));const repeated=assertCompleted(await dispatch(create));assert.deepEqual(repeated,created);
 assert.equal(created.locator.storage_host,'local-fixture-store');const sessionID=created.locator.session_id;assert(sessionID);
 await writeFile(grantFile,JSON.stringify({enabled:true,workspace,sessions:[sessionID],subject}));
 report.checks.push('SDK-only external plugin traverses host.session.route + selected session.transport and creates one remote session');
 const localList=await cloud.rpc('thread/list',{});assert(!JSON.stringify(localList).includes(sessionID));
 const altered=await dispatch({...create,params:{...create.params,name:'changed'}});assert.equal(altered.status,'unknown');assert(altered.error);report.checks.push('conflicting operation ID rejected without new effect');
 await invoke('disconnect',{});
 const offline=await invokeRaw('dispatch',{...create,operation_id:'must-not-fallback'});assert(offline.error);report.checks.push('known offline selected transport fails without local fallback');
 await invoke('reconnect',{});
 const send={version:1,operation:'send',operation_id:'stable-send',locator:created.locator,params:{session_id:sessionID,input:{prompt:'fixture remote task'}}};
 const sent=assertCompleted(await dispatch(send));assert.deepEqual(await dispatch(send),sent);
 await invoke('disconnect',{});await invoke('reconnect',{});assert.deepEqual(await dispatch(send),sent);
 const inspected=assertCompleted(await dispatch({version:1,operation:'inspect',operation_id:'observe',locator:created.locator,params:{session_id:sessionID,request_id:'stable-send',wait:'terminal',timeout_ms:20000}}));
 assert.equal(inspected.result.turn.final_output,'federated fixture result');report.checks.push('same operation survives reconnect and terminal result is retrieved');
 assert.equal(modelCalls.length,1,'duplicate remote inference');report.checks.push('one synthetic model execution for repeated send');
 const heldSend={...send,operation_id:'held-send',params:{session_id:sessionID,input:{prompt:'hold-fixture'}}};
 const held=assertCompleted(await dispatch(heldSend));await Promise.race([heldArrived,new Promise((_,reject)=>setTimeout(()=>reject(Error('held model did not start')),15000))]);
 const cancellation={version:1,operation:'cancel',operation_id:'cancel-exact',locator:created.locator,params:{session_id:sessionID,turn_id:held.result.turn_id}};
 const cancelled=assertCompleted(await dispatch(cancellation));assert.equal(cancelled.result.cancelled,true);assert.deepEqual(await dispatch(cancellation),cancelled);
 const stopped=assertCompleted(await dispatch({version:1,operation:'inspect',operation_id:'observe-cancel',locator:created.locator,params:{session_id:sessionID,request_id:'held-send',wait:'terminal',timeout_ms:20000}}));assert(stopped.result.turn);report.checks.push('exact-turn cancellation and idempotent cancel receipt through public remote adapter');
 const broad=await dispatch({...create,operation_id:'broader',params:{...create.params,permission_mode:'unconfined'}});assert.equal(broad.status,'unknown');assert(broad.error);report.checks.push('execution permission ceiling cannot be broadened');
 const listRequest={version:1,operation:'list',operation_id:'visible-list',locator:{version:1,execution_host:'local-fixture'},params:{}};
 const visibleList=assertCompleted(await dispatch(listRequest));assert(visibleList.result.sessions.some(session=>session.session_id===sessionID));
 await writeFile(grantFile,JSON.stringify({enabled:true,workspace,sessions:[],subject}));
 const filteredList=assertCompleted(await dispatch(listRequest));assert.equal(filteredList.result.sessions.length,0);report.checks.push('list visibility narrows through per-session inspect authorization');
 await writeFile(grantFile,JSON.stringify({enabled:true,workspace,sessions:[sessionID],subject}));
 localConfig.session_federation.enabled=false;await writeFile(join(home,'config.json'),JSON.stringify(localConfig));
 const disabled=await dispatch({...create,operation_id:'receiver-disabled'});assert.equal(disabled.status,'unknown');assert(disabled.error);assert(!JSON.stringify(await cloud.rpc('thread/list',{})).includes(sessionID));report.checks.push('receiver public configuration disable denies new work without local fallback');
 localConfig.session_federation.enabled=true;await writeFile(join(home,'config.json'),JSON.stringify(localConfig));
 await writeFile(grantFile,JSON.stringify({enabled:false,workspace,sessions:[sessionID],subject}));
 const revoked=await dispatch(send);assert.equal(revoked.status,'unknown');assert(revoked.error);report.checks.push('revocation blocks receipt replay through current resource policy');
 assert.equal(modelCalls.length,2,'unexpected model execution count');
 report.status='passed';report.session_id=sessionID;report.cloud_default_session=localDefault.thread.id;
}catch(error){report.error=String(error?.stack||error);throw error}
finally{
 await Promise.all(children.reverse().map(stop));
 if(model)await new Promise(resolve=>model.close(resolve));
 report.transports=[relay,receiver,cloud].filter(Boolean).map(peer=>peer.dump());
 await writeFile(join(scratch,'results.json'),JSON.stringify(report,null,2));
 await writeFile(join(scratch,'commands.json'),JSON.stringify(logs,null,2));
 console.log(`Session federation ${report.status}. Evidence: ${join(scratch,'results.json')}`);
}
