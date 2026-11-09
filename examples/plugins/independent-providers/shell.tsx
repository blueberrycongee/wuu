import type { PluginGenerationApi, ApplicationShellRenderProps } from '@wuu/plugin-sdk';

export const descriptor = {id: 'independent-shell'};
export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const navigation = {id: 'navigation', cardinality: 'one' as const, service: 'ui.navigation'};
  api.registerUISlot(navigation);
  function Shell({slots}: ApplicationShellRenderProps) {
    return <main aria-label="Independent application"><slots.Slot name="independent-shell/navigation" /></main>;
  }
  api.registerApplicationShell({id: 'ui.shell', render: Shell});
}
