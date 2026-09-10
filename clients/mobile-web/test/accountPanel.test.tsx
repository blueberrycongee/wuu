// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { AccountPanel, type AccountDeviceView, type AccountAction, type AccountDriver } from '../../../desktop/src/renderer/AccountPanel';
import { I18nProvider, translate } from '../../../desktop/src/renderer/i18n';
import { languagePreferenceStore } from '../src/lib/language';

it('keeps same-named devices distinct, connects only online computers, and follows the shared language', async () => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  await languagePreferenceStore.set('en-US');
  const devices: AccountDeviceView[] = [
    { pub: 'offline', name: 'Computer', role: 'host', online: false, account: 'test', added_at: 1 },
    { pub: 'online', name: 'Computer', role: 'host', online: true, account: 'test', added_at: 2 },
    { pub: 'current', name: 'Phone', role: 'phone', online: false, account: 'test', added_at: 3 },
    { pub: 'other', name: 'Phone', role: 'phone', online: false, account: 'test', added_at: 4 },
  ];
  const driver = vi.fn(async (action: AccountAction, input?: Record<string, string>) => {
    if (action === 'revoke') devices.splice(devices.findIndex(d => d.pub === input?.pub), 1);
    return { username: 'test', pub: 'current', devices };
  });
  const connect = vi.fn();
  const container = document.createElement('div'); document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => root.render(<I18nProvider preferenceStore={languagePreferenceStore}><AccountPanel driver={driver} onComputer={connect} managementContent={<div data-testid="management-only" />} /></I18nProvider>));
    const buttons = () => [...container.querySelectorAll('button')];
    const open = buttons().filter(b => b.getAttribute('aria-label') === translate('en-US', 'account.connectTo', { name: 'Computer' }));
    expect(open).toHaveLength(2);
    expect(open.map(b => b.disabled)).toEqual([false, true]);
    await act(async () => open[0].click());
    expect(connect).toHaveBeenCalledWith(devices[1]);
    expect(buttons().filter(b => b.textContent === translate('en-US', 'account.remove'))).toHaveLength(0);
    expect(container.querySelector('[data-testid="management-only"]')).toBeNull();
    const management = buttons().find(b => b.getAttribute('aria-label') === translate('en-US', 'account.manage'))!;
    await act(async () => management.click());
    expect(container.querySelector('[data-testid="management-only"]')).not.toBeNull();
    expect(buttons().filter(b => b.textContent === translate('en-US', 'account.remove'))).toHaveLength(3);
    const phones = container.querySelector(`section[aria-label="${translate('en-US', 'account.phones')}"]`)!;
    await act(async () => phones.querySelector<HTMLButtonElement>('button')!.click());
    expect(driver).toHaveBeenCalledWith('revoke', { pub: 'other' });
    expect(phones.querySelector('button')).toBeNull();
    await act(async () => buttons().find(b => b.textContent === translate('en-US', 'account.password'))!.click());
    expect(container.querySelector('input[autocomplete="current-password"]')).not.toBeNull();
    await act(async () => { window.dispatchEvent(new Event('wuu:native-back', { cancelable: true })); });
    expect(container.querySelector('[data-testid="management-only"]')).not.toBeNull();
    const back = new Event('wuu:native-back', { cancelable: true });
    await act(async () => { window.dispatchEvent(back); });
    expect(back.defaultPrevented).toBe(true);
    expect(container.querySelector('[data-testid="management-only"]')).toBeNull();
    await act(async () => { await languagePreferenceStore.set('zh-CN'); });
    expect(document.documentElement.lang).toBe('zh-CN');
    expect(buttons().filter(b => b.getAttribute('aria-label') === translate('zh-CN', 'account.connectTo', { name: 'Computer' }))).toHaveLength(2);
  } finally {
    await act(async () => root.unmount()); container.remove(); localStorage.clear();
  }
});

async function renderLogin(driver: AccountDriver) {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  await languagePreferenceStore.set('en-US');
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  const pair = vi.fn();
  await act(async () => root.render(<I18nProvider preferenceStore={languagePreferenceStore}><AccountPanel driver={driver} onComputer={() => {}} onPair={pair} /></I18nProvider>));
  return {
    container, pair,
    async click(key: Parameters<typeof translate>[1]) {
      const label = translate('en-US', key);
      const button = [...container.querySelectorAll('button')].find(b => b.textContent?.startsWith(label) || b.getAttribute('aria-label') === label);
      expect(button).toBeDefined();
      await act(async () => button!.click());
    },
    async fill(selector: string, value: string) {
      const input = container.querySelector<HTMLInputElement>(selector)!;
      expect(input).not.toBeNull();
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
    },
    async dispose() { await act(async () => root.unmount()); container.remove(); localStorage.clear(); },
  };
}

it('asks for connection settings before login, preserves credentials, and only applies saved settings', async () => {
  const driver = vi.fn<AccountDriver>(async () => ({}));
  const ui = await renderLogin(driver);
  try {
    expect(ui.container.querySelector('input[type="url"]')).toBeNull();
    await ui.fill('input[autocomplete="username"]', 'test-user');
    await ui.fill('input[autocomplete="current-password"]', 'long-test-password');
    await ui.click('account.login');
    expect(driver.mock.calls.some(([action]) => action === 'login')).toBe(false);
    expect(ui.container.querySelector('input[type="url"]')).not.toBeNull();
    await ui.fill('input[type="url"]', 'https://wuu.example.com');
    await ui.fill('input[maxlength="64"]', 'My phone');
    await ui.click('common.save');
    await ui.click('account.connectionSettings');
    await ui.fill('input[type="url"]', 'https://other.example.com');
    const back = new Event('wuu:native-back', { cancelable: true });
    await act(async () => { window.dispatchEvent(back); });
    expect(back.defaultPrevented).toBe(true);
    await ui.click('account.login');
    expect(driver).toHaveBeenCalledWith('login', expect.objectContaining({
      server: 'https://wuu.example.com', username: 'test-user', password: 'long-test-password', name: 'My phone',
    }));
  } finally { await ui.dispose(); }
});

it('keeps registration, recovery and pairing reachable through secondary navigation', async () => {
  const ui = await renderLogin(async () => ({}));
  try {
    await ui.fill('input[autocomplete="current-password"]', 'do-not-reuse-password');
    await ui.click('account.moreOptions');
    await ui.click('account.register');
    expect(ui.container.querySelector<HTMLInputElement>('input[autocomplete="new-password"]')?.value).toBe('');
    await ui.click('common.back');
    await ui.click('account.moreOptions');
    await ui.click('account.forgotPassword');
    expect(ui.container.querySelector('input[autocomplete="off"]')).not.toBeNull();
    await ui.click('common.back');
    await ui.click('account.moreOptions');
    await ui.click('account.pairLink');
    expect(ui.pair).toHaveBeenCalledOnce();
  } finally { await ui.dispose(); }
});
