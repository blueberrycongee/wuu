// @vitest-environment jsdom
import { act, useRef } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import AccountApp from '../src/AccountApp';
import { CompactConversationActions } from '../../../desktop/src/renderer/CompactConversationActions';
import { I18nProvider } from '../../../desktop/src/renderer/i18n';

vi.mock('../src/lib/accountStore', () => ({ loadAccount: async () => null, accountDriver: async () => ({}) }));
vi.mock('../src/lib/credStore', () => ({ webCredStore: { load: async () => null, loadPair: async () => null, clear: async () => {} } }));
vi.mock('../src/lib/notifications', () => ({ startPushLifecycle: async () => {}, consumeNotificationHost: () => null }));
vi.mock('../src/NotificationSettings', () => ({ NotificationSettings: () => null }));
vi.mock('../src/App', () => ({ default: function Workspace() {
  const ref = useRef<HTMLButtonElement>(null);
  return <CompactConversationActions environmentToggleRef={ref} canStartNewThread
    onStartNewThread={() => {}} environmentPanelVisible={false} onToggleEnvironmentPanel={() => {}}
    rightPanelOpen={false} onToggleRightPanel={() => {}} />;
} }));

it('native back closes the portaled conversation menu before leaving the workspace', async () => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  window.location.hash = 'pair=test';
  const container = document.createElement('div'); document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => { root.render(<I18nProvider><AccountApp /></I18nProvider>); });
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!.click(); });
    expect(document.querySelector('[role="menu"]')).not.toBeNull();
    const back = new Event('wuu:native-back', { cancelable: true });
    await act(async () => { window.dispatchEvent(back); });
    expect(back.defaultPrevented).toBe(true);
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(container.querySelector('[aria-haspopup="menu"]')).not.toBeNull();
    await act(async () => { window.dispatchEvent(new Event('wuu:native-back', { cancelable: true })); });
    expect(container.querySelector('[aria-haspopup="menu"]')).toBeNull();
  } finally {
    await act(async () => root.unmount()); container.remove(); window.location.hash = '';
  }
});
