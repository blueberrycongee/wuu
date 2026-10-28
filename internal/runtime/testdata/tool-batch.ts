import {
 TOOL_BATCH_SERVICE_DESCRIPTOR, TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR, TOOL_SERVICE_DESCRIPTOR, requireToolBatchResources,
 toolBatchResources,runJSONLRuntime,type ToolBatchRequest,type ToolServiceCallRequest,
 type ToolServiceFinalizeRequest,invokeTool,
} from '@wuu/plugin-sdk';
const mode=process.argv[2]??'normal';
const effects:string[]=[];
const finalized=new Map<string,number>();
const lastExecuted=Promise.withResolvers<void>();
const metadataMode=mode==='finalize';
runJSONLRuntime({
 concurrentServices:['agent.tool.batch','agent.tools'],
 initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[(mode==='stream'||metadataMode)?TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR:TOOL_BATCH_SERVICE_DESCRIPTOR,TOOL_SERVICE_DESCRIPTOR],required_services:[...requireToolBatchResources(),...(metadataMode?[{name:'execution.invoke-tool',major_version:1,required:true}]:[])]}),
 async invokeService({service,method,params},host,execution){
  if(service==='agent.tool.batch'){
   if(method==='stream_admit')return {admit:true};
   const batch=params as ToolBatchRequest;const resources=toolBatchResources(host,batch);
   try {await toolBatchResources(host,{id:'forged'}).execute(0);throw Error('forged batch accepted')}catch(error){if(!String(error).includes('identity mismatch'))throw error}
   if(mode==='cancel') { await resources.cancel(0); await resources.settle(1,{content:[{type:'text',text:'explicit rejection'}],is_error:true});return {completed:true} }
   if(metadataMode){await Promise.all(batch.calls.map((call,index)=>call.name==='reject'?resources.settle(index,{content:[{type:'text',text:'batch rejected'}],is_error:true}):resources.execute(index)));return {completed:true}}
   for(let i=batch.calls.length-1;i>=0;i--){
    await resources.resolve(i);
    await resources.execute(i);
    if(mode==='partial')throw Error('external partial failure');
   }
   await Promise.all([resources.execute(0),resources.execute(0)]);
   return {completed:true};
  }
  if(method==='list')return {tools:[{name:'echo',description:'external echo',input_schema:{type:'object'}}],catalog:[],catalog_prompt:'',system_fragment:''};
  if(method==='resolve')return {found:true,supports:true,metadata:{read_only:true,concurrency_safe:true,orchestrator:(params as ToolServiceCallRequest).call.name==='orchestrate',destructive:false,risk:'low',reason:''}};
  if(method==='prepare')return {messages:[]};
  if(method==='freeze'||method==='unfreeze')return {};
  if(method==='execute'){
   const {call}=params as ToolServiceCallRequest;
   effects.push(call.id!);
   if(metadataMode){
    if(call.id==='first')await lastExecuted.promise;
    if(call.id==='last')lastExecuted.resolve();
    if(call.name==='orchestrate'){
     await invokeTool(host,{execution_id:execution.executionId,call_id:'child',name:'echo',arguments:{marker:'nested'}});
     await invokeTool(host,{execution_id:execution.executionId,call_id:'failed-child',name:'fail',arguments:{marker:'nested-failed'}});
    }
   }
   if(call.name==='fail'||call.name==='reject')throw Error('individual external failure');
   const marker=JSON.parse(call.arguments??'{}').marker??call.id;
   return {result:{content:[{type:'text',text:`external-batch:${mode}:${effects.join(',')}`}]},...(metadataMode?{additional_context:`execute-context[${marker}]`,discovered_tools:[{type:'function',name:`execute_${marker}`,input_schema:{type:'object'}}]}:{})};
  }
  if(method==='finalize'){
   const {call,result}=params as ToolServiceFinalizeRequest;
   const count=(finalized.get(call.id!)??0)+1;finalized.set(call.id!,count);
   if(count!==1)throw Error('double finalization');
   const marker=call.arguments==='{'?'malformed':JSON.parse(call.arguments??'{}').marker??call.id;
   return {result,...(metadataMode?{additional_context:`finalize-context[${marker}]:${result.is_error?'error':'success'}`,discovered_tools:[{type:'function',name:`finalize_${marker}`,input_schema:{type:'object'}}]}:{})};
  }
  throw Error(method);
 }
},{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
