// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
const native = vi.hoisted(() => ({ listeners: new Map<string, (event: any) => void>(), register: vi.fn(), unregister: vi.fn(), request: vi.fn(), session: { server: 'https://own.example', token: 'device-token' } as { server: string; token: string } | null }));
vi.mock('@capacitor/core', () => ({ Capacitor: { isNativePlatform: () => true, getPlatform: () => 'ios' } }));
vi.mock('@capacitor/push-notifications', () => ({ PushNotifications: {
  addListener: async (name: string, callback: (event: any) => void) => { native.listeners.set(name, callback); return { remove: async () => {} }; },
  checkPermissions: async () => ({ receive: 'granted' }), register: native.register, unregister: native.unregister,
} }));
vi.mock('@wuu/remote-core', () => ({ accountRequest: native.request }));
vi.mock('../src/lib/accountStore', () => ({ loadAccount: async () => native.session }));
beforeEach(() => {
  vi.resetModules(); vi.stubEnv('VITE_WUU_PUSH_PLATFORMS', 'ios'); localStorage.clear(); native.listeners.clear();
  native.session = { server: 'https://own.example', token: 'device-token' };
  native.request.mockReset().mockResolvedValue({ push_platforms: ['ios'] });
  native.unregister.mockReset().mockResolvedValue(undefined);
  native.register.mockReset().mockImplementation(async () => { native.listeners.get('registration')?.({ value: 'platform-token' }); });
});
it('registers only its authenticated device and removes the destination before system unregister', async () => {
  const notifications = await import('../src/lib/notifications');
  await notifications.startPushLifecycle(); await notifications.enableNotifications();
  expect(native.request).toHaveBeenCalledWith('https://own.example', 'device-token', 'POST', '/push', { platform: 'ios', token: 'platform-token' });
  expect(notifications.notificationState().enabled).toBe(true);
  await notifications.disableNotifications();
  expect(native.request).toHaveBeenLastCalledWith('https://own.example', 'device-token', 'DELETE', '/push');
  expect(native.unregister).toHaveBeenCalledOnce();
  expect(notifications.notificationState().enabled).toBe(false);
  native.request.mockClear(); native.session = null;
  native.listeners.get('registration')?.({ value: 'rotated-token-after-logout' });
  await Promise.resolve(); await Promise.resolve();
  expect(native.request).not.toHaveBeenCalled();
});
it('does not register with the OS when the deployment has no provider configuration', async () => {
  native.request.mockResolvedValue({ push_platforms: [] });
  const notifications = await import('../src/lib/notifications');
  await notifications.startPushLifecycle();
  await expect(notifications.enableNotifications()).rejects.toThrow();
  expect(native.register).not.toHaveBeenCalled();
  expect(notifications.notificationState().enabled).toBe(false);
  expect(notifications.notificationState().busy).toBe(false);
});
