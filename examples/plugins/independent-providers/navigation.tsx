import type { PluginGenerationApi, UISlotRenderProps } from '@wuu/plugin-sdk';

export const descriptor = {id: 'independent-navigation'};
export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  function Navigation({application}: UISlotRenderProps) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    const [error, setError] = React.useState('');
    return <nav aria-label="Conversations">
      {snapshot.navigation.nodes.filter(node => node.kind === 'thread').map(node =>
        <button key={node.id} type="button" disabled={node.disabled} aria-current={node.active ? 'page' : undefined}
          onClick={() => {void application.invoke('thread.select', {threadId: node.id}).catch(reason => setError(String(reason)));}}>
          {node.label}
        </button>)}
      {error && <p role="alert">{error}</p>}
    </nav>;
  }
  api.registerUIContribution({id: 'ui.navigation', target: 'independent-shell/navigation', render: Navigation});
}
