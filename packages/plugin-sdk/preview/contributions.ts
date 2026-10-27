import type { ApplicationWorkbenchV1, UIContributionRegistryV1 } from '../dist/ui-contributions.js';

const empty = Object.freeze([]);
const unavailable = () => { throw new Error('This contribution operation requires the production desktop host.'); };
/** The standalone preview deliberately has no production views, storage or process bridge. */
export const previewContributions: UIContributionRegistryV1 = {
  subscribe: () => () => {}, getGenerationActivation: () => undefined, retainGeneration: () => () => {},
  observeGenerationRender: () => ({ failed: unavailable, dispose() {} }), hasActivePlugin: () => false,
  getCommands: () => empty, resolveCommandActions: () => ({ actions: empty, failures: empty }), executeCommandAction: unavailable,
  recordCommandFailure: unavailable, getConversationCards: () => empty, subscribeConversationCards: () => () => {},
  recordConversationCardFailure: unavailable, getViewTypes: () => empty, getViewPlacements: () => empty,
  getNavigationEntries: () => empty, getWorkspaceTools: () => empty, getSettingsPages: () => empty,
  getRenderers: () => empty, getThemeTokens: () => empty, getStatusItems: () => empty, getPresenters: () => empty,
  recordPresenterFailure: unavailable, getLocaleEntries: () => ({}), getComposerStatusSources: () => empty,
  subscribeComposerStatusSources: () => () => {}, getComposerInlineCompletionProviders: () => empty,
  subscribeComposerInlineCompletionProviders: () => () => {},
  getConflicts: () => empty, getGenerationDiagnostics: () => empty, setConflictPreferences: unavailable,
};
const snapshot = Object.freeze({ version: 2 as const, views: empty, activeViewByRegion: {}, dismissedPlacementIds: empty, viewTypes: empty, renderers: empty, webPageViewIds: empty });
export const previewWorkbench: ApplicationWorkbenchV1 = {
  registry: previewContributions, getSnapshot: () => snapshot, subscribe: () => () => {},
  openView: unavailable, openPluginView: unavailable, closeView: unavailable, activateView: unavailable, deactivateRegion: unavailable,
  createViewHostAPI: unavailable, createRendererHostAPI: unavailable, createPresentationHostAPI: unavailable,
  renderLayers: () => null, renderContent: input => input.fallback, renderView: unavailable,
};
