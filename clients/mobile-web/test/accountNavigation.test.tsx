// @vitest-environment jsdom
import { act, useContext } from 'react';
import { PhoneNavigationContext } from '../../../desktop/src/renderer/PhoneNavigationContext';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import AccountApp from '../src/AccountApp';
import { I18nProvider } from '../../../desktop/src/renderer/i18n';
import { languagePreferenceStore } from '../src/lib/language';
import { webCredStore } from '../src/lib/credStore';
const state = vi.hoisted(() => ({ account: null as any, status: vi.fn(), connect: vi.fn() }));
vi.mock('../src/lib/accountStore', () => ({ loadAccount: async () => state.account, accountDriver: (...args: any[]) => state.status(...args) }));
vi.mock('../src/lib/native', () => ({ isNative: false, reserveAuthorization: () => ({ open: async () => {}, close: () => {} }), secretStorage: {
 get: async (key: string) => localStorage.getItem(key), set: async (key: string, value: string) => localStorage.setItem(key, value), remove: async (key: string) => localStorage.removeItem(key),
} }));
vi.mock('../src/lib/notifications', () => ({ startPushLifecycle: async () => {}, consumeNotificationHost: () => null }));
vi.mock('../src/NotificationSettings', () => ({ NotificationSettings: () => null }));
vi.mock('../src/lib/desktopBridge', () => ({ RemoteDesktopBridge: class {
 connect = state.connect; disconnect = async () => {}; install = () => {};
 subscribeConnection = () => () => {}; getConnectionSnapshot = () => connected;
} }));
vi.mock('../src/WebWorkspace', () => ({ default: function Workbench() { const navigation = useContext(PhoneNavigationContext); return <div data-testid="connected">Connected workbench<button onClick={navigation?.openDevices}>电脑与账号</button></div>; } }));
const connected = { phase: 'connected', revision: 1 };
const saved = { v: 1 as const, host_pub: 'test-host', host_name: 'Test computer', device_seed: 'test-seed', relay_url: 'wss://example.test/v1/connect' };
let root: Root; let container: HTMLDivElement;
beforeEach(async () => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.clearAllMocks(); localStorage.clear();
 state.account = null; state.status.mockReset().mockResolvedValue({}); state.connect.mockResolvedValue(undefined);
 window.history.replaceState(null, '', '/'); await languagePreferenceStore.set('zh-CN'); await webCredStore.save(saved);
 container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); localStorage.clear(); });
async function render() { await act(async () => root.render(<I18nProvider preferenceStore={languagePreferenceStore}><AccountApp /></I18nProvider>)); }
async function click(text: string) { const b=[...container.querySelectorAll('button')].find(b=>b.textContent?.includes(text)); expect(b).toBeDefined(); await act(async()=>b!.click()); }
it('keeps a QR identity when opening devices from the workbench and resumes without pairing again', async () => {
 await render(); expect(container.querySelector('[data-testid="connected"]')).not.toBeNull();
 await click('电脑与账号'); expect(await webCredStore.load()).toEqual(saved);
 await click('返回已连接的电脑'); expect(container.querySelector('[data-testid="connected"]')).not.toBeNull(); expect(state.connect).toHaveBeenCalledTimes(2);
});
it('keeps QR pairing after native Back and after a cold reload', async () => {
 await render(); await act(async()=> {window.dispatchEvent(new Event('wuu:native-back', {cancelable:true}));});
 expect(await webCredStore.loadPair()).toEqual(saved);
 await act(async()=>root.unmount()); root=createRoot(container); await render();
 expect(container.querySelector('[data-testid="connected"]')).not.toBeNull();
});
it('explicitly forgetting removes QR identity and its resume entry', async () => {
 await render(); await click('电脑与账号'); await click('忘记此配对');
 expect(await webCredStore.load()).toBeNull(); expect(await webCredStore.loadPair()).toBeNull();
 expect(container.textContent).not.toContain('返回已连接的电脑');
});
it('starting another pairing does not reconnect to the previous computer', async () => {
 await render(); await click('电脑与账号'); await click('配对电脑');
 expect(container.textContent).toContain('配对链接'); expect(state.connect).toHaveBeenCalledOnce();
 expect(await webCredStore.loadPair()).toEqual(saved);
});

it('reopens an account computer through its list entry after returning from the workbench', async () => {
 state.account = { server: 'https://example.test', device_seed: 'test-seed', username: 'tester', token: 'test-token', pub: 'test-phone' };
 await webCredStore.forgetPair();
 await webCredStore.save({ ...saved, account_username: 'tester' });
 state.status.mockResolvedValue({ username: 'tester', server: 'https://example.test', devices: [
  { pub: saved.host_pub, name: saved.host_name, account: 'tester', role: 'host', online: true, added_at: 1 },
 ] });
 await render(); await click('电脑与账号');
 const entries = [...container.querySelectorAll('button')].filter(button => button.textContent?.includes(saved.host_name));
 expect(entries).toHaveLength(1);
 await act(async () => entries[0].click());
 expect(container.querySelector('[data-testid="connected"]')).not.toBeNull();
 expect(state.connect).toHaveBeenCalledTimes(2);
});

it('returns from pairing to its original account page for both the header and Android back', async () => {
 await webCredStore.forgetPair(); await webCredStore.clear();
 localStorage.setItem('wuu.account.server', 'https://example.test');
 await render(); await click('更多方式'); await click('配对电脑');
 expect(container.querySelector('.web-pair-card')).not.toBeNull();
 await act(async () => container.querySelector<HTMLButtonElement>('.web-gate-back')!.click());
 expect(container.querySelector('.web-pair-card')).toBeNull();
 expect(container.querySelector('h2')?.textContent).toBe('更多方式');
 await click('配对电脑');
 const back = new Event('wuu:native-back', { cancelable: true });
 await act(async () => { window.dispatchEvent(back); });
 expect(back.defaultPrevented).toBe(true);
 expect(container.querySelector('.web-pair-card')).toBeNull();
 expect(container.querySelector('h2')?.textContent).toBe('更多方式');
});
