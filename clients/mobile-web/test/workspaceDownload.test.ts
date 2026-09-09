import { describe, expect, it } from 'vitest';
import { downloadWorkspaceFile } from '../src/lib/workspaceDownload';

describe('complete workspace downloads', () => {
  it('assembles bounded chunks without truncating binary content', async () => {
    const source = Buffer.alloc(700001);
    for (let index = 0; index < source.length; index++) source[index] = index % 251;
    const requests: number[] = [];
    const blob = await downloadWorkspaceFile(async <T>(_method: string, raw?: unknown) => {
      const params = raw as { offset: number; version: string };
      requests.push(params.offset);
      if (params.offset) expect(params.version).toBe('revision');
      return { data: source.subarray(params.offset, params.offset + 262144).toString('base64'), offset: params.offset, size: source.length, version: 'revision', mime: 'application/octet-stream' } as T;
    }, 'archive.bin');
    expect(requests).toHaveLength(3);
    const bytes = await blob.arrayBuffer();
    expect(Buffer.from(bytes)).toEqual(source);
  });
  it('rejects a revision switch instead of exporting mixed file versions', async () => {
    await expect(downloadWorkspaceFile(async <T>(_method: string, raw?: unknown) => {
      const { offset } = raw as { offset: number };
      return { data: btoa('abc'), offset, size: 6, version: offset ? 'new' : 'old', mime: 'text/plain' } as T;
    }, 'changing.txt')).rejects.toThrow();
  });
  it('rejects an empty non-final chunk instead of looping forever', async () => {
    await expect(downloadWorkspaceFile(async <T>() => ({ data: '', offset: 0, size: 1, version: 'a', mime: '' }) as T, 'missing')).rejects.toThrow();
  });
});
