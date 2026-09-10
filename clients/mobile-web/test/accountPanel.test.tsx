// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { AccountPanel, type AccountDeviceView } from '../../../desktop/src/renderer/AccountPanel';
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
  const driver = vi.fn(async () => ({ username: 'test', pub: 'current', devices }));
  const connect = vi.fn();
  const container = document.createElement('div'); document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => root.render(<I18nProvider preferenceStore={languagePreferenceStore}><AccountPanel driver={driver} onComputer={connect} /></I18nProvider>));
    const buttons = () => [...container.querySelectorAll('button')];
    const open = buttons().filter(b => b.textContent === translate('en-US', 'account.open'));
    expect(open).toHaveLength(2);
    expect(open.map(b => b.disabled)).toEqual([false, true]);
    await act(async () => open[0].click());
    expect(connect).toHaveBeenCalledWith(devices[1]);
    expect(buttons().filter(b => b.textContent === translate('en-US', 'account.remove'))).toHaveLength(3);
    await act(async () => { await languagePreferenceStore.set('zh-CN'); });
    expect(document.documentElement.lang).toBe('zh-CN');
    expect(buttons().filter(b => b.textContent === translate('zh-CN', 'account.open'))).toHaveLength(2);
  } finally {
    await act(async () => root.unmount()); container.remove(); localStorage.clear();
  }
});
