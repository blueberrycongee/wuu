// An orchestrator delegates result recovery through the normal tool path.
// Pass the continuation from an archived result in this conversation.
import {pathToFileURL} from 'node:url';
import {INVOKE_TOOL_SERVICE, invokeTool, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      required_services: [{name: INVOKE_TOOL_SERVICE, major_version: 1, required: true}],
      tools: [{
        id: 'recover', description: 'Recover one page of a previously archived tool result.',
        input_schema: {type: 'object', properties: {continuation: {type: 'string'}}, required: ['continuation']},
        activity: {read_only: true, concurrency_safe: true, risk: 'low', orchestrator: true},
      }],
    };
  },
  async executeTool(input, host) {
    const args = input.arguments as {continuation?: unknown};
    if (input.tool_id !== 'recover' || typeof args.continuation !== 'string' || !input.execution_id) throw new Error('continuation and live execution are required');
    const result = await invokeTool(host, {execution_id: input.execution_id, call_id: 'recover-page', name: 'recover_tool_result', arguments: {continuation: args.continuation}});
    return {result};
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
