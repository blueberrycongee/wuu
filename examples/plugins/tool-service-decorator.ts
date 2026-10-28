// Select this provider for agent.tools and bind its own agent.tools dependency
// to the implementation being decorated. No tool implementation is copied.
import {pathToFileURL} from 'node:url';
import {
  continueService, delegateService, runJSONLRuntime, SERVICE_CONTINUATION_HOST_FEATURE,
  TOOL_SERVICE, TOOL_SERVICE_DESCRIPTOR, type RuntimePlugin, type ToolServiceExecution,
} from '@wuu/plugin-sdk';

export const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      required_host_features: [SERVICE_CONTINUATION_HOST_FEATURE],
      provided_services: [TOOL_SERVICE_DESCRIPTOR],
      required_services: [{name: TOOL_SERVICE, major_version: 1, required: true}],
    };
  },
  invokeService: delegateService(TOOL_SERVICE_DESCRIPTOR, {
    async execute(input, host) {
      // Only this explicit operation preserves the original selected invocation.
      // Ordinary toolService(host).execute() is a new, unprivileged service call.
      const result = await continueService<ToolServiceExecution>(host, input);
      return {...result, additional_context: [result.additional_context, 'Tool execution observed by the selected decorator.'].filter(Boolean).join('\n')};
    },
  }),
};

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
