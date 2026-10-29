// Local trusted-extension example. Install with `wuu plugin dev`, then bind its
// actual plugin ID and tool ID `read` to read_file in trusted user settings.
// This process reads local files itself and is NOT confined by Wuu's host path
// checks. Install only code you trust. It intentionally rejects large files.
import {open} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';

const maxBytes = 256 * 1024;
const plugin: RuntimePlugin = {
  initialize() {
    return {protocol_version: 3, lifecycle_version: 2, tools: [{
      id: 'read', replacement_contracts: ['read_file.text.v1'],
      description: 'Read local UTF-8 text up to 256 KiB via a trusted local extension. Images, remote reads and extension continuations are unavailable. The content does not establish a native active-file freshness record.',
      activity: {read_only: true, concurrency_safe: true, risk: 'low'},
      input_schema: {
        type: 'object', properties: {
          continuation: {type: 'string', description: 'Opaque continuation from the previous result. Supplies the path, remaining range, and content hash; pass it without offset or limit.'},
          path: {type: 'string', description: 'File path relative to workspace root, an absolute path in the allowed file scope, or a session artifact path. Required unless continuation is supplied.'},
          offset: {type: 'integer', description: 'Text only: 1-based line number to start reading from. Default 1.'},
          limit: {type: 'integer', description: 'Text only: max lines to return. Omit to read the whole file when it fits size limits.'},
        },
      },
    }]};
  },
  async executeTool(input) {
    const args = input.arguments as {path?: unknown; offset?: unknown; limit?: unknown; continuation?: unknown};
    if (input.tool_id !== 'read' || typeof args.path !== 'string' || !args.path.trim() || args.continuation !== undefined) throw new Error('a fresh path is required');
    if (args.offset !== undefined && !Number.isSafeInteger(args.offset)) throw new Error('invalid offset');
    if (args.limit !== undefined && (!Number.isSafeInteger(args.limit) || (args.limit as number) < 0)) throw new Error('invalid limit');
    const file = await open(resolve(input.cwd, args.path), 'r');
    try {
      const info = await file.stat();
      if (!info.isFile() || info.size > maxBytes) throw new Error('only regular files up to 256 KiB are supported');
      const buffer = Buffer.alloc(maxBytes + 1);
      let used = 0;
      while (used < buffer.length) {
        const {bytesRead} = await file.read(buffer, used, buffer.length - used, null);
        if (!bytesRead) break;
        used += bytesRead;
      }
      if (used > maxBytes) throw new Error('file exceeds 256 KiB');
      const text = new TextDecoder('utf-8', {fatal: true, ignoreBOM: true}).decode(buffer.subarray(0, used));
      if (text.includes('\0')) throw new Error('binary text is not supported');
      const lines = text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
      const offset = Math.max(1, (args.offset as number | undefined) ?? 1);
      const limit = (args.limit as number | undefined) ?? 0;
      const selected = limit > 0 ? lines.slice(offset - 1, offset - 1 + limit) : lines.slice(offset - 1);
      return {result: {structured_content: {text: selected.join(''), total_lines: lines.length}}};
    } finally { await file.close(); }
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
