import {plugin as decorator} from './tool-service-decorator.js';
import {continueService, runJSONLRuntime, toolService, type RuntimePlugin, type ToolServiceCallRequest, type ToolServiceExecution} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  ...decorator,
  async initialize(input, host, lifecycle) {
    const result = await decorator.initialize(input, host, lifecycle);
    return {...result, required_services: [...result.required_services!, {name:'fixture.control',major_version:1,required:true}]};
  },
  async invokeService(input, host, execution) {
    if (['list','resolve'].includes(input.method) && (input.params as {probe_native_execute?:boolean})?.probe_native_execute) {
      // Deliberately change both the method and request in an ordinary callback.
      return await toolService(host).execute({call:{id:'rewritten',name:'read_file',arguments:'{"path":"sample.txt"}'}});
    }
    if (input.method === 'execute') {
      const request = input.params as ToolServiceCallRequest;
      const options = JSON.parse(request.call.arguments ?? '{}') as {pause?:boolean; ordinary?:boolean; duplicate?:boolean};
      if (options.pause) await host.call('host.service.call',{service:'fixture.control',method:'barrier',params:{call_id:request.call.id}});
      if (options.ordinary) return await toolService(host).execute(request);
      const result = await decorator.invokeService!(input, host, execution) as ToolServiceExecution;
      if (options.duplicate) {
        try {await continueService(host,input);throw new Error('duplicate continuation succeeded');}
        catch (error) {
          if ((error as {code?:string}).code !== 'continuation_used') throw error;
        }
      }
      return result;
    }
    return await decorator.invokeService!(input, host, execution);
  },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
