import type { LegacyWorkbenchRenderInput } from "./LegacyUIContributions";
import type { HostApplicationService } from "./HostApplicationService";
import { scopeApplicationWebWorkspace } from "../../../../packages/plugin-sdk/src/ui-web-workspace";
import { scopeApplicationEnvironment, scopeApplicationArtifacts, scopeApplicationOnboarding, scopeApplicationBrowser, scopeApplicationFileSelection, scopeApplicationProjects, scopeApplicationTerminals, scopeApplicationCatalog, scopeApplicationPreviewContent, scopeApplicationConversationText, scopeApplicationSettingsControls } from "../../../../packages/plugin-sdk/src/ui";
import type { PluginServiceCall } from "../../../../packages/plugin-sdk/src";
import type {
  ApplicationActionMapV1, ApplicationGenerationAPI, ApplicationServiceV1,
  ApplicationShellDefinition, UISlotDefinition, UIContributionDefinition,
} from "../../../../packages/plugin-sdk/src/ui";
import type * as React from "react";
import type { PluginDesktopActivationParams, ExtensionInventoryRecord, ExtensionIconDescriptor, PluginVisibleHistoryRequest, PluginVisibleHistoryResult } from "../../shared/protocol";
import { settleOnAbort } from "./activation";
import { createPluginWebPage } from "./PluginWebPage";

import type {
  CSSSnippet,
  ComposerInlineCompletion,
  ComposerInlineCompletionContext,
  ComposerInlineCompletionProviderRegistration,
  ComposerInlineCompletionProviderHandle,
  ComposerInlineCompletionRefresh,
  PluginUIKit,
  PluginCommandPresentation,
  PluginCommandActionContext,
  PresentationMode,
  PresentationTarget,
  PresenterDefinition,
  RendererDefinition,
  StatusItemDefinition,
  ThemeTokens,
  ViewPlacementContribution,
  ViewPlacementRegion,
  ViewTypeDefinition,
  ViewHostAPI,
} from "../../shared/workbench";
import { createPluginUIKit, VIEW_PLACEMENT_REGIONS } from "../../shared/workbench";
import {
  isPublicIconName,
  isPublicSyntaxTokenName,
  isPublicThemeTokenName,
} from "../../shared/themeContract.generated";

export interface Disposable {
  dispose(): void;
}

export interface PluginContributionDeclarations {
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

export interface PluginCommandRegistration extends PluginCommandPresentation {
  /** Host action contexts in which this command can accept structured input. */
  contexts?: readonly string[];
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

export interface PluginGenerationApi extends ApplicationGenerationAPI {
  /** The React runtime owned by the Wuu renderer. Plugin bundles should use this object. */
  readonly react: typeof React;
  /** Host-owned primitives that inherit the active Wuu theme and layout rhythm. */
  readonly ui: PluginUIKit;
  readonly pluginId: string;
  readonly generation: string;
  /** Aborted when staging fails, is superseded, or this generation is disposed. */
  readonly signal: AbortSignal;
  invokeRuntime(method: string, input?: unknown, options?: PluginRuntimeInvokeOptions): Promise<unknown>;
  invokeService<TOutput = unknown>(request: PluginServiceCall, options?: PluginRuntimeInvokeOptions): Promise<TOutput>;
  readVisibleHistory(request: PluginVisibleHistoryRequest): Promise<PluginVisibleHistoryResult>;
  listWorkspaces(): Promise<PluginWorkspaceSnapshot>;
  /** List live threads inside a workspace, addressed by its root path (the
   * `root` value returned by listWorkspaces). Read-only kernel session state;
   * ephemeral and archived threads are excluded by the host. */
  listThreads(workspaceRoot: string): Promise<readonly PluginThreadSummary[]>;
  onHostEvent(handler: (event: unknown) => void): Disposable;
  registerCommand(command: PluginCommandRegistration): Disposable;
  /** Show a session-local card at the bottom of a conversation. */
  showConversationCard(card: ConversationCardRegistration): ConversationCardHandle;
  registerStyle(style: PluginStyleRegistration): Disposable;
  registerLocale(locale: PluginLocaleRegistration): Disposable;
  /** Publish structured status data for the host-owned composer capsule row. */
  registerComposerStatusSource(source: ComposerStatusSourceRegistration): Disposable;
  /** Supply append-only text; the host owns ghost rendering and keyboard acceptance. */
  registerComposerInlineCompletionProvider(provider: ComposerInlineCompletionProviderRegistration): ComposerInlineCompletionProviderHandle;
  registerCleanup(cleanup: () => void): Disposable;

  // Phase C — Workbench API

  /** Register a view type that the host can open in a semantic region. */
  registerViewType(definition: ViewTypeDefinition): Disposable;
  /** Request a default View placement in a stable host-owned region. */
  registerViewPlacement(contribution: ViewPlacementContribution): Disposable;
  /** Register a custom content renderer (message, tool result, document, file). */
  registerRenderer(definition: RendererDefinition): Disposable;
  /** Apply theme token overrides for a specific theme. */
  registerThemeTokens(tokens: ThemeTokens): Disposable;
  /** Inject a CSS snippet scoped to this plugin. */
  registerCSSSnippet(snippet: CSSSnippet): Disposable;
  /** Register a status bar item. */
  registerStatusItem(item: StatusItemDefinition): Disposable;
  registerPresenter(definition: PresenterDefinition): Disposable;
}

export interface PluginRuntimeInvokeOptions {
  readonly workspaceId?: string;
  readonly workspaceRoot?: string;
  /** Cancel this invocation and the host work owned by its execution. */
  readonly signal?: AbortSignal;
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
  readonly activeRoot?: string;
}

export interface PluginThreadSummary {
  readonly id: string;
  readonly title: string;
  readonly updatedAt?: string;
  readonly pinned?: boolean;
}

export interface ActivatePluginGenerationOptions {
  /** Public manifest identity; installation scope remains in pluginId. */
  manifestId?: string;
  graphGeneration?: string;
  pluginId: string;
  generation: string;
  contributions?: PluginContributionDeclarations;
  /** Cancels staging only; a published generation owns its own lifetime. */
  signal?: AbortSignal;
  register(api: PluginGenerationApi): void | Promise<void>;
}

export interface RegisteredPluginCommand extends PluginCommandRegistration {
  readonly pluginId: string;
  readonly generation: string;
}

export interface PluginCommandAction {
  readonly command: RegisteredPluginCommand;
  readonly enabled: boolean;
}

export interface PluginCommandActionResolution {
  readonly actions: readonly PluginCommandAction[];
  readonly failures: readonly { command: RegisteredPluginCommand; error: unknown }[];
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

export interface RegisteredComposerInlineCompletionProvider extends ComposerInlineCompletionProviderRegistration {
  subscribeRefresh(listener: (scope?: ComposerInlineCompletionRefresh) => void): () => void;
  readonly pluginId: string;
  readonly generation: string;
  readonly order: number;
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
  readonly kind: "presenter";
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
  readonly contributionId?: string;
}

export interface RegisteredApplicationShell extends ApplicationShellDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly application: ApplicationServiceV1;
}

export interface RegisteredUISlot extends UISlotDefinition {
  readonly application: ApplicationServiceV1;
  readonly pluginId: string;
  readonly generation: string;
  readonly name: string;
}

export interface RegisteredUIContribution extends UIContributionDefinition {
  readonly pluginId: string;
  readonly generation: string;
  readonly application: ApplicationServiceV1;
}

export interface LegacyViewHostIdentity {
  readonly pluginId: string;
  readonly generation: string;
  readonly viewTypeId?: string;
  readonly tabId?: string;
  readonly context?: Readonly<Record<string, unknown>>;
}

export interface PluginHostOptions {
  renderLegacyWorkbench?: (input: LegacyWorkbenchRenderInput) => React.ReactNode;
  createLegacyViewHost?: (identity: LegacyViewHostIdentity) => ViewHostAPI;
  reportActivation?: (report: PluginDesktopActivationParams) => Promise<void>;
  application?: HostApplicationService;
  /** Inject the renderer's existing React runtime; the host never supplies another copy. */
  react: typeof React;
  /** Internal startup budget override for deterministic host and renderer checks. */
  activationTimeoutMs?: number;
  styleContainer?: HTMLElement | (() => HTMLElement | null);
  invokeRuntime?: (request: Readonly<{
    pluginId: string;
    generation: string;
    graphGeneration?: string;
    service?: string;
    method: string;
    input?: unknown;
    workspaceId?: string;
    workspaceRoot?: string;
    signal?: AbortSignal;
  }>) => Promise<unknown>;
  listWorkspaces?: () => Promise<PluginWorkspaceSnapshot>;
  listThreads?: (workspaceRoot: string) => Promise<readonly PluginThreadSummary[]>;
  readVisibleHistory?: (request: PluginVisibleHistoryRequest) => Promise<PluginVisibleHistoryResult>;
}

interface OrderedRecord {
  readonly pluginId: string;
  readonly generation: string;
  readonly id: string;
  readonly order: number;
  removed: boolean;
}

interface CommandRecord extends OrderedRecord, PluginCommandPresentation {
  snapshot?: RegisteredPluginCommand;
  readonly contexts?: readonly string[];
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

interface ComposerInlineCompletionProviderRecord extends OrderedRecord {
  readonly subscribeRefresh: RegisteredComposerInlineCompletionProvider["subscribeRefresh"];
  readonly onDidInteract: ComposerInlineCompletionProviderRegistration["onDidInteract"];
  readonly provideCompletion: ComposerInlineCompletionProviderRegistration["provideCompletion"];
  readonly requests: Set<AbortController>;
}

interface PresenterRecord extends OrderedRecord {
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly mode: PresentationMode;
  readonly title?: string;
  readonly render: PresenterDefinition["render"];
}

interface ApplicationShellRecord extends OrderedRecord { readonly definition: RegisteredApplicationShell }
interface UISlotRecord extends OrderedRecord { readonly definition: RegisteredUISlot }
interface UIContributionRecord extends OrderedRecord { readonly definition: RegisteredUIContribution }

interface GenerationState {
  readonly manifestId: string;
  graphGeneration?: string;
  activation: number;
  registrationReported: boolean;
  renderedViews: number;
  renderFailed: boolean;
  cleanupFailed: boolean;
  retainedViews: number;
  cleaned: boolean;
  readonly cleanupComplete: Promise<void>;
  readonly completeCleanup: () => void;
  readonly shells: ApplicationShellRecord[];
  readonly uiSlots: UISlotRecord[];
  readonly uiContributions: UIContributionRecord[];
  readonly pluginId: string;
  readonly generation: string;
  readonly declaredContributions?: PluginContributionDeclarations;
  readonly commands: CommandRecord[];
  readonly conversationCards: ConversationCardRecord[];
  readonly styles: StyleRecord[];
  readonly locales: LocaleRecord[];
  // Phase C — Workbench
  readonly views: ViewTypeRecord[];
  readonly viewPlacements: ViewPlacementRecord[];
  readonly renderers: RendererRecord[];
  readonly themeTokens: ThemeTokenRecord[];
  readonly cssSnippets: CSSSnippetRecord[];
  readonly statusItems: StatusItemRecord[];
  readonly composerStatusSources: ComposerStatusSourceRecord[];
  readonly composerInlineCompletionProviders: ComposerInlineCompletionProviderRecord[];
  readonly presenters: PresenterRecord[];
  readonly registrationKeys: Set<string>;
  readonly hostEventHandlers: Set<(event: unknown) => void>;
  readonly teardown: Disposable[];
  readonly runtimeRequests: Set<AbortController>;
  readonly lifetime: AbortController;
  acceptingRegistrations: boolean;
  active: boolean;
  disposed: boolean;
}

interface PendingActivation {
  readonly state: GenerationState;
}

const EMPTY_COMMAND_SNAPSHOT: readonly RegisteredPluginCommand[] = Object.freeze([]);
const EMPTY_CONVERSATION_CARD_SNAPSHOT: readonly RegisteredConversationCard[] = Object.freeze([]);
const EMPTY_VIEW_SNAPSHOT: readonly RegisteredViewType[] = Object.freeze([]);
const EMPTY_VIEW_PLACEMENT_SNAPSHOT: readonly RegisteredViewPlacement[] = Object.freeze([]);
const EMPTY_VIEW_ENTRY_SNAPSHOT: readonly RegisteredPluginViewEntry[] = Object.freeze([]);
const EMPTY_RENDERER_SNAPSHOT: readonly RegisteredRenderer[] = Object.freeze([]);
const EMPTY_THEME_TOKEN_SNAPSHOT: readonly RegisteredThemeTokens[] = Object.freeze([]);
const EMPTY_STATUS_ITEM_SNAPSHOT: readonly RegisteredStatusItem[] = Object.freeze([]);
const EMPTY_COMPOSER_STATUS_SOURCE_SNAPSHOT: readonly RegisteredComposerStatusSource[] = Object.freeze([]);
const EMPTY_COMPOSER_INLINE_COMPLETION_SNAPSHOT: readonly RegisteredComposerInlineCompletionProvider[] = Object.freeze([]);
const EMPTY_PRESENTER_SNAPSHOT: readonly RegisteredPresenter[] = Object.freeze([]);
const EMPTY_LOCALE_SNAPSHOT: Readonly<Record<string, string>> = Object.freeze({});
const EMPTY_DIAGNOSTIC_SNAPSHOT: readonly PluginGenerationDiagnostic[] = Object.freeze([]);

const ACTIVATION_TIMEOUT_MS = 30_000;

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
  private readonly ui: Omit<PluginUIKit, "WebPage">;
  private readonly application?: HostApplicationService;
  private readonly legacyWorkbenchRenderer?: PluginHostOptions["renderLegacyWorkbench"];
  private readonly legacyViewHostFactory?: PluginHostOptions["createLegacyViewHost"];
  private applicationShells: readonly RegisteredApplicationShell[] = Object.freeze([]);
  private uiSlotSnapshot: readonly RegisteredUISlot[] = Object.freeze([]);
  private uiContributionSnapshot: readonly RegisteredUIContribution[] = Object.freeze([]);

  private readonly activationTimeoutMs: number;
  private readonly styleContainer?: PluginHostOptions["styleContainer"];
  private readonly runtimeInvoker?: PluginHostOptions["invokeRuntime"];
  private readonly workspaceLister?: PluginHostOptions["listWorkspaces"];
  private readonly threadLister?: PluginHostOptions["listThreads"];
  private readonly visibleHistoryReader?: PluginHostOptions["readVisibleHistory"];
  private readonly reportActivation?: PluginHostOptions["reportActivation"];
  private reportQueue: Promise<void> = Promise.resolve();
  private reportFailure?: string;
  private readonly reportFailures = new Map<string, string>();
  private inventoryGraphs?: Map<string, string | undefined>;
  private readonly activationCounts = new Map<string, number>();
  private readonly reportedActivations = new Map<string, number>();
  private readonly activeGenerations = new Map<string, GenerationState>();
  private readonly pendingActivations = new Map<string, PendingActivation>();
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
  private navigationSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private workspaceToolSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private settingsPageSnapshot: readonly RegisteredPluginViewEntry[] = EMPTY_VIEW_ENTRY_SNAPSHOT;
  private rendererSnapshot: readonly RegisteredRenderer[] = EMPTY_RENDERER_SNAPSHOT;
  private themeTokenSnapshot: readonly RegisteredThemeTokens[] = EMPTY_THEME_TOKEN_SNAPSHOT;
  private statusItemSnapshot: readonly RegisteredStatusItem[] = EMPTY_STATUS_ITEM_SNAPSHOT;
  private composerStatusSourceSnapshot: readonly RegisteredComposerStatusSource[] = EMPTY_COMPOSER_STATUS_SOURCE_SNAPSHOT;
  private readonly composerStatusSourceListeners = new Set<() => void>();
  private composerInlineCompletionProviderSnapshot: readonly RegisteredComposerInlineCompletionProvider[] = EMPTY_COMPOSER_INLINE_COMPLETION_SNAPSHOT;
  private readonly composerInlineCompletionProviderListeners = new Set<() => void>();
  private presenterSnapshot: readonly RegisteredPresenter[] = EMPTY_PRESENTER_SNAPSHOT;

  constructor(options: PluginHostOptions) {
    this.activationTimeoutMs = options.activationTimeoutMs ?? ACTIVATION_TIMEOUT_MS;
    if (!Number.isFinite(this.activationTimeoutMs) || this.activationTimeoutMs <= 0) {
      throw new Error("Plugin activation timeout must be a positive finite duration");
    }
    this.reportActivation = options.reportActivation;
    this.application = options.application;
    this.legacyViewHostFactory = options.createLegacyViewHost;
    this.legacyWorkbenchRenderer = options.renderLegacyWorkbench;
    this.react = options.react;
    this.ui = createPluginUIKit(options.react);
    this.styleContainer = options.styleContainer;
    this.runtimeInvoker = options.invokeRuntime;
    this.workspaceLister = options.listWorkspaces;
    this.threadLister = options.listThreads;
    this.visibleHistoryReader = options.readVisibleHistory;
  }

  async activateGeneration(options: ActivatePluginGenerationOptions): Promise<Disposable> {
    const pluginId = requireNonEmpty(options.pluginId, "plugin id");
    const generation = requireNonEmpty(options.generation, "plugin generation");
    if (this.reportActivation) requireNonEmpty(options.graphGeneration ?? "", "committed graph generation");
    const state = createGenerationState(pluginId, generation, options.contributions, requireNonEmpty(options.manifestId ?? pluginId, "manifest id"));
    state.graphGeneration = options.graphGeneration;
    state.activation = (this.activationCounts.get(pluginId) ?? 0) + 1;
    this.activationCounts.set(pluginId, state.activation);
    const pending: PendingActivation = { state };

    const previousPending = this.pendingActivations.get(pluginId);
    if (previousPending) {
      this.disposeGeneration(previousPending.state, new PluginGenerationSupersededError(pluginId, previousPending.state.generation));
    }
    this.pendingActivations.set(pluginId, pending);

    const cancel = () => this.disposeGeneration(state, new PluginGenerationSupersededError(pluginId, generation));
    const timeout = setTimeout(() => {
      this.disposeGeneration(state, new Error(`Plugin generation ${pluginId}@${generation} activation timed out after ${this.activationTimeoutMs}ms`));
    }, this.activationTimeoutMs);
    options.signal?.addEventListener("abort", cancel, { once: true });
    if (options.signal?.aborted) cancel();

    try {
      // The deadline bounds asynchronous staging, including module loading. It
      // cannot preempt synchronous plugin code running on the renderer thread.
      await settleOnAbort(() => options.register(this.createGenerationApi(state)), state.lifetime.signal);
      state.lifetime.signal.throwIfAborted();
      state.acceptingRegistrations = false;
      this.assertDeclaredContributionsRegistered(state);
      this.assertViewPlacementTargets(state);
      this.assertDeclaredViewEntryTargets(state);
      this.assertUIComposition(state);
    } catch (error: unknown) {
      state.acceptingRegistrations = false;
      if (this.pendingActivations.get(pluginId) === pending) {
        this.pendingActivations.delete(pluginId);
      }
      if (state.lifetime.signal.reason instanceof PluginGenerationSupersededError || error instanceof PluginGenerationSupersededError) {
        this.disposeGeneration(state);
        throw new PluginGenerationSupersededError(pluginId, generation);
      }
      this.addDiagnostic({
        pluginId,
        generation,
        kind: "activation",
        message: `Plugin generation registration failed: ${errorMessage(error)}`,
        cause: error,
      });
      this.reportRegistration(state, "failed");
      this.disposeGeneration(state, error);
      throw error;
    } finally {
      clearTimeout(timeout);
      options.signal?.removeEventListener("abort", cancel);
    }

    if (state.disposed || this.pendingActivations.get(pluginId) !== pending) {
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
    this.reportRegistration(state, "active");
    if (previous) {
      previous.active = false;
      this.disposeGeneration(previous);
    }
    // Fingerprints identify code, not this activation's callbacks and resource ownership.
    this.refreshPublicState(previous?.generation === generation);

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
      this.pendingActivations.delete(normalizedPluginId);
      this.disposeGeneration(pending.state, new PluginGenerationSupersededError(normalizedPluginId, pending.state.generation));
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

  /** A mounted React consumer keeps this activation's resources through effect cleanup. */
  retainGeneration(pluginId: string, generation: string): () => void {
    const state = this.activeGenerations.get(pluginId);
    if (!state || state.generation !== generation || state.disposed) throw new Error("Plugin generation is no longer active");
    state.retainedViews++;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      // Passive unmount cleanup runs parent-first. Release after the complete
      // subtree has had an opportunity to dispose its captured resources.
      queueMicrotask(() => { state.retainedViews--; this.finishGenerationCleanup(state); });
    };
  }

  unloadAndWait(pluginId: string): Promise<void> | undefined {
    const state = this.activeGenerations.get(pluginId);
    this.unload(pluginId);
    return state && !state.cleaned ? state.cleanupComplete : undefined;
  }

  getApplicationShells = (): readonly RegisteredApplicationShell[] => this.applicationShells;

  renderLegacyWorkbench(input: LegacyWorkbenchRenderInput): React.ReactNode {
    if (!this.legacyWorkbenchRenderer) throw new Error("Legacy workbench host is unavailable");
    return this.legacyWorkbenchRenderer(input);
  }

  createLegacyViewHost(identity: LegacyViewHostIdentity): ViewHostAPI {
    const activation = this.getGenerationActivation(identity.pluginId, identity.generation);
    const check = () => {
      if (!activation || this.getGenerationActivation(identity.pluginId, identity.generation) !== activation) throw new Error("Legacy view generation is retired");
    };
    check();
    if (!this.legacyViewHostFactory) throw new Error("Legacy view host is unavailable");
    const source = this.legacyViewHostFactory(identity);
    const invoke = async <T,>(operation: () => Promise<T>): Promise<T> => { check(); const result = await operation(); check(); return result; };
    return Object.freeze({ ...source,
      getStorage: (key, scope) => invoke(() => source.getStorage(key, scope)),
      setStorage: (key, value, scope) => invoke(() => source.setStorage(key, value, scope)),
      getSetting: key => invoke(() => source.getSetting(key)),
      executeCommand: (id, input) => invoke(() => source.executeCommand(id, input)),
      openView: (id, options) => invoke(() => source.openView(id, options)),
      closeView: () => invoke(() => source.closeView()),
    } satisfies ViewHostAPI);
  }

  getUISlot(name: string): RegisteredUISlot | undefined {
    return this.uiSlotSnapshot.find(slot => slot.name === name);
  }

  getUIContributions(name: string): readonly RegisteredUIContribution[] {
    return this.uiContributionSnapshot.filter(contribution => contribution.target === name);
  }

  getUICompositionSnapshot = (): readonly RegisteredUIContribution[] => this.uiContributionSnapshot;

  private assertUIComposition(staging: GenerationState): void {
    const generations = [...this.activeGenerations.values()].filter(state => state.pluginId !== staging.pluginId);
    generations.push(staging);
    for (const state of generations) for (const slot of state.uiSlots) {
      if (slot.removed) continue;
      const contributions = generations.flatMap(candidate => candidate.uiContributions)
        .filter(item => !item.removed && item.definition.target === slot.definition.name);
      if (slot.definition.service) {
        if (contributions.some(item => item.definition.mode === "wrap")) throw new Error(`Selected UI surfaces cannot be wrapped: ${slot.definition.name}`);
      } else if (slot.definition.cardinality === "one" && contributions.filter(item => item.definition.mode !== "wrap").length > 1) {
        throw new Error(`UI slot conflict: ${slot.definition.name}`);
      }
    }
  }

  private createApplicationService(state: GenerationState): ApplicationServiceV1 {
    let desktop: ApplicationServiceV1["desktop"] | undefined;
    let composerPresentation: ApplicationServiceV1["composerPresentation"] | undefined;
    const getPluginHost = () => this;
    const service = (): HostApplicationService => {
      this.assertActive(state);
      if (!this.application) throw new Error("Application service is unavailable");
      return this.application;
    };
    return Object.freeze({
      contractVersion: 1 as const,
      get composerPresentation() { return composerPresentation ??= service().createComposerPresentationScope(state.lifetime.signal, getPluginHost); },
      get desktop() { return desktop ??= service().createDesktopScope(state.lifetime.signal); },
      webWorkspace: scopeApplicationWebWorkspace(() => service().webWorkspace, state.lifetime.signal, () => this.assertActive(state)),
      environment: scopeApplicationEnvironment(() => service().environment, state.lifetime.signal, () => this.assertActive(state)),
      artifacts: scopeApplicationArtifacts(() => service().artifacts, state.lifetime.signal, () => this.assertActive(state)),
      onboarding: scopeApplicationOnboarding(() => service().onboarding, state.lifetime.signal, () => this.assertActive(state)),
      browser: scopeApplicationBrowser(() => service().browser, state.lifetime.signal, () => this.assertActive(state)),
      fileSelection: scopeApplicationFileSelection(() => service().fileSelection, state.lifetime.signal, () => this.assertActive(state)),
      projects: scopeApplicationProjects(() => service().projects, state.lifetime.signal, () => this.assertActive(state)),
      terminals: scopeApplicationTerminals(() => service().terminals, state.lifetime.signal, () => this.assertActive(state)),
      catalog: scopeApplicationCatalog(() => service().catalog, state.lifetime.signal, () => this.assertActive(state)),
      previewContent: scopeApplicationPreviewContent(() => service().previewContent, state.lifetime.signal, () => this.assertActive(state)),
      conversationText: scopeApplicationConversationText(() => service().conversationText, state.lifetime.signal, () => this.assertActive(state)),
      settingsControls: scopeApplicationSettingsControls(() => service().settingsControls, state.lifetime.signal, () => this.assertActive(state)),
      getSnapshot: () => service().getSnapshot(),
      subscribe: (listener: () => void) => {
        const unsubscribe = service().subscribe(() => {
          if (state.active && !state.disposed) listener();
        });
        const disposable = createDisposable(unsubscribe);
        state.teardown.push(disposable);
        return () => disposable.dispose();
      },
      invoke: async <Action extends keyof ApplicationActionMapV1>(
        action: Action, input: ApplicationActionMapV1[Action]["input"], options?: { readonly signal?: AbortSignal },
      ): Promise<ApplicationActionMapV1[Action]["result"]> => {
        const application = service();
        const signal = options?.signal ? AbortSignal.any([state.lifetime.signal, options.signal]) : state.lifetime.signal;
        signal.throwIfAborted();
        if (action === "preview.set-visibility") {
          application.setPreviewVisibility((input as ApplicationActionMapV1["preview.set-visibility"]["input"]).open, state.lifetime.signal);
          return undefined as ApplicationActionMapV1[Action]["result"];
        }
        const result = await application.invoke(action, input, { signal });
        this.assertActive(state);
        signal.throwIfAborted();
        return result;
      },
    });
  }

  /** Resolve host action rows without publishing diagnostics during React rendering. */
  resolveCommandActions(context: PluginCommandActionContext): PluginCommandActionResolution {
    const input = freezeCommandActionContext(context);
    const actions: PluginCommandAction[] = [];
    const failures: { command: RegisteredPluginCommand; error: unknown }[] = [];
    for (const command of this.commandSnapshot) {
      try {
        const enabled = commandActionEnabled(command, input);
        if (enabled !== undefined) actions.push({ command, enabled });
      } catch (error: unknown) {
        failures.push({ command, error });
      }
    }
    return { actions, failures };
  }

  /** Recheck the exact published callback and its predicates before dispatching a host action. */
  async executeCommandAction(command: RegisteredPluginCommand, context: PluginCommandActionContext): Promise<unknown> {
    if (!this.commandSnapshot.includes(command)) throw new Error("Plugin command is no longer active");
    const input = freezeCommandActionContext(context);
    const enabled = commandActionEnabled(command, input);
    if (enabled === undefined) throw new Error("Plugin command is no longer visible");
    if (!enabled) throw new Error("Plugin command is disabled");
    const result = await command.execute(input);
    if (!this.commandSnapshot.includes(command)) throw new Error("Plugin command is no longer active");
    return result;
  }

  recordCommandFailure(command: RegisteredPluginCommand, error: unknown): void {
    if (!this.commandSnapshot.includes(command)) return;
    this.addDiagnostic({
      pluginId: command.pluginId, generation: command.generation, kind: "render",
      contributionId: command.id, message: `Plugin command ${command.id} failed: ${errorMessage(error)}`, cause: error,
    });
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

  getComposerInlineCompletionProviders(): readonly RegisteredComposerInlineCompletionProvider[] {
    return this.composerInlineCompletionProviderSnapshot;
  }

  subscribeComposerInlineCompletionProviders(listener: () => void): () => void {
    this.composerInlineCompletionProviderListeners.add(listener);
    return () => this.composerInlineCompletionProviderListeners.delete(listener);
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

  /** Renderer-local identity for one activation, including retries of unchanged code. */
  getGenerationActivation(pluginId: string, generation: string): object | undefined {
    const active = this.activeGenerations.get(pluginId);
    return active?.generation === generation ? active : undefined;
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

  recordPresenterFailure(contribution: RegisteredPresenter, error: unknown): void {
    if (!this.presenterSnapshot.includes(contribution)) return;
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
    if (!this.conversationCardSnapshot.includes(card)) return;
    this.addDiagnostic({
      pluginId: card.pluginId,
      generation: card.generation,
      kind: "render",
      message: `Plugin conversation card ${card.id} failed to render: ${errorMessage(error)}`,
      cause: error,
      contributionId: card.id,
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
    const application = this.createApplicationService(state);
    const invokeRequest = async (method: string, input?: unknown, options?: PluginRuntimeInvokeOptions, service?: string): Promise<unknown> => {
      if (!this.runtimeInvoker) {
        throw new Error("Plugin runtime requests are unavailable");
      }
      this.assertActive(state);
      options?.signal?.throwIfAborted();
      const controller = new AbortController();
      const abort = () => controller.abort(options?.signal?.reason);
      options?.signal?.addEventListener("abort", abort, { once: true });
      state.runtimeRequests.add(controller);
      try {
        const result = await this.runtimeInvoker({
          pluginId: state.pluginId,
          generation: state.generation,
          graphGeneration: state.graphGeneration,
          ...(service !== undefined ? { service } : {}),
          method: requireNonEmpty(method, "plugin runtime method"),
          input,
          signal: controller.signal,
          ...(options?.workspaceId ? { workspaceId: options.workspaceId } : {}),
          ...(options?.workspaceRoot ? { workspaceRoot: options.workspaceRoot } : {}),
        });
        this.assertActive(state);
        controller.signal.throwIfAborted();
        return result;
      } finally {
        state.runtimeRequests.delete(controller);
        options?.signal?.removeEventListener("abort", abort);
      }
    };
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
      application,
      registerApplicationShell: (definition: ApplicationShellDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "application-shell", definition.id);
        const record: ApplicationShellRecord = {
          pluginId: state.pluginId, generation: state.generation, id, order: 0, removed: false,
          definition: Object.freeze({ ...definition, id, pluginId: state.pluginId, generation: state.generation, application }),
        };
        state.shells.push(record);
        return this.ownRecord(state, record);
      },
      registerUISlot: (definition: UISlotDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "ui-slot", definition.id);
        if (id.includes("/")) throw new Error("UI slot id must be local to its owner");
        if (definition.cardinality !== "one" && definition.cardinality !== "many") throw new Error("Invalid UI slot cardinality");
        if (definition.service !== undefined) {
          requireNonEmpty(definition.service, "UI surface service");
          if (definition.cardinality !== "one") throw new Error("Selected UI surfaces require cardinality one");
        }
        const record: UISlotRecord = {
          pluginId: state.pluginId, generation: state.generation, id, order: 0, removed: false,
          definition: Object.freeze({ ...definition, id, name: `${state.manifestId}/${id}`, pluginId: state.pluginId, generation: state.generation, application }),
        };
        state.uiSlots.push(record);
        return this.ownRecord(state, record);
      },
      registerUIContribution: (definition: UIContributionDefinition) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "ui-contribution", definition.id);
        const target = requireNonEmpty(definition.target, "UI slot target");
        if (!target.includes("/")) throw new Error("UI slot target must include its owner");
        if (definition.mode !== undefined && definition.mode !== "content" && definition.mode !== "wrap") throw new Error("Invalid UI contribution mode");
        const record: UIContributionRecord = {
          pluginId: state.pluginId, generation: state.generation, id, order: normalizeOrder(definition.order), removed: false,
          definition: Object.freeze({ ...definition, id, target, pluginId: state.pluginId, generation: state.generation, application }),
        };
        state.uiContributions.push(record);
        return this.ownRecord(state, record);
      },
      ui: Object.freeze({
        ...this.ui,
        WebPage: createPluginWebPage(this.react, state.lifetime.signal, (error) => {
          this.addDiagnostic({
            pluginId: state.pluginId,
            generation: state.generation,
            kind: "cleanup",
            message: `Plugin web page cleanup failed: ${errorMessage(error)}`,
            cause: error,
          });
        }, application.webWorkspace),
      }),
      pluginId: state.pluginId,
      manifestId: state.manifestId,
      generation: state.generation,
      signal: state.lifetime.signal,
      invokeRuntime: (method: string, input?: unknown, options?: PluginRuntimeInvokeOptions) => invokeRequest(method, input, options),
      invokeService: async <TOutput = unknown>(request: PluginServiceCall, options?: PluginRuntimeInvokeOptions): Promise<TOutput> =>
        await invokeRequest(request.method, request.params, options, requireNonEmpty(request.service, "plugin service name")) as TOutput,
      readVisibleHistory: async (request: PluginVisibleHistoryRequest) => {
        if (!this.visibleHistoryReader) throw new Error("Visible conversation history is unavailable");
        this.assertActive(state);
        const result = await this.visibleHistoryReader(request);
        this.assertActive(state);
        return result;
      },
      listWorkspaces: async () => {
        if (!this.workspaceLister) {
          throw new Error("Workspace listing is unavailable");
        }
        this.assertActive(state);
        const result = await this.workspaceLister();
        this.assertActive(state);
        return result;
      },
      listThreads: async (workspaceRoot: string) => {
        if (!this.threadLister) {
          throw new Error("Thread listing is unavailable");
        }
        this.assertActive(state);
        const result = await this.threadLister(requireNonEmpty(workspaceRoot, "workspace root"));
        this.assertActive(state);
        return result;
      },
      onHostEvent: (handler: (event: unknown) => void) => {
        this.assertUsable(state);
        state.hostEventHandlers.add(handler);
        const disposable = createDisposable(() => state.hostEventHandlers.delete(handler));
        state.teardown.push(disposable);
        return disposable;
      },
      registerCommand: (command: PluginCommandRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "command", command.id);
        if (command.icon !== undefined && !isPublicIconName(command.icon)) throw new Error(`Unsupported plugin command icon: ${command.icon}`);
        if (command.placements?.some((target) => target !== "view.title" && target !== "conversation.message.actions")) {
          throw new Error("Unsupported plugin command placement");
        }
        const record: CommandRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(command.order),
          title: requireNonEmpty(command.title, "command title"),
          contexts: command.contexts ? Object.freeze([...command.contexts]) : undefined,
          placements: command.placements ? Object.freeze([...new Set(command.placements)]) : undefined,
          icon: command.icon,
          when: command.when,
          enabled: command.enabled,
          execute: (input) => {
            this.assertActive(state);
            if (record.removed) throw new Error("Plugin command is no longer registered");
            return command.execute(input);
          },
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
      registerComposerInlineCompletionProvider: (provider: ComposerInlineCompletionProviderRegistration) => {
        this.assertAccepting(state);
        const id = this.claimRegistrationId(state, "composer-inline-completion", provider.id);
        if (typeof provider.provideCompletion !== "function") {
          throw new Error("Composer inline completion provider requires a provideCompletion function");
        }
        const provideCompletion = provider.provideCompletion;
        const onDidInteract = provider.onDidInteract;
        const listeners = new Set<(scope?: ComposerInlineCompletionRefresh) => void>();
        const scopes = new Map<string, ComposerInlineCompletionRefresh>();
        let refreshAll = false;
        let timer: ReturnType<typeof setTimeout> | undefined;
        const active = () => !record.removed && !state.disposed && this.activeGenerations.get(state.pluginId) === state;
        const record: ComposerInlineCompletionProviderRecord = {
          pluginId: state.pluginId,
          generation: state.generation,
          id,
          order: normalizeOrder(provider.order),
          requests: new Set(),
          removed: false,
          provideCompletion: (context, signal) => this.requestComposerInlineCompletion(state, record, provideCompletion, context, signal),
          subscribeRefresh: (listener) => {
            if (!active()) return () => {};
            listeners.add(listener);
            return () => { listeners.delete(listener); };
          },
          onDidInteract: onDidInteract ? (event) => {
            if (!active()) return;
            try {
              const result = onDidInteract(Object.freeze({ ...event, source: Object.freeze({ ...event.source }) }));
              void Promise.resolve(result).catch(reportInteractionError);
            } catch (error: unknown) { reportInteractionError(error); }
          } : undefined,
        };
        const reportInteractionError = (error: unknown) => {
          if (!active()) return;
          this.addDiagnostic({ pluginId: state.pluginId, generation: state.generation, contributionId: id,
            kind: "render", message: `Composer inline completion interaction failed: ${errorMessage(error)}`, cause: error });
        };
        state.composerInlineCompletionProviders.push(record);
        const disposable = this.ownRecord(state, record, () => {
          for (const request of record.requests) request.abort();
          clearTimeout(timer);
          timer = undefined;
          listeners.clear();
          scopes.clear();
        });
        return Object.freeze({
          dispose: () => disposable.dispose(),
          refresh: (scope?: ComposerInlineCompletionRefresh) => {
            if (!active()) return;
            if (scope) scopes.set(JSON.stringify([scope.composerId, scope.revision]), Object.freeze({ composerId: scope.composerId, revision: scope.revision }));
            else refreshAll = true;
            // Bound synchronous bursts and provider-triggered refresh loops without
            // changing the plugin's own inference/debounce budget policy.
            if (timer !== undefined) return;
            timer = setTimeout(() => {
              timer = undefined;
              const targets = refreshAll ? [undefined] : [...scopes.values()];
              refreshAll = false;
              scopes.clear();
              if (!active()) return;
              for (const target of targets) for (const listener of listeners) listener(target);
            }, 100);
          },
        });
      },
      registerCleanup: (cleanup: () => void) => {
        this.assertAccepting(state);
        const disposable = createDisposable(() => {
          try {
            cleanup();
          } catch (error: unknown) {
            state.cleanupFailed = true;
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
        if (target === "conversation.composer" || target === "navigation.primary") {
          throw new Error(`Retired presenter target ${target}; use registerUIContribution with the selected composer's or navigation provider's slots`);
        }
        const key = definition.key === undefined ? undefined : requireExactNonEmpty(definition.key, "presenter key");
        if (typeof definition.render !== "function") throw new Error("Plugin presenter render must be a function");
        const mode = normalizePresentationMode(definition.mode);
        const order = normalizeOrder(definition.priority);
        this.assertDeclaredPresenter(state, { id, target, mode, priority: order });
        const record: PresenterRecord = {
          pluginId: state.pluginId, generation: state.generation, id, target, key,
          mode, order, title: state.declaredContributions?.presenters?.find(declaration => declaration.id === id)?.title,
          removed: false, render: definition.render,
        };
        state.presenters.push(record);
        return this.ownRecord(state, record);
      },

    });
  }

  private requestComposerInlineCompletion(
    state: GenerationState,
    record: ComposerInlineCompletionProviderRecord,
    provideCompletion: ComposerInlineCompletionProviderRegistration["provideCompletion"],
    context: ComposerInlineCompletionContext,
    signal: AbortSignal,
  ): Promise<ComposerInlineCompletion | null> {
    // Fingerprints can recur after re-enabling the same code. Fence the exact
    // activation and registration, not just their public string identities.
    const active = () => !record.removed && !state.disposed && this.activeGenerations.get(state.pluginId) === state;
    if (signal.aborted || !active()) return Promise.resolve(null);
    const controller = new AbortController();
    record.requests.add(controller);
    return new Promise((resolve) => {
      let settled = false;
      const finish = (result: ComposerInlineCompletion | null) => {
        if (settled) return;
        settled = true;
        signal.removeEventListener("abort", cancel);
        controller.signal.removeEventListener("abort", aborted);
        record.requests.delete(controller);
        resolve(result);
      };
      const cancel = () => controller.abort();
      const aborted = () => finish(null);
      signal.addEventListener("abort", cancel, { once: true });
      controller.signal.addEventListener("abort", aborted, { once: true });
      Promise.resolve().then(() => {
        if (controller.signal.aborted || !active()) return null;
        return provideCompletion(Object.freeze({ ...context }), controller.signal);
      }).then((result) => {
        if (controller.signal.aborted || !active() || result == null) return finish(null);
        if (typeof result.text !== "string") throw new Error("Composer inline completion text must be a string");
        if (result.id !== undefined && (typeof result.id !== "string" || result.id.length === 0)) {
          throw new Error("Composer inline completion id must be a nonempty string");
        }
        finish(result.text.length === 0 ? null : Object.freeze({ text: result.text,
          ...(result.id === undefined ? {} : { id: result.id }) }));
      }).catch((error: unknown) => {
        if (!controller.signal.aborted && active()) {
          this.addDiagnostic({
            pluginId: state.pluginId,
            generation: state.generation,
            contributionId: record.id,
            kind: "render",
            message: `Composer inline completion failed: ${errorMessage(error)}`,
            cause: error,
          });
        }
        finish(null);
      });
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

  private assertActive(state: GenerationState): void {
    if (this.activeGenerations.get(state.pluginId) !== state || state.disposed) {
      throw new Error("Plugin generation is no longer active");
    }
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

  private assertDeclaredPresenter(
    state: GenerationState,
    actual: Readonly<{ id: string; target: string; mode: string; priority: number }>,
  ): void {
    const declared = state.declaredContributions?.presenters;
    if (declared === undefined) return;
    const match = declared.find(candidate => candidate.id === actual.id);
    if (!match) {
      throw new Error(`Plugin presenter registration ${actual.id} is not declared in the manifest`);
    }
    if (match.target !== actual.target || match.mode !== actual.mode || normalizeOrder(match.priority) !== actual.priority) {
      throw new Error(`Plugin presenter registration ${actual.id} does not match its manifest declaration`);
    }
  }

  private assertDeclaredContributionsRegistered(state: GenerationState): void {
    const declarations = state.declaredContributions;
    if (!declarations) return;
    for (const declaration of declarations.presenters ?? []) {
      if (!state.presenters.some((record) => !record.removed && record.id === declaration.id)) {
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

  private disposeGeneration(state: GenerationState, reason?: unknown): void {
    if (state.disposed) {
      return;
    }
    state.disposed = true;
    state.acceptingRegistrations = false;
    state.lifetime.abort(reason);
    for (const request of state.runtimeRequests) request.abort();
    this.finishGenerationCleanup(state);
  }

  private finishGenerationCleanup(state: GenerationState): void {
    if (!state.disposed || state.retainedViews > 0 || state.cleaned) return;
    state.cleaned = true;
    for (const disposable of [...state.teardown].reverse()) {
      try { disposable.dispose(); }
      catch (error: unknown) {
        state.cleanupFailed = true;
        this.addDiagnostic({ pluginId: state.pluginId, generation: state.generation, kind: "cleanup", message: `Plugin cleanup failed: ${errorMessage(error)}`, cause: error });
      }
    }
    if (state.registrationReported) this.report(state, "cleanup", state.cleanupFailed ? "failed" : "disposed");
    state.completeCleanup();
  }

  private reportRegistration(state: GenerationState, status: "active" | "failed"): void {
    state.registrationReported = true;
    this.report(state, "registration", status);
    if (state.cleaned) this.report(state, "cleanup", state.cleanupFailed ? "failed" : "disposed");
  }

  private report(state: GenerationState, phase: PluginDesktopActivationParams["phase"], status: PluginDesktopActivationParams["status"]): void {
    const send = this.reportActivation;
    if (!send) return;
    const params: PluginDesktopActivationParams = {
      id: state.pluginId, fingerprint: state.generation, graph_generation: state.graphGeneration!, activation: state.activation, phase, status,
      ...(status === "failed" ? { message: `Plugin desktop ${phase} failed; inspect local diagnostics` } : {}),
    };
    this.reportQueue = this.reportQueue.then(async () => {
      // A newly committed graph owns the registry. Its obsolete callbacks may
      // still finish effect cleanup, but cannot update the replacement graph.
      if (this.inventoryGraphs && this.inventoryGraphs.get(state.pluginId) !== state.graphGeneration) return;
      if (state.activation < (this.reportedActivations.get(state.pluginId) ?? 0)) return;
      if (phase === "registration") this.reportedActivations.set(state.pluginId, state.activation);
      await send(params);
      if (phase === "registration" && this.reportFailures.delete(state.pluginId)) {
        this.reportFailure = [...this.reportFailures.values()].join("\n") || undefined;
        this.notifyListeners();
      }
    }).catch((error: unknown) => {
      if (this.inventoryGraphs && this.inventoryGraphs.get(state.pluginId) !== state.graphGeneration) return;
      const message = `Desktop activation report failed for ${state.pluginId}: ${errorMessage(error)}`;
      this.reportFailures.set(state.pluginId, message);
      this.reportFailure = [...this.reportFailures.values()].join("\n");
      this.addDiagnostic({ pluginId: state.pluginId, generation: state.generation, kind: "activation", message, cause: error });
    });
  }

  setInventoryGraphs(inventory: readonly ExtensionInventoryRecord[]): void {
    this.inventoryGraphs = new Map(inventory.map(plugin => [plugin.id, plugin.graph_generation]));
  }

  flushActivationReports(): Promise<void> { return this.reportQueue; }
  getActivationReportFailure = (): string | undefined => this.reportFailure;

  observeGenerationRender(pluginId: string, generation: string, initialError?: Error): { failed(error: Error): void; dispose(): void } {
    const state = this.activeGenerations.get(pluginId);
    if (!state || state.generation !== generation) throw new Error("Plugin generation is no longer active");
    state.renderedViews++;
    let disposed = false;
    const observer = {
      failed: (error: Error) => {
        if (disposed || state.cleaned || state.renderFailed) return;
        state.renderFailed = true;
        this.addDiagnostic({ pluginId, generation, kind: "render", message: `Plugin UI render failed: ${errorMessage(error)}`, cause: error });
        this.report(state, "render", "failed");
      },
      dispose: () => {
        if (disposed) return;
        disposed = true;
        state.renderedViews--;
        if (!state.cleaned && !state.renderFailed && state.renderedViews === 0) this.report(state, "render", "disposed");
      },
    };
    if (initialError) observer.failed(initialError);
    else if (!state.renderFailed && state.renderedViews === 1) this.report(state, "render", "active");
    return observer;
  }

  private refreshPublicState(replaceSnapshots = false): void {
    let changed = false;
    const live = [...this.activeGenerations.values()];
    const shells = live.flatMap(state => state.shells.filter(record => !record.removed).map(record => record.definition));
    const slots = live.flatMap(state => state.uiSlots.filter(record => !record.removed).map(record => record.definition));
    const contributions = live.flatMap(state => state.uiContributions.filter(record => !record.removed)).sort(compareOrdered).map(record => record.definition);
    if (shells.length !== this.applicationShells.length || shells.some((value, index) => value !== this.applicationShells[index])) {
      this.applicationShells = Object.freeze(shells);
      changed = true;
    }
    if (slots.length !== this.uiSlotSnapshot.length || slots.some((value, index) => value !== this.uiSlotSnapshot[index])
      || contributions.length !== this.uiContributionSnapshot.length || contributions.some((value, index) => value !== this.uiContributionSnapshot[index])) {
      this.uiSlotSnapshot = Object.freeze(slots);
      this.uiContributionSnapshot = Object.freeze(contributions);
      changed = true;
    }
    const conflicts: PluginContributionConflict[] = [];
    const commands: CommandRecord[] = [];
    const locales: LocaleRecord[] = [];
    const styles: StyleRecord[] = [];
    const views: ViewTypeRecord[] = [];
    const viewPlacements: ViewPlacementRecord[] = [];
    const renderers: RendererRecord[] = [];
    const themeTokens: ThemeTokenRecord[] = [];
    const cssSnippets: CSSSnippetRecord[] = [];
    const statusItems: StatusItemRecord[] = [];
    const composerStatusSources: ComposerStatusSourceRecord[] = [];
    const composerInlineCompletionProviders: ComposerInlineCompletionProviderRecord[] = [];
    const presenters: PresenterRecord[] = [];
    const navigationEntries: RegisteredPluginViewEntry[] = [];
    const workspaceTools: RegisteredPluginViewEntry[] = [];
    const settingsPages: RegisteredPluginViewEntry[] = [];

    for (const state of this.activeGenerations.values()) {
      commands.push(...state.commands.filter((record) => !record.removed));
      locales.push(...state.locales.filter((record) => !record.removed));
      styles.push(...state.styles.filter((record) => !record.removed));
      views.push(...state.views.filter((record) => !record.removed));
      viewPlacements.push(...state.viewPlacements.filter((record) => !record.removed));
      renderers.push(...state.renderers.filter((record) => !record.removed));
      themeTokens.push(...state.themeTokens.filter((record) => !record.removed));
      cssSnippets.push(...state.cssSnippets.filter((record) => !record.removed));
      statusItems.push(...state.statusItems.filter((record) => !record.removed));
      composerStatusSources.push(...state.composerStatusSources.filter((record) => !record.removed));
      composerInlineCompletionProviders.push(...state.composerInlineCompletionProviders.filter((record) => !record.removed));
      presenters.push(...state.presenters.filter((record) => !record.removed));
      const declaredEntry = (entry: PluginViewEntryDeclaration): RegisteredPluginViewEntry => Object.freeze({
        ...entry,
        pluginId: state.pluginId,
        generation: state.generation,
      });
      navigationEntries.push(...(state.declaredContributions?.navigation ?? []).map(declaredEntry));
      workspaceTools.push(...(state.declaredContributions?.workspace_tools ?? []).map(declaredEntry));
      settingsPages.push(...(state.declaredContributions?.settings_pages ?? []).map(declaredEntry));
    }

    const nextCommands = Object.freeze(commands.sort(compareOrdered).map(toPublicCommand));
    if (replaceSnapshots || !sameContributions(this.commandSnapshot, nextCommands)) {
      this.commandSnapshot = nextCommands;
      changed = true;
    }

    const nextViews = Object.freeze(views.sort(compareOrdered).map(toPublicViewType));
    if (replaceSnapshots || !sameContributions(this.viewSnapshot, nextViews)) {
      this.viewSnapshot = nextViews;
      changed = true;
    }

    const nextViewPlacements = Object.freeze(
      viewPlacements.sort(compareOrdered).map(toPublicViewPlacement),
    );
    if (replaceSnapshots || !sameContributions(this.viewPlacementSnapshot, nextViewPlacements)) {
      this.viewPlacementSnapshot = nextViewPlacements;
      changed = true;
    }

    const compareViewEntry = (left: RegisteredPluginViewEntry, right: RegisteredPluginViewEntry): number =>
      normalizeOrder(left.order) - normalizeOrder(right.order)
      || compareText(left.pluginId, right.pluginId)
      || compareText(left.id, right.id);
    const nextNavigation = Object.freeze(navigationEntries.sort(compareViewEntry));
    if (replaceSnapshots || !sameContributions(this.navigationSnapshot, nextNavigation)) {
      this.navigationSnapshot = nextNavigation;
      changed = true;
    }
    const nextWorkspaceTools = Object.freeze(workspaceTools.sort(compareViewEntry));
    if (replaceSnapshots || !sameContributions(this.workspaceToolSnapshot, nextWorkspaceTools)) {
      this.workspaceToolSnapshot = nextWorkspaceTools;
      changed = true;
    }
    const nextSettingsPages = Object.freeze(settingsPages.sort(compareViewEntry));
    if (replaceSnapshots || !sameContributions(this.settingsPageSnapshot, nextSettingsPages)) {
      this.settingsPageSnapshot = nextSettingsPages;
      changed = true;
    }

    const nextRenderers = Object.freeze(renderers.sort(compareOrdered).map(toPublicRenderer));
    if (replaceSnapshots || !sameContributions(this.rendererSnapshot, nextRenderers)) {
      this.rendererSnapshot = nextRenderers;
      changed = true;
    }

    const nextThemeTokens = Object.freeze(themeTokens.sort(compareOrdered).map(toPublicThemeTokens));
    if (replaceSnapshots || !sameContributions(this.themeTokenSnapshot, nextThemeTokens)) {
      this.themeTokenSnapshot = nextThemeTokens;
      changed = true;
    }

    const nextStatusItems = Object.freeze(statusItems.sort(compareOrdered).map(toPublicStatusItem));
    if (replaceSnapshots || !sameContributions(this.statusItemSnapshot, nextStatusItems)) {
      this.statusItemSnapshot = nextStatusItems;
      changed = true;
    }

    const nextComposerStatusSources = Object.freeze(
      composerStatusSources.sort(compareOrdered).map(toPublicComposerStatusSource),
    );
    if (replaceSnapshots || !sameContributions(this.composerStatusSourceSnapshot, nextComposerStatusSources)) {
      this.composerStatusSourceSnapshot = nextComposerStatusSources;
      changed = true;
      for (const listener of this.composerStatusSourceListeners) listener();
    }

    const nextComposerInlineCompletionProviders = Object.freeze(
      composerInlineCompletionProviders.sort(compareOrdered).map((record) => Object.freeze({
        pluginId: record.pluginId,
        generation: record.generation,
        id: record.id,
        order: record.order,
        provideCompletion: record.provideCompletion,
        subscribeRefresh: record.subscribeRefresh,
        onDidInteract: record.onDidInteract,
      })),
    );
    if (replaceSnapshots || !sameContributions(this.composerInlineCompletionProviderSnapshot, nextComposerInlineCompletionProviders)) {
      this.composerInlineCompletionProviderSnapshot = nextComposerInlineCompletionProviders;
      changed = true;
      for (const listener of this.composerInlineCompletionProviderListeners) listener();
    }

    const resolvedPresenters = resolvePresenterConflicts(
      presenters.sort(compareOrdered),
      this.conflictPreferences,
    );
    conflicts.push(...resolvedPresenters.conflicts);
    const nextPresenters = Object.freeze(resolvedPresenters.records.map(toPublicPresenter));
    if (replaceSnapshots || !sameContributions(this.presenterSnapshot, nextPresenters)) {
      this.presenterSnapshot = nextPresenters;
      this.presenterQuerySnapshots.clear();
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
  declaredContributions: PluginContributionDeclarations | undefined,
  manifestId: string,
): GenerationState {
  let completeCleanup!: () => void;
  const cleanupComplete = new Promise<void>(resolve => { completeCleanup = resolve; });
  return {
    manifestId,
    activation: 0,
    registrationReported: false,
    renderedViews: 0,
    renderFailed: false,
    cleanupFailed: false,
    retainedViews: 0,
    cleaned: false,
    cleanupComplete,
    completeCleanup,
    pluginId,
    generation,
    declaredContributions,
    shells: [],
    uiSlots: [],
    uiContributions: [],
    commands: [],
    conversationCards: [],
    styles: [],
    locales: [],
    views: [],
    viewPlacements: [],
    renderers: [],
    themeTokens: [],
    cssSnippets: [],
    statusItems: [],
    composerStatusSources: [],
    composerInlineCompletionProviders: [],
    presenters: [],
    registrationKeys: new Set(),
    hostEventHandlers: new Set(),
    teardown: [],
    runtimeRequests: new Set(),
    lifetime: new AbortController(),
    acceptingRegistrations: true,
    active: false,
    disposed: false,
  };
}

function toPublicCommand(record: CommandRecord): RegisteredPluginCommand {
  return record.snapshot ??= Object.freeze({
    pluginId: record.pluginId,
    generation: record.generation,
    id: record.id,
    title: record.title,
    contexts: record.contexts,
    placements: record.placements,
    icon: record.icon,
    when: record.when,
    enabled: record.enabled,
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

function conflictPreferenceKey(
  kind: "presenter",
  target: string,
  presentationKey?: string,
): string {
  return `${kind}:${target}:${presentationKey ?? ""}`;
}

function resolveReplaceConflict<T extends OrderedRecord & { mode: "replace" | "wrap"; title?: string }>(
  kind: "presenter",
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

function commandActionEnabled(command: RegisteredPluginCommand, context: PluginCommandActionContext): boolean | undefined {
  if (!command.placements?.includes(context.target)) return undefined;
  const visible = command.when === undefined ? true : command.when(context);
  if (typeof visible !== "boolean") throw new Error("Plugin command visibility must return a boolean synchronously");
  if (!visible) return undefined;
  const enabled = command.enabled === undefined ? true : command.enabled(context);
  if (typeof enabled !== "boolean") throw new Error("Plugin command enablement must return a boolean synchronously");
  return enabled;
}

function freezeCommandActionContext(context: PluginCommandActionContext): PluginCommandActionContext {
  // These public snapshots contain only data. Never freeze the caller's live objects.
  const snapshot = structuredClone(context);
  const freeze = (value: unknown): void => {
    if (typeof value !== "object" || value === null || Object.isFrozen(value)) return;
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  };
  freeze(snapshot);
  return snapshot;
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
