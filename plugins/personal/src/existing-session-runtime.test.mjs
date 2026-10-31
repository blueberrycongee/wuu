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
 let routingSupported=true,reviewSupported=false,reviewName='fixture_direct_review',revokedReview,bindingChangesAfter=0;
 let grantHold;
 let autoRecover=false,backgroundDone;
 let sourceRecords=[];
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
   case 'host.background':if(autoRecover)backgroundDone=runtime.call('service.invoke',{service:'personal.recovery',method:'run',caller:'personal',execution_id:'background-recovery',params:{}}).catch(error=>error);return {job_id:'background',execution_id:'background'};
   case 'host.session.cancel':assert.equal(params.session_id,'personal-main');return {session_id:params.session_id,cancelled:true};
   case 'host.personal':if(params.operation==='owner.check')return {ok:true,value:{}};throw Error(`Unexpected Personal effect ${params.operation}`);
   case 'agent.tools':if(bindingChangesAfter&&!--bindingChangesAfter)reviewName='changed_review_binding';assert.equal(method,'resolve-own');assert.equal(params.tool_id,'review_task_response');return {found:true,name:reviewName,direct_only:true};
   case 'session.transport':assert.equal(method,'list-targets');return {version:1,targets:[{locator:target,title:'Existing Coding'}],unavailable:[]};
   case 'host.session.create':{created++;assert.equal(created,1,'only main binding may be created');const session_id='personal-main';contexts[session_id]={session_id,...params};return {session_id,created:true};}
   case 'host.session.context':return contexts[params.session_id];
   case 'host.session.inspect':assert.equal(params.session_id,'personal-main');if(params.request_id===revokedReview&&params.execution_authorization)throw Object.assign(Error('review grant revoked'),{code:'execution_authorization_required'});return {session:contexts['personal-main'],turn:params.turn_id?[...turns.values()].find(turn=>turn.turn_id===params.turn_id):params.request_id?turns.get(params.request_id):[...turns.values()].findLast(turn=>turn.turn_id?.startsWith('review-')&&turn.state==='running')};
   case 'host.session.send':assert.equal(params.session_id,'personal-main');assert.equal(params.presentation?.related_session_id,undefined,'external target must not become an owned local session relation');if(params.tool_policy){assert.deepEqual(params.tool_policy,{allow:[reviewName]});assert(!params.disable_tools);const turn={request_id:params.request_id,state:'running',turn_id:`review-${params.request_id}`};turns.set(params.request_id,turn);return {session_id:params.session_id,...turn};}assert.equal(params.disable_tools,true);turns.set(params.request_id,{request_id:params.request_id,state:'completed',turn_id:`feedback-${params.request_id}`});return {session_id:params.session_id,state:'running',turn_id:`feedback-${params.request_id}`};
   case 'host.session.history.read':return {session_id:params.session_id,records:sourceRecords.filter(record=>record.seq===params.start_seq),head_seq:0,snapshot_seq:0,has_more:false};
   case 'host.execution.authorization':{
    if(params.operation==='describe')return {default_lifetime_seconds:3600,max_lifetime_seconds:3600,authority_kinds:['fixture'],scope_kinds:['task-lineage','exact-run'],...(routingSupported?{host_features:['session-route.v1',...(reviewSupported?['session-turn-tool-policy.v1']:[])]}:{})};
    if(params.operation==='issue'){
     if(params.kind==='exact-run'&&grantHold&&!grantHold.started){grantHold.started=true;grantHold.onStart();await grantHold.wait;}
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
  // Activation may restore a persisted grant through the current process.
  runtime={call,client};
  await call('initialize',{protocol_version:1,capability_protocol_version:3,lifecycle_version:2,plugin_id:'personal',plugin_root:scratch,project_root:scratch,wuu_home:scratch,workspace_state_dir:join(scratch,'state'),product_domain:'personal',user_space_id:'local',supported_host_services:['host.service.call']});
  await call('prepare');await call('activate');
  return {call,client,async crash(){const exited=once(child,'exit');child.kill('SIGKILL');await exited;children.delete(child);for(const entry of pending.values())entry.reject(Error('fixture process crashed'));pending.clear();},async close(){await call('shutdown');const exited=once(child,'exit');child.stdin.end();await exited;children.delete(child);assert.equal(child.exitCode,0,stderr)}};
 }
 t.after(async()=>{for(const child of children)child.kill();const out=join(root,'.tmp/e2e/personal-existing-session');await mkdir(out,{recursive:true});await writeFile(join(out,'protocol.jsonl'),frames.map(f=>JSON.stringify(f)).join('\n')+'\n');});
 runtime=await start();const {agent}=await runtime.client('agents.ensure_default');
 const create=()=>runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'existing-task',title:'Use existing context',goal:'Read the existing work and report',existing_session:target});
 routingSupported=false;await assert.rejects(create(),/routing is unavailable/);assert.equal(created,1);assert.equal(sends,0);assert(![...storage.values()].map(raw=>JSON.parse(raw)).some(value=>value.request_id==='existing-task'));routingSupported=true;
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
 const createFromSource=(request_id)=>runtime.call('tool.execute',{tool_id:'create_task',session_id:'personal-main',thread_id:'personal-main',turn_id:'source-turn',call_id:request_id,execution_id:request_id,source_message:{session_id:'personal-main',seq:7},arguments:{request_id,title:'Original wording',goal:'Controller reformulation',existing_session:target}}).then(r=>JSON.parse(r.result.content[0].text));
 sourceRecords=[{seq:7,role:'user',origin:'user',content:'原话：请检查这个已有会话，不要新建。'}];
 const sourceTask=(await createFromSource('original-text')).task;
 assert.equal(sourceTask.source.original_text,sourceRecords[0].content);
 assert(routeInputs.at(-1).params.input.prompt.includes(sourceRecords[0].content));
 assert(routeInputs.at(-1).params.input.prompt.includes('Controller task goal (not a verbatim user quote)'));
 sourceRecords=[{...sourceRecords[0],attachments:[{path:'/private/source-only.pdf'}]}];
 const beforeAttachment=sends, blocked=(await createFromSource('attachment-text-only')).task;
 assert.equal(sends,beforeAttachment);assert.equal(blocked.execution.dispatch_pending,false);assert.match(blocked.error,/text-only/);
 assert.equal((await runtime.client('tasks.get',{task_id:blocked.task_id})).task.task_id,blocked.task_id);
 await assert.rejects(runtime.call('tool.execute',{tool_id:'update_task',session_id:'personal-main',thread_id:'personal-main',turn_id:'followup-turn',call_id:'attached-followup',execution_id:'attached-followup',source_message:{session_id:'personal-main',seq:7},arguments:{task_id:blocked.task_id,expected_revision:blocked.revision,goal:'Read the attached file'}}),/text-only/);
 assert.equal(sends,beforeAttachment);
 assert.equal((await runtime.client('tasks.get',{task_id:blocked.task_id})).task.revision,blocked.revision);
 const revised=(await runtime.client('tasks.update',{task_id:blocked.task_id,expected_revision:blocked.revision,goal:'The files are already at the target; use its existing context'})).task;
 assert.equal(sends,beforeAttachment+1);assert.equal(revised.state,'running');assert(!revised.previous_executions[0].cancel_pending);
 // Review is an intent inside a host-bound isolated main turn, never immediate authority.
 reviewSupported=true;
 const reviewCreate=async request_id=>(await runtime.client('tasks.create',{agent_id:agent.agent_id,request_id,title:'Scoped review',goal:'Finish the originally authorized task',existing_session:target})).task;
 const workerFinish=async(task,text)=>{turns.set(task.execution.request_id,{request_id:task.execution.request_id,state:'completed',turn_id:`worker-${task.execution.request_id}`,final_output:text});return (await runtime.client('tasks.reconcile',{task_id:task.task_id})).task;};
 const decide=(task,decision,extra={})=>runtime.call('tool.execute',{tool_id:'review_task_response',tool:task.delivery.review.tool_name,session_id:'personal-main',thread_id:'personal-main',turn_id:turns.get(task.delivery.request_id).turn_id,call_id:`decision-${task.delivery.request_id}`,execution_id:'controller-intent',arguments:{task_id:task.task_id,requirements_revision:task.requirements_revision,worker_request_id:task.execution.request_id,decision,summary:'Controller assessment within the original objective',evidence:['The exact worker response supports this decision'],...extra}});
 const endReview=async(task,state='completed')=>{const turn=turns.get(task.delivery.request_id);turn.state=state;turn.final_output='Review intent recorded';return (await runtime.client('tasks.reconcile',{task_id:task.task_id})).task;};
 let reviewed=await workerFinish(await reviewCreate('automatic-followup'),'Which branch should I inspect?');
 const surface=tools=>runtime.call('capability.invoke',{capability:'agent.request.transform',execution_id:'review-surface',input:{session_id:'personal-main',step_index:0,request:{version:1,model:'fixture',messages:[],tools}}});
 await runtime.call('capability.invoke',{capability:'agent.request.transform',execution_id:'unrelated-surface',input:{session_id:'unrelated-session',step_index:0,request:{version:1,model:'fixture',messages:[],tools:[{name:'ordinary-tool',input_schema:{}}]}}});
 await surface([{name:reviewName,input_schema:{}}]);await assert.rejects(surface([{name:'execute_code',input_schema:{}}]),/effective isolated model surface/);await assert.rejects(surface([{name:reviewName,input_schema:{}},{name:'unrelated',input_schema:{}}]),/effective isolated model surface/);
 assert.equal(reviewed.delivery.kind,'review');const originalReviewRun=reviewed.execution.request_id,originalRoot=reviewed.authorization.reference.id,reviewSends=sends;
 await assert.rejects(decide(reviewed,'accept',{task_id:'another-task'}),/target or revision/);
 await assert.rejects(runtime.call('tool.execute',{tool_id:'create_task',tool:'unrelated',session_id:'personal-main',turn_id:turns.get(reviewed.delivery.request_id).turn_id,call_id:'wrong-tool',arguments:{}}),/cannot invoke unrelated/);
 await decide(reviewed,'follow_up',{follow_up:'Use the branch already named in the original session context; do not change branches'});
 assert.equal(sends,reviewSends);assert.equal((await runtime.client('tasks.get',{task_id:reviewed.task_id})).task.state,'awaiting_review');
 const firstReviewRequest=reviewed.delivery.request_id;turns.get(firstReviewRequest).state='completed';turns.get(firstReviewRequest).final_output='Decision recorded';await runtime.call('capability.invoke',{capability:'agent.turn.lifecycle',input:{thread_id:'personal-main',request_id:firstReviewRequest,state:'completed',final_output:'Decision recorded'},execution_id:'review-terminal'});assert.equal(sends,reviewSends,'review callback only persists the continuation outbox');
 reviewed=(await runtime.client('tasks.reconcile',{task_id:reviewed.task_id})).task;assert.equal(reviewed.requirements_revision,1);assert.equal(reviewed.authorization.reference.id,originalRoot);assert.notEqual(reviewed.execution.request_id,originalReviewRun);assert.deepEqual(reviewed.existing_session,target);assert.equal(reviewed.goal,'Finish the originally authorized task');assert.equal(reviewed.previous_executions.at(-1).worker_result.text,'Which branch should I inspect?');assert.equal(sends,reviewSends+1);
 reviewed=await workerFinish(reviewed,'Finished the requested work with evidence');await decide(reviewed,'accept');assert.equal((await runtime.client('tasks.get',{task_id:reviewed.task_id})).task.result,undefined);
 reviewed=await endReview(reviewed);assert.equal(reviewed.state,'completed');assert.equal(reviewed.result.origin,'controller_review');assert(!reviewed.result.verified_at);assert.equal(reviewed.worker_result.text,'Finished the requested work with evidence');
 let questionReview=await workerFinish(await reviewCreate('automatic-question'),'An owner choice is missing');await decide(questionReview,'ask_user',{question:'Which repository should I use?'});questionReview=await endReview(questionReview);assert.equal(questionReview.state,'waiting_user');assert.equal(questionReview.question.text,'Which repository should I use?');
 let interruptedReview=await workerFinish(await reviewCreate('automatic-interrupted'),'Partial result');const interruptedSends=sends;await decide(interruptedReview,'follow_up',{follow_up:'Continue only the original task'});interruptedReview=await endReview(interruptedReview,'interrupted');assert.equal(interruptedReview.state,'awaiting_review');assert.equal(interruptedReview.delivery.status,'failed');assert.equal(sends,interruptedSends);const interruptedRequest=interruptedReview.delivery.request_id;turns.get(interruptedRequest).state='completed';await runtime.call('capability.invoke',{capability:'agent.turn.lifecycle',input:{thread_id:'personal-main',request_id:interruptedRequest,state:'completed'},execution_id:'late-interrupted-review'});assert.equal(sends,interruptedSends);assert.equal((await runtime.client('tasks.get',{task_id:interruptedReview.task_id})).task.state,'awaiting_review');
 // Retrying a failed review creates a new bound turn, never reuses its old intent.
 const interruptedOld=interruptedReview.delivery.request_id;
 interruptedReview=(await runtime.client('tasks.get',{task_id:interruptedReview.task_id})).task;
 interruptedReview=(await runtime.client('tasks.reconcile',{task_id:interruptedReview.task_id,expected_revision:interruptedReview.revision,retry_delivery:true})).task;
 assert.notEqual(interruptedReview.delivery.request_id,interruptedOld);assert.equal(interruptedReview.delivery.review.intent,undefined);assert.equal(interruptedReview.delivery.review.worker_request_id,interruptedReview.execution.request_id);
 await decide(interruptedReview,'accept');interruptedReview=await endReview(interruptedReview);assert.equal(interruptedReview.state,'completed');
 let emptyReview=await workerFinish(await reviewCreate('automatic-no-decision'),'Worker completed');emptyReview=await endReview(emptyReview);assert.equal(emptyReview.delivery.status,'failed');const emptyOld=emptyReview.delivery.request_id;
 emptyReview=(await runtime.client('tasks.reconcile',{task_id:emptyReview.task_id,expected_revision:emptyReview.revision,retry_delivery:true})).task;assert.notEqual(emptyReview.delivery.request_id,emptyOld);assert.equal(emptyReview.delivery.status,'submitted');await decide(emptyReview,'accept');emptyReview=await endReview(emptyReview);assert.equal(emptyReview.state,'completed');
 let unavailableReview=await reviewCreate('automatic-binding-unavailable');bindingChangesAfter=2;unavailableReview=await workerFinish(unavailableReview,'Worker result retained');assert.equal(unavailableReview.state,'awaiting_review');assert.equal(unavailableReview.delivery.status,'failed');assert.match(unavailableReview.delivery.error,/tool is unavailable/);assert.equal(unavailableReview.delivery.authorization,undefined);assert.equal(turns.has(unavailableReview.delivery.request_id),false);const unavailableRevision=unavailableReview.revision;
 unavailableReview=(await runtime.client('tasks.reconcile',{task_id:unavailableReview.task_id})).task;assert.equal(unavailableReview.revision,unavailableRevision,'background recovery does not repeatedly retry a definite unsupported review');reviewName='fixture_direct_review';
 unavailableReview=(await runtime.client('tasks.reconcile',{task_id:unavailableReview.task_id,expected_revision:unavailableReview.revision,retry_delivery:true})).task;await decide(unavailableReview,'accept');unavailableReview=await endReview(unavailableReview);assert.equal(unavailableReview.state,'completed');
 let unknownReview=await workerFinish(await reviewCreate('automatic-unknown-review'),'Retain uncertain review admission');const unknownRequest=unknownReview.delivery.request_id,unknownTurn=turns.get(unknownRequest);turns.delete(unknownRequest);reviewName='changed_review_binding';
 unknownReview=(await runtime.client('tasks.reconcile',{task_id:unknownReview.task_id})).task;assert.equal(unknownReview.delivery.status,'submitted');assert.match(unknownReview.delivery.error,/tool is unavailable/);assert(unknownReview.delivery.authorization,'missing receipt cannot erase possible admission');
 unknownReview=(await runtime.client('tasks.stop',{task_id:unknownReview.task_id,expected_revision:unknownReview.revision})).task;assert(unknownReview.previous_deliveries.some(delivery=>delivery.request_id===unknownRequest&&delivery.cancel_pending),'stop retains exact uncertain review cleanup');reviewName='fixture_direct_review';turns.set(unknownRequest,{...unknownTurn,state:'interrupted'});await runtime.client('tasks.reconcile',{task_id:unknownReview.task_id});
 let staleReview=await workerFinish(await reviewCreate('automatic-revoked'),'Claimed complete');await decide(staleReview,'accept');revokedReview=staleReview.delivery.request_id;staleReview=await endReview(staleReview);assert.notEqual(staleReview.state,'completed');assert.equal(staleReview.authorization_state,'needs_authorization');revokedReview=undefined;
 let stoppedReview=await workerFinish(await reviewCreate('automatic-stopped'),'Claimed complete');await decide(stoppedReview,'accept');const stoppedRequest=stoppedReview.delivery.request_id;await runtime.client('tasks.stop',{task_id:stoppedReview.task_id,expected_revision:(await runtime.client('tasks.get',{task_id:stoppedReview.task_id})).task.revision});turns.get(stoppedRequest).state='completed';await runtime.call('capability.invoke',{capability:'agent.turn.lifecycle',input:{thread_id:'personal-main',request_id:stoppedRequest,state:'completed',final_output:'Late review'},execution_id:'late-review'});assert.notEqual((await runtime.client('tasks.get',{task_id:stoppedReview.task_id})).task.state,'completed');
 reviewSupported=false;
 let grantStarted,grantRelease;const grantReady=new Promise(resolve=>grantStarted=resolve),grantWait=new Promise(resolve=>grantRelease=resolve);
 grantHold={started:false,onStart:grantStarted,wait:grantWait};
 const sendsBeforeGrantRace=sends;
 const racedCreate=runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'grant-admission-race',title:'Concurrent grant issuance',goal:'Read existing context',existing_session:target});
 await grantReady;
 const pendingGrant=[...storage.values()].map(raw=>JSON.parse(raw)).find(value=>value.request_id==='grant-admission-race');
 assert(pendingGrant.authorization);assert(!pendingGrant.execution.authorization);
 await runtime.client('tasks.reconcile',{task_id:pendingGrant.task_id});
 grantRelease();await racedCreate;grantHold=undefined;
 const admittedGrant=(await runtime.client('tasks.get',{task_id:pendingGrant.task_id})).task;
 assert.equal(admittedGrant.authorization_state,undefined,'in-flight run grant must not poison authorized task recovery');
 assert.equal(admittedGrant.execution.status,'submitted');assert.equal(sends,sendsBeforeGrantRace+1,'concurrent grant completion keeps one receiver request');
 turns.set(admittedGrant.execution.request_id,{request_id:admittedGrant.execution.request_id,state:'completed',turn_id:'grant-race-result',final_output:'Result after grant admission'});
 assert.equal((await runtime.client('tasks.reconcile',{task_id:admittedGrant.task_id})).task.state,'awaiting_review');
 let crashReady;grantHold={started:false,onStart:()=>crashReady(),wait:new Promise(()=>{})};
 const readyToCrash=new Promise(resolve=>crashReady=resolve);
 const interruptedCreate=runtime.client('tasks.create',{agent_id:agent.agent_id,request_id:'grant-crash-recovery',title:'Recover grant issuance',goal:'Read existing context',existing_session:target}).catch(error=>error);
 await readyToCrash;
 const persistedPending=[...storage.values()].map(raw=>JSON.parse(raw)).find(value=>value.request_id==='grant-crash-recovery');
 assert(persistedPending.authorization);assert(!persistedPending.execution.authorization);
 await runtime.crash();assert.match((await interruptedCreate).message,/crashed/);grantHold=undefined;
 autoRecover=true;runtime=await start();
 let afterCrash;for(let attempt=0;attempt<100;attempt++){afterCrash=(await runtime.client('tasks.get',{task_id:persistedPending.task_id})).task;if(afterCrash.execution.status==='submitted')break;await new Promise(resolve=>setTimeout(resolve,20));}
 assert.equal(afterCrash.authorization_state,undefined);assert.equal(afterCrash.execution.status,'submitted');
 assert.equal(afterCrash.execution.request_id,persistedPending.execution.request_id,'restart retains the authorizing run identity');
 await runtime.close();assert.equal((await backgroundDone) instanceof Error,false,'shutdown drains the tracked recovery service');
 for(const [key,raw] of storage){const record=JSON.parse(raw);if(record.agent_id===agent.agent_id&&record.task_id){record.state='stopped';record.execution.finished_state='interrupted';record.previous_executions=[];record.previous_deliveries=[];record.delivery=undefined;if(record.task_id===afterCrash.task_id){record.state='completed';record.execution.finished_state='completed';record.delivery={kind:'notice',request_id:'pending-paused-feedback',status:'pending',prompt:'Do not deliver while owner is paused'};}storage.set(key,JSON.stringify(record));}else if(record.agent_id===agent.agent_id&&record.main_session_id){record.status='paused';storage.set(key,JSON.stringify(record));}}
 runtime=await start();
 let pausedTask;for(let attempt=0;attempt<100;attempt++){pausedTask=[...storage.values()].map(raw=>JSON.parse(raw)).find(record=>record.task_id===afterCrash.task_id);if(pausedTask.delivery?.status==='suppressed')break;await new Promise(resolve=>setTimeout(resolve,20));}
 assert.equal(pausedTask.delivery.status,'suppressed','post-activation cleanup suppresses terminal routed feedback for paused owner');


 await runtime.close();assert.equal((await backgroundDone) instanceof Error,false);
});
