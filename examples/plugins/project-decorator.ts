// Change only project instructions; the selected delegate still owns work,
// recovery, persistence and its own scheduler. Requires a project_agent build.
import {
  delegateService, PROJECT_COORDINATION_DESCRIPTOR, PROJECT_COORDINATION_SERVICE,
  runJSONLRuntime, type RuntimePlugin,
} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  // Managed-session creation can call coordination again before returning.
  concurrentServices: [PROJECT_COORDINATION_SERVICE],
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [PROJECT_COORDINATION_DESCRIPTOR],
      required_services: [{name:PROJECT_COORDINATION_SERVICE,major_version:1,required:true}],
    };
  },
  invokeService: delegateService(PROJECT_COORDINATION_DESCRIPTOR, {
    async instructions(input, host) {
      const instructions = await host.call('host.service.call', {
        service:PROJECT_COORDINATION_SERVICE, method:input.method, params:input.params,
      });
      return `${instructions}\nReport concrete verification evidence.`;
    },
  }),
};
runJSONLRuntime(plugin, {input:process.stdin,output:process.stdout}).catch(error => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
