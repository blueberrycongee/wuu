/** @param {import('@wuu/plugin-sdk').PluginGenerationApi} api */
export function activate(api) {
  const React = api.react;
  api.registerUIContribution({
    id: 'inspect', target: 'wuu.default-conversation/conversation.message.actions',
    render({context}) {
      const [clicks, setClicks] = React.useState(0);
      React.useEffect(() => { void api.invokeRuntime('mounted'); }, []);
      return React.createElement('button', {
        'aria-label': 'Inspect raw message',
        onClick() { void api.invokeRuntime('inspect', context); setClicks(count => count + 1); },
      }, `Raw ${clicks}`);
    },
  });
}
