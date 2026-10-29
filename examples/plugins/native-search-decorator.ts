// Demonstration: continue the native search, then label its result titles.
// Install with `wuu plugin dev` and bind its actual plugin ID to web_search.
import {continueNativeTool, NATIVE_TOOL_CONTINUATION_SERVICE, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
import {pathToFileURL} from 'node:url';

const plugin: RuntimePlugin = {
  initialize() {
    return {protocol_version: 3, lifecycle_version: 2, required_services: [{name: NATIVE_TOOL_CONTINUATION_SERVICE, major_version: 1, required: true}], tools: [{
      id: 'search', description: 'Search with the native provider and label the result titles.',
      activity: {read_only: true, concurrency_safe: true, risk: 'low'},
      input_schema: {
        type: 'object',
        properties: {
          query: {type: 'string', description: 'Search query.'},
          version_hint: {type: 'string', description: 'Optional repo dependency or API version this search should be matched against, for example next@15.2.1.'},
          package_context: {
            type: 'object', description: 'Optional package coordinates from the repo so web evidence can be checked against the installed dependency version.',
            properties: {
              name: {type: 'string', description: 'Package or library name from the repo.'},
              version: {type: 'string', description: 'Version found in the repo manifest or lockfile.'},
              ecosystem: {type: 'string', description: 'Package ecosystem, for example npm, pypi, go, cargo, or maven.'},
            },
          },
        },
        required: ['query'],
      },
    }]};
  },
  async executeTool(input, host) {
    if (input.tool_id !== 'search') throw new Error('unknown tool');
    const result = await continueNativeTool(host);
    if (result.is_error) return {result};
    const text = result.content?.[0]?.text;
    if (typeof text !== 'string') throw new Error('native search returned no text');
    const value = JSON.parse(text) as {results?: {title: string}[]};
    if (Array.isArray(value.results)) {
      value.results = value.results.map(item => ({...item, title: `[Example] ${item.title}`}));
    }
    return {result: {content: [{type: 'text', text: JSON.stringify(value)}]}};
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
