import { beforeEach, expect, it, vi } from 'vitest';

const native = vi.hoisted(() => ({ start: vi.fn(), stop: vi.fn(), addListener: vi.fn(), remove: vi.fn(), platform: 'android' }));
vi.mock('@capacitor/core', () => ({
  Capacitor: { getPlatform: () => native.platform },
  registerPlugin: () => native,
}));
import { AndroidBackgroundSync } from '../src/lib/backgroundSync';

beforeEach(() => {
  native.platform = 'android';
  native.start.mockReset().mockResolvedValue({ active: true });
  native.stop.mockReset().mockResolvedValue(undefined);
  native.remove.mockReset().mockResolvedValue(undefined);
  native.addListener.mockReset().mockResolvedValue({ remove: native.remove });
});

it('waits for foreground acknowledgement and deduplicates concurrent starts', async () => {
  let acknowledge!: (value: { active: boolean }) => void;
  native.start.mockReturnValue(new Promise(resolve => { acknowledge = resolve; }));
  const sync = new AndroidBackgroundSync(vi.fn());
  const first = sync.start();
  expect(sync.start()).toBe(first);
  await Promise.resolve();
  expect(sync.active).toBe(false);
  acknowledge({ active: true });
  await first;
  expect(sync.active).toBe(true);
  expect(native.start).toHaveBeenCalledOnce();
});

it('releases a late startup without stopping a replacement connection', async () => {
  let acknowledge!: (value: { active: boolean }) => void;
  native.start.mockReturnValueOnce(new Promise(resolve => { acknowledge = resolve; }));
  const old = new AndroidBackgroundSync(vi.fn());
  const starting = old.start();
  await Promise.resolve();
  const stopping = old.stop();
  const replacement = new AndroidBackgroundSync(vi.fn());
  await replacement.start();
  acknowledge({ active: true });
  await Promise.all([starting, stopping]);
  expect(old.active).toBe(false);
  expect(replacement.active).toBe(true);
  const oldID = native.start.mock.calls[0][0].id;
  expect(oldID).not.toBe(native.start.mock.calls[1][0].id);
  expect(native.stop).toHaveBeenCalledExactlyOnceWith({ id: oldID });
  expect(native.remove).toHaveBeenCalledOnce();
});

it('reports denial, retries on foreground and observes service loss', async () => {
  native.start.mockRejectedValueOnce(new Error('start denied'));
  const changed = vi.fn();
  const sync = new AndroidBackgroundSync(changed);
  await sync.start();
  expect(sync.active).toBe(false);
  expect(changed).toHaveBeenLastCalledWith('start denied');
  await sync.start();
  expect(sync.active).toBe(true);
  const id = native.start.mock.calls[1][0].id;
  const listener = native.addListener.mock.calls[0][1];
  listener({ id: 'other', active: false });
  expect(sync.active).toBe(true);
  listener({ id, active: false, error: 'stopped' });
  expect(sync.active).toBe(false);
  expect(changed).toHaveBeenLastCalledWith('stopped');
  await sync.stop();
  listener({ id, active: true });
  expect(sync.active).toBe(false);
});

it('does not request Android services on other platforms', async () => {
  native.platform = 'ios';
  const sync = new AndroidBackgroundSync(vi.fn());
  await sync.start();
  await sync.stop();
  expect(sync.active).toBe(false);
  expect(native.start).not.toHaveBeenCalled();
  expect(native.stop).not.toHaveBeenCalled();
});
