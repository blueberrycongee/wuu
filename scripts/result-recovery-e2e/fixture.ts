import {INVOKE_TOOL_SERVICE, invokeTool, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin: RuntimePlugin = {
 initialize() {return {protocol_version:3,lifecycle_version:2, required_services:[{name:INVOKE_TOOL_SERVICE,major_version:1,required:true}],tools:[
  {id:'produce',direct_only:true,description:'Produce synthetic long text',input_schema:{type:'object',properties:{}},activity:{read_only:true,concurrency_safe:true,risk:'low'}},
  {id:'recover',direct_only:true,description:'Recover a previous archived result',input_schema:{type:'object',properties:{continuation:{type:'string'}},required:['continuation']},activity:{read_only:true,concurrency_safe:true,risk:'low',orchestrator:true}},
 ]};},
 async executeTool(input,host) {
  if(input.tool_id==='produce') return {result:{content:[{type:'text',text:'证据🙂\n'.repeat(5000)}]}};
  const args=input.arguments as {continuation?:unknown};
  if(input.tool_id!=='recover'||typeof args.continuation!=='string'||!input.execution_id) throw new Error('invalid input');
  return {result:await invokeTool(host,{execution_id:input.execution_id,call_id:'page',name:'recover_tool_result',arguments:{continuation:args.continuation}})};
 },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
