// Select this provider for agent.prompt and bind its dependency to a provider
// that advertises wuu.prompt.sections.v1, such as wuu.prompt.default.
import {
  PROMPT_SERVICE, PROMPT_SERVICE_DESCRIPTOR, promptService, replacePromptSection,
  runJSONLRuntime, type PromptAssemblyRequest, type RuntimePlugin,
} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [PROMPT_SERVICE_DESCRIPTOR],
      required_services: [{name: PROMPT_SERVICE, major_version: 1, required: true}],
    };
  },
  async invokeService({service, method, params}, host) {
    if (service !== PROMPT_SERVICE || method !== 'assemble') throw new Error('unknown prompt method');
    const request = params as PromptAssemblyRequest;
    const base = await promptService(host).assemble(request);
    const environment = ['# Environment', request.cwd && `- Current working directory: ${request.cwd}`, request.date && `- Current date: ${request.date}`].filter(Boolean).join('\n');
    return replacePromptSection(base, 'environment', environment);
  },
};
runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
