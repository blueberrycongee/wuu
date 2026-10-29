import {continueService, toolService, runJSONLRuntime, TOOL_SERVICE, TOOL_SERVICE_DESCRIPTOR, SERVICE_CONTINUATION_HOST_FEATURE, type RuntimePlugin, type ToolServiceCallRequest, type ToolServicePrepareRequest} from '@wuu/plugin-sdk';
import {existsSync} from 'node:fs';
import {join} from 'node:path';
let first = true;
const threadAdmission = process.argv[2] === 'thread-admission';
const backgroundAdmission = process.argv[2] === 'background-admission';
const plugin: RuntimePlugin = {
 concurrentServices: threadAdmission && process.argv[4] !== 'serial' ? [TOOL_SERVICE] : [],
 initialize() { return {protocol_version:3,lifecycle_version:2, required_host_features:[SERVICE_CONTINUATION_HOST_FEATURE],provided_services:[TOOL_SERVICE_DESCRIPTOR],required_services:[{name:TOOL_SERVICE,major_version:1,required:true}]}; },
 async invokeService(input, host) {
  if (input.method==='list') {
   if(backgroundAdmission && existsSync(process.argv[3]!)) {
    void toolService(host).list().catch(()=>{});
    const until=Date.now()+5000;
    while(!existsSync(join(process.argv[4]!,'entered')) && Date.now()<until) await new Promise(resolve=>setTimeout(resolve,10));
    return {tools:[],catalog:[],catalog_prompt:'',system_fragment:''};
   }
   if ((threadAdmission && existsSync(process.argv[3]!)) || (first && !threadAdmission && !backgroundAdmission)) { first=false; return await toolService(host).list(); }
   return {tools:[{name:'read_file',description:'read',input_schema:{type:'object',properties:{path:{type:'string'}}}},{name:'write_file',description:'write',input_schema:{type:'object'}},{name:'fixture_custom',description:'fixture',input_schema:{type:'object'}}],catalog:[],catalog_prompt:'',system_fragment:''};
  }
  if(input.method==='resolve') {
   if(threadAdmission && existsSync(process.argv[3]!)) return await toolService(host).resolve(input.params as ToolServiceCallRequest);
   return {found:true,supports:true,metadata:{orchestrator:false,read_only:true,concurrency_safe:true,destructive:false,risk:'low',reason:''}};
  }
  if(input.method==='execute') {
   const {call} = input.params as {call:{name:string}};
   if (call.name === 'fixture_custom') return {result:{content:[{type:'text',text:'external provider works'}]}};
   return await continueService(host,input);
  }
  if(input.method==='prepare') {
   if(threadAdmission && existsSync(process.argv[3]!)) return await toolService(host).prepare(input.params as ToolServicePrepareRequest);
   return {messages:[]};
  }
  if(input.method==='finalize') return {result:(input.params as {result:unknown}).result};
  return {};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
