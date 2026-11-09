// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
const fs = vi.hoisted(() => ({ writeFile: vi.fn(), readdir: vi.fn(), rmdir: vi.fn(), deleteFile: vi.fn() }));
const share = vi.hoisted(() => vi.fn());
vi.mock('@capacitor/core', () => ({ Capacitor: { isNativePlatform: () => true }, registerPlugin: () => ({}) }));
vi.mock('@capacitor/filesystem', () => ({ Filesystem: fs, Directory: { Cache: 'CACHE' } }));
vi.mock('@capacitor/share', () => ({ Share: { share } }));
import { clearNativeShareCache, shareNativeFile } from '../src/lib/native';
beforeEach(() => {
  vi.clearAllMocks();
  fs.readdir.mockResolvedValue({ files: [] });
  fs.writeFile.mockImplementation(async ({ path }) => ({ uri: 'content://cache/' + path }));
  fs.rmdir.mockResolvedValue(undefined); fs.deleteFile.mockResolvedValue(undefined); share.mockResolvedValue({});
});
it('keeps the original filename and readable cache after the Android chooser returns', async () => {
  await shareNativeFile('report.txt', 'aGVsbG8=');
  const options = fs.writeFile.mock.calls[0][0];
  expect(options.path.split('/').at(-1)).toBe('report.txt');
  expect(options.data).toBe('aGVsbG8=');
  expect(share).toHaveBeenCalledWith({ files: ['content://cache/' + options.path], title: 'report.txt' });
  expect(fs.deleteFile).not.toHaveBeenCalled(); expect(fs.rmdir).not.toHaveBeenCalled();
  await clearNativeShareCache();
  expect(fs.rmdir).toHaveBeenCalledWith({ path: 'shared', directory: 'CACHE', recursive: true });
});
it('expires old exports without removing a destination currently reading a recent export', async () => {
  const old = String(Date.now() - 25 * 3600000) + '-old';
  const recent = String(Date.now() - 1000) + '-recent';
  fs.readdir.mockResolvedValue({ files: [{ name: old, type: 'directory' }, { name: recent, type: 'directory' }] });
  await shareNativeFile('next.txt', 'bmV4dA==');
  expect(fs.rmdir).toHaveBeenCalledExactlyOnceWith({ path: 'shared/' + old, directory: 'CACHE', recursive: true });
});
