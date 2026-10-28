import * as React from 'react';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from '../../packages/plugin-sdk/src/ui';
import { PluginHost } from '@parity/PluginHost';
import { ApplicationShellOutlet } from '@parity/ApplicationShell';
import { activate as shell } from '../../examples/plugins/independent-providers/shell';
import { activate as navigation } from '../../examples/plugins/independent-providers/navigation';

// Runs the production renderer host/outlet. Application commands are observed at
// the public boundary; app-server delivery and visual acceptance are separate.
it('selects an independent navigation owner and fences unloaded commands/listeners', async () => {
  const listeners = new Set<() => void>();
  const snapshot = {navigation: {nodes: [{id: 'thread-one', kind: 'thread', label: 'Synthetic thread'}]}} as ApplicationSnapshotV1;
  const invoke = vi.fn(async () => undefined);
  const application: ApplicationServiceV1 = {
    contractVersion: 1, getSnapshot: () => snapshot,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    invoke: invoke as ApplicationServiceV1['invoke'],
  };
  const host = new PluginHost({react: React, application});
  await host.activateGeneration({pluginId: 'independent-shell', generation: 'one', register: shell});
  await host.activateGeneration({pluginId: 'other-navigation', generation: 'one', register: api => {
    api.registerUIContribution({id: 'ui.navigation', target: 'independent-shell/navigation', render: () => <p>Unselected navigation</p>});
  }});
  let retained: ApplicationServiceV1;
  await host.activateGeneration({pluginId: 'independent-navigation', generation: 'one', register: api => {retained = api.application; navigation(api);}});
  const container = document.createElement('div');
  const root = createRoot(container);
  try {
    await act(async () => root.render(<ApplicationShellOutlet host={host}
      binding={{pluginId: 'independent-shell', generation: 'one', id: 'ui.shell'}}
      surfaceBindings={{'ui.navigation': {pluginId: 'independent-navigation', generation: 'one', id: 'ui.navigation'}}} />));
    expect(container.textContent).toContain('Synthetic thread');
    expect(container.textContent).not.toContain('Unselected navigation');
    expect(listeners.size).toBe(1);
    await act(async () => container.querySelector('button')!.click());
    expect(invoke).toHaveBeenCalledWith('thread.select', {threadId: 'thread-one'}, expect.objectContaining({signal: expect.any(AbortSignal)}));
    await act(async () => {await host.unloadAndWait('independent-navigation');});
    expect(container.textContent).not.toContain('Synthetic thread');
    expect(container.textContent).not.toContain('Unselected navigation');
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(listeners.size).toBe(0);
    await expect(retained!.invoke('thread.select', {threadId: 'thread-one'})).rejects.toThrow(/no longer active/);
    expect(invoke).toHaveBeenCalledTimes(1);
  } finally {act(() => root.unmount()); await host.unloadAndWait('independent-shell'); await host.unloadAndWait('other-navigation');}
});
