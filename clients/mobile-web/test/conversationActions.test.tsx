// @vitest-environment jsdom
import { act, createRef } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import { CompactConversationActions } from '../../../desktop/src/renderer/CompactConversationActions';
import { I18nProvider } from '../../../desktop/src/renderer/i18n';

afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.hostKind;
});

it.each(['web', 'desktop'])('%s touch shell exposes only appropriate conversation actions', async (hostKind) => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true })));
  document.documentElement.dataset.hostKind = hostKind;
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  const onStartNewThread = vi.fn();
  try {
    const render = (canStartNewThread: boolean) => root.render(<I18nProvider>
      <CompactConversationActions canStartNewThread={canStartNewThread}
        onStartNewThread={onStartNewThread} environmentToggleRef={createRef()}
        environmentPanelVisible={false} onToggleEnvironmentPanel={() => {}}
        rightPanelOpen={false} onToggleRightPanel={() => {}} />
    </I18nProvider>);
    await act(async () => render(true));
    const buttons = container.querySelectorAll('button');
    expect(buttons).toHaveLength(hostKind === 'web' ? 1 : 2);
    expect(Boolean(container.querySelector('[aria-haspopup="menu"]'))).toBe(hostKind !== 'web');
    expect(document.querySelector('[role="menu"]')).toBeNull();
    await act(async () => buttons[0].click());
    expect(onStartNewThread).toHaveBeenCalledOnce();
    await act(async () => render(false));
    expect(container.querySelector('button')!.disabled).toBe(true);
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});
