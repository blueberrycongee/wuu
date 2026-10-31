import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import { expect, it, vi } from 'vitest';
import { AppServerClientPool } from './appServerClients';
import { RegisteredSessionTransport } from './registeredSessionTransport';
import type { ServerEvent, RuntimeContext } from '../shared/protocol';

// Exercise actual pool registration and JSONL generation routing. Refusal and
// response loss must never create a replacement target or borrow pipe ownership.
it('routes only an explicitly registered receiver and preserves source provenance and receipts', async () => {
 vi.stubEnv('WUU_DESKTOP_CORE','test-wuu-core');
 let loseReply=false;
 const calls: {cwd:string;method:string;params:any}[]=[];
 const children=new Map<string, any>(); const events:ServerEvent[]=[];
 const source:RuntimeContext={kind:'project',project_id:'source',cwd:'/tmp/wuu-route-source'};
 const target:RuntimeContext={kind:'project',project_id:'target',cwd:'/tmp/wuu-route-target'};
 const spawn=vi.fn((_command:any,_args:any,options:any)=>{
  const emitter=new EventEmitter();const child=Object.assign(emitter,{stdin:new PassThrough(),stdout:new PassThrough(),stderr:new PassThrough(),kill(){emitter.emit('exit',0);return true;}});
  let buffered=''; child.stdin.on('data',(bytes)=>{buffered+=String(bytes);let end;while((end=buffered.indexOf('\n'))>=0){const input=JSON.parse(buffered.slice(0,end));buffered=buffered.slice(end+1);if(!input.method)continue;calls.push({cwd:options.cwd,method:input.method,params:input.params});let result:any={};
   if(input.method==='initialize')result={session_federation:options.cwd===target.cwd?{version:1,execution_host:'coding-host',storage_host:'coding-host'}:undefined};
   if(input.method==='transport/dispatch'&&loseReply){child.stdout.write(JSON.stringify({id:input.id,error:{code:'lost',message:'Reply lost after receiver admission'}})+'\n');continue;}
   if(input.method==='transport/dispatch'){const request=input.params.params;result={version:1,generation:'receiver-generation',receipt:'receipt',message:{result:{version:1,operation_id:request.operation_id,locator:request.locator,status:'completed',result:request.operation==='list'?{sessions:[{session_id:'existing',name:'Existing coding'}]}:{session_id:'existing',request_id:'stable-run',status:'accepted'}}}};}
   child.stdout.write(JSON.stringify({id:input.id,result})+'\n');if(input.method==='shutdown')queueMicrotask(()=>child.emit('exit',0));
  }});children.set(options.cwd,child);return child as unknown as ChildProcessWithoutNullStreams;
 });
 const pool=new AppServerClientPool(()=>source,()=>source.cwd,event=>events.push(event),spawn,()=>({}));
 const adapter=new RegisteredSessionTransport(pool);
 try {
  await pool.requestInContext(source,'initialize',{});await pool.requestInContext(target,'initialize',{});
  const peer={version:1,peer_id:'source-grant',owner:false,principal:{kind:'user',issuer:'test',tenant:'one',subject:'owner'},validation:{address:'127.0.0.1:12345',token:'a'.repeat(64)}};
  const request={version:1,operation:'send',operation_id:'stable-run',locator:{version:1,execution_host:'coding-host',session_id:'existing'},params:{session_id:'existing',request_id:'stable-run',input:{prompt:'work'},if_running:'queue'}};
  children.get(source.cwd).stdout.write(JSON.stringify({id:'reverse',method:'session/transport.dispatch',params:{version:1,peer,request}})+'\n');
  await vi.waitFor(()=>expect(events.some(e=>e.kind==='server-request')).toBe(true));
  await adapter.handleServerRequest(events.find(e=>e.kind==='server-request')!);
  const forwarded=calls.find(c=>c.method==='transport/dispatch');expect(forwarded?.cwd).toBe(target.cwd);expect(forwarded?.params).toMatchObject({...peer,owner:false,method:'session/federation',params:request});
  expect(spawn).toHaveBeenCalledTimes(2);
  expect(await adapter.dispatch(peer as any,{...request,locator:{...request.locator,execution_host:'absent'}} as any)).toMatchObject({status:'completed',error:{code:'session_host_unavailable'}});
  expect(spawn).toHaveBeenCalledTimes(2);
  const catalog=await adapter.listTargets(peer as any);expect(catalog.targets).toEqual([{locator:{version:1,execution_host:'coding-host',storage_host:'coding-host',session_id:'existing'},title:'Existing coding',route_kind:'federation'}]);
  loseReply=true;
  const releasesBefore=calls.filter(c=>c.method==='transport/release').length;
  await adapter.listTargets({...peer,peer_id:'lost-discovery'} as any);
  expect(calls.filter(c=>c.method==='transport/release')).toHaveLength(releasesBefore+1);
  const sourceEvent=events.find(e=>e.kind==='server-request')!;
  expect(await adapter.dispatch({...peer,peer_id:'lost-send'} as any,request as any,sourceEvent)).toMatchObject({status:'unknown',operation_id:'stable-run'});
  children.get(source.cwd).stdout.write(JSON.stringify({id:'release-lost',method:'session/transport.release',params:{peer_id:'lost-send'}})+'\n');
  await vi.waitFor(()=>expect(events.some(e=>e.kind==='server-request'&&e.message.method==='session/transport.release')).toBe(true));
  await adapter.handleServerRequest(events.find(e=>e.kind==='server-request'&&e.message.method==='session/transport.release')!);
  expect(calls.some(c=>c.method==='transport/release'&&c.params.peer_id==='lost-send')).toBe(true);
  expect(spawn).toHaveBeenCalledTimes(2);

 } finally {await pool.shutdown();vi.unstubAllEnvs();}
});

it.skipIf(!process.env.WUU_REGISTERED_SESSION_TEST_BINARY)('runs a selected native source through registered desktop routing into an existing real receiver session', async () => {
 const {spawn}=await import('node:child_process');const {mkdtemp,writeFile,mkdir}=await import('node:fs/promises');const {tmpdir}=await import('node:os');const {join}=await import('node:path');
 const root=await mkdtemp(join(tmpdir(),'wuu-registered-control-'));const source:RuntimeContext={kind:'project',project_id:'source',cwd:join(root,'source')};const target:RuntimeContext={kind:'project',project_id:'target',cwd:join(root,'target')};await mkdir(source.cwd);await mkdir(target.cwd);
 vi.stubEnv('WUU_DESKTOP_CORE',process.env.WUU_REGISTERED_SESSION_TEST_BINARY!);
 let adapter:RegisteredSessionTransport;const events:ServerEvent[]=[];const children:ChildProcessWithoutNullStreams[]=[];
 const pool=new AppServerClientPool(()=>source,()=>source.cwd,event=>{events.push(event);adapter?.handleConnectionEvent(event);if(event.kind==='server-request')void adapter.handleServerRequest(event);},(_command,_args,options)=>{const child=spawn(process.env.WUU_REGISTERED_SESSION_TEST_BINARY!,['-test.run=^TestRegisteredSessionTransportProcess$'],{cwd:options.cwd,env:{...options.env,WUU_REGISTERED_SESSION_ROLE:options.cwd===target.cwd?'target':'source'},stdio:['pipe','pipe','pipe']});children.push(child);return child;},()=>({capabilities:{reverse_rpc:{methods:['session/transport.dispatch','session/transport.list-targets','session/transport.release']}}}));
 adapter=new RegisteredSessionTransport(pool);
 const evidence:any={checks:[]};
 try{
  await pool.requestInContext(source,'initialize',{});await pool.requestInContext(target,'initialize',{});
  // Keep the negotiated source reverse methods when issuing a later initialize.
  await pool.requestInContext(source,'initialize',{capabilities:{reverse_rpc:{methods:['session/transport.dispatch','session/transport.list-targets','session/transport.release']}}});
  const existing=await pool.requestInContext<{session_id:string}>(target,'fixture/existing');
  const catalog=await pool.requestInContext<any>(source,'fixture/catalog',{});expect(catalog.targets.some((entry:any)=>entry.route_kind==='owner-local'&&entry.locator.session_id===existing.session_id)).toBe(true);
  const locator={version:1 as const,execution_host:'registered-coding',session_id:existing.session_id};
  const send={version:1,operation:'send',operation_id:'existing-run',locator,params:{session_id:existing.session_id,request_id:'existing-run',input:{prompt:'Return the result in the existing session'},if_running:'queue'}};
  const accepted=await pool.requestInContext<any>(source,'fixture/route',send);expect(accepted.status).toBe('completed');expect(accepted.error).toBeUndefined();evidence.checks.push('actual registered receiver accepted existing-session send');
  const replay=await pool.requestInContext<any>(source,'fixture/route',send);expect(replay.result).toEqual(accepted.result);evidence.checks.push('stable operation replay preserves original request');
  const inspected=await pool.requestInContext<any>(source,'fixture/route',{version:1,operation:'inspect',operation_id:'inspect-existing',locator,params:{session_id:existing.session_id,request_id:'existing-run',wait:'terminal',timeout_ms:10000}});
  expect(inspected.status).toBe('completed');expect(inspected.error).toBeUndefined();expect(inspected.result.turn.state).toBe('completed');expect(JSON.stringify(inspected.result)).toContain('existing Coding result');evidence.checks.push('terminal result returns across real source/native/desktop/receiver path');
  expect(children).toHaveLength(2);evidence.checks.push('no replacement core or worker created');
  await pool.requestInContext(target,'fixture/revoke');
  const denied=await pool.requestInContext<any>(source,'fixture/route',{...send,operation_id:'denied-run',params:{...send.params,request_id:'denied-run'}});expect(denied.error).toBeDefined();evidence.checks.push('receiver policy revocation denies later control without owner fallback');
  evidence.passed=true;
 }finally{await pool.shutdown();for(const child of children)child.kill();vi.unstubAllEnvs();if(process.env.WUU_REGISTERED_SESSION_EVIDENCE)await writeFile(process.env.WUU_REGISTERED_SESSION_EVIDENCE,JSON.stringify(evidence,null,2));}
},60000);

it.skipIf(!process.env.WUU_REGISTERED_SESSION_TEST_BINARY).each(['owner-local','federation'])('runs conversational Personal task through %s after its main turn has ended', async (routeKind) => {
 const {spawn}=await import('node:child_process');const {mkdtemp,writeFile,mkdir,readFile}=await import('node:fs/promises');const {tmpdir}=await import('node:os');const {join}=await import('node:path');
 const root=await mkdtemp(join(tmpdir(),'wuu-registered-control-'));const source:RuntimeContext={kind:'user',user_space_id:'local',product_domain:'personal',cwd:join(root,'source')};const target:RuntimeContext={kind:'project',project_id:'target',cwd:join(root,'target')};await mkdir(source.cwd);await mkdir(target.cwd);
 vi.stubEnv('WUU_DESKTOP_CORE',process.env.WUU_REGISTERED_SESSION_TEST_BINARY!);
 let adapter:RegisteredSessionTransport;const events:ServerEvent[]=[];const children:ChildProcessWithoutNullStreams[]=[];
 const pool=new AppServerClientPool(()=>source,()=>source.cwd,event=>{events.push(event);adapter?.handleConnectionEvent(event);if(event.kind==='server-request')void adapter.handleServerRequest(event);},(_command,_args,options)=>{const child=spawn(process.env.WUU_REGISTERED_SESSION_TEST_BINARY!,['-test.run=^TestRegisteredSessionTransportProcess$'],{cwd:options.cwd,env:{...options.env,WUU_REGISTERED_SESSION_ROLE:options.cwd===target.cwd?(routeKind==='owner-local'?'target-owner':'target-delayed-federation'):'personal-source',WUU_REGISTERED_PERSONAL_DESCRIPTOR:join(root,'personal.json'),WUU_REGISTERED_TEST_ROUTE_KIND:routeKind},stdio:['pipe','pipe','pipe','pipe']}) as unknown as ChildProcessWithoutNullStreams;children.push(child);return child;},()=>({capabilities:{reverse_rpc:{methods:['session/transport.dispatch','session/transport.list-targets','session/transport.release']}}}));
 adapter=new RegisteredSessionTransport(pool);

 const evidence:any={checks:[]};
 try {
  await pool.requestInContext(source,'initialize',{});const targetInit=await pool.requestInContext<any>(target,'initialize',{});if(routeKind==='owner-local')expect(targetInit.session_federation).toBeUndefined();else expect(targetInit.session_federation).toBeDefined();
  await pool.requestInContext(source,'initialize',{capabilities:{reverse_rpc:{methods:['session/transport.dispatch','session/transport.list-targets','session/transport.release']}}});
  const descriptor=JSON.parse(await readFile(join(root,'personal.json'),'utf8'));
  const client=async(method:string,input:unknown={})=> (await pool.requestInContext<any>(source,'plugin/client/request',{...descriptor,user_space_id:'local',product_domain:'personal',method,input})).result;
  const {agent}=await client('agents.ensure_default');
  const existing=await pool.requestInContext<{session_id:string}>(target,'fixture/existing');
  await pool.requestInContext<any>(source,'plugin/client/request',{...descriptor,user_space_id:'local',product_domain:'personal',method:'agents.send',input:{agent_id:agent.agent_id,request_id:'discover-existing',prompt:'Use my existing Coding workspace to read the existing work and return its result here.'},user_input:{request_id:'discover-existing',prompt:'Use my existing Coding workspace to read the existing work and return its result here.'}});
  let rawCatalog='';for(let attempt=0;attempt<50&&!rawCatalog;attempt++){try{rawCatalog=await readFile(join(root,'personal.json.catalog'),'utf8');}catch{await new Promise(resolve=>setTimeout(resolve,100));}}
  const findCatalog=(value:any):any=>{if(typeof value==='string'){try{return findCatalog(JSON.parse(value));}catch{return undefined;}}if(value&&typeof value==='object'){if(Array.isArray(value.targets))return value;for(const entry of Object.values(value)){const result=findCatalog(entry);if(result)return result;}}};
  const catalog=findCatalog(JSON.parse(rawCatalog));expect(catalog).toBeDefined();
  const available=catalog.targets.filter((entry:any)=>entry.route_kind===routeKind&&entry.title==='Existing Coding workspace');expect(available).toHaveLength(1);
  const locator=available[0].locator;expect(locator.session_id).toBe(existing.session_id);expect(locator.execution_host).toBe((routeKind==='owner-local'?targetInit.owner_local_session_control:targetInit.session_federation).execution_host);
  evidence.checks.push(`actual Personal list_sessions discovers named ${routeKind} target through selected catalog`);
  let createdReceipt='';for(let attempt=0;attempt<100&&!createdReceipt;attempt++){try{createdReceipt=await readFile(join(root,'personal.json.created'),'utf8');}catch{await new Promise(resolve=>setTimeout(resolve,100));}}expect(createdReceipt).toContain('actual-personal-task');
  let task:any;for(let attempt=0;attempt<50&&!task?.execution?.existing_session;attempt++){const listed=await client('tasks.list',{agent_id:agent.agent_id});task=listed.tasks.find((entry:any)=>entry.request_id==='actual-personal-task');if(!task?.execution?.existing_session)await new Promise(resolve=>setTimeout(resolve,100));}
  expect(task).toBeDefined();expect(task.source.message_binding).toBe('host_turn');expect(task.source.original_text).toBe('Use my existing Coding workspace to read the existing work and return its result here.');
  evidence.checks.push('same Personal conversation invokes actual create_task tool with observed catalog locator and original human request provenance');
  expect(task.execution.existing_session).toMatchObject(locator);evidence.checks.push(`generated Personal task selected exact existing ${routeKind} target`);
  await vi.waitFor(()=>expect(events.some(event=>event.kind==='notification'&&event.message.method==='turn/completed'&&(event.message.params as any)?.thread_id===agent.main_session_id)).toBe(true),{timeout:10000});
  await pool.requestInContext(target,'fixture/release');
  evidence.checks.push('main Personal model turn and create_task callback ended before target model completed');
  const run=task.execution.request_id;
  expect(run).toBeTruthy();
  for(let attempt=0;attempt<30 && !task.worker_result?.text;attempt++){({task}=await client('tasks.reconcile',{task_id:task.task_id}));if(!task.worker_result?.text)await new Promise(resolve=>setTimeout(resolve,100));}
  expect(task.worker_result,JSON.stringify(task)).toBeDefined();expect(task.worker_result.text).toContain('existing Coding result');
  expect(task.execution.request_id).toBe(run);expect(task.state).toBe('awaiting_review');
  evidence.checks.push('actual Personal lineage/grant/restore gateway reached selected native transport');
  evidence.checks.push('registered Coding result returned into actual Personal task worker_result');
  const replay=await client('tasks.create',{agent_id:agent.agent_id,request_id:'actual-personal-task',title:'Use existing Coding session',goal:'Read existing work and report the result',existing_session:locator});
  expect(replay.task.task_id).toBe(task.task_id);expect(replay.task.execution.request_id).toBe(run);
  expect(children).toHaveLength(2);evidence.checks.push('same task replay retains run identity without creating target or core');
  evidence.passed=true;
 } finally {await pool.shutdown();for(const child of children)child.kill();vi.unstubAllEnvs();if(process.env.WUU_REGISTERED_SESSION_EVIDENCE)await writeFile(process.env.WUU_REGISTERED_SESSION_EVIDENCE+(routeKind==='owner-local'?'.personal.json':'.personal-federation.json'),JSON.stringify(evidence,null,2));}
},60000);
