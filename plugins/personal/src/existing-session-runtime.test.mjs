import assert from 'node:assert/strict';
import {test} from 'node:test';
import {spawn} from 'node:child_process';
import {createInterface} from 'node:readline';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {once} from 'node:events';

// Public process/persistence failures: task submission must not create a worker,
// lost send reply must reconcile the original run, follow-up retains target,
// cancellation cannot stop unrelated work, restart must not replace a target,
// and a worker final response must not be labeled independently verified.
const root=fileURLToPath(new URL('../../../',import.meta.url));
test('Personal process controls an exact existing target across lost replies, follow-up, cancel and restart',async t=>{
 const scratch=await mkdtemp(join(tmpdir(),'personal-existing-runtime-'));
 t.after(()=>rm(scratch,{recursive:true,force:true}));
 const storage=new Map(),grants=new Map(),turns=new Map(),frames=[],children=new Set();
 const target={version:1,execution_host:'registered-coding',session_id:'existing-user-session'};
 const targetMetadata={session_id:target.session_id,owner:'user',parent_session_id:'unchanged-parent',visibility:'user',state:'idle'};
 const original=structuredClone(targetMetadata);
 const contexts={};let created=0,sends=0,cancels=0,loseSend=false,loseBeforeAdmission=false,unavailable=false,sendOffline=false,runtime;
 const routeInputs=[];let concurrentHold,concurrentReads=0;
 const host=async frame=>{
  const {service,method,params}=frame.params;const key=`${params?.scope}:${params?.key}`;
  switch(service){
   case 'host.storage.get':{
    const value=storage.get(key)??null;
    if(concurrentHold?.started){
     if(value&&JSON.parse(value).request_id==='concurrent-unknown')concurrentReads++;
     if(concurrentReads>=2&&key.includes('.agent.'))concurrentHold.release();
    }
    return {value};
   }
   case 'host.storage.keys':return {keys:[...storage.keys()].map(k=>k.slice('user:'.length))};
   case 'host.storage.compare-exchange':{const current=storage.get(key)??null;if(current!==params.expected)return {swapped:false,value:current};storage.set(key,params.value);return {swapped:true,value:params.value};}
   case 'host.file-lock':return method==='acquire'?{lock_id:'lock'}:{released:true};
   case 'host.client.invalidate':return {delivered:true};
   case 'host.background':return {job_id:'background',execution_id:'background'};
   case 'host.personal':if(params.operation==='owner.check')return {ok:true,value:{}};throw Error(`Unexpected Personal effect ${params.operation}`);
   case 'session.transport':assert.equal(method,'list-targets');return {version:1,targets:[{locator:target,title:'Existing Coding'}],unavailable:[]};
   case 'host.session.create':{created++;assert.equal(created,1,'only main binding may be created');const session_id='personal-main';contexts[session_id]={session_id,...params};return {session_id,created:true};}
   case 'host.session.context':return contexts[params.session_id];
   case 'host.session.inspect':assert.equal(params.session_id,'personal-main');return {session:contexts['personal-main'],turn:turns.get(params.request_id)};
   case 'host.session.send':assert.equal(params.session_id,'personal-main');assert.equal(params.disable_tools,true);turns.set(params.request_id,{request_id:params.request_id,state:'completed',turn_id:`feedback-${params.request_id}`});return {session_id:params.session_id,state:'running',turn_id:`feedback-${params.request_id}`};
   case 'host.session.history.read':return {session_id:params.session_id,records:[],head_seq:0,snapshot_seq:0,has_more:false};
   case 'host.execution.authorization':{
    if(params.operation==='describe')return {default_lifetime_seconds:3600,max_lifetime_seconds:3600,authority_kinds:['fixture'],scope_kinds:['task-lineage','exact-run']};
    if(params.operation==='issue'){
     assert.equal(params.session_id,'personal-main','source authority stays on owned controller');
     const observation=await runtime.call('service.invoke',{service:'personal.agents',method:'resolve-lineage',caller:'host',execution_id:'lineage',params:{kind:params.kind,task_id:params.task_id,session_id:params.session_id,request_id:params.run_id??params.request_id,...(params.parent?{parent:grants.get(params.parent.id)}:{})}});
     assert.deepEqual(observation.route,{locator:target,operations:['send','inspect','cancel']});
     const grant={reference:{provider:'fixture',id:params.request_id},expires_at:params.expires_at,scope:{user_space_id:'local',product_domain:'personal',task_id:params.task_id,revision:observation.revision,context_digest:'host-bound-route',...observation}};
     grants.set(params.request_id,grant);return grant;
    }
    assert.equal(params.operation,'route-session');const request=params.route;assert.deepEqual(request.locator,target);assert(grants.has(params.reference.id));
    if(unavailable)throw Object.assign(Error('registered target unavailable'),{code:'session_host_offline'});
    const result=value=>({version:1,operation_id:request.operation_id,locator:target,status:'completed',result:value});
    const p=request.params;
    if(request.operation==='send'){
     if(concurrentHold&&!concurrentHold.started){const hold=concurrentHold;hold.started=true;hold.onStart();await hold.wait;sendOffline=true;throw Error('first concurrent send outcome unknown');}
     if(sendOffline)throw Object.assign(Error('offline before this retry'),{code:'session_host_offline'});
     routeInputs.push(structuredClone(request));
     if(loseBeforeAdmission){loseBeforeAdmission=false;throw Error('unconfirmed delivery before receipt');}
     assert.equal(p.session_id,target.session_id);assert.equal(p.request_id,request.operation_id);assert.equal(p.if_running,'queue');assert(!p.user_input_request_id);assert(!p.execution_authorization);
     if(!turns.has(p.request_id)){sends++;turns.set(p.request_id,{request_id:p.request_id,state:'queued',queue_id:`queue-${p.request_id}`});}
     if(loseSend){loseSend=false;throw Error('response lost after acceptance');}
     return result({session_id:target.session_id,...turns.get(p.request_id)});
    }
    if(request.operation==='inspect')return result({session:targetMetadata,turn:turns.get(p.request_id)});
    assert.equal(request.operation,'cancel');assert(p.turn_id||p.queue_id,'never whole-session stop');
    const turn=[...turns.values()].find(turn=>p.turn_id?turn.turn_id===p.turn_id:turn.queue_id===p.queue_id);assert(turn);turn.state='interrupted';cancels++;return result({session_id:target.session_id,cancelled:true});
   }
   default:throw Error(`Unexpected service ${service}/${method}`);
  }
 };
 async function start(){
  const child=spawn(process.execPath,[join(root,'internal/plugin/bundled/personal/runtime.mjs')],{stdio:['pipe','pipe','pipe']});children.add(child);
  let seq=0,stderr='';const pending=new Map();child.stderr.on('data',b=>stderr+=b);
  const send=frame=>{frames.push({direction:'host',frame});child.stdin.write(JSON.stringify(frame)+'\n');};
  createInterface({input:child.stdout}).on('line',line=>{const frame=JSON.parse(line);frames.push({direction:'plugin',frame});if(frame.method)host(frame).then(result=>send({id:frame.id,result}),error=>send({id:frame.id,error:{code:error.code??'fixture_error',message:error.message}}));else{const entry=pending.get(frame.id);if(!entry)return;pending.delete(frame.id);frame.error?entry.reject(Error(frame.error.message)):entry.resolve(frame.result);}});
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=`request-${++seq}`,timer=setTimeout(()=>{pending.delete(id);reject(Error(`Timeout ${method}: ${stderr}`));},10000);pending.set(id,{resolve:r=>{clearTimeout(timer);resolve(r)},reject:e=>{clearTimeout(timer);reject(e)}});send({id,method,params});});
  const client=(method,input={})=>call('capability.invoke',{capability:'plugin.client.request',input:{method,input},execution_id:`client-${seq}`}).then(r=>r.output.result);
  await call('initialize',{protocol_version:1,capability_protocol_version:3,lifecycle_version:2,plugin_id:'personal',plugin_root:scratch,project_root:scratch,wuu_home:scratch,workspace_state_dir:join(scratch,'state'),product_domain:'personal',user_space_id:'local',supported_host_services:['host.service.call']});
  await call('prepare');await call('activate');
  return {call,client,async close(){await call('shutdown');const exited=once(child,'exit');child.stdin.end();await exited;children.delete(child);assert.equal(child.exitCode,0,stderr)}};
 }
 t.after(async()=>{for(const child of children)child.kill();const out=join(root,'.tmp/e2e/personal-existing-session');await mkdir(out,{recursive:true});await writeFile(join(out,'protocol.jsonl'),frames.map(f=>JSON.stringify(f)).join('\n')+'\n');});
 runtime=await start();const {agent}=await runtime.client('agents.ensure_default');
 const create=()=>runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'existing-task',title:'Use existing context',goal:'Read the existing work and report',existing_session:target});
 loseSend=true;let {task}=await create();assert.equal(task.execution.status,'unknown');assert.equal(created,1);assert.equal(sends,1);assert(task.execution.dispatch_pending);
 ({task}=await create());assert.equal(sends,1,'retry does not invent a second run');
 const run=task.execution.request_id;turns.set(run,{request_id:run,state:'completed',turn_id:'worker-turn',final_output:'Which branch should I use?'});
 ({task}=await runtime.client('tasks.reconcile',{task_id:task.task_id}));assert.equal(task.state,'awaiting_review');assert.equal(task.result,undefined);assert.equal(task.worker_result.text,'Which branch should I use?');assert(!task.worker_result.verified_at);assert.equal(created,1);
 await runtime.close();runtime=await start();({task}=await runtime.client('tasks.get',{task_id:task.task_id}));assert.deepEqual(task.existing_session,target);
 ({task}=await runtime.client('tasks.update',{task_id:task.task_id,expected_revision:task.revision,goal:'Follow up in the same existing session'}));assert.equal(sends,2);assert.equal(created,1);assert.deepEqual(task.execution.existing_session,target);
 const secondRun=task.execution.request_id;turns.set(secondRun,{request_id:secondRun,state:'completed',turn_id:'second-turn',final_output:'Reviewed work completed with evidence.'});
 ({task}=await runtime.client('tasks.reconcile',{task_id:task.task_id}));assert.equal(task.state,'awaiting_review');
 const accept=(request_id,revision=task.revision)=>runtime.call('tool.execute',{tool_id:'accept_task_result',session_id:'personal-main',thread_id:'personal-main',turn_id:'controller-review',call_id:'accept-result',execution_id:'accept-result',arguments:{task_id:task.task_id,expected_revision:revision,request_id,summary:'Controller assessed the work against the goal',evidence:['The worker response addresses the requested follow-up']}}).then(r=>JSON.parse(r.result.content[0].text));
 await assert.rejects(accept('stale-run'),/no longer current/);
 ({task}=await accept(secondRun));assert.equal(task.state,'completed');assert.equal(task.result.origin,'controller_review');assert.equal(task.result.verified_at,undefined);assert.equal(task.controller_decision.request_id,secondRun);assert.equal(task.worker_result.text,'Reviewed work completed with evidence.');
 ({task}=await runtime.client('tasks.resume',{task_id:task.task_id,expected_revision:task.revision,goal:'One more bounded follow-up'}));assert.equal(sends,3);
 ({task}=await runtime.client('tasks.stop',{task_id:task.task_id,expected_revision:task.revision}));assert.equal(task.state,'stopped');assert.equal(cancels,1);assert.deepEqual(targetMetadata,original);
 unavailable=true;({task}=await runtime.client('tasks.resume',{task_id:task.task_id,expected_revision:task.revision,goal:'Check the existing target again'}));assert.equal(task.state,'waiting_external');assert.match(task.error,/unavailable/);assert.equal(created,1);assert.equal(task.execution.dispatch_pending,false);
 unavailable=false;({task}=await runtime.client('tasks.reconcile',{task_id:task.task_id}));assert.equal(task.execution.status,'submitted');assert.equal(sends,4);
 await assert.rejects(runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'existing-task',title:'Use existing context',goal:'Read the existing work and report',existing_session:{...target,session_id:'different'}}),/request_id_conflict/);
 ({task}=await runtime.client('tasks.stop',{task_id:task.task_id,expected_revision:task.revision}));
 loseBeforeAdmission=true;let uncertain=(await runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'unknown-empty-receipt',title:'Same identity retry',goal:'Return existing context',existing_session:target})).task;
 assert.equal(uncertain.execution.status,'unknown');const originalInput=routeInputs.at(-1);
 uncertain=(await runtime.client('tasks.reconcile',{task_id:uncertain.task_id})).task;assert.equal(uncertain.execution.status,'submitted');assert.deepEqual(routeInputs.at(-1),originalInput,'uncertain retry preserves exact payload and operation identity');
 loseBeforeAdmission=true;let stillUnknown=(await runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'unknown-then-offline',title:'Preserve original uncertainty',goal:'Read target context',existing_session:target})).task;
 sendOffline=true;stillUnknown=(await runtime.client('tasks.reconcile',{task_id:stillUnknown.task_id})).task;
 assert.equal(stillUnknown.execution.status,'unknown');assert.equal(stillUnknown.execution.dispatch_pending,true,'a later unsent retry cannot erase the earlier uncertain admission');
 stillUnknown=(await runtime.client('tasks.stop',{task_id:stillUnknown.task_id,expected_revision:stillUnknown.revision})).task;assert.equal(stillUnknown.state,'waiting_external','missing receipt cannot establish cancellation');
 sendOffline=false;turns.set(stillUnknown.execution.request_id,{request_id:stillUnknown.execution.request_id,state:'queued',queue_id:'late-original-queue'});
 stillUnknown=(await runtime.client('tasks.reconcile',{task_id:stillUnknown.task_id})).task;assert.equal(stillUnknown.state,'stopped');
 sendOffline=false;let startHeld,releaseHeld;const started=new Promise(resolve=>startHeld=resolve), held=new Promise(resolve=>releaseHeld=resolve);
 concurrentHold={started:false,wait:held,onStart:startHeld,release:releaseHeld};
 const concurrentInput={agent_id:agent.agent_id,request_id:'concurrent-unknown',title:'Concurrent retry',goal:'Read existing context',existing_session:target};
 const first=runtime.client('tasks.create',concurrentInput);await started;
 const second=runtime.client('tasks.create',concurrentInput);await Promise.all([first,second]);
 const concurrentTask=[...storage.values()].map(raw=>JSON.parse(raw)).find(value=>value.request_id==='concurrent-unknown');
 assert(concurrentReads>=2,'second dispatch captured pending state before first outcome');assert.equal(concurrentTask.execution.status,'unknown');assert.equal(concurrentTask.execution.dispatch_pending,true);
 concurrentHold=undefined;sendOffline=false;
 await runtime.close();
});
