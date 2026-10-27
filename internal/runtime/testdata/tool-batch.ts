import {
 TOOL_BATCH_SERVICE_DESCRIPTOR, TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR, TOOL_SERVICE_DESCRIPTOR, requireToolBatchResources,
 toolBatchResources,runJSONLRuntime,type ToolBatchRequest,type ToolServiceCallRequest,
 type ToolServiceFinalizeRequest,
} from '@wuu/plugin-sdk';
const mode=process.argv[2]??'normal';
const effects:string[]=[];
const finalized=new Map<string,number>();
runJSONLRuntime({
 concurrentServices:['agent.tool.batch','agent.tools'],
 initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[mode==='stream'?TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR:TOOL_BATCH_SERVICE_DESCRIPTOR,TOOL_SERVICE_DESCRIPTOR],required_services:requireToolBatchResources()}),
 async invokeService({service,method,params},host){
  if(service==='agent.tool.batch'){
   if(method==='stream_admit')return {admit:true};
   const batch=params as ToolBatchRequest;const resources=toolBatchResources(host,batch);
   try {await toolBatchResources(host,{id:'forged'}).execute(0);throw Error('forged batch accepted')}catch(error){if(!String(error).includes('identity mismatch'))throw error}
   if(mode==='cancel') { await resources.cancel(0); await resources.settle(1,{content:[{type:'text',text:'explicit rejection'}],is_error:true});return {completed:true} }
   for(let i=batch.calls.length-1;i>=0;i--){
    await resources.resolve(i);
    await resources.execute(i);
    if(mode==='partial')throw Error('external partial failure');
   }
   await Promise.all([resources.execute(0),resources.execute(0)]);
   return {completed:true};
  }
  if(method==='list')return {tools:[{name:'echo',description:'external echo',input_schema:{type:'object'}}],catalog:[],catalog_prompt:'',system_fragment:''};
  if(method==='resolve')return {found:true,supports:true,metadata:{read_only:true,concurrency_safe:true,orchestrator:false,destructive:false,risk:'low',reason:''}};
  if(method==='prepare')return {messages:[]};
  if(method==='freeze'||method==='unfreeze')return {};
  if(method==='execute'){
   const {call}=params as ToolServiceCallRequest;
   effects.push(call.id!);
   if(call.name==='fail')throw Error('individual external failure');
   return {result:{content:[{type:'text',text:`external-batch:${mode}:${effects.join(',')}`}]}};
  }
  if(method==='finalize'){
   const {call,result}=params as ToolServiceFinalizeRequest;
   const count=(finalized.get(call.id!)??0)+1;finalized.set(call.id!,count);
   if(count!==1)throw Error('double finalization');
   return {result};
  }
  throw Error(method);
 }
},{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
