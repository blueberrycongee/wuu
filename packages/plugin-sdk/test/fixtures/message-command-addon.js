/** @param {import('@wuu/plugin-sdk').PluginGenerationApi} api */
export function activate(api) {
  api.registerCommand({
    id: 'inspect', title: 'Inspect command message', placements: ['conversation.message.actions'],
    when: context => context.target === 'conversation.message.actions' && context.item.id !== 'without-commands',
    enabled: context => context.target === 'conversation.message.actions' && context.item.status !== 'streaming',
    execute: context => api.invokeRuntime('inspect', context),
  });
}
