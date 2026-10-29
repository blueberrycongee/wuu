// Trusted extension example: paginate text read by the pinned remote worker.
// Bind this plugin's actual ID and tool ID "read" to read_file in trusted user
// settings. The extension itself remains trusted code and is not OS-confined.
import {pathToFileURL} from 'node:url';
import {continueNativeTool, getProcessEnvironment, NATIVE_TOOL_CONTINUATION_SERVICE, requireProcessService, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  initialize() {
    return {protocol_version: 3, lifecycle_version: 2,
      required_services: [{name: NATIVE_TOOL_CONTINUATION_SERVICE, major_version: 1, required: true}, requireProcessService()],
      tools: [{id: 'read', replacement_contracts: ['read_file.text.v1', 'read_file.pagination.v1', 'read_file.remote.v1'],
        description: 'Read and label UTF-8 text from the selected remote workspace. Pass continuation.next for the next page. Images and byte selectors are unavailable. Output is unverified.',
        activity: {read_only: true, concurrency_safe: true, risk: 'low'},
        input_schema: {type: 'object', properties: {
          continuation: {type: 'string', description: 'Opaque continuation from the previous result. Supplies the path, remaining range, and content hash; pass it without offset or limit.'},
          path: {type: 'string', description: 'File path relative to workspace root, an absolute path in the allowed file scope, or a session artifact path. Required unless continuation is supplied.'},
          offset: {type: 'integer', description: 'Text only: 1-based line number to start reading from. Default 1.'},
          limit: {type: 'integer', description: 'Text only: max lines to return. Omit to read the whole file when it fits size limits.'},
        }},
      }]};
  },
  async executeTool(input, host) {
    if (input.tool_id !== 'read') throw new Error('unknown tool');
    const environment = await getProcessEnvironment(host);
    if (environment.backend === 'local' || !environment.identity || environment.root !== input.cwd) throw new Error('a pinned remote workspace is required');
    const native = await continueNativeTool(host);
    const data = native.structured_content as {text?: unknown; total_lines?: unknown};
    if (!data || typeof data.text !== 'string' || typeof data.total_lines !== 'number') throw new Error('worker returned no text');
    const cursor = (input.arguments as {continuation?: unknown}).continuation;
    if (cursor !== undefined && cursor !== 'next') throw new Error('invalid provider continuation');
    const lines = data.text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
    const page = lines.slice(0, 128);
    const text = page.join('').replace(/(^|\n)(?=[^\n])/g, '$1[remote] ');
    const continuation = page.length < lines.length ? {next_cursor: 'next'} : {};
    return {result: {structured_content: {text, total_lines: data.total_lines, ...continuation}}};
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
