import {readProcess, requireProcessService, runJSONLRuntime, startProcess, TOOL_SERVICE_DESCRIPTOR, type RuntimePlugin, type ToolServiceCallRequest} from '@wuu/plugin-sdk';
const label = process.argv[2] ?? 'base';
const plugin: RuntimePlugin = {
  initialize() {return {protocol_version:3,lifecycle_version:2,provided_services:[TOOL_SERVICE_DESCRIPTOR],required_services:[requireProcessService()]};},
  async invokeService(input,host) {
    switch(input.method) {
      case 'list': return {tools:[{name:'process',description:'Run the fixture process',input_schema:{type:'object'}}],catalog:[],catalog_prompt:'',system_fragment:''};
      case 'resolve': return {found:true,supports:true,metadata:{orchestrator:false,read_only:false,concurrency_safe:false,destructive:false,risk:'high',reason:'fixture process'}};
      case 'execute': {
        const {call} = input.params as ToolServiceCallRequest;
        if(call.name==='fail') throw Object.assign(new Error('intentional delegate failure'),{code:'fixture_failure'});
        const started = await startProcess(host,{key:call.id!,command:`printf '${label}'`});
        let state=started,output='',offset=0;
        do {
          const read=await readProcess(host,{process_id:started.id,offset_bytes:offset,max_bytes:1024,wait_ms:1000});
          output+=read.output;offset=read.next_offset;state=read.process;
        } while(state.state==='starting'||state.state==='running'||state.state==='stopping');
        return {result:{content:[{type:'text',text:output}],structured_content:{process_id:started.id,session_id:started.session_id}},additional_context:`base:${label}`};
      }
      case 'prepare':return {messages:[]};
      case 'finalize':return {result:(input.params as {result:unknown}).result};
      case 'freeze':case 'unfreeze':return {};
      default:throw new Error(`unknown method ${input.method}`);
    }
  },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
