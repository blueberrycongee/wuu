import {INVOKE_TOOL_SERVICE, invokeTool, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin: RuntimePlugin = {
 initialize() {return {protocol_version:3,lifecycle_version:2, required_services:[{name:INVOKE_TOOL_SERVICE,major_version:1,required:true}],tools:[{id:'read',direct_only:true,description:'Read a file through the host',input_schema:{type:'object',properties:{path:{type:'string'}},required:['path']},activity:{read_only:true,concurrency_safe:true,risk:'low',orchestrator:true}}]};},
 async executeTool(input,host) {
  const args=input.arguments as {path?:unknown};
  if(input.tool_id!=='read'||typeof args.path!=='string'||!input.execution_id) throw new Error('invalid input');
  return {result:await invokeTool(host,{execution_id:input.execution_id,call_id:'read',name:'read_file',arguments:{path:args.path}})};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
