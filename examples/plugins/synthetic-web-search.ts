// Demonstration only: returns a synthetic result without searching the web.
// Install with `wuu plugin dev` and bind its actual plugin ID to web_search.
import {randomUUID} from 'node:crypto';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
import {pathToFileURL} from 'node:url';

// Match the host's Unicode White_Space rules, including NEL but excluding BOM.
const trimSpace = (value: string) => value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');

const plugin: RuntimePlugin = {
  initialize() {
    return {protocol_version: 3, lifecycle_version: 2, tools: [{
      id: 'search', description: 'Return a synthetic search result for a query.',
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
  executeTool(input) {
    const args = input.arguments as {query?: unknown; version_hint?: string; package_context?: {name?: string; version?: string; ecosystem?: string}};
    if (input.tool_id !== 'search' || typeof args.query !== 'string' || !trimSpace(args.query)) throw new Error('query is required');
    const pkg = args.package_context ?? {};
    const name = trimSpace(pkg.name ?? '');
    const packageVersion = trimSpace(pkg.version ?? '');
    const ecosystem = trimSpace(pkg.ecosystem ?? '');
    const label = name ? `${name}${packageVersion ? `@${packageVersion}` : ''}` : packageVersion;
    const requestedVersion = trimSpace(args.version_hint ?? '') || [ecosystem, label].filter(Boolean).join(' ');
    const compact = trimSpace(requestedVersion).replace(/\p{White_Space}+/gu, ' ');
    const version = Array.from(compact);
    const matched = version.length > 160 ? `${trimSpace(version.slice(0, 160).join(''))}...` : compact;
    const value = {
      action: 'web_search', query: args.query,
      evidence: {id: `example_${randomUUID()}`, kind: 'search', source: 'https://example.com/', source_tier: 'search_index', retrieved_at: new Date().toISOString(), version_matched_to_repo: matched || 'unknown'},
      results: [{title: 'Synthetic example', url: 'https://example.com/', snippet: 'This is a demonstration, not a search result.'}],
    };
    return {result: {content: [{type: 'text', text: JSON.stringify(value)}]}};
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
