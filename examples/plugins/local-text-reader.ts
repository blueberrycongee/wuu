// Local trusted-extension example. Install with `wuu plugin dev`, then bind its
// actual plugin ID and tool ID `read` to read_file in trusted user settings.
// This process reads local files itself and is NOT confined by Wuu's host path
// checks. Install only code you trust. It intentionally rejects large files.
import {open} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';

const maxBytes = 256 * 1024;
const plugin: RuntimePlugin = {
  initialize() {
    return {protocol_version: 3, lifecycle_version: 2, tools: [{
      id: 'read', replacement_contracts: ['read_file.text.v1', 'read_file.image.v1', 'read_file.pagination.v1'],
      description: 'Read local UTF-8 text or PNG, JPEG, static GIF and WebP images up to 256 KiB via a trusted local extension. For additional text pages, pass continuation.next as the next call arguments. Images do not support pagination; remote reads are unavailable. The content does not establish a native active-file freshness record.',
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
    if (input.tool_id !== 'read' || typeof args.path !== 'string' || !args.path.trim() || (args.continuation !== undefined && typeof args.continuation !== 'string')) throw new Error('a path and valid continuation are required');
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
      const data = buffer.subarray(0, used);
      let mime = '';
      if (data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) mime = 'image/png';
      else if (data.subarray(0, 3).equals(Buffer.from([255, 216, 255]))) mime = 'image/jpeg';
      else if (data.subarray(0, 6).toString('ascii') === 'GIF87a' || data.subarray(0, 6).toString('ascii') === 'GIF89a') mime = 'image/gif';
      else if (data.subarray(0, 4).toString('ascii') === 'RIFF' && data.subarray(8, 12).toString('ascii') === 'WEBP') mime = 'image/webp';
      if (mime) {
        if (((args.offset as number | undefined) ?? 0) > 1 || args.limit !== undefined || args.continuation !== undefined) throw new Error('image ranges are not supported');
        return {result: {content: [{type: 'image', mime_type: mime, data: data.toString('base64')}]}};
      }
      const text = new TextDecoder('utf-8', {fatal: true, ignoreBOM: true}).decode(data);
      if (text.includes('\0')) throw new Error('binary text is not supported');
      const lines = text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
      const offset = Math.max(1, (args.offset as number | undefined) ?? 1);
      const limit = (args.limit as number | undefined) ?? 0;
      const cursor = `sha256:${createHash('sha256').update(data).digest('hex')}`;
      if (args.continuation !== undefined && args.continuation !== cursor) throw new Error('file changed; restart the read');
      const remaining = Math.max(0, lines.length - offset + 1);
      const requested = limit > 0 ? Math.min(remaining, limit) : remaining;
      const count = Math.min(requested, 128);
      const selected = lines.slice(offset - 1, offset - 1 + count);
      const next = count < requested ? {next_cursor: cursor} : {};
      return {result: {structured_content: {text: selected.join(''), total_lines: lines.length, ...next}}};
    } finally { await file.close(); }
  },
};
export default plugin;
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runJSONLRuntime(plugin, {input: process.stdin, output: process.stdout}).catch(error => {console.error(error); process.exitCode = 1;});
}
