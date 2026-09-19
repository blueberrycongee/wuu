import type * as React from "react";
import type { ExtensionIconDescriptor } from "../../shared/protocol";

import type {
  CSSSnippet,
  InspectorSectionDefinition,
  PluginUIKit,
  PresentationMode,
  PresentationTarget,
  PresenterDefinition,
  RendererDefinition,
  StatusItemDefinition,
  ThemeTokens,
  ToolActivityPresenterDefinition,
  ViewPlacementContribution,
  ViewPlacementRegion,
  ViewTypeDefinition,
} from "../../shared/workbench";
import { createPluginUIKit, VIEW_PLACEMENT_REGIONS } from "../../shared/workbench";
import {
  isPublicIconName,
  isPublicSyntaxTokenName,
  isPublicThemeTokenName,
} from "../../shared/themeContract.generated";

export const PLUGIN_SLOT_IDS = [
  "sidebar.primary",
  "sidebar.footer",
  "workspace.header",
  "conversation.header",
  "conversation.message.before",
  "conversation.message.after",
  "composer.above",
  "composer.toolbar",
] as const;

export type PluginSlotId = (typeof PLUGIN_SLOT_IDS)[number];

export const PLUGIN_SURFACE_IDS = [
  "conversation.timeline",
  "conversation.message",
] as const;

export type PluginSurfaceId = (typeof PLUGIN_SURFACE_IDS)[number];

export interface Disposable {
  dispose(): void;
}

export type PluginSlotRenderContext = Readonly<Record<string, unknown>>;

export interface PluginSlotRegistration {
  id: string;
  order?: number;
  render(context: PluginSlotRenderContext): React.ReactNode;
}

export type PluginSurfaceMode = "replace" | "wrap";

export interface PluginSurfaceRegistration {
  id: string;
  mode: PluginSurfaceMode;
  order?: number;
  render(context: PluginSlotRenderContext, fallback: React.ReactNode): React.ReactNode;
}

export interface PluginContributionDeclarations {
  readonly slots?: readonly Readonly<{ id: string; target: PluginSlotId; order?: number; title?: string }>[];
  readonly surfaces?: readonly Readonly<{ id: string; target: PluginSurfaceId; mode: PluginSurfaceMode; order?: number; title?: string }>[];
  readonly presenters?: readonly Readonly<{ id: string; target: PresentationTarget; mode: PresentationMode; priority?: number; title?: string }>[];
  readonly navigation?: readonly PluginViewEntryDeclaration[];
  readonly workspace_tools?: readonly PluginViewEntryDeclaration[];
  readonly settings_pages?: readonly PluginViewEntryDeclaration[];
}

export interface PluginViewEntryDeclaration {
  readonly id: string;
  readonly view: string;
  readonly title: string;
  readonly description?: string;
  readonly icon?: ExtensionIconDescriptor;
  readonly order?: number;
}

export interface RegisteredPluginViewEntry extends PluginViewEntryDeclaration {
  readonly pluginId: string;
  readonly generation: string;
}

export interface PluginCommandRegistration {
  id: string;
  title: string;
  order?: number;
  execute(input?: unknown): unknown | Promise<unknown>;
}

export interface ConversationCardRenderProps {
  readonly id: string;
  readonly threadId: string;
  readonly state: unknown;
  dismiss(): void;
}

export interface ConversationCardRegistration {
  id?: string;
  threadId?: string;
  title: string;
  state?: unknown;
  render(props: ConversationCardRenderProps): React.ReactNode;
}

export interface ConversationCardHandle extends Disposable {
  readonly id: string;
  update(state: unknown): void;
  dismiss(): void;
}

export interface PluginStyleRegistration {
  id: string;
  css: string;
  order?: number;
}

export interface PluginLocaleRegistration {
  id: string;
  locale: string;
  entries: Readonly<Record<string, string>>;
  order?: number;
}

export type ComposerStatusState = "running" | "queued" | "waiting" | "error" | "idle";

export type ComposerStatusAction = Readonly<{
  kind: "open-session";
  sessionId: string;
}>;

export interface ComposerStatusItem {
  readonly id: string;
  readonly label: string;
  readonly state?: ComposerStatusState;
  readonly secondaryText?: string;
  readonly tooltip?: string;
  readonly updatedAt?: string;
  readonly action?: ComposerStatusAction;
}

export type ComposerStatusContext = Readonly<{
  threadId?: string;
  mainConversation: true;
}>;

export interface ComposerStatusSourceRegistration {
  readonly id: string;
  readonly order?: number;
  getSnapshot(context: ComposerStatusContext): readonly ComposerStatusItem[];
  subscribe(context: ComposerStatusContext, listener: () => void): () => void;
}

export interface PluginGenerationApi {
  /** The React runtime owned by the Wuu renderer. Plugin bundles should use this object. */
  readonly react: typeof React;
  /** Host-owned primitives that inherit the active Wuu theme and layout rhythm. */
  readonly ui: PluginUIKit;
  readonly pluginId: string;
  readonly generation: string;
  invokeRuntime(method: string, input?: unknown, options?: PluginRuntimeInvokeOptions): Promise<unknown>;
  listWorkspaces(): Promise<PluginWorkspaceSnapshot>;
  /** List live threads inside a workspace, addressed by its root path (the
   * `root` value returned by listWorkspaces). Read-only kernel session state;
   * ephemeral and archived threads are excluded by the host. */
  listThreads(workspaceRoot: string): Promise<readonly PluginThreadSummary[]>;
  onHostEvent(handler: (event: unknown) => void): Disposable;
  registerSlot(slotId: PluginSlotId, contribution: PluginSlotRegistration): Disposable;
  registerSurface(surfaceId: PluginSurfaceId, contribution: PluginSurfaceRegistration): Disposable;
  registerCommand(command: PluginCommandRegistration): Disposable;
  /** Show a session-local card at the bottom of a conversation. */
  showConversationCard(card: ConversationCardRegistration): ConversationCardHandle;
  registerStyle(style: PluginStyleRegistration): Disposable;
  registerLocale(locale: PluginLocaleRegistration): Disposable;
  /** Publish structured status data for the host-owned composer capsule row. */
  registerComposerStatusSource(source: ComposerStatusSourceRegistration): Disposable;
  registerCleanup(cleanup: () => void): Disposable;

  // Phase C — Workbench API

  /** Register a view type that the host can open in a semantic region. */
  registerViewType(definition: ViewTypeDefinition): Disposable;
  /** Request a default View placement in a stable host-owned region. */
  registerViewPlacement(contribution: ViewPlacementContribution): Disposable;
  /** Register a short, context-driven summary in the host-owned Inspector. */
  registerInspectorSection(definition: InspectorSectionDefinition): Disposable;
  /** Register a custom content renderer (message, tool result, document, file). */
  registerRenderer(definition: RendererDefinition): Disposable;
  /** Apply theme token overrides for a specific theme. */
  registerThemeTokens(tokens: ThemeTokens): Disposable;
  /** Inject a CSS snippet scoped to this plugin. */
  registerCSSSnippet(snippet: CSSSnippet): Disposable;
  /** Register a status bar item. */
  registerStatusItem(item: StatusItemDefinition): Disposable;
  registerPresenter(definition: PresenterDefinition): Disposable;
  registerToolActivityPresenter(definition: ToolActivityPresenterDefinition): Disposable;
}

export interface PluginRuntimeInvokeOptions {
  readonly workspaceId?: string;
}

export interface PluginWorkspace {
  readonly id: string;
  readonly name: string;
  readonly root: string;
  readonly available: boolean;
}

export interface PluginWorkspaceSnapshot {
  readonly workspaces: readonly PluginWorkspace[];
  readonly activeWorkspaceId?: string;
}

export interface PluginThreadSummary {
  readonly id: string;
  readonly title: string;
  readonly updatedAt?: string;
  readonly pinned?: boolean;
}

export interface ActivatePluginGenerationOptions {
  pluginId: string;
  generation: string;
  contributions?: PluginContributionDeclarations;
  register(api: PluginGenerationApi): void | Promise<void>;
}

export interface RegisteredPluginSlotContribution extends PluginSlotRegistration {
  readonly pluginId: string;
  readonly generation: string;
  readonly title?: string;
}

export interface RegisteredPluginSurfaceContribution extends PluginSurfaceRegistration {
  readonly pluginId: string;
  readonly generation: string;
}

export interface RegisteredPluginCommand extends PluginCommandRegistration {
  readonly pluginId: string;
  readonly generation: string;
}

export interface RegisteredComposerStatusSource extends ComposerStatusSourceRegistration {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
}

export interface RegisteredConversationCard {
  readonly id: string;
  readonly pluginId: string;
  readonly generation: string;
  readonly threadId: string;
  readonly title: string;
  readonly state: unknown;
  readonly order: number;
  readonly render: ConversationCardRegistration["render"];
  readonly dismiss: () => void;
}

export interface RegisteredViewType extends ViewTypeDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
}

export interface RegisteredInspectorSection extends InspectorSectionDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
}

export interface RegisteredViewPlacement {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
  readonly id: string;
  readonly view: string;
  readonly region: ViewPlacementRegion;
}

export interface RegisteredRenderer extends RendererDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
}

export interface RegisteredThemeTokens extends ThemeTokens {
  readonly pluginId: string;
  readonly generation: string;
  readonly id: string;
  readonly order: number;
}

export interface RegisteredStatusItem extends StatusItemDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
}

export interface RegisteredToolActivityPresenter extends ToolActivityPresenterDefinition {
  readonly pluginId: string;
  readonly generation: string;
}

export interface RegisteredPresenter extends PresenterDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly mode: PresentationMode;
  readonly priority: number;
  readonly order: number;
}

export interface PluginConflictCandidate {
  readonly pluginId: string;
  readonly generation: string;
  readonly contributionId: string;
  readonly title?: string;
}

export interface PluginContributionConflict {
  readonly key: string;
  readonly kind: "surface" | "presenter";
  readonly target: string;
  readonly presentationKey?: string;
  readonly candidates: readonly PluginConflictCandidate[];
  readonly winnerPluginId: string;
}

export type PluginDiagnosticKind = "activation" | "cleanup" | "render";

export interface PluginGenerationDiagnostic {
  readonly pluginId: string;
  readonly generation: string;
  readonly kind: PluginDiagnosticKind;
  readonly message: string;
  readonly cause: unknown;
  readonly slotId?: PluginSlotId;
  readonly surfaceId?: PluginSurfaceId;
  readonly contributionId?: string;
}

export interface PluginHostOptions {
  /** Inject the renderer's existing React runtime; the host never supplies another copy. */
  react: typeof React;
  styleContainer?: HTMLElement | (() => HTMLElement | null);
  invokeRuntime?: (request: Readonly<{
    pluginId: string;
    generation: string;
    method: string;
    input?: unknown;
    workspaceId?: string;
  }>) => Promise<unknown>;
  listWorkspaces?: () => Promise<PluginWorkspaceSnapshot>;
  listThreads?: (workspaceRoot: string) => Promise<readonly PluginThreadSummary[]>;
}

interface OrderedRecord {
  readonly pluginId: string;
  readonly generation: string;
  readonly id: string;
  readonly order: number;
  removed: boolean;
}

interface SlotRecord extends OrderedRecord {
  readonly slotId: PluginSlotId;
  readonly title?: string;
  readonly render: PluginSlotRegistration["render"];
}

interface SurfaceRecord extends OrderedRecord {
  readonly surfaceId: PluginSurfaceId;
  readonly mode: PluginSurfaceMode;
  readonly title?: string;
  readonly render: PluginSurfaceRegistration["render"];
}

interface CommandRecord extends OrderedRecord {
  readonly title: string;
  readonly execute: PluginCommandRegistration["execute"];
}

interface ConversationCardRecord extends OrderedRecord {
  readonly threadId: string;
  readonly title: string;
  state: unknown;
  readonly render: ConversationCardRegistration["render"];
  readonly dismiss: () => void;
}

interface StyleRecord extends OrderedRecord {
  readonly css: string;
  element?: HTMLStyleElement;
}

interface LocaleRecord extends OrderedRecord {
  readonly locale: string;
  readonly entries: Readonly<Record<string, string>>;
}

// Phase C — Workbench record types

interface ViewTypeRecord extends OrderedRecord {
  readonly definition: ViewTypeDefinition;
}

interface ViewPlacementRecord extends OrderedRecord {
  readonly view: string;
  readonly region: ViewPlacementRegion;
}

interface InspectorSectionRecord extends OrderedRecord {
  readonly title: string;
  readonly when: InspectorSectionDefinition["when"];
  readonly render: InspectorSectionDefinition["render"];
}

interface RendererRecord extends OrderedRecord {
  readonly definition: RendererDefinition;
}

interface ThemeTokenRecord extends OrderedRecord {
  readonly tokens: ThemeTokens;
}

interface CSSSnippetRecord extends OrderedRecord {
  readonly snippet: CSSSnippet;
  element?: HTMLStyleElement;
}

interface StatusItemRecord extends OrderedRecord {
  readonly item: StatusItemDefinition;
}

interface ComposerStatusSourceRecord extends OrderedRecord {
  readonly getSnapshot: ComposerStatusSourceRegistration["getSnapshot"];
  readonly subscribe: ComposerStatusSourceRegistration["subscribe"];
}

interface PresenterRecord extends OrderedRecord {
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly mode: PresentationMode;
  readonly title?: string;
  readonly render: PresenterDefinition["render"];
  readonly compatibilityRender?: ToolActivityPresenterDefinition["render"];
}

interface GenerationState {
  readonly pluginId: string;
  readonly generation: string;
  readonly declaredContributions?: PluginContributionDeclarations;
  readonly slots: SlotRecord[];
  readonly surfaces: SurfaceRecord[];
  readonly commands: CommandRecord[];
  readonly conversationCards: ConversationCardRecord[];
  readonly styles: StyleRecord[];
  readonly locales: LocaleRecord[];
  // Phase C — Workbench
  readonly views: ViewTypeRecord[];
  readonly viewPlacements: ViewPlacementRecord[];
  readonly inspectorSections: InspectorSectionRecord[];
  readonly renderers: RendererRecord[];
  readonly themeTokens: ThemeTokenRecord[];
  readonly cssSnippets: CSSSnippetRecord[];
  readonly statusItems: StatusItemRecord[];
  readonly composerStatusSources: ComposerStatusSourceRecord[];
  readonly toolActivityPresenters: PresenterRecord[];
  readonly registrationKeys: Set<string>;
  readonly hostEventHandlers: Set<(event: unknown) => void>;
  readonly teardown: Disposable[];
  acceptingRegistrations: boolean;
  active: boolean;
  disposed: boolean;
}

interface PendingActivation {
  readonly state: GenerationState;
  cancelled: boolean;
}

const EMPTY_SLOT_SNAPSHOT: readonly RegisteredPluginSlotContribution[] = Object.freeze([]);
const EMPTY_SURFACE_SNAPSHOT: readonly RegisteredPluginSurfaceContribution[] = Object.freeze([]);
const EMPTY_COMMAND_SNAPSHOT: readonly RegisteredPluginCommand[] = Object.freeze([]);
const EMPTY_CONVERSATION_CARD_SNAPSHOT: readonly RegisteredConversationCard[] = Object.freeze([]);
const EMPTY_VIEW_SNAPSHOT: readonly RegisteredViewType[] = Object.freeze([]);
const EMPTY_VIEW_PLACEMENT_SNAPSHOT: readonly RegisteredViewPlacement[] = Object.freeze([]);
const EMPTY_INSPECTOR_SECTION_SNAPSHOT: readonly RegisteredInspectorSection[] = Object.freeze([]);
const EMPTY_VIEW_ENTRY_SNAPSHOT: readonly RegisteredPluginViewEntry[] = Object.freeze([]);
const EMPTY_RENDERER_SNAPSHOT: readonly RegisteredRenderer[] = Object.freeze([]);
const EMPTY_THEME_TOKEN_SNAPSHOT: readonly RegisteredThemeTokens[] = Object.freeze([]);
const EMPTY_STATUS_ITEM_SNAPSHOT: readonly RegisteredStatusItem[] = Object.freeze([]);
const EMPTY_COMPOSER_STATUS_SOURCE_SNAPSHOT: readonly RegisteredComposerStatusSource[] = Object.freeze([]);
const EMPTY_PRESENTER_SNAPSHOT: readonly RegisteredPresenter[] = Object.freeze([]);
const EMPTY_TOOL_ACTIVITY_PRESENTER_SNAPSHOT: readonly RegisteredToolActivityPresenter[] = Object.freeze([]);
const EMPTY_LOCALE_SNAPSHOT: Readonly<Record<string, string>> = Object.freeze({});
const EMPTY_DIAGNOSTIC_SNAPSHOT: readonly PluginGenerationDiagnostic[] = Object.freeze([]);

export class PluginGenerationSupersededError extends Error {
  constructor(pluginId: string, generation: string) {
    super(`Plugin generation ${pluginId}@${generation} was superseded before activation`);
    this.name = "PluginGenerationSupersededError";
  }
}

/**
 * Owns renderer plugin registrations and swaps one active generation per plugin.
 * This lifecycle boundary provides cleanup and startup recovery, not strong isolation.
 */
export class PluginHost {
  readonly react: typeof React;
  readonly ui: PluginUIKit;

  private readonly styleContainer?: PluginHostOptions["styleContainer"];
  private readonly runtimeInvoker?: PluginHostOptions["invokeRuntime"];
  private readonly workspaceLister?: PluginHostOptions["listWorkspaces"];
  private readonly threadLister?: PluginHostOptions["listThreads"];
  private readonly activeGenerations = new Map<string, GenerationState>();
  private readonly pendingActivations = new Map<string, PendingActivation>();
  private readonly slotSnapshots = new Map<PluginSlotId, readonly RegisteredPluginSlotContribution[]>();
  private readonly slotListeners = new Map<PluginSlotId, Set<() => void>>();
  private readonly surfaceSnapshots = new Map<PluginSurfaceId, readonly RegisteredPluginSurfaceContribution[]>();
  private readonly surfaceListeners = new Map<PluginSurfaceId, Set<() => void>>();
  private readonly listeners = new Set<() => void>();
  private readonly localeSnapshots = new Map<string, Readonly<Record<string, string>>>();
  private readonly diagnostics = new Map<string, readonly PluginGenerationDiagnostic[]>();
  private readonly presenterQuerySnapshots = new Map<string, readonly RegisteredPresenter[]>();
  private conflictPreferences: Readonly<Record<string, string>> = Object.freeze({});
  private conflictSnapshot: readonly PluginContributionConflict[] = Object.freeze([]);
  private commandSnapshot: readonly RegisteredPluginCommand[] = EMPTY_COMMAND_SNAPSHOT;
  private conversationCardSnapshot: readonly RegisteredConversationCard[] = EMPTY_CONVERSATION_CARD_SNAPSHOT;
  private readonly conversationCardListeners = new Set<() => void>();
  private activeConversationThreadId?: string;
  private nextConversationCardSequence = 1;
  private viewSnapshot: readonly RegisteredViewType[] = EMPTY_VIEW_SNAPSHOT;
  private viewPlacementSnapshot: readonly RegisteredViewPlacement[] = EMPTY_VIEW_PLACEMENT_SNAPSHOT;
  private inspectorSectionSnapshot: readonly RegisteredInspectorSection[] = EMPTY_INSPECTOR_SECTION_SNAPSHOT;
  private navigationSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private workspaceToolSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private settingsPageSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private rendererSnapshot: readonly RegisteredRenderer[] = EMPTY_RENDERER_SNAPSHOT;
  private themeTokenSnapshot: readonly RegisteredThemeTokens[] = EMPTY_THEME_TOKEN_SNAPSHOT;
  private statusItemSnapshot: readonly RegisteredStatusItem[] = EMPTY_STATUS_ITEM_SNAPSHOT;
  private composerStatusSourceSnapshot: readonly RegisteredComposerStatusSource[] = EMPTY_COMPOSER_STATUS_SOURCE_SNAPSHOT;
  private readonly composerStatusSourceListeners = new Set<() => void>();
  private presenterSnapshot: readonly RegisteredPresenter[] = EMPTY_PRESENTER_SNAPSHOT;
  private toolActivityPresenterSnapshot: readonly RegisteredToolActivityPresenter[] = EMPTY_TOOL_ACTIVITY_PRESENTER_SNAPSHOT;

  constructor(options: PluginHostOptions) {
    this.react = options.react;
    this.ui = createPluginUIKit(options.react);
    this.styleContainer = options.styleContainer;
    this.runtimeInvoker = options.invokeRuntime;
    this.workspaceLister = options.listWorkspaces;
    this.threadLister = options.listThreads;
  }

  async activateGeneration(options: ActivatePluginGenerationOptions): Promise<Disposable> {
    const pluginId = requireNonEmpty(options.pluginId, "plugin id");
    const generation = requireNonEmpty(options.generation, "plugin generation");
    const state = createGenerationState(pluginId, generation, options.contributions);
    const pending: PendingActivation = { state, cancelled: false };

    const previousPending = this.pendingActivations.get(pluginId);
    if (previousPending) {
      previousPending.cancelled = true;
    }
    this.pendingActivations.set(pluginId, pending);

    try {
      await options.register(this.createGenerationApi(state));
      state.acceptingRegistrations = false;
      this.assertDeclaredContributionsRegistered(state);
      this.assertViewPlacementTargets(state);
      this.assertDeclaredViewEntryTargets(state);
      this.assertToolActivityPresenterOwnership(state);
    } catch (error: unknown) {
      state.acceptingRegistrations = false;
      if (this.pendingActivations.get(pluginId) === pending) {
        this.pendingActivations.delete(pluginId);
      }
      this.addDiagnostic({
        pluginId,
        generation,
        kind: "activation",
        message: `Plugin generation registration failed: ${errorMessage(error)}`,
        cause: error,
      });
      this.disposeGeneration(state);
      throw error;
    }

    if (pending.cancelled || this.pendingActivations.get(pluginId) !== pending) {
      this.disposeGeneration(state);
      throw new PluginGenerationSupersededError(pluginId, generation);
    }

    this.pendingActivations.delete(pluginId);
    const previous = this.activeGenerations.get(pluginId);
    if (this.diagnostics.delete(diagnosticKey(pluginId, generation))) {
      this.notifyListeners();
    }
    this.activeGenerations.set(pluginId, state);
    state.active = true;
    if (previous) {
      previous.active = false;
      this.disposeGeneration(previous);
    }
    this.refreshPublicState();

    return createDisposable(() => {
      if (this.activeGenerations.get(pluginId) === state) {
        this.unload(pluginId);
      }
    });
  }

  disable(pluginId: string): void {
    this.unload(pluginId);
  }

  unload(pluginId: string): void {
    const normalizedPluginId = requireNonEmpty(pluginId, "plugin id");
    const pending = this.pendingActivations.get(normalizedPluginId);
    if (pending) {
      pending.cancelled = true;
      this.pendingActivations.delete(normalizedPluginId);
    }

    const active = this.activeGenerations.get(normalizedPluginId);
    if (!active) {
      return;
    }
    this.activeGenerations.delete(normalizedPluginId);
    active.active = false;
    this.disposeGeneration(active);
    this.refreshPublicState();
  }

  getSlotSnapshot(slotId: PluginSlotId): readonly RegisteredPluginSlotContribution[] {
    return this.slotSnapshots.get(slotId) ?? EMPTY_SLOT_SNAPSHOT;
  }

  subscribeSlot(slotId: PluginSlotId, listener: () => void): () => void {
    let listeners = this.slotListeners.get(slotId);
    if (!listeners) {
      listeners = new Set();
      this.slotListeners.set(slotId, listeners);
    }
    listeners.add(listener);
    return () => {
      listeners?.delete(listener);
    };
  }

  getSurfaceSnapshot(surfaceId: PluginSurfaceId): readonly RegisteredPluginSurfaceContribution[] {
    return this.surfaceSnapshots.get(surfaceId) ?? EMPTY_SURFACE_SNAPSHOT;
  }

  subscribeSurface(surfaceId: PluginSurfaceId, listener: () => void): () => void {
    let listeners = this.surfaceListeners.get(surfaceId);
    if (!listeners) {
      listeners = new Set();
      this.surfaceListeners.set(surfaceId, listeners);
    }
    listeners.add(listener);
    return () => {
      listeners?.delete(listener);
    };
  }

  getCommands(): readonly RegisteredPluginCommand[] {
    return this.commandSnapshot;
  }

  setActiveConversationThread(threadId?: string): void {
    this.activeConversationThreadId = threadId?.trim() || undefined;
  }

  getActiveConversationThreadId(): string | undefined {
    return this.activeConversationThreadId;
  }

  getConversationCards(): readonly RegisteredConversationCard[] {
    return this.conversationCardSnapshot;
  }

  subscribeConversationCards(listener: () => void): () => void {
    this.conversationCardListeners.add(listener);
    return () => this.conversationCardListeners.delete(listener);
  }

  getViewTypes(): readonly RegisteredViewType[] {
    return this.viewSnapshot;
  }

  getViewPlacements(): readonly RegisteredViewPlacement[] {
    return this.viewPlacementSnapshot;
  }

  getInspectorSections(): readonly RegisteredInspectorSection[] {
    return this.inspectorSectionSnapshot;
  }

  getNavigationEntries(): readonly RegisteredPluginViewEntry[] {
    return this.navigationSnapshot;
  }

  getWorkspaceTools(): readonly RegisteredPluginViewEntry[] {
    return this.workspaceToolSnapshot;
  }

  getSettingsPages(): readonly RegisteredPluginViewEntry[] {
    return this.settingsPageSnapshot;
  }

  getRenderers(category?: RendererDefinition["category"]): readonly RegisteredRenderer[] {
    if (category === undefined) {
      return this.rendererSnapshot;
    }
    return Object.freeze(this.rendererSnapshot.filter((renderer) => renderer.category === category));
  }

  getThemeTokens(theme?: string): readonly RegisteredThemeTokens[] {
    if (theme === undefined) {
      return this.themeTokenSnapshot;
    }
    return Object.freeze(this.themeTokenSnapshot.filter((tokens) => tokens.theme === theme));
  }

  getStatusItems(): readonly RegisteredStatusItem[] {
    return this.statusItemSnapshot;
  }

  getComposerStatusSources(): readonly RegisteredComposerStatusSource[] {
    return this.composerStatusSourceSnapshot;
  }

  subscribeComposerStatusSources(listener: () => void): () => void {
    this.composerStatusSourceListeners.add(listener);
    return () => this.composerStatusSourceListeners.delete(listener);
  }

  getToolActivityPresenters(): readonly RegisteredToolActivityPresenter[] {
    return this.toolActivityPresenterSnapshot;
  }

  getToolActivityPresenter(key: string): RegisteredToolActivityPresenter | undefined {
    return this.getToolActivityPresenters().find((presenter) => presenter.key === key);
  }

  getPresenters(target: PresentationTarget, key?: string): readonly RegisteredPresenter[] {
    const query = `${target}\u0000${key ?? ""}\u0000${key === undefined ? "absent" : "present"}`;
    const cached = this.presenterQuerySnapshots.get(query);
    if (cached) return cached;
    const snapshot = Object.freeze(this.presenterSnapshot.filter((presenter) =>
      presenter.target === target && presenter.key === key));
    this.presenterQuerySnapshots.set(query, snapshot);
    return snapshot;
  }

  isGenerationActive(pluginId: string, generation: string): boolean {
    return this.activeGenerations.get(pluginId)?.generation === generation;
  }

  hasActivePlugin(pluginId: string): boolean {
    return this.activeGenerations.has(pluginId);
  }

  getLocaleEntries(locale: string): Readonly<Record<string, string>> {
    return this.localeSnapshots.get(locale) ?? EMPTY_LOCALE_SNAPSHOT;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  getGenerationDiagnostics(pluginId: string, generation: string): readonly PluginGenerationDiagnostic[] {
    return this.diagnostics.get(diagnosticKey(pluginId, generation)) ?? EMPTY_DIAGNOSTIC_SNAPSHOT;
  }

  getConflicts(): readonly PluginContributionConflict[] {
    return this.conflictSnapshot;
  }

  setConflictPreferences(preferences: Readonly<Record<string, string>>): void {
    this.conflictPreferences = Object.freeze({ ...preferences });
    this.refreshPublicState();
  }

  setConflictPreference(key: string, pluginId: string): void {
    this.setConflictPreferences({ ...this.conflictPreferences, [key]: pluginId });
  }

  recordRenderFailure(
    contribution: RegisteredPluginSlotContribution | RegisteredPluginSurfaceContribution,
    location: { slotId: PluginSlotId } | { surfaceId: PluginSurfaceId },
    error: unknown,
  ): void {
    const kind = "slotId" in location ? "slot" : "surface";
    this.addDiagnostic({
      pluginId: contribution.pluginId,
      generation: contribution.generation,
      kind: "render",
      message: `Plugin ${kind} contribution ${contribution.id} failed to render: ${errorMessage(error)}`,
      cause: error,
      ...location,
      contributionId: contribution.id,
    });
  }

  recordPresenterFailure(contribution: RegisteredPresenter, error: unknown): void {
    this.addDiagnostic({
      pluginId: contribution.pluginId,
      generation: contribution.generation,
      kind: "render",
      message: `Plugin presenter contribution ${contribution.id} failed to render: ${errorMessage(error)}`,
      cause: error,
      contributionId: contribution.id,
    });
  }

  recordConversationCardFailure(card: RegisteredConversationCard, error: unknown): void {
    this.addDiagnostic({
      pluginId: card.pluginId,
      generation: card.generation,
      kind: "render",
      message: `Plugin conversation card ${card.id} failed to render: ${errorMessage(error)}`,
      cause: error,
      contributionId: card.id,
    });
  }

  recordInspectorFailure(contribution: RegisteredInspectorSection, error: unknown): void {
    this.addDiagnostic({
      pluginId: contribution.pluginId,
      generation: contribution.generation,
      kind: "render",
      message: `Plugin Inspector section ${contribution.id} failed to render: ${errorMessage(error)}`,
      cause: error,
      contributionId: contribution.id,
    });
  }

  publishHostEvent(event: unknown): void {
    for (const state of this.activeGenerations.values()) {
      for (const handler of state.hostEventHandlers) {
        try {
          handler(event);
        } catch (error: unknown) {
          this.addDiagnostic({
            pluginId: state.pluginId,
            generation: state.generation,
            kind: "render",
            message: `Plugin host event handler failed: ${errorMessage(error)}`,
            cause: error,
          });
        }
      }
    }
  }

  private createGenerationApi(state: GenerationState): PluginGenerationApi {
    const registerViewPlacement = (
      contribution: {
        id: string;
        view: string;
        region: ViewPlacementRegion;
        priority?: number;
      },
    ): Disposable => {
      this.assertAccepting(state);
      const id = this.claimRegistrationId(state, "view-placement", contribution.id);
      const record: ViewPlacementRecord = {
        pluginId: state.pluginId,
        generation: state.generation,
        id,
        order: normalizeOrder(contribution.priority),
        removed: false,
        view: requireNonEmpty(contribution.view, "view placement view id"),
        region: contribution.region,
      };
      state.viewPlacements.push(record);
      return this.ownRecord(state, record);
    };

    return Object.freeze({
      react: this.react,
      ui: this.ui,
      pluginId: state.pluginId,
      generation: state.generation,
      invokeRuntime: async (method: string, input?: unknown, options?: PluginRuntimeInvokeOptions) => {
        if (!this.runtimeInvoker) {
          throw new Error("Plugin runtime requests are unavailable");
        }
        if (this.activeGenerations.get(state.pluginId) !== state || state.disposed) {
          throw new Error("Plugin generation is no longer active");
        }
        return this.runtimeInvoker({
          pluginId: state.pluginId,
          generation: state.generation,
          method: requireNonEmpty(method, "plugin runtime method"),
          input,
          ...(options?.workspaceId ? { workspaceId: options.workspaceId } : {}),
        });
      },
      listWorkspaces: async () => {
        if (!this.workspaceLister) {
          throw new Error("Workspace listing is unavailable");
        }
        if (this.activeGenerations.get(state.pluginId) !== state || state.disposed) {
          throw new Error("Plugin generation is no longer active");
        }
        return this.workspaceLister();
      },
      listThreads: async (workspaceRoot: string) => {
        if (!this.threadLister) {
          throw new Error("Thread listing is unavailable");
        }
        if (this.activeGenerations.get(state.pluginId) !== state || state.disposed) {
          throw new Error("Plugin generation is no longer active");
        }
        return this.threadLister(requireNonEmpty(workspaceRoot, "workspace root"));
      },
      onHostEvent: (handler: (event: unknown) => void) => {
        this.assertUsable(state);
        state.hostEventHandlers.add(handler);
        const disposable = createDisposable(() => state.hostEventHandlers.delete(handler));
        state.teardown.push(disposable);
        return disposable;
      },
      registerSlot: (slotId: PluginSlotId, contribution: PluginSlotRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, `slot:${slotId}`, contribution.id);
        const order = normalizeOrder(contribution.order);
        this.assertDeclaredContribution(state, "slot", { id, target: slotId, order });
        const record: SlotRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order,
          slotId,
          title: declaredContributionTitle(state, "slot", id),
          render: contribution.render,
          removed: false,
        };
        state.slots.push(record);
        return this.ownRecord(state, record);
      },
      registerSurface: (surfaceId: PluginSurfaceId, contribution: PluginSurfaceRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, `surface:${surfaceId}`, contribution.id);
        const order = normalizeOrder(contribution.order);
        const mode = normalizeSurfaceMode(contribution.mode);
        this.assertDeclaredContribution(state, "surface", { id, target: surfaceId, mode, order });
        const record: SurfaceRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order,
          surfaceId,
          mode,
          title: declaredContributionTitle(state, "surface", id),
          render: contribution.render,
          removed: false,
        };
        state.surfaces.push(record);
        return this.ownRecord(state, record);
      },
      registerCommand: (command: PluginCommandRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "command", command.id);
        const record: CommandRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(command.order),
          title: requireNonEmpty(command.title, "command title"),
          execute: command.execute,
          removed: false,
        };
        state.commands.push(record);
        return this.ownRecord(state, record);
      },
      showConversationCard: (card: ConversationCardRegistration): ConversationCardHandle => {
        this.assertUsable(state);
        const threadId = card.threadId?.trim() || this.activeConversationThreadId;
        if (!threadId) {
          throw new Error("Plugin conversation card requires an active or explicit thread id");
        }
        const localId = card.id?.trim() || `card-${this.nextConversationCardSequence}`;
        if (state.conversationCards.some((record) => !record.removed && record.id === localId)) {
          throw new Error(`Duplicate plugin conversation card id: ${localId}`);
        }
        if (typeof card.render !== "function") {
          throw new Error("Plugin conversation card render must be a function");
        }
        const sequence = this.nextConversationCardSequence++;
        let cleanupDisposable: Disposable | undefined;
        const dismiss = (): void => {
          if (record.removed) return;
          record.removed = true;
          const cardIndex = state.conversationCards.indexOf(record);
          if (cardIndex >= 0) state.conversationCards.splice(cardIndex, 1);
          if (cleanupDisposable) {
            const teardownIndex = state.teardown.indexOf(cleanupDisposable);
            if (teardownIndex >= 0) state.teardown.splice(teardownIndex, 1);
          }
          this.refreshConversationCards();
        };
        const record: ConversationCardRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id: localId,
          order: sequence,
          threadId,
          title: requireNonEmpty(card.title, "conversation card title"),
          state: card.state,
          render: card.render,
          dismiss,
          removed: false,
        };
        state.conversationCards.push(record);
        const handle: ConversationCardHandle = Object.freeze({
          id: localId,
          update: (nextState: unknown) => {
            this.assertUsable(state);
            if (record.removed) throw new Error(`Plugin conversation card is no longer active: ${localId}`);
            record.state = nextState;
            this.refreshConversationCards();
          },
          dismiss,
          dispose: dismiss,
        });
        cleanupDisposable = createDisposable(dismiss);
        state.teardown.push(cleanupDisposable);
        if (state.active) this.refreshConversationCards();
        return handle;
      },
      registerStyle: (style: PluginStyleRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "style", style.id);
        const record: StyleRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(style.order),
          css: style.css,
          removed: false,
        };
        state.styles.push(record);
        return this.ownRecord(state, record, () => record.element?.remove());
      },
      registerLocale: (locale: PluginLocaleRegistration) => {
        this.assertAccepting(state);
        const normalizedLocale = requireNonEmpty(locale.locale, "locale");
        const id = this.claimRegistrationId(state, `locale:${normalizedLocale}`, locale.id);
        const record: LocaleRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(locale.order),
          locale: normalizedLocale,
          entries: Object.freeze({ ...locale.entries }),
          removed: false,
        };
        state.locales.push(record);
        return this.ownRecord(state, record);
      },
      registerComposerStatusSource: (source: ComposerStatusSourceRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "composer-status-source", source.id);
        if (typeof source.getSnapshot !== "function" || typeof source.subscribe !== "function") {
          throw new Error("Composer status source requires getSnapshot and subscribe functions");
        }
        const record: ComposerStatusSourceRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(source.order),
          getSnapshot: source.getSnapshot,
          subscribe: source.subscribe,
          removed: false,
        };
        state.composerStatusSources.push(record);
        return this.ownRecord(state, record);
      },
      registerCleanup: (cleanup: () => void) => {
        this.assertAccepting(state);
        const disposable = createDisposable(() => {
          try {
            cleanup();
          } catch (error: unknown) {
            this.addDiagnostic({
              pluginId: state.pluginId,
              generation: state.generation,
              kind: "cleanup",
              message: `Plugin cleanup failed: ${errorMessage(error)}`,
              cause: error,
            });
          }
        });
        state.teardown.push(disposable);
        return disposable;
      },

      registerViewType: (definition: ViewTypeDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "view", definition.id);
        validatePublicIcon(definition.icon, `Plugin view ${id}`);
        const record: ViewTypeRecord = {
          pluginId: state.pluginId, generation: state.generation, id,
          order: 0, removed: false, definition: { ...definition, id },
        };
        state.views.push(record);
        return this.ownRecord(state, record);
      },

      registerViewPlacement: (contribution: ViewPlacementContribution) => {
        const region = normalizeViewPlacementRegion(contribution.region);
        return registerViewPlacement({
          id: contribution.id,
          view: contribution.view,
          region,
          priority: contribution.priority,
        });
      },

      registerInspectorSection: (definition: InspectorSectionDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "inspector-section", definition.id);
        const record: InspectorSectionRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(definition.priority),
          removed: false,
          title: requireNonEmpty(definition.title, "Inspector section title"),
          when: definition.when,
          render: definition.render,
        };
        state.inspectorSections.push(record);
        return this.ownRecord(state, record);
      },

      registerRenderer: (definition: RendererDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "renderer", definition.id);
        const record: RendererRecord = {
          pluginId: state.pluginId, generation: state.generation, id,
          order: definition.priority ?? 0, removed: false,
          definition: { ...definition, priority: definition.priority ?? 0 },
        };
        state.renderers.push(record);
        return this.ownRecord(state, record);
      },

      registerThemeTokens: (tokens: ThemeTokens) => {
        this.assertAccepting(state);
        validateThemeTokens(tokens);
        const id = this.claimRegistrationId(state, "theme", `theme:${tokens.theme}`);
        const record: ThemeTokenRecord = {
          pluginId: state.pluginId, generation: state.generation, id,
          order: 0, removed: false, tokens,
        };
        state.themeTokens.push(record);
        return this.ownRecord(state, record);
      },

      registerCSSSnippet: (snippet: CSSSnippet) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "css", snippet.id);
        const record: CSSSnippetRecord = {
          pluginId: state.pluginId, generation: state.generation, id,
          order: snippet.priority ?? 0, removed: false, snippet,
        };
        state.cssSnippets.push(record);
        return this.ownRecord(state, record, () => record.element?.remove());
      },

      registerStatusItem: (item: StatusItemDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "status", item.id);
        const record: StatusItemRecord = {
          pluginId: state.pluginId, generation: state.generation, id,
          order: item.priority ?? 0, removed: false, item,
        };
        state.statusItems.push(record);
        return this.ownRecord(state, record);
      },

      registerPresenter: (definition: PresenterDefinition) => {
        this.assertAccepting(state);
        const id = this.claimExactRegistrationId(state, "presenter", definition.id);
        const target = requireExactNonEmpty(definition.target, "presenter target");
        const key = definition.key === undefined ? undefined : requireExactNonEmpty(definition.key, "presenter key");
        if (typeof definition.render !== "function") throw new Error("Plugin presenter render must be a function");
        const mode = normalizePresentationMode(definition.mode);
        const order = normalizeOrder(definition.priority);
        this.assertDeclaredContribution(state, "presenter", { id, target, mode, priority: order });
        const record: PresenterRecord = {
          pluginId: state.pluginId, generation: state.generation, id, target, key,
          mode, order, title: declaredContributionTitle(state, "presenter", id),
          removed: false, render: definition.render,
        };
        state.toolActivityPresenters.push(record);
        return this.ownRecord(state, record);
      },

      registerToolActivityPresenter: (definition: ToolActivityPresenterDefinition) => {
        this.assertAccepting(state);
        const id = this.claimExactRegistrationId(state, "tool activity presenter", definition.id);
        const key = requireExactNonEmpty(definition.key, "tool activity presenter key");
        if (typeof definition.render !== "function") {
          throw new Error("Plugin tool activity presenter render must be a function");
        }
        if (state.toolActivityPresenters.some((record) => !record.removed && record.key === key)) {
          throw new Error(`Duplicate plugin tool activity presenter key: ${key}`);
        }
        this.assertToolActivityPresenterKeyAvailable(state.pluginId, key);
        const record: PresenterRecord = {
          pluginId: state.pluginId, generation: state.generation, id, key,
          target: "conversation.tool-activity", mode: "replace",
          order: 0, removed: false,
          render: (props) => this.react.createElement(definition.render, {
            activity: props.snapshot as never, host: props.host, fallback: props.fallback,
          }),
          compatibilityRender: definition.render,
        };
        state.toolActivityPresenters.push(record);
        return this.ownRecord(state, record);
      },
    });
  }

  private ownRecord(
    state: GenerationState,
    record: OrderedRecord,
    remove?: () => void,
  ): Disposable {
    const disposable = createDisposable(() => {
      record.removed = true;
      remove?.();
      if (state.active) {
        this.refreshPublicState();
      }
    });
    state.teardown.push(disposable);
    return disposable;
  }

  private assertAccepting(state: GenerationState): void {
    if (!state.acceptingRegistrations) {
      throw new Error(`Plugin generation ${state.pluginId}@${state.generation} is no longer registering`);
    }
  }

  private assertUsable(state: GenerationState): void {
    if (state.disposed || (!state.acceptingRegistrations && !state.active)) {
      throw new Error(`Plugin generation ${state.pluginId}@${state.generation} is no longer active`);
    }
  }

  private claimRegistrationId(state: GenerationState, kind: string, value: string): string {
    const id = requireNonEmpty(value, `${kind} registration id`);
    const key = `${kind}:${id}`;
    if (state.registrationKeys.has(key)) {
      throw new Error(`Duplicate plugin ${kind} registration id: ${id}`);
    }
    state.registrationKeys.add(key);
    return id;
  }

  private claimExactRegistrationId(state: GenerationState, kind: string, value: string): string {
    const id = requireExactNonEmpty(value, `${kind} registration id`);
    const key = `${kind}:${id}`;
    if (state.registrationKeys.has(key)) {
      throw new Error(`Duplicate plugin ${kind} registration id: ${id}`);
    }
    state.registrationKeys.add(key);
    return id;
  }

  private assertToolActivityPresenterKeyAvailable(pluginId: string, key: string): void {
    for (const state of this.activeGenerations.values()) {
      if (state.pluginId !== pluginId && state.toolActivityPresenters.some((record) =>
        !record.removed && record.compatibilityRender !== undefined && record.key === key)) {
        throw new Error(`Tool activity presenter key is already owned by another plugin: ${key}`);
      }
    }
    for (const pending of this.pendingActivations.values()) {
      const state = pending.state;
      if (!pending.cancelled && state.pluginId !== pluginId
        && state.toolActivityPresenters.some((record) =>
          !record.removed && record.compatibilityRender !== undefined && record.key === key)) {
        throw new Error(`Tool activity presenter key is already owned by another plugin: ${key}`);
      }
    }
  }

  private assertToolActivityPresenterOwnership(state: GenerationState): void {
    for (const presenter of state.toolActivityPresenters) {
      if (!presenter.removed && presenter.compatibilityRender !== undefined && presenter.key !== undefined) {
        this.assertToolActivityPresenterKeyAvailable(state.pluginId, presenter.key);
      }
    }
  }

  private assertDeclaredContribution(
    state: GenerationState,
    kind: "slot" | "surface" | "presenter",
    actual: Readonly<{ id: string; target: string; mode?: string; order?: number; priority?: number }>,
  ): void {
    const declarations = state.declaredContributions;
    if (!declarations) return;
    const declared = kind === "slot" ? declarations.slots
      : kind === "surface" ? declarations.surfaces
      : declarations.presenters;
    if (declared === undefined) return;
    const match = declared.find((candidate) => candidate.id === actual.id);
    if (!match) {
      throw new Error(`Plugin ${kind} registration ${actual.id} is not declared in the manifest`);
    }
    const declaredOrder = "priority" in match
      ? normalizeOrder(match.priority)
      : normalizeOrder("order" in match ? match.order : undefined);
    const actualOrder = actual.priority ?? actual.order ?? 0;
    const declaredMode = "mode" in match ? match.mode : undefined;
    if (match.target !== actual.target || declaredMode !== actual.mode || declaredOrder !== actualOrder) {
      throw new Error(`Plugin ${kind} registration ${actual.id} does not match its manifest declaration`);
    }
  }

  private assertDeclaredContributionsRegistered(state: GenerationState): void {
    const declarations = state.declaredContributions;
    if (!declarations) return;
    for (const declaration of declarations.slots ?? []) {
      if (!state.slots.some((record) => !record.removed && record.id === declaration.id)) {
        throw new Error(`Manifest slot contribution ${declaration.id} was not registered during activation`);
      }
    }
    for (const declaration of declarations.surfaces ?? []) {
      if (!state.surfaces.some((record) => !record.removed && record.id === declaration.id)) {
        throw new Error(`Manifest surface contribution ${declaration.id} was not registered during activation`);
      }
    }
    for (const declaration of declarations.presenters ?? []) {
      if (!state.toolActivityPresenters.some((record) => !record.removed && record.id === declaration.id)) {
        throw new Error(`Manifest presenter contribution ${declaration.id} was not registered during activation`);
      }
    }
  }

  private assertViewPlacementTargets(state: GenerationState): void {
    for (const placement of state.viewPlacements) {
      if (placement.removed) continue;
      if (!state.views.some((view) => !view.removed && view.id === placement.view)) {
        throw new Error(
          `Plugin View placement ${placement.id} references an unregistered View: ${placement.view}`,
        );
      }
    }
  }

  private assertDeclaredViewEntryTargets(state: GenerationState): void {
    const declarations = state.declaredContributions;
    if (!declarations) return;
    for (const entry of [
      ...(declarations.navigation ?? []),
      ...(declarations.workspace_tools ?? []),
      ...(declarations.settings_pages ?? []),
    ]) {
      if (!state.views.some((view) => !view.removed && view.id === entry.view)) {
        throw new Error(`Manifest View entry ${entry.id} references an unregistered View: ${entry.view}`);
      }
    }
  }

  private disposeGeneration(state: GenerationState): void {
    if (state.disposed) {
      return;
    }
    state.disposed = true;
    state.acceptingRegistrations = false;
    for (const disposable of [...state.teardown].reverse()) {
      disposable.dispose();
    }
  }

  private refreshPublicState(): void {
    let changed = false;
    const conflicts: PluginContributionConflict[] = [];
    const slotRecords = new Map<PluginSlotId, SlotRecord[]>();
    const surfaceRecords = new Map<PluginSurfaceId, SurfaceRecord[]>();
    const commands: CommandRecord[] = [];
    const locales: LocaleRecord[] = [];
    const styles: StyleRecord[] = [];
    const views: ViewTypeRecord[] = [];
    const viewPlacements: ViewPlacementRecord[] = [];
    const inspectorSections: InspectorSectionRecord[] = [];
    const renderers: RendererRecord[] = [];
    const themeTokens: ThemeTokenRecord[] = [];
    const cssSnippets: CSSSnippetRecord[] = [];
    const statusItems: StatusItemRecord[] = [];
    const composerStatusSources: ComposerStatusSourceRecord[] = [];
    const toolActivityPresenters: PresenterRecord[] = [];
    const navigationEntries: RegisteredPluginViewEntry[] = [];
    const workspaceTools: RegisteredPluginViewEntry[] = [];
    const settingsPages: RegisteredPluginViewEntry[] = [];

    for (const state of this.activeGenerations.values()) {
      for (const record of state.slots) {
        if (!record.removed) {
          const records = slotRecords.get(record.slotId) ?? [];
          records.push(record);
          slotRecords.set(record.slotId, records);
        }
      }
      for (const record of state.surfaces) {
        if (!record.removed) {
          const records = surfaceRecords.get(record.surfaceId) ?? [];
          records.push(record);
          surfaceRecords.set(record.surfaceId, records);
        }
      }
      commands.push(...state.commands.filter((record) => !record.removed));
      locales.push(...state.locales.filter((record) => !record.removed));
      styles.push(...state.styles.filter((record) => !record.removed));
      views.push(...state.views.filter((record) => !record.removed));
      viewPlacements.push(...state.viewPlacements.filter((record) => !record.removed));
      inspectorSections.push(...state.inspectorSections.filter((record) => !record.removed));
      renderers.push(...state.renderers.filter((record) => !record.removed));
      themeTokens.push(...state.themeTokens.filter((record) => !record.removed));
      cssSnippets.push(...state.cssSnippets.filter((record) => !record.removed));
      statusItems.push(...state.statusItems.filter((record) => !record.removed));
      composerStatusSources.push(...state.composerStatusSources.filter((record) => !record.removed));
      toolActivityPresenters.push(...state.toolActivityPresenters.filter((record) => !record.removed));
      const declaredEntry = (entry: PluginViewEntryDeclaration): RegisteredPluginViewEntry => Object.freeze({
        ...entry,
        pluginId: state.pluginId,
        generation: state.generation,
      });
      navigationEntries.push(...(state.declaredContributions?.navigation ?? []).map(declaredEntry));
      workspaceTools.push(...(state.declaredContributions?.workspace_tools ?? []).map(declaredEntry));
      settingsPages.push(...(state.declaredContributions?.settings_pages ?? []).map(declaredEntry));
    }

    for (const slotId of PLUGIN_SLOT_IDS) {
      const next = Object.freeze((slotRecords.get(slotId) ?? [])
        .sort(compareOrdered)
        .map(toPublicSlotContribution));
      const previous = this.slotSnapshots.get(slotId) ?? EMPTY_SLOT_SNAPSHOT;
      if (!sameContributions(previous, next)) {
        this.slotSnapshots.set(slotId, next);
        changed = true;
        for (const listener of this.slotListeners.get(slotId) ?? []) {
          listener();
        }
      }
    }

    for (const surfaceId of PLUGIN_SURFACE_IDS) {
      const resolved = resolveReplaceConflict(
        "surface",
        surfaceId,
        undefined,
        (surfaceRecords.get(surfaceId) ?? []).sort(compareOrdered),
        this.conflictPreferences,
      );
      if (resolved.conflict) conflicts.push(resolved.conflict);
      const next = Object.freeze(resolved.records.map(toPublicSurfaceContribution));
      const previous = this.surfaceSnapshots.get(surfaceId) ?? EMPTY_SURFACE_SNAPSHOT;
      if (!sameContributions(previous, next)) {
        this.surfaceSnapshots.set(surfaceId, next);
        changed = true;
        for (const listener of this.surfaceListeners.get(surfaceId) ?? []) {
          listener();
        }
      }
    }

    const nextCommands = Object.freeze(commands.sort(compareOrdered).map(toPublicCommand));
    if (!sameContributions(this.commandSnapshot, nextCommands)) {
      this.commandSnapshot = nextCommands;
      changed = true;
    }

    const nextViews = Object.freeze(views.sort(compareOrdered).map(toPublicViewType));
    if (!sameContributions(this.viewSnapshot, nextViews)) {
      this.viewSnapshot = nextViews;
      changed = true;
    }

    const nextViewPlacements = Object.freeze(
      viewPlacements.sort(compareOrdered).map(toPublicViewPlacement),
    );
    if (!sameContributions(this.viewPlacementSnapshot, nextViewPlacements)) {
      this.viewPlacementSnapshot = nextViewPlacements;
      changed = true;
    }

    const nextInspectorSections = Object.freeze(
      inspectorSections.sort(compareOrdered).map(toPublicInspectorSection),
    );
    if (!sameContributions(this.inspectorSectionSnapshot, nextInspectorSections)) {
      this.inspectorSectionSnapshot = nextInspectorSections;
      changed = true;
    }

    const compareViewEntry = (left: RegisteredPluginViewEntry, right: RegisteredPluginViewEntry): number =>
      normalizeOrder(left.order) - normalizeOrder(right.order)
      || compareText(left.pluginId, right.pluginId)
      || compareText(left.id, right.id);
    const nextNavigation = Object.freeze(navigationEntries.sort(compareViewEntry));
    if (!sameContributions(this.navigationSnapshot, nextNavigation)) {
      this.navigationSnapshot = nextNavigation;
      changed = true;
    }
    const nextWorkspaceTools = Object.freeze(workspaceTools.sort(compareViewEntry));
    if (!sameContributions(this.workspaceToolSnapshot, nextWorkspaceTools)) {
      this.workspaceToolSnapshot = nextWorkspaceTools;
      changed = true;
    }
    const nextSettingsPages = Object.freeze(settingsPages.sort(compareViewEntry));
    if (!sameContributions(this.settingsPageSnapshot, nextSettingsPages)) {
      this.settingsPageSnapshot = nextSettingsPages;
      changed = true;
    }

    const nextRenderers = Object.freeze(renderers.sort(compareOrdered).map(toPublicRenderer));
    if (!sameContributions(this.rendererSnapshot, nextRenderers)) {
      this.rendererSnapshot = nextRenderers;
      changed = true;
    }

    const nextThemeTokens = Object.freeze(themeTokens.sort(compareOrdered).map(toPublicThemeTokens));
    if (!sameContributions(this.themeTokenSnapshot, nextThemeTokens)) {
      this.themeTokenSnapshot = nextThemeTokens;
      changed = true;
    }

    const nextStatusItems = Object.freeze(statusItems.sort(compareOrdered).map(toPublicStatusItem));
    if (!sameContributions(this.statusItemSnapshot, nextStatusItems)) {
      this.statusItemSnapshot = nextStatusItems;
      changed = true;
    }

    const nextComposerStatusSources = Object.freeze(
      composerStatusSources.sort(compareOrdered).map(toPublicComposerStatusSource),
    );
    if (!sameContributions(this.composerStatusSourceSnapshot, nextComposerStatusSources)) {
      this.composerStatusSourceSnapshot = nextComposerStatusSources;
      changed = true;
      for (const listener of this.composerStatusSourceListeners) listener();
    }

    const resolvedPresenters = resolvePresenterConflicts(
      toolActivityPresenters.sort(compareOrdered),
      this.conflictPreferences,
    );
    conflicts.push(...resolvedPresenters.conflicts);
    const nextPresenters = Object.freeze(resolvedPresenters.records.map(toPublicPresenter));
    if (!sameContributions(this.presenterSnapshot, nextPresenters)) {
      this.presenterSnapshot = nextPresenters;
      this.presenterQuerySnapshots.clear();
      changed = true;
    }

    const nextToolActivityPresenters = Object.freeze(toolActivityPresenters
      .filter((record) => record.compatibilityRender !== undefined && record.key !== undefined)
      .map((record) => Object.freeze({
        pluginId: record.pluginId,
        generation: record.generation,
        id: record.id,
        key: record.key!,
        render: record.compatibilityRender!,
      })));
    if (!sameContributions(this.toolActivityPresenterSnapshot, nextToolActivityPresenters)) {
      this.toolActivityPresenterSnapshot = nextToolActivityPresenters;
      changed = true;
    }

    const nextConflicts = Object.freeze(conflicts.map((conflict) => Object.freeze(conflict)));
    if (JSON.stringify(this.conflictSnapshot) !== JSON.stringify(nextConflicts)) {
      this.conflictSnapshot = nextConflicts;
      changed = true;
    }

    if (this.refreshLocales(locales)) {
      changed = true;
    }
    this.refreshStyles(styles);
    this.refreshCSSSnippets(cssSnippets);

    if (changed) {
      this.notifyListeners();
    }
    this.refreshConversationCards();
  }

  private refreshConversationCards(): void {
    const next = Object.freeze([...this.activeGenerations.values()]
      .flatMap((state) => state.conversationCards.filter((record) => !record.removed))
      .sort(compareOrdered)
      .map((record): RegisteredConversationCard => Object.freeze({
        id: record.id,
        pluginId: record.pluginId,
        generation: record.generation,
        threadId: record.threadId,
        title: record.title,
        state: record.state,
        order: record.order,
        render: record.render,
        dismiss: record.dismiss,
      })));
    this.conversationCardSnapshot = next;
    for (const listener of this.conversationCardListeners) listener();
  }

  private refreshLocales(records: LocaleRecord[]): boolean {
    const grouped = new Map<string, LocaleRecord[]>();
    for (const record of records) {
      const localeRecords = grouped.get(record.locale) ?? [];
      localeRecords.push(record);
      grouped.set(record.locale, localeRecords);
    }

    let changed = false;
    const allLocales = new Set([...this.localeSnapshots.keys(), ...grouped.keys()]);
    for (const locale of allLocales) {
      const merged: Record<string, string> = {};
      for (const record of (grouped.get(locale) ?? []).sort(compareOrdered)) {
        for (const key of Object.keys(record.entries).sort(compareText)) {
          merged[key] = record.entries[key];
        }
      }
      const next = Object.freeze(merged);
      const previous = this.localeSnapshots.get(locale) ?? EMPTY_LOCALE_SNAPSHOT;
      if (!sameStringRecord(previous, next)) {
        changed = true;
        if (Object.keys(next).length === 0) {
          this.localeSnapshots.delete(locale);
        } else {
          this.localeSnapshots.set(locale, next);
        }
      }
    }
    return changed;
  }

  private refreshStyles(records: StyleRecord[]): void {
    const container = this.resolveStyleContainer();
    if (!container) {
      return;
    }
    for (const record of records.sort(compareOrdered)) {
      let element = record.element;
      if (!element) {
        element = container.ownerDocument.createElement("style");
        element.dataset.wuuPluginId = record.pluginId;
        element.dataset.wuuPluginGeneration = record.generation;
        element.dataset.wuuPluginStyle = record.id;
        element.textContent = record.css;
        record.element = element;
      }
      container.appendChild(element);
    }
  }

  private refreshCSSSnippets(records: CSSSnippetRecord[]): void {
    const container = this.resolveStyleContainer();
    if (!container) {
      return;
    }
    for (const record of records.sort(compareOrdered)) {
      let element = record.element;
      if (!element) {
        element = container.ownerDocument.createElement("style");
        element.dataset.wuuPluginId = record.pluginId;
        element.dataset.wuuPluginGeneration = record.generation;
        element.dataset.wuuPluginCssSnippet = record.id;
        element.textContent = record.snippet.css;
        record.element = element;
      }
      container.appendChild(element);
    }
  }

  private resolveStyleContainer(): HTMLElement | null {
    if (typeof this.styleContainer === "function") {
      return this.styleContainer();
    }
    if (this.styleContainer) {
      return this.styleContainer;
    }
    return typeof document === "undefined" ? null : document.head;
  }

  private addDiagnostic(diagnostic: PluginGenerationDiagnostic): void {
    const key = diagnosticKey(diagnostic.pluginId, diagnostic.generation);
    const diagnostics = this.diagnostics.get(key) ?? [];
    if (diagnostics.some((existing) =>
      existing.kind === diagnostic.kind && existing.message === diagnostic.message)) {
      return;
    }
    this.diagnostics.set(key, Object.freeze([
      ...diagnostics,
      Object.freeze(diagnostic),
    ]));
    this.notifyListeners();
  }

  private notifyListeners(): void {
    for (const listener of this.listeners) {
      listener();
    }
  }
}

function createGenerationState(
  pluginId: string,
  generation: string,
  declaredContributions?: PluginContributionDeclarations,
): GenerationState {
  return {
    pluginId,
    generation,
    declaredContributions,
    slots: [],
    surfaces: [],
    commands: [],
    conversationCards: [],
    styles: [],
    locales: [],
    views: [],
    viewPlacements: [],
    inspectorSections: [],
    renderers: [],
    themeTokens: [],
    cssSnippets: [],
    statusItems: [],
    composerStatusSources: [],
    toolActivityPresenters: [],
    registrationKeys: new Set(),
    hostEventHandlers: new Set(),
    teardown: [],
    acceptingRegistrations: true,
    active: false,
    disposed: false,
  };
}

function toPublicSlotContribution(record: SlotRecord): RegisteredPluginSlotContribution {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
    title: record.title,
    render: record.render,
  });
}

function toPublicSurfaceContribution(record: SurfaceRecord): RegisteredPluginSurfaceContribution {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
    mode: record.mode,
    render: record.render,
  });
}

function toPublicCommand(record: CommandRecord): RegisteredPluginCommand {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    title: record.title,
    order: record.order,
    execute: record.execute,
  });
}

function toPublicViewType(record: ViewTypeRecord): RegisteredViewType {
  return Object.freeze({
    ...record.definition,
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
  });
}

function toPublicViewPlacement(record: ViewPlacementRecord): RegisteredViewPlacement {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
    view: record.view,
    region: record.region,
  });
}

function toPublicInspectorSection(record: InspectorSectionRecord): RegisteredInspectorSection {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    title: record.title,
    priority: record.order,
    order: record.order,
    when: record.when,
    render: record.render,
  });
}

function toPublicRenderer(record: RendererRecord): RegisteredRenderer {
  return Object.freeze({
    ...record.definition,
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
  });
}

function toPublicThemeTokens(record: ThemeTokenRecord): RegisteredThemeTokens {
  return Object.freeze({
    ...record.tokens,
    tokens: Object.freeze({ ...record.tokens.tokens }),
    syntax: record.tokens.syntax === undefined
      ? undefined
      : Object.freeze({ ...record.tokens.syntax }),
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
  });
}

function toPublicStatusItem(record: StatusItemRecord): RegisteredStatusItem {
  return Object.freeze({
    ...record.item,
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
  });
}

function toPublicComposerStatusSource(
  record: ComposerStatusSourceRecord,
): RegisteredComposerStatusSource {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    order: record.order,
    getSnapshot: record.getSnapshot,
    subscribe: record.subscribe,
  });
}

function toPublicPresenter(record: PresenterRecord): RegisteredPresenter {
  return Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    target: record.target,
    key: record.key,
    mode: record.mode,
    priority: record.order,
    order: record.order,
    render: record.render,
  });
}

function declaredContributionTitle(
  state: GenerationState,
  kind: "slot" | "surface" | "presenter",
  id: string,
): string | undefined {
  const declarations = kind === "slot"
    ? state.declaredContributions?.slots
    : kind === "surface"
      ? state.declaredContributions?.surfaces
      : state.declaredContributions?.presenters;
  return declarations?.find((declaration) => declaration.id === id)?.title;
}

function conflictPreferenceKey(
  kind: "surface" | "presenter",
  target: string,
  presentationKey?: string,
): string {
  return kind === "surface"
    ? `surface:${target}`
    : `presenter:${target}:${presentationKey ?? ""}`;
}

function resolveReplaceConflict<T extends OrderedRecord & { mode: "replace" | "wrap"; title?: string }>(
  kind: "surface" | "presenter",
  target: string,
  presentationKey: string | undefined,
  records: T[],
  preferences: Readonly<Record<string, string>>,
): { records: T[]; conflict?: PluginContributionConflict } {
  const replacements = records.filter((record) => record.mode === "replace");
  if (replacements.length < 2) return { records };
  const key = conflictPreferenceKey(kind, target, presentationKey);
  const preferredPluginId = preferences[key];
  let winner = replacements.at(-1)!;
  if (preferredPluginId) {
    for (let index = replacements.length - 1; index >= 0; index--) {
      if (replacements[index].pluginId === preferredPluginId) {
        winner = replacements[index];
        break;
      }
    }
  }
  const resolved = records.filter((record) => record !== winner);
  resolved.push(winner);
  return {
    records: resolved,
    conflict: {
      key,
      kind,
      target,
      ...(presentationKey === undefined ? {} : { presentationKey }),
      candidates: Object.freeze(replacements.map((record) => Object.freeze({
        pluginId: record.pluginId,
        generation: record.generation,
        contributionId: record.id,
        ...(record.title ? { title: record.title } : {}),
      }))),
      winnerPluginId: winner.pluginId,
    },
  };
}

function resolvePresenterConflicts(
  records: PresenterRecord[],
  preferences: Readonly<Record<string, string>>,
): { records: PresenterRecord[]; conflicts: PluginContributionConflict[] } {
  const groups = new Map<string, PresenterRecord[]>();
  for (const record of records) {
    const key = `${record.target}\u0000${record.key ?? ""}\u0000${record.key === undefined ? "absent" : "present"}`;
    const group = groups.get(key) ?? [];
    group.push(record);
    groups.set(key, group);
  }
  let resolved = [...records];
  const conflicts: PluginContributionConflict[] = [];
  for (const group of groups.values()) {
    const first = group[0];
    const result = resolveReplaceConflict(
      "presenter",
      first.target,
      first.key,
      group,
      preferences,
    );
    if (!result.conflict) continue;
    conflicts.push(result.conflict);
    const winner = result.records.at(-1)!;
    resolved = resolved.filter((record) => record !== winner);
    resolved.push(winner);
  }
  return { records: resolved, conflicts };
}

function compareOrdered(left: OrderedRecord, right: OrderedRecord): number {
  return left.order - right.order
    || compareText(left.pluginId, right.pluginId)
    || compareText(left.id, right.id);
}

function compareText(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function normalizeOrder(order: number | undefined): number {
  if (order === undefined) {
    return 0;
  }
  if (!Number.isFinite(order)) {
    throw new Error("Plugin registration order must be a finite number");
  }
  return order;
}

function normalizeViewPlacementRegion(region: unknown): ViewPlacementContribution["region"] {
  if (typeof region !== "string"
    || !(VIEW_PLACEMENT_REGIONS as readonly string[]).includes(region)) {
    throw new Error(`Unsupported plugin View placement region: ${String(region)}`);
  }
  return region as ViewPlacementContribution["region"];
}

function normalizeSurfaceMode(mode: PluginSurfaceMode): PluginSurfaceMode {
  if (mode !== "replace" && mode !== "wrap") {
    throw new Error(`Unsupported plugin surface mode: ${String(mode)}`);
  }
  return mode;
}

function normalizePresentationMode(mode: PresentationMode | undefined): PresentationMode {
  if (mode === undefined) return "replace";
  if (mode !== "replace" && mode !== "wrap") {
    throw new Error(`Unsupported presenter mode: ${String(mode)}`);
  }
  return mode;
}

function requireNonEmpty(value: string, label: string): string {
  const normalized = value.trim();
  if (!normalized) {
    throw new Error(`Plugin ${label} must not be empty`);
  }
  return normalized;
}

function requireExactNonEmpty(value: string, label: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new Error(`Plugin ${label} must not be empty`);
  }
  return value;
}

function validatePublicIcon(icon: string | undefined, label: string): void {
  if (icon !== undefined && !isPublicIconName(icon)) {
    throw new Error(`${label} uses unsupported icon: ${icon}`);
  }
}

function validateThemeTokens(contribution: ThemeTokens): void {
  requireNonEmpty(contribution.theme, "theme id");
  if (contribution.base !== "light" && contribution.base !== "dark") {
    throw new Error(`Plugin theme base must be light or dark: ${String(contribution.base)}`);
  }
  const tokens = Object.entries(contribution.tokens);
  const syntax = Object.entries(contribution.syntax ?? {});
  if (tokens.length === 0 && syntax.length === 0) {
    throw new Error("Plugin theme token contribution must not be empty");
  }
  for (const [name, value] of tokens) {
    if (!isPublicThemeTokenName(name)) {
      throw new Error(`Unsupported plugin theme token: ${name}`);
    }
    requireExactNonEmpty(value, `theme token ${name}`);
  }
  for (const [name, value] of syntax) {
    if (!isPublicSyntaxTokenName(name)) {
      throw new Error(`Unsupported plugin syntax token: ${name}`);
    }
    requireExactNonEmpty(value, `syntax token ${name}`);
  }
}

function sameContributions(
  previous: readonly { pluginId: string; generation: string; id: string; order?: number }[],
  next: readonly { pluginId: string; generation: string; id: string; order?: number }[],
): boolean {
  return previous.length === next.length && previous.every((item, index) => {
    const candidate = next[index];
    return candidate !== undefined
      && item.pluginId === candidate.pluginId
      && item.generation === candidate.generation
      && item.id === candidate.id
      && item.order === candidate.order;
  });
}

function sameStringRecord(
  previous: Readonly<Record<string, string>>,
  next: Readonly<Record<string, string>>,
): boolean {
  const previousKeys = Object.keys(previous);
  const nextKeys = Object.keys(next);
  return previousKeys.length === nextKeys.length
    && previousKeys.every((key) => previous[key] === next[key]);
}

function diagnosticKey(pluginId: string, generation: string): string {
  return `${pluginId}\u0000${generation}`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function createDisposable(dispose: () => void): Disposable {
  let disposed = false;
  return Object.freeze({
    dispose: () => {
      if (disposed) {
        return;
      }
      disposed = true;
      dispose();
    },
  });
}
