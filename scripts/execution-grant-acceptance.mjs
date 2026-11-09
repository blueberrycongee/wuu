#!/usr/bin/env node
// Real production gateway + shipped Personal. Only the detachable transport
// bootstrap is repository-internal; all replacement providers use the packed SDK.
import assert from 'node:assert/strict';
import {execFile,spawn} from 'node:child_process';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {once} from 'node:events';
import {cp,mkdir,mkdtemp,readFile,writeFile,rename} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const binary=resolve(process.env.WUU_GRANTS_BINARY??join(root,'bin/wuu'));
const output=resolve(process.env.WUU_GRANTS_OUTPUT??join(root,'.tmp/e2e/execution-grants'));await mkdir(output,{recursive:true});
const run=await mkdtemp(join(output,'run-')),home=join(run,'state'),workspace=join(run,'workspace');await mkdir(home);await mkdir(workspace);
const sdk=resolve(process.env.WUU_GRANTS_SDK_ROOT??join(root,'packages/plugin-sdk'));
const configPath=join(home,'config.json'),statePath=join(run,'control.json'),databasePath=join(run,'grants.json'),auditPath=join(run,'authority.jsonl'),consumerAudit=join(run,'consumer.jsonl'),gatePath=join(run,'gates.json');
const issuer='https://execution-acceptance.invalid',audience='execution-acceptance',authorityID='fixture.external-authority',accessID='fixture.external-access',consumerID='fixture.task-consumer';
const identity={kind:'user',issuer,tenant:'synthetic-tenant',subject:'synthetic-nonowner'};
const env={...process.env,HOME:join(run,'home'),WUU_HOME:home,WUU_NODE_EXECUTABLE:process.execPath};await mkdir(env.HOME);
const control={account_enabled:true,revoked_tokens:[],revoked_grants:[],expired_grants:[],deny_actions:[],deny_plugin_methods:[],grants:[]};
const writeJSON=async(path,value)=>{await writeFile(path+'.tmp',JSON.stringify(value));await rename(path+'.tmp',path);};
const saveControl=()=>writeJSON(statePath,control);await saveControl();await writeJSON(gatePath,[]);
const {publicKey,privateKey}=generateKeyPairSync('ed25519'),jwksPath=join(run,'jwks.json');await writeFile(jwksPath,JSON.stringify({keys:[{...publicKey.export({format:'jwk'}),kid:'acceptance'}]}));
const token=(jti='login-one',overrides={})=>{const header=Buffer.from(JSON.stringify({alg:'EdDSA',kid:'acceptance'})).toString('base64url'),payload=Buffer.from(JSON.stringify({iss:issuer,aud:audience,kind:'user',tenant:identity.tenant,sub:identity.subject,jti,exp:Math.floor(Date.now()/1000)+3600,...overrides})).toString('base64url');return `${header}.${payload}.${sign(null,Buffer.from(`${header}.${payload}`),privateKey).toString('base64url')}`;};
const transcript=[],commands=[],checks=[];let host,configuration;
const report={status:'running',run,checks,limitations:['All credentials/accounts are synthetic. No public network listener, live model, production account, rendered GUI, or OS isolation claim.','Transport fixture embeds the production SDK only to keep one UserHost alive across independent provider-authenticated pipe connections.','Independent provider file persistence is a single-writer acceptance backend, not production HA.'],failure_matrix:JSON.parse(await readFile(join(root,'scripts/execution-grant-acceptance/cases.json'),'utf8'))};
async function save(){await writeFile(join(run,'report.json'),JSON.stringify(report,null,2));await writeFile(join(run,'transcript.json'),JSON.stringify(transcript,null,2));await writeFile(join(run,'commands.json'),JSON.stringify(commands,null,2));await writeFile(join(output,'latest.json'),JSON.stringify({run,status:report.status},null,2));}
async function command(program,args,options={}){try{const r=await exec(program,args,{cwd:root,env,maxBuffer:8*1024*1024,...options});commands.push({program,args,stdout:r.stdout,stderr:r.stderr,status:'passed'});return r.stdout;}catch(e){commands.push({program,args,stdout:e.stdout,stderr:e.stderr,status:'failed'});throw e;}}
async function check(name,fn){const value={name,status:'running'};checks.push(value);try{value.evidence=await fn();value.status='passed';console.log('PASS '+name);}catch(error){value.status='failed';value.error=error.stack;throw error;}finally{await save();}}
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const events=async path=>{try{return (await readFile(path,'utf8')).trim().split('\n').filter(Boolean).map(line=>JSON.parse(line));}catch(e){if(e.code==='ENOENT')return [];throw e;}};
async function waitEvent(path,predicate,label,timeout=30000){const end=Date.now()+timeout;while(Date.now()<end){const match=(await events(path)).find(predicate);if(match)return match;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);}
const denied=frame=>{assert(frame.error,JSON.stringify(frame));assert.equal(frame.result,undefined);return frame.error;};
class Host {
  clients=new Map();sequence=0;stderr='';
  constructor(binary){this.child=spawn(binary,['--workdir',workspace,'--config',configPath,'--audience',audience],{cwd:root,env,stdio:['pipe','pipe','pipe']});this.exited=once(this.child,'exit');this.child.stderr.on('data',chunk=>{this.stderr+=chunk;});createInterface({input:this.child.stdout}).on('line',line=>{let data;try{data=JSON.parse(line);}catch{return;}transcript.push({received:data});const c=this.clients.get(data.client);if(!c)return;if(data.action==='closed'){c.closed?.();return;}const p=c.pending.get(String(data.frame.id));if(p){c.pending.delete(String(data.frame.id));clearTimeout(p.timer);p.resolve(data.frame);}});this.child.on('exit',()=>{for(const c of this.clients.values())for(const p of c.pending.values()){clearTimeout(p.timer);p.reject(new Error('host exited '+this.stderr));}});}
  send(frame){transcript.push({sent:frame.action?frame:{...frame,frame:frame.frame.method==='authenticate'?{...frame.frame,params:{version:1,scheme:'Bearer',credential:'[omitted]'}}:frame.frame}});this.child.stdin.write(JSON.stringify(frame)+'\n');}
  async open(name,jti='login-one'){const client={pending:new Map()};this.clients.set(name,client);this.send({client:name,action:'open'});const auth=await this.rpc(name,'authenticate',{version:1,scheme:'Bearer',credential:token(jti)});assert.deepEqual(auth.identity,identity);client.initialized=await this.rpc(name,'initialize');return name;}
  async raw(name,method,params={}){assert.equal(this.child.exitCode,null,this.stderr);const id=String(++this.sequence),c=this.clients.get(name);return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{c.pending.delete(id);reject(new Error(method+' timeout '+this.stderr));},30000);c.pending.set(id,{resolve,reject,timer});this.send({client:name,frame:{id,method,params}});});}
  async rpc(name,method,params){const frame=await this.raw(name,method,params);assert(!frame.error,JSON.stringify(frame.error));return frame.result;}
  async clientRaw(name,plugin,method,input={},options={}){const initialized=this.clients.get(name).initialized,item=initialized.extension_inventory.find(p=>p.id===plugin||p.id.endsWith(':'+plugin));assert(item,'missing '+plugin+' '+JSON.stringify(initialized.extension_inventory));const generation=item.graph_generation??initialized.extension_inventory.find(p=>p.graph_generation)?.graph_generation;return this.raw(name,'plugin/client/request',{id:item.id,fingerprint:item.fingerprint,graph_generation:generation,request_id:`fixture-${++this.sequence}`,method,input,...options});}
  async client(name,plugin,method,input={}){const frame=await this.clientRaw(name,plugin,method,input);assert(!frame.error,JSON.stringify(frame.error));return frame.result.result;}
  async disconnect(name){const c=this.clients.get(name);const closed=new Promise(resolve=>{c.closed=resolve;});this.send({client:name,action:'close'});await closed;this.clients.delete(name);}
  async close(){if(this.child.exitCode===null&&this.child.signalCode===null){this.child.stdin.end();const timer=setTimeout(()=>this.child.kill('SIGKILL'),10000);try{await this.exited;}finally{clearTimeout(timer);}}await writeFile(join(run,`host-${Date.now()}-stderr.log`),this.stderr);}
}
try{
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();report.sources={};for(const name of ['provider.ts','consumer.ts','transport.go','cases.json'])report.sources[name]=hash(await readFile(join(root,'scripts/execution-grant-acceptance',name)));report.sources.harness=hash(await readFile(fileURLToPath(import.meta.url)));
 let prepared,transport;
 await check('packed SDK-only replacement providers compile',async()=>{
  prepared=await preparePublicSDK({sdk,temporary:run,tarball:process.env.WUU_GRANTS_SDK_TARBALL});
  for(const name of ['provider','consumer'])await cp(join(root,`scripts/execution-grant-acceptance/${name}.ts`),join(prepared.project,`${name}.ts`));
  await cp(join(root,'examples/plugins/independent-access/provider.ts'),join(prepared.project,'access.ts'));await cp(join(root,'examples/compositions/minimal/engine.ts'),join(prepared.project,'engine.ts'));
  await command(process.execPath,[prepared.tsc,'provider.ts','consumer.ts','access.ts','engine.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:prepared.project});
  for(const name of ['provider','consumer','access','engine']){const source=await readFile(join(prepared.project,name+'.ts'),'utf8');assert(!source.includes('/internal/')&&!source.includes('LocalOwner'));}
  return {tarball_sha256:hash(await readFile(prepared.packed)),source_imports:'@wuu/plugin-sdk and Node built-ins only'};
 });
 await check('real host composition selects independent authority without native fallback',async()=>{
  configuration={default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:'http://127.0.0.1:1',api_key:'synthetic-unused',model:'fixture-model'}},agent:{permission_mode:'standard'},skills:{enabled:false},composition:{version:4,profile:'minimal'}};await writeFile(configPath,JSON.stringify(configuration));
  for(const id of [accessID,authorityID,consumerID]){
   const dir=join(run,id);await mkdir(dir);
   const options=id!==consumerID?{id,issuer,jwksPath,statePath,databasePath,auditPath,identity}:{statePath:join(run,'task.json'),gatePath,auditPath:consumerAudit};
   const contents=id===consumerID?`import {runJSONLRuntime} from '@wuu/plugin-sdk';import {createConsumer} from './consumer.js';await runJSONLRuntime(createConsumer(${JSON.stringify(options)}),{input:process.stdin,output:process.stdout});`:`import {runJSONLRuntime} from '@wuu/plugin-sdk';import {createGrantProvider} from './provider.js';import {createEchoEngine} from './engine.js';const a=createGrantProvider(${JSON.stringify(options)}),e=createEchoEngine('bounded-task'),grantOnly=${JSON.stringify(id===authorityID)};const plugin={...a,concurrentServices:[...a.concurrentServices,...(grantOnly?[]:['engine.wuu'])],initialize:async(...p)=>{const x=await a.initialize(...p),y=await e.initialize(...p);return {...x,provided_services:[...x.provided_services.filter(s=>grantOnly?s.name==='execution.authorization':s.name!=='execution.authorization'),...(grantOnly?[]:y.provided_services)],required_services:grantOnly?[]:y.required_services};},activate:async(...p)=>{await a.activate(...p);if(!grantOnly)await e.activate(...p);},shutdown:async()=>{if(!grantOnly)await e.shutdown();await a.shutdown();},invokeService:async(r,...p)=>{if(r.service!=='engine.wuu')return a.invokeService(r,...p);const value=await e.invokeService(r,...p);return r.method==='descriptor'?{...value,capabilities:[...(value.capabilities??[]),'explicit-context']}:value;}};await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`;
   const wrapper=join(prepared.project,id+'.mjs');await writeFile(wrapper,contents);await command(join(sdk,'node_modules/esbuild/bin/esbuild'),[wrapper,'--bundle','--platform=node','--format=esm','--outfile='+join(dir,'runtime.mjs')]);
   await writeFile(join(dir,'plugin.json'),JSON.stringify({schema_version:1,id,name:id,version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
   await command(binary,['plugin','install',dir],{cwd:workspace});
  }
  configuration=JSON.parse(await readFile(configPath,'utf8'));configuration.composition={version:4,profile:'personal',patches:[{modules:{[accessID]:true,[authorityID]:true,[consumerID]:true,'wuu.execution-authorization.default':false,'wuu.resource-policy.default':false,'wuu.engines.native':false},services:[...['identity.authentication','resource.authorization','execution.authorization','engine.wuu','fixture.effects'].map(service=>({service,major:1,provider:service==='execution.authorization'?authorityID:accessID})),{service:'fixture.tasks',major:1,provider:consumerID}]}]};await writeFile(configPath,JSON.stringify(configuration));
  transport=process.env.WUU_GRANTS_TRANSPORT??join(run,'transport');if(!process.env.WUU_GRANTS_TRANSPORT)await command('go',['build','-buildvcs=false','-ldflags','-X github.com/blueberrycongee/wuu/internal/version.Version='+String(await readFile(join(root,'VERSION'),'utf8')).trim(),'-o',transport,'./scripts/execution-grant-acceptance/transport.go']);
  report.transport={path:transport,sha256:hash(await readFile(transport))};report.installer={path:binary,sha256:hash(await readFile(binary))};report.composition=structuredClone(configuration.composition);
  host=new Host(transport);await host.open('gui');const inventory=host.clients.get('gui').initialized.extension_inventory;for(const id of [accessID,authorityID,consumerID,'personal'])assert.equal(inventory.find(p=>p.id===id||p.id.endsWith(':'+id))?.runtime_state,'active',JSON.stringify(inventory));return {selected:authorityID,native_execution_authority:false};
 });
 let personalTask,probe,parent,grant;
 await check('shipped Personal admits nonowner task through public execution gateway',async()=>{
  const main=(await host.client('gui','personal','agents.ensure_default')).agent;
  personalTask=(await host.client('gui','personal','tasks.create',{agent_id:main.agent_id,request_id:'independent-personal-task',title:'Independent bounded task',goal:'Synthetic nonnetwork task with no external effect'})).task;
  assert.equal(personalTask.authorization.reference.provider,authorityID);assert.equal(personalTask.execution.authorization.reference.provider,authorityID);assert(personalTask.execution.session_id);assert.equal(personalTask.execution.status,'submitted',JSON.stringify(personalTask));
  const deadline=Date.now()+10000;while(Date.now()<deadline){personalTask=(await host.client('gui','personal','tasks.get',{task_id:personalTask.task_id})).task;if(personalTask.execution.finished_state)break;await new Promise(resolve=>setTimeout(resolve,50));}
  assert.equal(personalTask.execution.finished_state,'completed',JSON.stringify(personalTask));const history=await host.client('gui','personal','tasks.history',{task_id:personalTask.task_id,start_seq:1,limit:50});assert(JSON.stringify(history).includes('bounded-task:'));
  const issued=(await events(auditPath)).filter(e=>e.event==='issued'&&e.data.scope.task_id===personalTask.task_id);assert(issued.some(e=>e.data.scope.kind==='task-lineage'));assert(issued.some(e=>e.data.scope.kind==='exact-run'));assert(issued.every(e=>JSON.stringify(e.data.principal)===JSON.stringify(identity)));return {task_id:personalTask.task_id,session_id:personalTask.execution.session_id,grants:issued.map(e=>e.data.reference)};
 });
 await check('independent task consumer issues and uses bounded grants via public gateway',async()=>{
  probe=await host.client('gui',consumerID,'prepare');const expires_at=new Date(Date.now()+3600_000).toISOString();
  parent=await host.client('gui',consumerID,'issue',{kind:'task-lineage',request_id:'fixture-parent',expires_at});
  grant=await host.client('gui',consumerID,'issue',{parent:parent.reference,expires_at});
  const repeat=await host.client('gui',consumerID,'issue',{parent:parent.reference,expires_at});assert.deepEqual(repeat.reference,grant.reference);
  const effect=await host.client('gui',consumerID,'effect',{tag:'connected'});assert.equal(effect.recorded,true);return {parent:parent.reference,child:grant.reference,scope:grant.scope};
 });
 await check('external policy enforces exact plugin method and dependency-service scope',async()=>{
  const before=await host.client('gui',consumerID,'read');control.deny_plugin_methods=['read'];await saveControl();const method=denied(await host.clientRaw('gui',consumerID,'read'));control.deny_plugin_methods=[];await saveControl();
  assert.deepEqual(await host.client('gui',consumerID,'read'),before);const service=denied(await host.clientRaw('gui',consumerID,'record',{tag:'forbidden-direct-service'},{service:'fixture.effects'}));
  const policies=(await events(auditPath)).filter(e=>e.event==='policy'&&e.data.action==='plugin/client/request');assert(policies.some(e=>e.data.resources.some(r=>r.kind==='plugin-service'&&r.id==='fixture.effects')));return {method,service,policy_resources:policies.at(-1).data.resources};
 });
 await check('disconnect revokes login transport but not independently authorized durable account grant',async()=>{
  await host.client('gui',consumerID,'background',{tag:'detached'});await waitEvent(consumerAudit,e=>e.event==='background-ready'&&e.data.tag==='detached','background admission');await host.disconnect('gui');assert.equal(host.clients.size,0);
  control.revoked_tokens.push('login-one');await saveControl();await writeJSON(gatePath,['detached']);
  const result=await waitEvent(consumerAudit,e=>e.event==='background-effect'&&e.data.tag==='detached','effect after GUI disconnect and token logout');await host.open('gui-two','login-two');return result;
 });
 await check('complete host restart restores same provider-owned grant and scope',async()=>{await host.close();host=new Host(transport);await host.open('restored','login-two');const stored=await host.client('restored',consumerID,'read');assert.deepEqual(stored.grant.reference,grant.reference);assert.deepEqual(Object.keys(stored.grant).sort(),['expires_at','reference']);assert.equal((await host.client('restored',consumerID,'effect',{tag:'restarted'})).recorded,true);return {restored:grant.reference};});
 await check('wrong session, wrong run, widened child and expired issuance fail closed',async()=>{
  const forgedAdmission=denied(await host.clientRaw('restored',consumerID,'forged-issue'));
  const wrongSession=denied(await host.clientRaw('restored',consumerID,'send',{session_id:probe.main}));
  const wrongRun=denied(await host.clientRaw('restored',consumerID,'send',{request_id:'different-run'}));
  const expanded=denied(await host.clientRaw('restored',consumerID,'issue',{parent:parent.reference,actions:['personal/file.execute'],expires_at:parent.expires_at}));
  const expired=denied(await host.clientRaw('restored',consumerID,'issue',{expires_at:new Date(Date.now()-1000).toISOString()}));return {forgedAdmission,wrongSession,wrongRun,expanded,expired};
 });
 await check('selected provider failure denies effects without native fallback',async()=>{control.fail_grants=true;await saveControl();const error=denied(await host.clientRaw('restored',consumerID,'effect',{tag:'provider-failure'}));control.fail_grants=false;await saveControl();return error;});
 await check('cancellation drains selected background work without an effect',async()=>{
  const job=await host.client('restored',consumerID,'background',{tag:'cancelled'});await waitEvent(consumerAudit,e=>e.event==='background-ready'&&e.data.tag==='cancelled','cancellable background admission');
  const cancelled=await host.client('restored',consumerID,'cancel-background',{job_id:job.job_id});assert.equal(cancelled.cancelled,true);await waitEvent(consumerAudit,e=>e.event==='background-cancelled'&&e.data.tag==='cancelled','background cancellation');return cancelled;
 });
 await check('provider-owned account disablement blocks detached restoration',async()=>{
  await host.client('restored',consumerID,'background',{tag:'account-disabled'});await waitEvent(consumerAudit,e=>e.event==='background-ready'&&e.data.tag==='account-disabled','account background admission');await host.disconnect('restored');control.account_enabled=false;await saveControl();await writeJSON(gatePath,['detached','account-disabled']);const error=await waitEvent(consumerAudit,e=>e.event==='background-denied'&&e.data.tag==='account-disabled','disabled account denies effect');control.account_enabled=true;await saveControl();await host.open('revoker','login-three');return error;
 });
 await check('provider-owned grant revocation invalidates descendants and restart',async()=>{
  assert.equal((await host.client('revoker',consumerID,'revoke',{reference:parent.reference})).revoked,true);const error=denied(await host.clientRaw('revoker',consumerID,'effect',{tag:'revoked'}));await host.close();host=new Host(transport);await host.open('revoked-restored','login-three');denied(await host.clientRaw('revoked-restored',consumerID,'effect',{tag:'revoked-restart'}));return error;
 });
 await check('a genuinely expired grant cannot restore an effect',async()=>{
  await host.client('revoked-restored',consumerID,'new-run',{run_id:'fixture-expiring-run'});const expires_at=new Date(Date.now()+4000).toISOString();
  const expiring=await host.client('revoked-restored',consumerID,'issue',{request_id:'fixture-expiring-run',expires_at});assert.equal((await host.client('revoked-restored',consumerID,'effect',{tag:'before-expiry'})).recorded,true);
  while(Date.now()<=Date.parse(expires_at))await new Promise(resolve=>setTimeout(resolve,Math.max(1,Math.min(50,Date.parse(expires_at)-Date.now()+1))));
  const error=denied(await host.clientRaw('revoked-restored',consumerID,'effect',{tag:'expired'}));return {reference:expiring.reference,expires_at,error};
 });
 await check('native selected authority honestly rejects nonlocal durable account',async()=>{
  await host.client('revoked-restored',consumerID,'new-run',{run_id:'fixture-removal-run'});await host.client('revoked-restored',consumerID,'issue',{request_id:'fixture-removal-run'});assert.equal((await host.client('revoked-restored',consumerID,'effect',{tag:'before-removal'})).recorded,true);
  await host.close();configuration.composition.patches[0].modules['wuu.execution-authorization.default']=true;configuration.composition.patches[0].services.find(s=>s.service==='execution.authorization').provider='wuu.execution-authorization.default';await writeFile(configPath,JSON.stringify(configuration));host=new Host(transport);await host.open('native','login-three');const error=denied(await host.clientRaw('native',consumerID,'issue',{kind:'task-lineage',request_id:'native-unsupported'}));assert.match(JSON.stringify(error),/durable account validity is unsupported/);return {error,composition:structuredClone(configuration.composition)};
 });

 await check('removing selected authority fails closed instead of native fallback',async()=>{
  await host.close();configuration.composition.patches[0].modules[authorityID]=false;configuration.composition.patches[0].modules['wuu.execution-authorization.default']=false;configuration.composition.patches[0].services=configuration.composition.patches[0].services.filter(s=>s.service!=='execution.authorization');await writeFile(configPath,JSON.stringify(configuration));host=new Host(transport);
  await host.open('removed','login-three');const error=denied(await host.clientRaw('removed',consumerID,'effect',{tag:'removed'}));assert.match(JSON.stringify(error),/selected execution authorization unavailable|provider binding changed|unavailable/);return {no_native_fallback:true,error,composition:structuredClone(configuration.composition)};
 });
 await check('all external policy and issue calls retain the real nonowner principal',async()=>{const all=await events(auditPath),policies=all.filter(e=>e.event==='policy'),issued=all.filter(e=>e.event==='issued');assert(policies.length>0&&issued.length>=4);assert(policies.every(e=>e.data.owner===false));assert(issued.every(e=>JSON.stringify(e.data.principal)===JSON.stringify(identity)));assert(!all.some(e=>e.event==='effect'&&['provider-failure','account-disabled','revoked','revoked-restart','expired','cancelled','forbidden-direct-service','removed'].includes(e.data.tag)));return {policy_calls:policies.length,issued:issued.length,no_owner_synthesis:true};});
 report.status='passed';
}catch(error){report.status='failed';report.error=error.stack;process.exitCode=1;console.error(error);}finally{if(host)await host.close();await save();console.log('Execution-grant evidence: '+run);}
