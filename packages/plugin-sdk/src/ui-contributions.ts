import type { ReactNode, ComponentType } from 'react';
import type {
  CommandRegistration, ComposerStatusSourceRegistration, ComposerInlineCompletionProviderRegistration,
  ComposerInlineCompletionRefresh, ConversationCardRegistration, ViewTypeDefinition, ViewPlacementRegion,
  RendererDefinition, ThemeTokens, StatusItemDefinition, PresenterDefinition, PresentationMode,
  PluginCommandActionContext, PresentationTarget, OpenViewOptions, ViewHostAPI, PresentationHost,
} from './index.js';

/** Published activation identity; a code fingerprint can have several activations. */
export interface UIContributionIdentityV1 { readonly pluginId: string; readonly generation: string }
export interface RegisteredCommandV1 extends CommandRegistration, UIContributionIdentityV1 {}
export interface RegisteredViewV1 extends ViewTypeDefinition, UIContributionIdentityV1 { readonly order: number }
export interface RegisteredRendererV1 extends Omit<RendererDefinition, "render">, UIContributionIdentityV1 { readonly order: number; readonly render: ComponentType<{ content: unknown; metadata: Readonly<Record<string, unknown>>; host: PresentationHost }> }
export interface RegisteredPresenterV1 extends PresenterDefinition, UIContributionIdentityV1 { readonly mode: PresentationMode; readonly priority: number; readonly order: number }
export interface RegisteredStatusItemV1 extends StatusItemDefinition, UIContributionIdentityV1 { readonly order: number }
export interface RegisteredThemeTokensV1 extends ThemeTokens, UIContributionIdentityV1 { readonly id: string; readonly order: number }
export interface RegisteredViewPlacementV1 extends UIContributionIdentityV1 { readonly id: string; readonly view: string; readonly region: ViewPlacementRegion; readonly order: number }
export interface RegisteredViewEntryV1 extends UIContributionIdentityV1 {
  readonly id: string; readonly view: string; readonly title: string; readonly description?: string; readonly order?: number;
  readonly icon?: import('./ui-catalog.js').CatalogIconDescriptorV1;
}
export interface RegisteredConversationCardV1 extends UIContributionIdentityV1 {
  readonly id: string; readonly threadId: string; readonly title: string; readonly state: unknown; readonly order: number;
  readonly render: ConversationCardRegistration['render']; readonly dismiss: () => void;
}
export interface RegisteredComposerStatusSourceV1 extends ComposerStatusSourceRegistration, UIContributionIdentityV1 { readonly order: number }
export interface RegisteredComposerInlineCompletionProviderV1 extends ComposerInlineCompletionProviderRegistration, UIContributionIdentityV1 {
  readonly order: number; subscribeRefresh(listener: (scope?: ComposerInlineCompletionRefresh) => void): () => void;
}
export interface UICommandActionV1 { readonly command: RegisteredCommandV1; readonly enabled: boolean }
export interface UICommandActionResolutionV1 { readonly actions: readonly UICommandActionV1[]; readonly failures: readonly { command: RegisteredCommandV1; error: unknown }[] }

/** Lifecycle and immutable contribution discovery, not a product layout or business dispatcher.
 * Snapshots retain identity until their registry changes. Retained activations drain before
 * cleanup; stale callback dispatch fails rather than reaching a successor generation. */
export interface UIContributionRegistryV1 {
  getConflicts(): readonly import("./ui-settings-controls.js").SettingsControlsConflictV1[];
  getGenerationDiagnostics(pluginId: string, generation: string): readonly import("./ui-settings-controls.js").SettingsControlsDiagnosticV1[];
  setConflictPreferences(preferences: Readonly<Record<string, string>>): void;
  subscribe(listener: () => void): () => void;
  getGenerationActivation(pluginId: string, generation: string): object | undefined;
  retainGeneration(pluginId: string, generation: string): () => void;
  observeGenerationRender(pluginId: string, generation: string, initialError?: Error): { failed(error: Error): void; dispose(): void };
  hasActivePlugin(pluginId: string): boolean;
  getCommands(): readonly RegisteredCommandV1[];
  resolveCommandActions(context: PluginCommandActionContext): UICommandActionResolutionV1;
  executeCommandAction(command: RegisteredCommandV1, context: PluginCommandActionContext): Promise<unknown>;
  recordCommandFailure(command: RegisteredCommandV1, error: unknown): void;
  getConversationCards(): readonly RegisteredConversationCardV1[];
  subscribeConversationCards(listener: () => void): () => void;
  recordConversationCardFailure(card: RegisteredConversationCardV1, error: unknown): void;
  getViewTypes(): readonly RegisteredViewV1[];
  getViewPlacements(): readonly RegisteredViewPlacementV1[];
  getNavigationEntries(): readonly RegisteredViewEntryV1[];
  getWorkspaceTools(): readonly RegisteredViewEntryV1[];
  getSettingsPages(): readonly RegisteredViewEntryV1[];
  getRenderers(category?: RendererDefinition['category']): readonly RegisteredRendererV1[];
  getThemeTokens(theme?: string): readonly RegisteredThemeTokensV1[];
  getStatusItems(): readonly RegisteredStatusItemV1[];
  getPresenters(target: PresentationTarget, key?: string): readonly RegisteredPresenterV1[];
  recordPresenterFailure(record: RegisteredPresenterV1, error: unknown): void;
  getLocaleEntries(locale: string): Readonly<Record<string, string>>;
  getComposerStatusSources(): readonly RegisteredComposerStatusSourceV1[];
  subscribeComposerStatusSources(listener: () => void): () => void;
  getComposerInlineCompletionProviders(): readonly RegisteredComposerInlineCompletionProviderV1[];
  subscribeComposerInlineCompletionProviders(listener: () => void): () => void;
}

export interface WorkbenchViewStateV1 {
  readonly id: string; readonly pluginId: string; readonly generation: string; readonly viewTypeId: string;
  readonly region: ViewPlacementRegion; readonly persistence: 'session' | 'durable';
  readonly context: Readonly<Record<string, unknown>>; readonly sourcePlacementId?: string;
}
export interface WorkbenchSnapshotV1 {
  readonly version: 2; readonly views: readonly WorkbenchViewStateV1[];
  readonly activeViewByRegion: Readonly<Partial<Record<ViewPlacementRegion, string>>>;
  readonly dismissedPlacementIds: readonly string[];
  readonly viewTypes: readonly RegisteredViewV1[]; readonly renderers: readonly RegisteredRendererV1[];
  readonly webPageViewIds: readonly string[];
}
/** Business implementation supplied by the selected application provider, including its views,
 * layout state, storage policy and native-page lifetime. No host-global workbench is implied. */
export interface ApplicationWorkbenchV1 {
  readonly registry: UIContributionRegistryV1;
  getSnapshot(): WorkbenchSnapshotV1;
  subscribe(listener: () => void): () => void;
  openView(viewTypeId: string, options?: OpenViewOptions): Promise<string>;
  openPluginView(pluginId: string, viewTypeId: string, options?: OpenViewOptions): Promise<string>;
  closeView(instanceId: string): Promise<void>;
  activateView(instanceId: string): void;
  deactivateRegion(region: ViewPlacementRegion): void;
  createViewHostAPI(view: WorkbenchViewStateV1): ViewHostAPI;
  createRendererHostAPI(pluginId: string, generation: string): ViewHostAPI;
  createPresentationHostAPI(pluginId: string, generation: string, actions: readonly string[], dispatcher?: (action: string, input?: unknown) => unknown | Promise<unknown>): PresentationHost;
  renderLayers(input: { readonly settingsOnly?: boolean }): ReactNode;
  renderContent(input: { readonly category: RendererDefinition['category']; readonly contentType: string; readonly content: unknown; readonly metadata?: Readonly<Record<string, unknown>>; readonly fallback: ReactNode;
    readonly actions?: readonly string[];
    readonly dispatchAction?: (action: string, input: unknown, isCurrent: () => boolean, signal: AbortSignal) => unknown | Promise<unknown> }): ReactNode;
  renderView(input: { readonly pluginId: string; readonly viewTypeId: string; readonly region?: ViewPlacementRegion; readonly context?: Readonly<Record<string, unknown>>; readonly settings?: import('./index.js').SettingsPageHostAPI; readonly onFailure?: () => void }): ReactNode;
}

/** Fence a workbench consumer without changing the provider's snapshot identity. */
export function scopeApplicationWorkbench(source: () => ApplicationWorkbenchV1, signal: AbortSignal): ApplicationWorkbenchV1 {
  return new Proxy({} as ApplicationWorkbenchV1, { get: (_target, key) => {
    signal.throwIfAborted();
    const target = source();
    const value = Reflect.get(target, key, target);
    if (typeof value !== 'function') return value;
    return (...args: unknown[]) => {
      signal.throwIfAborted();
      const result: unknown = value.apply(target, args);
      if (key === 'subscribe' && typeof result === 'function') {
        let active = true;
        const off = () => { if (!active) return; active = false; signal.removeEventListener('abort', off); result(); };
        signal.addEventListener('abort', off, { once: true }); return off;
      }
      if ((key === 'createViewHostAPI' || key === 'createRendererHostAPI' || key === 'createPresentationHostAPI') && result && typeof result === 'object') {
        return new Proxy(result, { get: (handle, method) => {
          const value = Reflect.get(handle, method);
          if (typeof value !== 'function') return value;
          return async (...input: unknown[]) => {
            signal.throwIfAborted();
            const output = await value.apply(handle, input);
            signal.throwIfAborted();
            return output;
          };
        } });
      }
      if (result instanceof Promise) return result.then(value => { signal.throwIfAborted(); return value; });
      return result;
    };
  } });
}
