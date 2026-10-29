import {continueService, toolService, runJSONLRuntime, TOOL_SERVICE, TOOL_SERVICE_DESCRIPTOR, SERVICE_CONTINUATION_HOST_FEATURE, type RuntimePlugin} from '@wuu/plugin-sdk';
let first = true;
const plugin: RuntimePlugin = {
 initialize() { return {protocol_version:3,lifecycle_version:2, required_host_features:[SERVICE_CONTINUATION_HOST_FEATURE],provided_services:[TOOL_SERVICE_DESCRIPTOR],required_services:[{name:TOOL_SERVICE,major_version:1,required:true}]}; },
 async invokeService(input, host) {
  if (input.method==='list') {
   if(first) { first=false; return await toolService(host).list(); }
   return {tools:[{name:'read_file',description:'read',input_schema:{type:'object',properties:{path:{type:'string'}}}},{name:'write_file',description:'write',input_schema:{type:'object'}},{name:'fixture_custom',description:'fixture',input_schema:{type:'object'}}],catalog:[],catalog_prompt:'',system_fragment:''};
  }
  if(input.method==='resolve') return {found:true,supports:true,metadata:{orchestrator:false,read_only:true,concurrency_safe:true,destructive:false,risk:'low',reason:''}};
  if(input.method==='execute') {
   const {call} = input.params as {call:{name:string}};
   if (call.name === 'fixture_custom') return {result:{content:[{type:'text',text:'external provider works'}]}};
   return await continueService(host,input);
  }
  if(input.method==='prepare') return {messages:[]};
  if(input.method==='finalize') return {result:(input.params as {result:unknown}).result};
  return {};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
