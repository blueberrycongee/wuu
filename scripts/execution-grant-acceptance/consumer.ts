/** Independent task consumer / negative-path probe; uses only public SDK calls. */
import {readFile,writeFile,rename,appendFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {executionGrants, requireExecutionGrants, requireKernelService, requireBackgroundService, startBackground, cancelBackground,
  type RuntimePlugin, type RuntimeHost, type ExecutionAuthorizationGrant as Grant, type ExecutionAuthorizationReference as Reference, type ExecutionLineageRequest,
} from '@wuu/plugin-sdk';
interface Task {id:string;main:string;worker:string;run:string;revision:number;digest:string;root?:Pick<Grant,'reference'|'expires_at'>;grant?:Pick<Grant,'reference'|'expires_at'>;}
export function createConsumer(options:{statePath:string;gatePath:string;auditPath:string}):RuntimePlugin {
  const load=async()=>{try{return JSON.parse(await readFile(options.statePath,'utf8')) as Task;}catch(error){if((error as NodeJS.ErrnoException).code==='ENOENT')return undefined;throw error;}};
  const save=async(task:Task)=>{await writeFile(options.statePath+'.tmp',JSON.stringify(task));await rename(options.statePath+'.tmp',options.statePath);};
  const audit=(event:string,data:unknown)=>appendFile(options.auditPath,JSON.stringify({at:new Date().toISOString(),pid:process.pid,event,data})+'\n');
  async function issue(host:RuntimeHost,input:any) {
    const task=(await load())!;if(!task)throw new Error('prepare task first');
    const kind=input.kind??'exact-run';
    const grant=await executionGrants(host).issue({kind,authority_service:'fixture.tasks',task_id:task.id,session_id:kind==='exact-run'?task.worker:task.main,request_id:input.request_id??task.run,expires_at:input.expires_at??new Date(Date.now()+3600_000).toISOString(),max_depth:2,requested_actions:input.actions??['user/session/send','user/session/inspect','user/session/cancel','service/fixture.effects/record'],...(input.parent?{parent:input.parent}: {})});
    const saved={reference:grant.reference,expires_at:grant.expires_at};if(kind==='exact-run')task.grant=saved;else task.root=saved;await save(task);return grant;
  }
  async function effect(host:RuntimeHost,input:{reference?:Reference;tag:string}) {
    const task=(await load())!;return executionGrants(host).callService({reference:input.reference??task.grant!.reference,service:'fixture.effects',method:'record',params:{tag:input.tag}});
  }
  return {
    concurrentCapabilities:['plugin.client.request'],concurrentServices:['fixture.tasks','fixture.recovery'],
    initialize:()=>({protocol_version:3,lifecycle_version:2,capabilities:[{id:'plugin.client.request',kind:'decision',version:1}],
      required_services:[requireExecutionGrants(),requireBackgroundService(),...(['host.session.create','host.session.send','host.session.inspect','host.session.cancel'] as const).map(requireKernelService),{name:'fixture.effects',major_version:1,required:true}],
      provided_services:[{name:'fixture.tasks',version:'1.0.0',methods:[{name:'resolve-lineage',input_schema:'execution.lineage.resolve.input.v1',output_schema:'execution.lineage.resolve.output.v1'}]},{name:'fixture.recovery',version:'1.0.0',methods:[{name:'run',input_schema:'fixture.recovery.run.input.v1',output_schema:'fixture.recovery.run.output.v1'}]}]}),
    async invokeCapability(call,host) {
      const {method,input={}}=call.input as {method:string;input:any};let result:unknown;
      switch(method){
        case 'prepare':{
          const existing=await load();if(existing){result=existing;break;}
          const main=await host.call('host.session.create',{request_id:'fixture-main',name:'Fixture main',visibility:'plugin',context_source:'fresh',user_space_id:'local',product_domain:'personal'});
          const worker=await host.call('host.session.create',{request_id:'fixture-worker',name:'Fixture worker',visibility:'plugin',context_source:'fresh',parent_session_id:main.session_id,user_space_id:'local',product_domain:'personal'});
          const task={id:'fixture-task',main:main.session_id,worker:worker.session_id,run:'fixture-run',revision:1,digest:createHash('sha256').update('immutable synthetic task requirements').digest('hex')};await save(task);result=task;break;
        }
        case 'issue':result=await issue(host,input);break;
        case 'effect':result=await effect(host,input);break;
        case 'forged-issue':{const task=(await load())!;result=await host.call('host.service.call',{service:'host.execution.authorization',method:'call',params:{operation:'issue',kind:'exact-run',authority_service:'fixture.tasks',task_id:task.id,session_id:task.worker,request_id:task.run,expires_at:new Date(Date.now()+60000).toISOString(),requested_actions:['user/session/send'],principal:{kind:'user',issuer:'forged',tenant:'forged',subject:'forged'},owner:true,scope:{models:['expanded-model']}}});break;}
        case 'background':result=await startBackground(host,{key:input.tag,service:'fixture.recovery',method:'run',params:input});break;
        case 'read':result=await load();break;
        case 'cancel-background':result=await cancelBackground(host,{job_id:input.job_id});break;
        case 'new-run':{const task=(await load())!;task.run=input.run_id;task.revision++;task.digest=createHash('sha256').update(task.digest+task.run).digest('hex');delete task.grant;delete task.root;await save(task);result=task;break;}
        case 'send':{const task=(await load())!;result=await host.call('host.session.send',{execution_authorization:input.reference??task.grant!.reference,session_id:input.session_id??task.worker,request_id:input.request_id??task.run,input:{prompt:input.prompt??'Independent bounded execution'},cause:'fixture.task',if_running:'queue'});break;}
        case 'inspect':{const task=(await load())!;result=await host.call('host.session.inspect',{execution_authorization:input.reference??task.grant!.reference,session_id:input.session_id??task.worker,request_id:input.request_id??task.run});break;}
        case 'revoke':result=await executionGrants(host).revoke(input.reference);break;
        default:throw new Error('unsupported fixture request');
      }
      return {output:{result}};
    },
    async invokeService(call,host,execution){
      if(call.service==='fixture.tasks'){
        const input=call.params as ExecutionLineageRequest,task=(await load())!;
        if(input.task_id!==task.id)throw new Error('unknown task');
        if(input.kind==='exact-run'&&(input.session_id!==task.worker||input.request_id!==task.run))throw new Error('unknown task run');
        return {task_id:task.id,session_id:input.kind==='exact-run'?task.worker:task.main,run_id:input.kind==='exact-run'?task.run:'',revision:task.revision,context_digest:task.digest,parent_task_id:input.parent?.scope.task_id??'',parent_revision:input.parent?.scope.revision??0};
      }
      if(call.service!=='fixture.recovery')throw new Error('unsupported fixture service');
      const input=call.params as {tag:string;reference?:Reference};await audit('background-ready',input);
      while(!execution.signal.aborted){const gates=JSON.parse(await readFile(options.gatePath,'utf8')) as string[];if(gates.includes(input.tag))break;await new Promise(resolve=>setTimeout(resolve,30));}
      if(execution.signal.aborted){await audit('background-cancelled',input);return {};}
      try {const result=await effect(host,input);await audit('background-effect',{tag:input.tag,result});return result;}
      catch(error){await audit('background-denied',{tag:input.tag,error:String(error)});throw error;}
    },
  };
}
