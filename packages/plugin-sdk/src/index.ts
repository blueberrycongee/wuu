/** Durable acceptance and execution are independent; read_at records inclusion in an agent model context. */
export interface ClientInputReceipt {
 request_id: string; receipt_id: string; host_request_id: string; client_id: string; queue_id: string; session_id: string;
 user_space_id?: string; product_domain?: string; agent_id?: string; plugin_id: string; owner?: string;
 message_seq?: number; hostturn_id?: string;
 receipt_state: "accepted" | "in_history";
 execution_state: "queued" | "held" | "running" | "completed" | "failed" | "interrupted" | "cancelled" | "unconfirmed";
 accepted_at: string; read_at?: string; revision: number; error_stage?: string; error?: string;
}
export interface AcceptedClientInputView {
 receipt: ClientInputReceipt; prompt: string; relation?: MessageRelation; attachments?: SessionInputAttachment[];
}

/** Stable durable message address; seq is not a provider item or turn ID. */
export interface MessageRef { session_id: string; seq: number; }
/** Host-validated quote target plus extension-owned context. Never encode routing in prompt text. */
export interface MessageRelation { reply_to?: MessageRef; metadata?: Record<string, unknown>; }

/**
 * @wuu/plugin-sdk
 *
 * Public SDK for building Wuu plugins. This package provides TypeScript
 * types, interfaces, and helper utilities that plugin developers depend on.
 * Plugins must not import Wuu private source; all public API surfaces
 * through this package.
 *
 * @module
 */

import type * as React from "react";
import type { ApplicationGenerationAPI } from "./ui.js";
export * from "./ui.js";

import type { PluginDependency } from "./bundle-contract.js";
import type { ModelAdapterDescriptor } from "./model.js";
export * from "./process.js";
export * from "./execution-backend.js";
export * from "./settings.js";
export * from "./driver.js";
export * from "./composition.js";
export * from "./engine-admin.js";
export * from "./engine.js";
export * from "./persistence.js";
import { DOCUMENT_AUTHORITY_HOST_FEATURE, DOCUMENT_STORE_SERVICE } from "./persistence.js";
export { continueService, delegateService, SERVICE_CONTINUATION_HOST_FEATURE, type ServiceMethodOverride } from "./service-delegation.js";
export { GOAL_CONTROL_SERVICE_DESCRIPTOR, type GoalSnapshotV1, type GoalControlRequestV1, type GoalControlResultV1 } from "./goal.js";

import type {
  PublicIconName,
  PublicSyntaxTokenName,
  PublicThemeTokenName,
} from "./theme-contract.generated.js";

export {
  LEGACY_THEME_TOKEN_ALIASES,
  PUBLIC_ICON_NAMES,
  PUBLIC_SYNTAX_TOKEN_NAMES,
  PUBLIC_THEME_TOKEN_NAMES,
  canonicalThemeTokenName,
  isPublicIconName,
  isPublicSyntaxTokenName,
  isPublicThemeTokenName,
  type PublicIconName,
  type PublicSyntaxTokenName,
  type PublicThemeTokenName,
} from "./theme-contract.generated.js";

// ---------------------------------------------------------------------------
// Core workbench types
// ---------------------------------------------------------------------------

/** Unique identifier for a view type. */
export type ViewTypeId = string;
/** Where a view instance appears. */
/** Stable host-owned semantic regions available for View placement. */
export const VIEW_PLACEMENT_REGIONS = [
  "navigation",
  "primary",
  "auxiliary",
  "inspector",
  "settings",
  "overlay",
] as const;
export type ViewPlacementRegion = (typeof VIEW_PLACEMENT_REGIONS)[number];

/** Stable host action locations; commands without placements remain programmatic. */
export type PluginCommandPlacement = "view.title" | "conversation.message.actions";

/** Public action input. Direct or palette invocations may still omit input. */
export type PluginCommandActionContext =
  | Readonly<{
    contractVersion: 1;
    target: "view.title";
    viewId: string;
    viewTypeId: string;
    viewPluginId: string;
    region: ViewPlacementRegion;
  }>
  | Readonly<{
    contractVersion: 1;
    target: "conversation.message.actions";
    threadId?: string;
    turnId: string;
    item: ConversationItemSnapshotV1;
  }>;

/** Optional presentation of an existing command in host-owned action rows. */
export interface PluginCommandPresentation {
  readonly placements?: readonly PluginCommandPlacement[];
  readonly icon?: PublicIconName;
  /** Synchronous, side-effect-free visibility predicate; a thrown error hides this action. */
  readonly when?: (context: PluginCommandActionContext) => boolean;
  /** Synchronous, side-effect-free availability predicate, checked again on click. */
  readonly enabled?: (context: PluginCommandActionContext) => boolean;
}

export type ViewPersistence = "session" | "durable";

/** Artwork declared in plugin.json for the package or a host-owned entry. */
export type PluginManifestIcon =
  | PublicIconName
  | Readonly<{ path: string }>
  | Readonly<{ light: string; dark: string }>;

export interface ViewTypeDefinition {
  /** Presentation-only user-space shell; never grants data or tool authority. */
  readonly layout?: "user-space";
  id: ViewTypeId;
  title: string;
  icon?: PublicIconName;
  defaultRegion?: ViewPlacementRegion;
  persistence?: ViewPersistence;
  render: React.ComponentType<ViewRenderProps>;
}

export interface ViewRenderProps {
  host: ViewHostAPI;
  context: Readonly<Record<string, unknown>>;
  locale: string;
  translate(key: string, values?: Readonly<Record<string, string | number>>): string;
}

export interface SettingsModelAliasV1 {
  readonly provider: string;
  readonly model: string;
  readonly effort?: string;
  readonly variant?: string;
}

export interface SettingsAvailableModelV1 {
  readonly provider: string;
  readonly model: string;
  readonly label: string;
  readonly variants: readonly string[];
}

export interface SettingsValueMapV1 {
  readonly "runtime.modelAliases": Readonly<Record<string, SettingsModelAliasV1>>;
}

export type SettingsValueKeyV1 = keyof SettingsValueMapV1;

/** Narrow settings service exposed only to plugin views mounted as Settings pages. */
export interface SettingsPageHostAPI {
  readonly contractVersion: 1;
  getValue<Key extends SettingsValueKeyV1>(key: Key): SettingsValueMapV1[Key];
  updateValue<Key extends SettingsValueKeyV1>(key: Key, value: SettingsValueMapV1[Key]): Promise<void>;
  listAvailableModels?(): readonly SettingsAvailableModelV1[];
}

export interface ViewHostAPI {
  getStorage(key: string, scope?: "user" | "workspace"): Promise<string | null>;
  setStorage(key: string, value: string, scope?: "user" | "workspace"): Promise<void>;
  getSetting(key: string): Promise<unknown>;
  executeCommand(commandId: string, input?: unknown): Promise<unknown>;
  openView(viewTypeId: ViewTypeId, options?: OpenViewOptions): Promise<void>;
  closeView(): Promise<void>;
  readonly settings?: SettingsPageHostAPI;
}

export interface PluginUIContainerProps {
  readonly children?: React.ReactNode;
  readonly className?: string;
  readonly [property: string]: unknown;
}

export interface PluginUIPageProps extends PluginUIContainerProps {
  readonly density?: "comfortable" | "compact";
}

export interface PluginUISectionProps extends PluginUIContainerProps {
  readonly title?: React.ReactNode;
  readonly description?: React.ReactNode;
}

export interface PluginUIStackProps extends PluginUIContainerProps {
  readonly gap?: "small" | "medium" | "large";
}

export interface PluginUIButtonProps extends PluginUIContainerProps {
  readonly variant?: "primary" | "secondary" | "ghost" | "danger";
  readonly type?: "button" | "submit" | "reset";
  readonly disabled?: boolean;
  readonly onClick?: React.MouseEventHandler<HTMLButtonElement>;
}

export interface PluginUIToolbarToggleProps extends PluginUIContainerProps {
  readonly pressed: boolean;
  readonly type?: "button" | "submit" | "reset";
  readonly disabled?: boolean;
  readonly title?: string;
  readonly "aria-label": string;
  readonly onClick?: React.MouseEventHandler<HTMLButtonElement>;
}

export interface PluginUITextInputProps extends PluginUIContainerProps {
  readonly label: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly value?: string | number;
  readonly defaultValue?: string | number;
  readonly placeholder?: string;
  readonly disabled?: boolean;
  readonly onChange?: React.ChangeEventHandler<HTMLInputElement>;
}

export interface PluginUITextAreaProps extends PluginUIContainerProps {
  readonly label: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly value?: string;
  readonly placeholder?: string;
  readonly disabled?: boolean;
  readonly onChange?: React.ChangeEventHandler<HTMLTextAreaElement>;
}

export interface PluginUICheckboxProps extends PluginUIContainerProps {
  readonly label: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly checked?: boolean;
  readonly disabled?: boolean;
  readonly onChange?: React.ChangeEventHandler<HTMLInputElement>;
}

export interface PluginUISelectProps extends PluginUIContainerProps {
  readonly label: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly value?: string | number;
  readonly defaultValue?: string | number;
  readonly disabled?: boolean;
  readonly onChange?: React.ChangeEventHandler<HTMLSelectElement>;
}

export interface PluginUIEmptyStateProps extends PluginUIContainerProps {
  readonly title: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly actions?: React.ReactNode;
}


export interface PluginUILoadingStateProps extends PluginUIContainerProps {
  readonly title?: React.ReactNode;
  readonly description?: React.ReactNode;
}

export interface PluginUIErrorStateProps extends PluginUIContainerProps {
  readonly title: React.ReactNode;
  readonly description?: React.ReactNode;
  readonly actions?: React.ReactNode;
}

export interface PluginUILiveDurationProps extends PluginUIContainerProps {
  readonly elapsedMs: number;
  readonly runningSinceMs?: number;
  readonly active?: boolean;
}

/** A desktop Chromium page inside an existing plugin View. */
export interface PluginUIWebPageProps {
  /** Initial HTTP(S) destination. Changing it navigates this page. */
  readonly url: string;
  readonly title?: string;
}

export type HostUIComponent<Props> = React.ComponentType<Props>;

/** Composer accessory shell with shared sizing, scrolling and keyboard dismissal.
 * Render in composer.above. Empty-state visibility and expansion belong to the caller.
 */
export interface PluginUIComposerDrawerProps extends PluginUIContainerProps {
  readonly expanded: boolean;
  readonly onExpandedChange: (expanded: boolean) => void;
  readonly toggleLabel: string;
  /** Feedback displayed in both collapsed and expanded states. */
  readonly notice?: React.ReactNode;
  readonly icon?: React.ReactNode;
  readonly summary: React.ReactNode;
  readonly actions?: React.ReactNode;
  readonly tone?: "default" | "muted" | "warning";
}

/** Stable host-owned primitives for plugin views. */
export interface PluginUIInteractiveAnswerProps {
  snapshot: unknown;
  fallbackText?: string;
  onStateChange?(state: unknown): void;
  onSync?(input: unknown): Promise<unknown>;
  onAction(input: unknown): Promise<unknown>;
  onRefresh(): Promise<unknown>;
  onRetry(resumeHeld?: boolean): Promise<unknown>;
}
export interface PluginUIConversationMessageProps { interactive?: readonly PluginUIInteractiveAnswerProps[]; footer?: React.ReactNode; reaction?: { emoji?: string; label: string; pending?: boolean; error?: string; onChange?(emoji: string | null): void }; replyPreview?: React.ReactNode; onReply?(): void; replyLabel?: string; role: "user" | "assistant"; content: string; className?: string; linkPolicy?: "external-only"; timestamp?: string; id?: string; latest?: boolean; attachments?: React.ReactNode; streaming?: boolean; turnId?: string; turnStatus?: "in_progress" | "completed" | "failed" | "interrupted"; process?: { label: string; content?: string; details?: React.ReactNode }; messageActions?: "copy-only" | "none" }

export interface PluginUIConversationViewProps {
  presentation?: "standard" | "bubbles";
  /** Submit an offer reply to this conversation with stable admission identity. */
  onQuestionReply?: (requestId: string, prompt: string, relation?: MessageRelation) => Promise<void>;
  conversationId: string;
  sessionId?: string;
  sessionScope?: { userSpaceId: string; productDomain: "personal" | "coding"; compositionProfile?: string };
  actions?: React.ReactNode;
  title: string;
  subtitle?: string;
  avatar?: React.ReactNode;
  status?: React.ReactNode;
  loading?: boolean;
  /** Hide only this conversation’s history trigger; defaults to true. */
  showHistorySearch?: boolean;
  /** Suppress the floating control when another conversation overlays this view. */
  showJumpToLatest?: boolean;
  searchLabel: string;
  searchContent: React.ReactNode;
  children: React.ReactNode;
  composer: React.ReactNode;
}

export interface PluginUIConversationAttachmentProps {
  resourceId?: string;
  onOpen?(): void;
  /** Read a verified byte snapshot independently of the card mount/Blob URL lifetime. */
  onRead?(): Promise<Blob>;
  name: string;
  mediaType: string;
  size: number;
  url?: string;
  loading?: boolean;
  error?: string;
  loadLabel: string;
  downloadLabel: string;
  onLoad(): void | Promise<string | void>;
}

export interface PluginUIConversationComposerProps {
 replyContext?: { label: string; preview: string; cancelLabel: string; onCancel(): void };
  conversationId: string;
  submitting?: boolean;
  queuedCount?: number;
  pendingCount?: number;
  stopState?: "pending" | "retry";
  prompt: string;
  placeholder?: string;
  running: boolean;
  disabled?: boolean;
  sendDisabled?: boolean;
  attachments: readonly { id: string; filename: string; media_type: string; data: string }[];
  onChangePrompt(value: string): void;
  onAddAttachments(files: File[]): void;
  onRemoveAttachment(id: string): void;
  onSend(prompt: string): void;
  onStop(): void;
}

/** Decorative host identity. Static is the default; ambient keeps the same sphere and face. */
export interface PluginUIAvatarProps {
  readonly size?: number;
  readonly status?: "idle" | "thinking" | "sending" | "responding" | "queued" | "waiting" | "failed" | "interrupted";
  readonly motion?: "static" | "ambient";
  readonly className?: string;
}

export interface PluginUIKit {
  readonly Avatar: HostUIComponent<PluginUIAvatarProps>;
  readonly ConversationView: HostUIComponent<PluginUIConversationViewProps>;
  readonly ConversationAttachment: HostUIComponent<PluginUIConversationAttachmentProps>;
  readonly ConversationComposer: HostUIComponent<PluginUIConversationComposerProps>;
  readonly ConversationMessage: HostUIComponent<PluginUIConversationMessageProps>;
  /** Shares Wuu browser logins; closes when unmounted or the generation ends. */
  readonly WebPage: HostUIComponent<PluginUIWebPageProps>;
  readonly ComposerDrawer: HostUIComponent<PluginUIComposerDrawerProps>;
  readonly Page: HostUIComponent<PluginUIPageProps>;
  readonly Panel: HostUIComponent<PluginUIContainerProps>;
  readonly Card: HostUIComponent<PluginUIContainerProps>;
  readonly Section: HostUIComponent<PluginUISectionProps>;
  readonly Stack: HostUIComponent<PluginUIStackProps>;
  readonly Row: HostUIComponent<PluginUIContainerProps>;
  readonly Button: HostUIComponent<PluginUIButtonProps>;
  readonly ToolbarToggle: HostUIComponent<PluginUIToolbarToggleProps>;
  readonly TextInput: HostUIComponent<PluginUITextInputProps>;
  readonly TextArea: HostUIComponent<PluginUITextAreaProps>;
  readonly Select: HostUIComponent<PluginUISelectProps>;
  readonly Checkbox: HostUIComponent<PluginUICheckboxProps>;
  readonly EmptyState: HostUIComponent<PluginUIEmptyStateProps>;
  readonly LoadingState: HostUIComponent<PluginUILoadingStateProps>;
  readonly ErrorState: HostUIComponent<PluginUIErrorStateProps>;
  readonly LiveDuration: HostUIComponent<PluginUILiveDurationProps>;
}

export interface OpenViewOptions {
  region?: ViewPlacementRegion;
  context?: Readonly<Record<string, unknown>>;
  persistence?: ViewPersistence;
  reveal?: boolean;
}

export interface InspectorSessionSnapshotV1 {
  readonly id?: string;
  readonly status: "idle" | "running";
  readonly turnId?: string;
  readonly turnStatus?: "in_progress" | "completed" | "failed" | "interrupted";
}

export interface InspectorWorkspaceSnapshotV1 {
  readonly kind: "project" | "no_project";
  readonly cwd: string;
  readonly projectId?: string;
  readonly projectName?: string;
  readonly branch?: string;
  readonly dirtyFileCount?: number;
}

export interface InspectorTodoSnapshotV1 {
  readonly completed: number;
  readonly total: number;
  readonly activeContent?: string;
  readonly items: readonly InspectorTodoItemSnapshotV1[];
}

export interface InspectorTodoItemSnapshotV1 {
  readonly content: string;
  readonly status: "pending" | "in_progress" | "completed";
}

export interface InspectorSnapshotV1 {
  readonly contractVersion: 1;
  readonly session: InspectorSessionSnapshotV1;
  readonly workspace?: InspectorWorkspaceSnapshotV1;
  readonly todo?: InspectorTodoSnapshotV1;
}

export type ToolActivityStatus = "running" | "completed" | "failed";

export interface ToolActivityResultContentPart {
  readonly type: string;
  readonly text?: string;
  readonly data?: string;
  readonly mimeType?: string;
  readonly uri?: string;
  readonly name?: string;
  readonly resource?: unknown;
  readonly artifact?: ArtifactPresentation;
}

export interface ToolActivityStructuredResult {
  readonly content?: readonly ToolActivityResultContentPart[];
  readonly structuredContent?: unknown;
  readonly metadata?: unknown;
  readonly isError?: boolean;
  readonly activity?: Readonly<{
    id: string;
    kind: string;
    state?: string;
    threadId?: string;
    previewUri?: string;
  }>;
}

/** Host-owned immutable view of a tool call. It intentionally excludes thread internals. */
export interface ToolActivitySnapshot {
  readonly contractVersion: 1;
  readonly id: string;
  readonly toolName: string;
  readonly displayLabel?: string;
  readonly capability?: string;
  readonly kind?: string;
  readonly status: ToolActivityStatus;
  readonly argumentsText?: string;
  readonly resultText?: string;
  readonly structuredResult?: ToolActivityStructuredResult;
  readonly error?: string;
}

/**
 * Requests that one registered View be opened in a stable host-owned region.
 * The host retains its shell, dimensions, protected chrome, and recovery UI.
 */
export interface ViewPlacementContribution {
  id: string;
  view: ViewTypeId;
  region: ViewPlacementRegion;
  priority?: number;
}

export type RendererCategory = "message" | "tool-result" | "document" | "file-preview";

export interface RendererDefinition {
  id: string;
  category: RendererCategory;
  match: string | RegExp;
  priority?: number;
  /** Opt into cumulative/coalesced tool argument snapshots for one dispatch
   * capability. Requires a literal MIME match and the tool-result category.
   * The host supplies content.preview and metadata.toolInput with turnId,
   * argumentsText, status, and optional error. Previews are transient: do not
   * execute effects or persist input state. The first matching final resource
   * reuses the preview's part identity. */
  toolInput?: Readonly<{ capability: string }>;
  render: unknown; // React.ComponentType<RendererProps> — opaque in SDK
}

/** Props supplied to a registered content renderer. */
export interface RendererProps {
  readonly content: unknown;
  readonly metadata: Readonly<Record<string, unknown>>;
  readonly host: PresentationHost;
}

/** Input to conversation.composer.submit when advertised by a content renderer.
 * Call synchronously from a trusted button click or its native form submission
 * (including keyboard activation). Text is limited to 8,000
 * characters; optional JSON context to 16,384 characters. The host binds the
 * origin, rejects running/stale sources and preserves composer drafts. */
export interface RendererSubmissionV1 {
  readonly text: string;
  readonly context?: unknown;
}

/** Host-owned metadata.interaction on conversation artifact renderers. */
export interface RendererInteractionSnapshotV1 {
  readonly contractVersion: 1;
  readonly threadId?: string;
  readonly running: boolean;
  readonly readOnly: boolean;
  readonly disabledReason?: string;
}

export interface ThemeTokens {
  theme: string;
  base: string;
  tokens: Readonly<Partial<Record<PublicThemeTokenName, string>>>;
  syntax?: Readonly<Partial<Record<PublicSyntaxTokenName, string>>>;
}

export interface CSSSnippet {
  id: string;
  css: string;
  priority?: number;
}

export interface PluginSettingDefinition {
  type: "boolean" | "string" | "number" | "enum" | "secret";
  title: string;
  description?: string;
  default: unknown;
  enum?: string[];
  scope: "user" | "workspace";
  apply: "live" | "next-operation" | "restart";
  /** Exact HTTPS origins allowed to receive this secret as a Bearer token. Required for secret settings. */
  credential_origins?: string[];
}

export interface PluginStorageAPI {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  delete(key: string): Promise<void>;
  keys(): Promise<string[]>;
}

export interface PluginSettingsAPI {
  get(key: string): Promise<unknown>;
  getAll(): Promise<Record<string, unknown>>;
}

export interface CommandDefinition {
  id: string;
  title: string;
  description?: string;
  shortcut?: string;
  contexts?: string[];
  execute(input?: unknown): unknown | Promise<unknown>;
}

export interface StatusItemDefinition {
  id: string;
  label: string;
  icon?: string;
  tooltip?: string;
  command?: string;
  priority?: number;
}

export interface LocaleDefinition {
  locale: string;
  fallback?: string[];
  entries: Record<string, string>;
}

// ---------------------------------------------------------------------------
// Plugin generation API
// ---------------------------------------------------------------------------

export interface PluginGenerationApi extends ApplicationGenerationAPI {
  /** Host-owned React runtime. Executable desktop bundles must use this instance. */
  readonly react: HostReact;
  /** Host-owned primitives that inherit the active Wuu theme and spacing rhythm. */
  readonly ui: PluginUIKit;
  /** Installation identity used for authenticated host calls; UI slots use manifestId. */
  readonly pluginId: string;
  readonly generation: string;
  /** Aborted when staging fails, is superseded, or this generation is disposed. */
  readonly signal: AbortSignal;
  /** Invoke one method owned by this plugin's active runtime generation. */
  invokeRuntime(method: string, input?: unknown, options?: PluginRuntimeInvokeOptions): Promise<unknown>;
  /** Call a selected service through this desktop consumer's declared dependency.
   * The host binds identity, generation and execution; callers cannot choose a
   * provider. TOutput describes the service contract, not runtime validation.
   */
  invokeService<TOutput = unknown>(request: PluginServiceCall, options?: PluginRuntimeInvokeOptions): Promise<TOutput>;
  /** Read bounded, display-only completed turns from the addressed conversation. */
  readVisibleHistory(request: PluginVisibleHistoryRequest): Promise<PluginVisibleHistoryResult>;
  /** List registered project workspaces that a trusted extension may target. */
  listWorkspaces(): Promise<PluginWorkspaceSnapshot>;
  /** Live, nonarchived workspace conversations; no private controller state. */
  listThreads(workspaceRoot: string): Promise<readonly PluginThreadSummary[]>;
  /** Observe host lifecycle events; disposal is bound to this generation. */
  onHostEvent(handler: (event: unknown) => void): Disposable;
  registerCommand(command: CommandRegistration): Disposable;
  /** Show a non-persistent card at the bottom of a conversation. */
  showConversationCard(card: ConversationCardRegistration): ConversationCardHandle;
  registerStyle(style: StyleRegistration): Disposable;
  registerLocale(locale: LocaleRegistration): Disposable;
  registerComposerStatusSource(source: ComposerStatusSourceRegistration): Disposable;
  /** Supply append-only text for the host-owned composer ghost and Tab action. */
  registerComposerInlineCompletionProvider(provider: ComposerInlineCompletionProviderRegistration): ComposerInlineCompletionProviderHandle;
  registerCleanup(cleanup: () => void): Disposable;
  registerViewType(definition: ViewTypeDefinition): Disposable;
  /** Set a declared navigation entry title for this generation only (1–64 characters). */
  setNavigationTitle(id: string, title: string): boolean;
  registerViewPlacement(contribution: ViewPlacementContribution): Disposable;
  registerRenderer(definition: RendererDefinition): Disposable;
  registerThemeTokens(tokens: ThemeTokens): Disposable;
  registerCSSSnippet(snippet: CSSSnippet): Disposable;
  registerStatusItem(item: StatusItemDefinition): Disposable;
  registerPresenter(definition: PresenterDefinition): Disposable;
}

interface PluginVisibleHistoryScope {
  readonly threadId: string;
  readonly workspaceRoot: string;
  readonly workspaceId?: string;
}
export type PluginVisibleHistoryRequest = PluginVisibleHistoryScope & (
  | { readonly kind: "main"; readonly beforeTurnId: string; readonly sideThreadId?: never }
  | { readonly kind: "side"; readonly sideThreadId: string; readonly beforeTurnId?: never }
);

export interface PluginVisibleHistoryResult {
  readonly turns: readonly {
    readonly id: string;
    readonly userText: string;
    readonly assistantText: string;
    readonly imageCount: number;
    readonly fileCount: number;
  }[];
}

/** Exact selected bytes, carried only in the authenticated client envelope. */
export interface ClientInputAttachment {
 readonly sha256: string;
 readonly filename: string;
 readonly media_type: string;
 readonly data: string;
}
export interface SessionInputAttachment { sha256: string; filename: string; media_type: string }
export interface SessionHistoryAttachment { kind: "image" | "file" | "artifact"; index: number; name: string; mime_type: string; size: number; sha256: string }
/** Renderer request envelope. Major version comes from required_services;
 * routing and execution identity remain host-owned.
 */
export interface PluginServiceCall {
  readonly service: string;
  readonly method: string;
  readonly params?: unknown;
}

export interface PluginRuntimeInvokeOptions {
  /** Explicit host-configured composition profile for a scoped target runtime.
   * The host verifies this activation's graph, plugin fingerprint and declared
   * dependencies before admitting the target graph. This is not a graph ID or
   * an authority grant; unavailable/unbound profiles fail without substitution. */
  readonly compositionProfile?: string;
 readonly userInput?: { readonly requestId: string; readonly prompt: string; readonly relation?: MessageRelation; readonly attachments?: readonly ClientInputAttachment[] };
  readonly userSpaceId?: string;
  readonly productDomain?: "personal" | "coding";
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

export interface PluginThreadSummary {
  readonly id: string;
  readonly title: string;
  readonly updatedAt?: string;
  readonly pinned?: boolean;
  readonly latestCompletedTurnId?: string;
}

export interface PluginWorkspaceSnapshot {
  readonly workspaces: readonly PluginWorkspace[];
  readonly activeWorkspaceId?: string;
  readonly activeRoot?: string;
}

/** The host-owned React runtime. Plugins must not bundle a second instance. */
export type HostReact = typeof React;

// ---------------------------------------------------------------------------
// Executable runtime contract
// ---------------------------------------------------------------------------

export const RUNTIME_PROTOCOL_V1 = 1 as const;
export const CAPABILITY_PROTOCOL_V2 = 2 as const;
export const CAPABILITY_PROTOCOL_V3 = 3 as const;
export const REQUEST_TRANSFORM_CAPABILITY = "agent.request.transform" as const;
export const AGENT_PRE_STEP_CAPABILITY = "agent.pre_step" as const;
export const AGENT_STEP_POLICY_CAPABILITY = "agent.step.policy" as const;
export const AGENT_TURN_COMPLETED_CAPABILITY = "agent.turn.completed" as const;
export const AGENT_TURN_LIFECYCLE_CAPABILITY = "agent.turn.lifecycle" as const;
export const AGENT_TURN_INTERRUPTED_CAPABILITY = "agent.turn.interrupted" as const;
export const SYSTEM_PROMPT_SECTION_CAPABILITY = "agent.system_prompt.section" as const;
/** @experimental No distributed first-party consumer has proven this contract. */
export const COMPACTION_CAPABILITY = "agent.compaction" as const;
export const PLUGIN_CLIENT_REQUEST_CAPABILITY = "plugin.client.request" as const;
/** Host-only admission after a persisted main conversation settles. */
export const SESSION_ATTENTION_CAPABILITY = "session.attention" as const;
export interface SessionAttentionInput {
  /** Exact host-admitted send bound to this persisted turn; not read or execution authority. */
  request_id: string;
  user_space_id: string;
  product_domain: string;
  session_id: string;
  agent_id: string;
  message_seq?: number;
  turn_id: string;
  outcome: "completed" | "failed";
}
export interface SessionAttentionOutput { allowed: boolean; batch_window_seconds?: number }


export type CapabilityKind = "observe" | "transform" | "decision";
export type CapabilityErrorPolicy = "propagate" | "isolate" | "ignore";

export interface CapabilityDescriptor {
  id: string;
  kind: CapabilityKind;
  error_policy?: CapabilityErrorPolicy;
  version: number;
  priority?: number;
  depends_on?: string[];
  conflicts?: string[];
}

export const HOST_SERVICE_METHODS = [
  "host.storage.get",
  "host.storage.set",
  "host.storage.delete",
  "host.storage.keys",
  "host.storage.compare_exchange",
  "host.settings.get",
  "host.settings.list",
  "host.credentials.request",
  "host.session.create",
  "host.session.route",
  "host.session.send",
  "host.session.list",
  "host.session.cancel",
  "host.session.inspect",
  "host.session.context",
  "host.session.control",
  "host.session.attachment.read",
  "host.session.history.read",
  "host.session.history.search",
  "host.workspace.status",
  "host.workspace.apply",
  "host.workspace.discard",
  "host.service.call",
  "host.service.continue",
] as const;

export type HostServiceMethod = (typeof HOST_SERVICE_METHODS)[number];

export const KERNEL_SERVICE_METHOD = "call" as const;
export const EXECUTION_UPDATE_SERVICE = "execution.update" as const;
export const INVOKE_TOOL_SERVICE = "execution.invoke-tool" as const;
export const USER_QUESTION_ASK_SERVICE = "host.user-question.ask" as const;
export const USER_QUESTION_OFFER_SERVICE = "host.user-question.offer" as const;
export const ARTIFACT_IMPORT_SERVICE = "host.artifact.import" as const;
export const FILE_LOCK_SERVICE = "host.file-lock" as const;
export const MODEL_REQUEST_SERVICE = "host.model.request" as const;
/** Opt-in, bounded live session progress; not event replay or an input receipt. */
export const SESSION_PROGRESS_HOST_FEATURE = "session.progress.v1" as const;
export const SECURITY_AUTHORIZE_SERVICE = "security.authorize" as const;
export const PROCESS_SANDBOX_SERVICE = "sandbox.process" as const;
export const SECURITY_AUTHORIZE_METHOD = "authorize" as const;
export const PROCESS_SANDBOX_METHOD = "confine" as const;
export const KERNEL_SERVICE_NAMES = {
  "host.storage.get": "host.storage.get",
  "host.storage.set": "host.storage.set",
  "host.storage.delete": "host.storage.delete",
  "host.storage.keys": "host.storage.keys",
  "host.storage.compare_exchange": "host.storage.compare-exchange",
  "host.settings.get": "host.settings.get",
  "host.settings.list": "host.settings.list",
  "host.credentials.request": "host.credentials.request",
  "host.session.create": "host.session.create",
  "host.session.route": "host.session.route",
  "host.session.send": "host.session.send",
  "host.session.list": "host.session.list",
  "host.session.cancel": "host.session.cancel",
  "host.session.inspect": "host.session.inspect",
  "host.session.context": "host.session.context",
  "host.session.control": "host.session.control",
  "host.session.attachment.read": "host.session.attachment.read",
  "host.session.history.read": "host.session.history.read",
  "host.session.history.search": "host.session.history.search",
  "host.workspace.status": "host.workspace.status",
  "host.workspace.apply": "host.workspace.apply",
  "host.workspace.discard": "host.workspace.discard",
} as const satisfies Record<Exclude<HostServiceMethod, "host.service.call" | "host.service.continue">, string>;

export type KernelHostServiceMethod = keyof typeof KERNEL_SERVICE_NAMES;

/** Build a typed kernel service call through the Service Registry gateway. */
export function kernelServiceCall<M extends KernelHostServiceMethod>(
  id: string,
  method: M,
  params: HostServiceContracts[M]["params"],
) {
  return {
    id,
    method: "host.service.call" as const,
    params: { service: KERNEL_SERVICE_NAMES[method], method: KERNEL_SERVICE_METHOD, params },
  };
}

export function requireKernelService(method: KernelHostServiceMethod): ServiceRequirement {
  return { name: KERNEL_SERVICE_NAMES[method], major_version: 1, required: true };
}

export function requireArtifactImportService(): ServiceRequirement {
  return { name: ARTIFACT_IMPORT_SERVICE, major_version: 1, required: true };
}

export function requireModelRequestService(): ServiceRequirement {
  return { name: MODEL_REQUEST_SERVICE, major_version: 1, required: true };
}

/** Bootstrap choices; independent of runtime protocol and product version. */
export type ProductComposition = {
  profile?: string;
  /** Replaces the profile's complete module set. [] starts no modules. */
  modules?: string[];
  /** Overrides profile bindings by consumer (if any), service name and major version. */
  services?: ServiceBinding[];
} & ({
  version: 1;
  profiles?: Record<string, { modules: string[]; services?: ServiceBinding[] }>;
} | {
  version: 2 | 3 | 4 | 5;
  profiles?: Record<string, CompositionProfile>;
  bundles?: Record<string, CompositionBundle>;
  /** Applied last. Without a profile/modules, patches start from an empty set. */
  patches?: CompositionPatch[];
});

/** Bundles, then inline choices, then patches. Inclusion never grants trust. */
export interface CompositionProfile {
  /** Applied in order, starting from an empty module set. */
  bundles?: string[];
  /** When present, replaces the complete set produced by the bundles. */
  modules?: string[];
  services?: ServiceBinding[];
  patches?: CompositionPatch[];
}

/** Reusable static configuration; bundles do not execute code or grant trust. */
export interface CompositionBundle {
  patches: CompositionPatch[];
}

export interface CompositionPatch {
  /** true selects a module, false excludes it without removing its bindings. */
  modules?: Record<string, boolean>;
  services?: ServiceBinding[];
  /** Restores unique-provider resolution; multiple providers remain an error. */
  remove_services?: Pick<ServiceBinding, "consumer" | "service" | "major">[];
}

export type CompositionSource = "composition" | `profile:${string}` | `bundle:${string}` | `composition:patch:${number}`;

/** Public registry diagnostics; no provider configuration or private data. */
export interface ServiceRegistrySnapshot {
  generation: number;
  /** Immutable identity of this running graph; catalog epoch may advance independently. */
  graph_generation?: string;
  /** Resolved edges used by host lifecycle ordering, not reconstructed selectors. */
  dependencies?: Record<string, { provider: string; required: boolean; service?: string; major?: number }[]>;
  /** Includes requirements that could not be resolved. */
  requirements?: Record<string, ServiceRequirement[]>;
  services: {
    /** Present only on a consumer-specific route. */
    consumer?: string;
    service: string;
    version: string;
    provider: string;
    methods: string[];
    descriptor?: ServiceDescriptor;
    binding_source: CompositionSource | "unique-provider";
  }[];
  composition?: {
    profile?: string;
    module_source: CompositionSource | "discovery-default";
    /** Ordered applied layers, including patches that remove selections. */
    layers?: CompositionSource[];
    modules: {
      id: string;
      source: CompositionSource | "discovery-default";
      /** Package version, or product build for a native module. */
      version?: string;
      /** Omitted for a package without a runtime. */
      state?: "starting" | "initialized" | "prepared" | "active" | "failed" | "stopped";
      error?: string;
    }[];
  };
}

/** Selects a provider without fallback when it fails or is removed. */
export interface ServiceBinding {
  /** Selects only this declared consumer; absence selects the global route. */
  consumer?: string;
  service: string;
  major: number;
  provider: string;
}

/** One typed method of a provided service. */
export interface ServiceMethodDescriptor {
  name: string;
  input_schema: string;
  output_schema: string;
  /** Safe during candidate validation: no durable writes or external effects. */
  preparation?: boolean;
  /** Releases owned resources after admission stops; cannot start new work. */
  cleanup?: boolean;
}

/** A versioned service a plugin provides; consumers resolve by name and major version. */
export interface ServiceDescriptor {
  name: string;
  version: string;
  methods: ServiceMethodDescriptor[];
}

/** Optional plugin-owned durable user notebook; declare major 1 with required: false. */
export const MEMORY_NOTEBOOK_SERVICE = "memory.notebook";

/** Reads the sanitized MEMORY.md index, at most 200 lines and 25,000 UTF-8 bytes. */
export interface MemoryNotebookIndexRequest {
  /** Positive UTF-8 byte budget, at most 25,000; absent or 0 uses that maximum. */
  max_bytes?: number;
}

/** Reads one topic, without scanning or loading the rest of the notebook. */
export interface MemoryNotebookReadRequest {
  /** Plain, non-hidden .md filename; use the index method for MEMORY.md. */
  file: string;
  /** Positive UTF-8 byte budget, at most 65,536; absent or 0 uses 16,384. */
  max_bytes?: number;
}

/** Fresh file evidence, not a model summary or an instruction authority. */
export interface MemoryNotebookSnapshotV1 {
  version: 1;
  source: { scope: "user_notebook"; file: string };
  found: boolean;
  /** Opaque full-source revision. Present for empty files, absent for missing files. */
  revision?: string;
  content: string;
  truncated: boolean;
}

/** Advertise the optional effect fence only when both operations retain the
 * selected policy generation until explicit cleanup; no snapshot fallback. */
export function authorizationServiceDescriptor(options: {effect_fence?:boolean} = {}): ServiceDescriptor {
  return {
    name: SECURITY_AUTHORIZE_SERVICE, version: "1.0.0",
    methods: [{name:SECURITY_AUTHORIZE_METHOD,input_schema:"security.authorize.input.v1",output_schema:"security.authorize.output.v1"}, ...(options.effect_fence ? [
      {name:"acquire-authorization",input_schema:"security.authorize.acquire-authorization.input.v1",output_schema:"security.authorize.acquire-authorization.output.v1"},
      {name:"release-authorization",input_schema:"security.authorize.release-authorization.input.v1",output_schema:"security.authorize.release-authorization.output.v1",cleanup:true},
    ] : [])],
  };
}
export const AUTHORIZATION_SERVICE_DESCRIPTOR = authorizationServiceDescriptor();

export const PROCESS_SANDBOX_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: PROCESS_SANDBOX_SERVICE,
  version: "1.0.0",
  methods: [{ name: PROCESS_SANDBOX_METHOD, input_schema: "sandbox.process.input.v1", output_schema: "sandbox.process.output.v1" }],
};

/** A consumed service; declaring it is the only way to gain call authority. */
export interface ServiceRequirement {
  name: string;
  major_version: number;
  required?: boolean;
}

/** Host-attested authenticated, policy-approved invocation. Never accepted from
 * service.call input. Providers verify the version/action and exact input digest;
 * authority names the durable validity source, not ambient owner privilege. */
export interface ServiceAdmission {
  readonly version: 1;
  readonly principal: import("./identity.js").PrincipalIdentity;
  readonly action: string;
  /** Lowercase SHA-256 of the exact admitted JSON parameter bytes. */
  readonly input_digest: string;
  /** Exact UTF-8 JSON used for input_digest. Verify its hash, parse it, then
   * structurally compare with params; reserialization is not byte-stable. */
  readonly input_json: string;
  readonly authority: string;
  /** Host-attested selected task-authority output for child derivation. Never
   * accepted from issue request JSON or inferred from a persisted record. */
  readonly lineage?: Readonly<Record<string, unknown>>;
}

/** One validated, host-routed call delivered to a service provider. */
/** Configured host storage coordination, not a user principal. Bound to the
 * exact selected provider/input; unavailable to service.call request JSON. */
export interface StorageStewardship {
  readonly version: 1;
  readonly authority: string;
  readonly provider: string;
  readonly operation: 'verify' | 'migration' | 'recover';
  readonly input_digest: string;
  readonly input_json: string;
  readonly consumer?: string;
}
export interface ServiceInvokeParams<TParams = unknown> {
  readonly storage_stewardship?: StorageStewardship;
  readonly admission?: ServiceAdmission;
  service: string;
  method: string;
  caller: string;
  execution_id?: string;
  /** Single-use unchanged selected-tool dispatch; never generic call authority. */
  continuation_id?: string;
  /** Authenticated caller attribution; never use its ID to issue host calls. */
  caller_scope?: CallerExecutionScope;
  params?: TParams;
}

/** Notification that a consumed service resolution changed. */
export interface ServiceChangedParams {
  service: string;
  reason?: string;
}

export interface RuntimeInitializeParams {
  /** Immutable scope selected by the host; absent fields mean legacy Coding. */
  user_space_id?: string;
  product_domain?: "personal" | "coding";
  data_root?: string;
  protocol_version: number;
  plugin_id: string;
  plugin_root: string;
  project_root: string;
  workspace_id?: string;
  wuu_home: string;
  workspace_state_dir?: string;
  capability_protocol_version?: number;
  supported_host_services?: HostServiceMethod[];
  /** Exact versioned host semantics. Missing means none; unrelated names imply nothing. */
  supported_host_features?: readonly string[];
  lifecycle_version?: 2;
}

export interface RuntimeInitializeResult {
  /** The SDK verifies these exact names against the host offer before replying.
   * Authority-aware document descriptors add their required feature automatically. */
  required_host_features?: readonly string[];
  model_adapters?: readonly ModelAdapterDescriptor[];
  tools?: ToolRegistration[];
  /** Capability protocol selected by the plugin, not initialize params.protocol_version.
   * Omission retains tool-only v1 unless service declarations select v3.
   * Capabilities and model adapters require an explicit 2 or 3 without services. */
  protocol_version?: 1 | 2 | 3;
  capabilities?: CapabilityDescriptor[];
  provided_services?: ServiceDescriptor[];
  required_services?: ServiceRequirement[];
  lifecycle_version?: 2;
}

export interface JSONSchemaObject {
  type: "object";
  properties?: Readonly<Record<string, unknown>>;
  required?: string[];
  additionalProperties?: boolean;
  [key: string]: unknown;
}

export interface ToolActivityMetadata {
  /** Delegate effects through execution.invoke-tool without holding a leaf scheduling slot. */
  orchestrator?: boolean;
  read_only?: boolean;
  /** Overlap with other explicitly safe tools; ordinary callbacks and unsafe tools stay exclusive. */
  concurrency_safe?: boolean;
  destructive?: boolean;
  risk?: string;
  reason?: string;
}

export interface AuthorizationRequest {
  session_id?: string;
  actor_id?: string;
  cwd: string;
  permission_mode: string;
  tool: AuthorizationTool;
}

export interface AuthorizationTool {
  name: string;
  kind: string;
  arguments?: string;
  read_only: boolean;
  concurrency_safe: boolean;
  destructive: boolean;
  risk?: string;
  reason?: string;
}

export interface AuthorizationDecision {
  outcome: "allow" | "deny";
  reason?: string;
}

export interface AuthorizationAcquireRequest { readonly id: string; readonly request: AuthorizationRequest; }
export interface AuthorizationLease { readonly id: string; readonly decision: AuthorizationDecision; }
export interface AuthorizationReleaseRequest { readonly id: string; }

export interface ProcessSandboxRequest {
  argv: string[];
  policy: ProcessSandboxPolicy;
}

export interface ProcessSandboxPolicy {
  mode: "read-only" | "workspace-write";
  writable_roots?: string[];
  /** Absolute read/write exclusions, independent of writable grants. These do
   * not promise network, process-visibility or metadata-query isolation.
   */
  protected_paths?: string[];
}

export interface ProcessSandboxResult {
  argv: string[];
  enforcement: "full" | "partial";
  /** Required true before spawn when protected_paths is nonempty. An older
   * provider's "full" alone is insufficient; unsupported protection fails closed.
   */
  protected_paths_enforced?: boolean;
  denial_signatures?: string[];
  runner_failure_signatures?: string[];
}

export interface ToolDisplayMetadata {
  /** Short stable user-facing tool name. Dispatch identity remains host-owned. */
  label?: string;
  /** Optional locale overrides, resolved by exact locale, language, then label.
   * Requires a default label. Stored with each call for historical display. */
  label_translations?: Record<string, string>;
  kind?: string;
  text?: string;
  capability?: string;
}

export interface ToolRegistration {
  /** Keep interaction/lifecycle controls directly visible; reject nested invocation. */
  direct_only?: boolean;
  id: string;
  description: string;
  input_schema: JSONSchemaObject;
  execution_scopes?: Array<"root" | "child">;
  activity?: ToolActivityMetadata;
  display?: ToolDisplayMetadata;
}

export interface CapabilityInvokeParams<TInput = unknown, TOutput = unknown> {
  capability: string;
  execution_id?: string;
  input: TInput;
  output: TOutput;
}

export interface CapabilityInvokeResult<TOutput = unknown> {
  output: TOutput;
}

export interface RequestTransformInput {
  session_id?: string;
  thread_id?: string;
  cwd?: string;
  provider?: string;
  step_index: number;
  request: ModelRequestViewV1;
}

export interface ModelRequestViewV1 {
  version: 1;
  model: string;
  messages: readonly ModelMessageViewV1[];
  tools: readonly ModelToolViewV1[];
  temperature?: number;
  max_tokens?: number;
  effort?: string;
  native_deferred_tool_discovery?: boolean;
  force_tool_name?: string;
}

export interface ModelMessageViewV1 {
  role: string;
  name?: string;
  content?: string;
  hidden?: boolean;
  origin?: string;
  origin_id?: string;
  cause?: string;
  read_only?: boolean;
  has_images?: boolean;
  has_files?: boolean;
  tool_call_id?: string;
  tool_calls?: readonly ModelToolCallViewV1[];
  has_tool_result?: boolean;
  discovered_tools?: readonly string[];
}

export interface ModelToolCallViewV1 {
  id?: string;
  name?: string;
  arguments?: string;
  kind?: string;
}

export interface ModelToolViewV1 {
  name: string;
  description?: string;
  input_schema: Readonly<Record<string, unknown>>;
  defer_loading?: boolean;
}

export interface RequestTransformOutput {
  prepend_system_messages?: readonly string[];
}

export interface AgentPreStepInput {
 source_message?: MessageRef;
 message_relation?: MessageRelation;
  session_id?: string;
  thread_id?: string;
  cwd?: string;
  provider?: string;
  model?: string;
  step_index: number;
  messages: readonly ModelMessageViewV1[];
}

/** Evaluated after durable input admission, before compaction and model dispatch.
 * The provider/model and generation are already bound. Descending priority,
 * then plugin ID determines order. Stop short-circuits later policies. Errors
 * follow the declared capability error policy; cancellation always propagates.
 * Replacement drivers receive the decision through admitInput().
 */
export type AgentStepPolicyInput = AgentPreStepInput;
export interface AgentStepPolicyOutput {
  stop: boolean;
  /** Required when stopping; preserved as the terminal stop reason. */
  reason?: string;
  /** Required when stopping; recorded as a visible policy-authored message. */
  content?: string;
}

export interface AgentPreStepMessage {
  id: string;
  content: string;
}

export interface AgentPreStepOutput {
  append_messages?: readonly AgentPreStepMessage[];
}

export interface AgentTurnInterruptedInput {
  thread_id: string;
  turn_id: string;
  cause?: string;
}

/** Settled-turn observation, emitted after the host persists conversation history. */
export interface AgentTurnCompletedInput {
  thread_id: string;
  turn_id: string;
  started_at: string;
  completed_at: string;
  /** True only when execution and required durable settlement succeeded; not task-level acceptance. */
  succeeded: boolean;
  input_tokens: number;
  output_tokens: number;
}

export interface TurnLifecycleInput {
  request_id: string;
  state: string;
  /** A queued input discarded by host shutdown may be resubmitted with the same request ID. */
  retryable?: boolean;
  thread_id: string;
  turn_id?: string;
  queue_id?: string;
  error?: string;
  started_at?: string;
  completed_at?: string;
  input_tokens?: number;
  output_tokens?: number;
  final_output?: string;
}

export interface SystemPromptSectionInput {
  cwd: string;
  provider: string;
  model: string;
}

export interface SystemPromptSectionOutput {
  text: string;
}

/** @experimental Provider-neutral message payload. Preserve unknown fields when compacting. */
export type CompactionMessage = Readonly<Record<string, unknown>>;

/** @experimental No distributed first-party consumer has proven this contract. */
export interface CompactionInput<TMessage extends CompactionMessage = CompactionMessage> {
  model: string;
  messages: readonly TMessage[];
}

/** @experimental No distributed first-party consumer has proven this contract. */
export interface CompactionOutput<TMessage extends CompactionMessage = CompactionMessage> {
  messages: readonly TMessage[];
}

export interface ToolExecuteParams<TArguments = unknown> {
  /** Host-attested originating human input for this turn; absent for generated wakeups. */
  source_message?: MessageRef;
  message_relation?: MessageRelation;
  tool_id: string;
  session_id?: string;
  thread_id?: string;
  turn_id?: string;
  execution_id?: string;
  actor_id?: string;
  cwd: string;
  step_index?: number;
  call_id: string;
  tool: string;
  arguments: TArguments;
}

export interface UserQuestionOption {
  label: string;
  description?: string;
}

export interface UserQuestion {
  id: string;
  question: string;
  header?: string;
  detail?: string;
  options?: readonly UserQuestionOption[];
  multi_select?: boolean;
  allow_custom?: boolean;
}

export interface UserQuestionAnswerItem {
  id: string;
  selected: readonly string[];
  custom?: string;
}

export interface UserQuestionAnswer {
  answers: readonly UserQuestionAnswerItem[];
}

export interface UserQuestionOfferResult {
  request_id: string;
  expires_at: string;
}

export type ToolContentType = "text" | "image" | "audio" | "file" | "resource" | "resource_link";

export interface ArtifactPresentation {
  /** Optional placement hint. Images default inline; other artifacts default to the turn end. */
  placement?: "inline" | "turn_end";
  /** Stable identity returned by host.artifact.import. */
  ref?: string;
  sha256?: string;
  size_bytes?: number;
}

export interface ToolContentPart {
  /** Output-only deferred-media reference preserved in model snapshots. */
  remote_ref?: string;
  type: ToolContentType;
  text?: string;
  data?: string;
  mime_type?: string;
  uri?: string;
  name?: string;
  resource?: unknown;
  artifact?: ArtifactPresentation;
}

export interface ToolResult {
  content?: ToolContentPart[];
  structured_content?: unknown;
  meta?: unknown;
  is_error?: boolean;
  activity?: {
    id: string;
    kind: string;
    state?: string;
    thread_id?: string;
    preview_uri?: string;
  };
}

export interface ToolExecuteResult {
  result: ToolResult;
}

/** Host-only Bearer authentication. Secret setting values never enter the plugin process.
 * Only declared exact HTTPS origins are allowed; redirects are rejected. Requests time out
 * after 20 seconds, request/response bodies are capped at 4 MiB, and errors are sanitized.
 * A transport failure after a write begins does not establish whether it was applied.
 */
export interface CredentialRequestParams {
  credential: string;
  url: string;
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";
  headers?: Record<string, string>;
  body?: string;
}
export interface CredentialRequestResult {
  status: number;
  /** Lower-case content-type, retry-after and request-id headers only. */
  headers: Record<string, string>;
  body: string;
}

/** Replace the entire display snapshot on each read; do not merge it as events.
 * Only the caller's owned session and an exclusively correlated request can be
 * projected. Shared/mixed-input turns are unavailable rather than misattributed.
 * There is no replay, persistence, delivery, admission or control-authority grant.
 */
export type SessionProgressSnapshotV1 =
  | { readonly version: 1; readonly available: false; readonly reason: "session_not_owned" | "request_not_exclusive" | "turn_unavailable" }
  | {
    readonly version: 1;
    readonly available: true;
    readonly session_id: string;
    readonly request_id: string;
    readonly turn_id: string;
    /** Latest eligible display items, in conversation order. No prompts,
     * reasoning, tool arguments/results, attachments or private provider state. */
    readonly items: readonly SessionProgressItemV1[];
    /** An item or text was omitted. The entire JSON projection is at most
     * 32 KiB and contains at most 32 items; text is at most 4096 UTF-8 bytes. */
    readonly truncated: boolean;
  };

export interface SessionProgressItemV1 {
  readonly id: string;
  readonly type: "assistant" | "tool";
  readonly status: "in_progress" | "completed" | "failed";
  readonly text?: string;
  readonly name?: string;
  readonly truncated?: boolean;
}

export interface HostServiceContracts {
  "host.session.route": {
    params: import("./session-transport.js").SessionRouteRequest;
    result: import("./session-transport.js").SessionRouteResult;
  };
  "host.credentials.request": { params: CredentialRequestParams; result: CredentialRequestResult };
  "host.storage.get": { params: { scope: "user" | "workspace"; key: string }; result: { value: string | null } };
  "host.storage.set": { params: { scope: "user" | "workspace"; key: string; value: string }; result: Record<string, never> };
  "host.storage.delete": { params: { scope: "user" | "workspace"; key: string }; result: Record<string, never> };
  "host.storage.keys": { params: { scope: "user" | "workspace" }; result: { keys: string[] } };
  "host.storage.compare_exchange": {
    params: { scope: "user" | "workspace"; key: string; expected: string | null; value: string | null };
    result: { swapped: boolean; value: string | null };
  };
  "host.settings.get": { params: { key: string }; result: { value: unknown } };
  "host.settings.list": {
    params: { scope?: "user" | "workspace" };
    result: {
      entries: Record<string, unknown>;
      /** Present on snapshot-aware hosts; old entries-only readers remain compatible. */
      revision?: string;
      scope_revisions?: Record<string, string>;
      restart_required?: string[];
    };
  };
  "host.session.create": {
    params: {
      /** Parent lineage proof for one persisted pending task/create receipt. */
      execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference;
      authorization_task_id?: string;
      user_space_id?: string;
      product_domain?: "personal" | "coding";
      agent_id?: string;
      request_id: string;
      name?: string;
      visibility: "user" | "plugin";
      parent_session_id?: string;
      context_source: "fresh" | "fork" | "seed";
      workspace?: "shared" | "worktree";
      workspace_id?: string;
      workspace_root?: string;
      model_alias?: string;
      provider?: string;
      model?: string;
      variant?: string;
      effort?: string;
      speed?: string;
      permission_mode?: string;
      instructions?: string;
      tool_policy?: { allow?: string[]; deny?: string[] };
      seed?: {
        version: 1;
        id?: string;
        body: string;
        source: { session_id: string; through_seq: number };
        references?: Array<{ id: string; label?: string; history: { snapshot: { session_id: string; through_seq: number }; start_seq: number; end_seq: number } }>;
        artifacts?: Array<{ id: string; label?: string }>;
        provenance: { producer: string; source_model?: string; created_at?: string };
      };
      launch?: { revision?: number; kind?: string; intent?: string; prompt?: string };
    };
    result: { session_id: string; created: boolean; workspace_root?: string };
  };
  "host.session.send": {
    params: {
      /** Opaque unattended grant. The host derives current scope from selected
       * task/session authorities and validates it at admission; JSON cannot
       * supply a principal, owner claim, or substitute task scope. */
      execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference;
      /** Prohibits all tools for this turn. Unsupported with steer or external engines. */
      disable_tools?: boolean;
      user_input_request_id?: string;
      resume_accepted?: boolean;
      resume_receipt_id?: string;
 relation?: MessageRelation;
      request_id: string;
      session_id: string;
      input: {
        prompt: string;
        attachments?: SessionInputAttachment[];
        context_blocks?: Array<{ kind?: string; title?: string; source?: string; content: string }>;
      };
      presentation?: { kind: "query_bubble" | "session_message"; text: string; name?: string; related_session_id?: string };
      cause?: string;
      if_running?: "queue" | "steer";
      reply_to_turn_id?: string;
      control_revision?: number;
    };
    result: { state: string; session_id: string; turn_id?: string; queue_id?: string; steered?: boolean; receipt?: ClientInputReceipt };
  };
  "host.session.list": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference; parent_session_id?: string; scope?: "owned" | "shared" };
    result: {
      sessions: Array<{
        session_id: string;
        user_space_id?: string;
        product_domain?: "personal" | "coding";
        agent_id?: string;
        name?: string;
        parent_session_id?: string;
        visibility: "user" | "plugin";
        state: string;
        workspace_id?: string;
        created_at?: string;
        updated_at?: string;
      }>;
    };
  };
  "host.session.cancel": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference; session_id: string; turn_id?: string; queue_id?: string; control_revision?: number };
    result: { session_id: string; turn_id?: string; queue_id?: string; cancelled: boolean };
  };
  "host.session.control": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference; session_id: string; state?: "active" | "paused" | "released"; revision?: number };
    result: { session_id: string; manager_id?: string; state?: string; revision: number };
  };
  "host.session.context": { params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference; session_id: string }; result: { session_id: string; user_space_id: string; product_domain: "personal" | "coding"; agent_id: string; parent_session_id: string } };
  "host.session.inspect": {
    params: {
      execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference;
      session_id: string;
      turn_id?: string;
      request_id?: string;
      wait?: "none" | "terminal";
      timeout_ms?: number;
      /** Requires explicit request_id and session.progress.v1 host feature.
       * Samples current display progress without subscribing or changing work. */
      include_progress?: boolean;
    };
    result: {
      receipt?: ClientInputReceipt;
      accepted_input?: AcceptedClientInputView;
      pending_inputs?: AcceptedClientInputView[];
      session: {
        session_id: string;
        user_space_id?: string;
        product_domain?: "personal" | "coding";
        agent_id?: string;
        name?: string;
        parent_session_id?: string;
        visibility: "user" | "plugin";
        state: string;
        workspace_id?: string;
        created_at?: string;
        updated_at?: string;
      };
      turn?: {
        request_id?: string;
        state: string;
        /** Only queued inputs discarded by host shutdown are retryable. */
        retryable?: boolean;
        turn_id?: string;
        queue_id?: string;
        error?: string;
        started_at?: string;
        completed_at?: string;
        input_tokens?: number;
        output_tokens?: number;
        final_output?: string;
      };
      workspace?: { kind: "shared" | "worktree"; dirty?: boolean; changed_files?: string[] };
      timed_out?: boolean;
      progress?: SessionProgressSnapshotV1;
    };
  };
  "host.session.attachment.read": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference; session_id: string; seq: number; kind: "image" | "file" | "artifact"; index: number; sha256: string; offset?: number };
    /** Offset/total count base64 characters, chunks are at most 128 KiB. */
    result: { data: string; offset: number; total: number; content_type: string; name: string; sha256: string };
  };
  "host.session.history.read": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference;
      session_id: string;
      start_seq?: number;
      end_seq?: number;
      snapshot_seq?: number;
      limit?: number;
      cursor?: string;
    };
    result: {
      session_id: string;
      head_seq: number;
      snapshot_seq: number;
      records?: Array<{
        receipt?: ClientInputReceipt;
        relation?: MessageRelation;
        at?: string;
        seq: number;
        attachments?: SessionHistoryAttachment[];
        role: string;
        /** Physical history includes hidden/generated model context; role alone is not authorship. */
        hidden?: boolean;
        read_only?: boolean;
        origin?: string;
        name?: string;
        content?: string;
        display_content?: string;
        /** Provider identity, not the host-generated live item or turn ID. */
        provider_item_id?: string;
        engine_turn_id?: string;
        reasoning_content?: string;
        tool_calls?: string;
        tool_call_id?: string;
        tool_result?: string;
        finish_reason?: string;
        truncated?: boolean;
      }>;
      has_more?: boolean;
      budget_exhausted?: boolean;
      next_cursor?: string;
    };
  };
  "host.session.history.search": {
    params: { execution_authorization?: import("./execution-authorization.js").ExecutionAuthorizationReference;
      view?: "conversation";
      time_min?: string;
      time_max?: string;
      session_id: string;
      query: string;
      start_seq?: number;
      end_seq?: number;
      snapshot_seq?: number;
      before_seq?: number;
      limit?: number;
      cursor?: string;
    };
    result: {
      session_id: string;
      head_seq: number;
      snapshot_seq: number;
      matches?: Array<{ at?: string; seq: number; role: string; name?: string; excerpt?: string }>;
      has_more?: boolean;
      budget_exhausted?: boolean;
      next_cursor?: string;
    };
  };
  "host.workspace.status": {
    params: { session_id: string };
    result: {
      session_id: string;
      dirty: boolean;
      changed_files?: string[];
      porcelain?: string[];
      diff?: string;
      can_apply: boolean;
      conflict_files?: string[];
      error?: string;
    };
  };
  "host.workspace.apply": {
    params: { session_id: string };
    result: { session_id: string; applied: boolean; changed_files?: string[]; discarded: boolean };
  };
  "host.workspace.discard": {
    params: { session_id: string };
    result: { session_id: string; discarded: boolean };
  };
  "host.service.call": {
    params: { service: string; method: string; execution_id?: string; cleanup?: boolean; params?: unknown };
    result: unknown;
  };
  "host.service.continue": {
    params: { continuation_id: string; execution_id?: string };
    result: unknown;
  };
}

export type HostServiceCall<M extends HostServiceMethod = HostServiceMethod> = M extends HostServiceMethod
  ? { id: string; method: M; params?: HostServiceContracts[M]["params"] }
  : never;

export interface HostServiceError {
  code: string;
  message: string;
}

export type HostServiceResponse<M extends HostServiceMethod = HostServiceMethod> =
  | { id: string; result?: HostServiceContracts[M]["result"]; error?: never }
  | { id: string; result?: never; error: HostServiceError };

export type HostServiceRequest<M extends HostServiceMethod = HostServiceMethod> = HostServiceCall<M>;
export type HostServiceResult<M extends HostServiceMethod = HostServiceMethod> = HostServiceResponse<M>;

export class RuntimeHostServiceError extends Error {
  readonly code: string;

  constructor(error: HostServiceError) {
    super(error.message);
    this.name = "RuntimeHostServiceError";
    this.code = error.code;
  }
}

export interface RuntimeHostCallOptions {
  /** Settle an already owned resource without borrowing a cancelled execution. The selected service still enforces its cleanup/owner contract. */
  cleanup?: boolean;
  /** Stop waiting for this call. This does not undo effects already accepted by the host. */
  signal?: AbortSignal;
}

export interface RuntimeHost {
  supports(method: HostServiceMethod): boolean;
  call<M extends HostServiceMethod>(
    method: M,
    params: HostServiceContracts[M]["params"],
    options?: RuntimeHostCallOptions,
  ): Promise<HostServiceContracts[M]["result"]>;
}

/** Lifetime of background work started by initialize or activate. */
export interface RuntimeLifecycleContext {
  /** Aborts on shutdown or transport loss. Background tasks must stop when it aborts. */
  readonly signal: AbortSignal;
}

/** Host-authenticated owner of resources created by a service callback. */
export interface CallerExecutionScope {
  readonly id: string;
  readonly plugin_id: string;
  readonly kind: "tool" | "callback";
  readonly session_id?: string;
  readonly thread_id?: string;
  readonly turn_id?: string;
  readonly actor_id?: string;
  readonly call_id?: string;
  readonly tool?: string;
  readonly cwd?: string;
}

export interface RuntimeExecutionContext {
  readonly executionId: string;
  /** Survives this callback; aborts when the caller ends or this provider stops.
   * Attribution and lifetime do not grant the caller's tool authority. */
  readonly caller?: CallerExecutionScope & { readonly signal: AbortSignal };
  /** Aborts when this execution ends, is cancelled, or its runtime transport closes. */
  readonly signal: AbortSignal;
}

export interface ExecutionUpdate {
  execution_id: string;
  message?: string;
  detail?: unknown;
}

export interface InvokeToolParams {
  execution_id: string;
  /** Scope-local idempotency key; retries must keep name and arguments identical. */
  call_id: string;
  name: string;
  arguments: unknown;
}

/** Invoke an enabled child tool from a live orchestrator execution. Requires execution.invoke-tool v1. */
export async function invokeTool(host: RuntimeHost, params: InvokeToolParams): Promise<ToolResult> {
  return await host.call("host.service.call", {
    service: INVOKE_TOOL_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params,
  }) as ToolResult;
}

/** Text-only input: no tools, attachments, credentials, or implicit conversation history. */
export interface ModelRequestMessage {
  readonly role: "system" | "user" | "assistant";
  readonly content: string;
}

export interface ModelRequestParams {
  /** One to 32 nonempty messages, at most 256 KiB of UTF-8 content in total. */
  readonly messages: readonly ModelRequestMessage[];
  /** Existing configured model alias; omitted uses the host's configured model. */
  readonly model_alias?: string;
  /** Explicit configured provider and model; supply both, without model_alias. */
  readonly provider?: string;
  readonly model?: string;
  /** Strict configured variant for an explicit provider/model. Unsupported values fail. */
  readonly variant?: string;
  /** For an explicit provider/model, use this reasoning tier if the model supports it. */
  readonly preferred_effort?: string;
  /**
   * Requested output-token limit, integer 1–8192; defaults to 1024.
   * An adapter that cannot preserve the limit fails with unsupported_limit
   * before inference unless allow_unbounded_output is true. Remote-server
   * enforcement and billing are not guaranteed.
   */
  readonly max_output_tokens?: number;
  /** Permit an adapter to omit the output cap; defaults to false. */
  readonly allow_unbounded_output?: boolean;
  /** Integer 1–45000 milliseconds; defaults to 45000. */
  readonly timeout_ms?: number;
}

export interface ModelRequestUsage {
  readonly input_tokens: number;
  readonly output_tokens: number;
  readonly cache_creation_tokens: number;
  readonly cache_read_tokens: number;
  readonly cache_creation_unknown: boolean;
}

export interface ModelRequestResult {
  readonly text: string;
  readonly provider: string;
  readonly model: string;
  readonly finish_reason: string;
  readonly truncated: boolean;
  readonly usage?: ModelRequestUsage;
}

/**
 * Run one bounded, tools-free model request through Wuu's configured provider.
 * Requires host.model.request v1 and a host scoped to a live tool, capability,
 * or service callback, including a generation-owned background job's callback.
 * The request is cancelled when that execution ends or is cancelled.
 */
export async function requestModel(host: RuntimeHost, input: ModelRequestParams): Promise<ModelRequestResult> {
  return await host.call("host.service.call", {
    service: MODEL_REQUEST_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params: input,
  }) as ModelRequestResult;
}

export interface ArtifactImportParams {
  /** Absolute path, or a path relative to the current tool execution's cwd. */
  path?: string;
  /** Standard-base64 bytes. Exactly one of path or data must be provided. */
  data?: string;
  name?: string;
  mime_type?: string;
}

export interface ImportedArtifact {
  id: string;
  uri: string;
  name: string;
  mime_type: string;
  size: number;
  sha256: string;
}

export async function reportExecutionUpdate(host: RuntimeHost, update: ExecutionUpdate): Promise<void> {
  await host.call("host.service.call", {
    service: EXECUTION_UPDATE_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params: update,
  });
}

export async function askUserQuestions(
  host: RuntimeHost,
  questions: readonly UserQuestion[],
): Promise<UserQuestionAnswer> {
  return await host.call("host.service.call", {
    service: USER_QUESTION_ASK_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params: { questions },
  }) as UserQuestionAnswer;
}

/** Offer a non-blocking question; any answer arrives through the conversation later. */
export async function offerUserQuestions(
  host: RuntimeHost,
  questions: readonly UserQuestion[],
): Promise<UserQuestionOfferResult> {
  return await host.call("host.service.call", {
    service: USER_QUESTION_OFFER_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params: { questions },
  }) as UserQuestionOfferResult;
}

/** Copy a generated file into storage owned by the current tool execution's thread. */
export async function importArtifact(
  host: RuntimeHost,
  artifact: ArtifactImportParams,
): Promise<ImportedArtifact> {
  return await host.call("host.service.call", {
    service: ARTIFACT_IMPORT_SERVICE,
    method: KERNEL_SERVICE_METHOD,
    params: artifact,
  }) as ImportedArtifact;
}

export interface FileLockAcquireParams {
  root: "workspace_state" | "wuu_home" | "project";
  /** Relative lock-file path, separate from the data file it protects. */
  path: string;
}

export interface FileLockAcquireResult {
  lock_id: string;
}

export interface FileLockReleaseResult {
  released: boolean;
}

/**
 * Acquire a cross-process OS lock through the host retained from initialize/activate.
 * An execution ID lets the kernel cancel a blocked acquisition. Wait for its
 * reply even after cancellation so a successfully acquired lock can be released.
 * The host releases outstanding locks when the runtime closes.
 */
export async function acquireFileLock(
  host: RuntimeHost,
  params: FileLockAcquireParams,
  executionId?: string,
): Promise<FileLockAcquireResult> {
  return await host.call("host.service.call", {
    service: FILE_LOCK_SERVICE,
    method: "acquire",
    execution_id: executionId,
    params,
  }) as FileLockAcquireResult;
}

/**
 * Release a lock idempotently. Use the host retained from initialize/activate
 * for cleanup after execution cancellation; an execution-scoped host is aborted.
 */
export async function releaseFileLock(
  host: RuntimeHost,
  lockId: string,
  options?: RuntimeHostCallOptions,
): Promise<FileLockReleaseResult> {
  return await host.call("host.service.call", {
    service: FILE_LOCK_SERVICE,
    method: "release",
    params: { lock_id: lockId },
  }, options) as FileLockReleaseResult;
}

export interface RuntimePlugin {
  /** Additional reentrant handlers. model.adapter and model.middleware are always reentrant. */
  concurrentCapabilities?: readonly string[];
  /**
   * Service names whose handlers may overlap tools, capabilities, and services.
   * Opt in only when handlers are safe for concurrent access. This permits a
   * service callback into a plugin already awaiting another plugin. Other
   * callbacks and tools without concurrency-safe metadata remain ordered;
   * lifecycle requests are barriers for all handlers.
   */
  concurrentServices?: readonly string[];
  initialize(params: RuntimeInitializeParams, host: RuntimeHost, lifecycle: RuntimeLifecycleContext): RuntimeInitializeResult | Promise<RuntimeInitializeResult>;
  /** Read and validate state through declared, prepared service dependencies. */
  prepare?(host: RuntimeHost, lifecycle: RuntimeLifecycleContext): void | Promise<void>;
  activate?(host: RuntimeHost, lifecycle: RuntimeLifecycleContext): void | Promise<void>;
  invokeCapability?(params: CapabilityInvokeParams, host: RuntimeHost, execution: RuntimeExecutionContext): CapabilityInvokeResult | Promise<CapabilityInvokeResult>;
  invokeService?(params: ServiceInvokeParams, host: RuntimeHost, execution: RuntimeExecutionContext): unknown | Promise<unknown>;
  serviceChanged?(params: ServiceChangedParams, host: RuntimeHost): void | Promise<void>;
  executeTool?(params: ToolExecuteParams, host: RuntimeHost, execution: RuntimeExecutionContext): ToolExecuteResult | Promise<ToolExecuteResult>;
  shutdown?(): void | Promise<void>;
}

export interface RuntimeInitializeRequest {
  id: string;
  method: "initialize";
  params: RuntimeInitializeParams;
}

export interface RuntimeCapabilityRequest {
  id: string;
  method: "capability.invoke";
  params: CapabilityInvokeParams;
}

export interface RuntimeServiceInvokeRequest {
  id: string;
  method: "service.invoke";
  params: ServiceInvokeParams;
}

export interface RuntimeServiceChangedRequest {
  id: string;
  method: "service.changed";
  params: ServiceChangedParams;
}

export interface RuntimePrepareRequest {
  id: string;
  method: "prepare";
  params?: undefined;
}

export interface RuntimeActivateRequest {
  id: string;
  method: "activate";
  params?: undefined;
}

export interface RuntimeToolRequest {
  id: string;
  method: "tool.execute";
  params: ToolExecuteParams;
}

export interface RuntimeShutdownRequest {
  id: string;
  method: "shutdown";
  params?: undefined;
}

export interface RuntimeExecutionCancelRequest {
  id: string;
  method: "execution.cancel";
  params: { execution_id: string };
}

export type RuntimeRequest =
  | RuntimeInitializeRequest
  | RuntimePrepareRequest
  | RuntimeActivateRequest
  | RuntimeCapabilityRequest
  | RuntimeServiceInvokeRequest
  | RuntimeServiceChangedRequest
  | RuntimeToolRequest
  | RuntimeShutdownRequest;

export type RuntimeResponse =
  | { id: string; result: unknown }
  | { id: string; error: { code?: string; message: string } };

export async function handleRuntimeRequest(
  plugin: RuntimePlugin,
  request: RuntimeRequest,
  host: RuntimeHost = unavailableRuntimeHost,
  execution: RuntimeExecutionContext = idleRuntimeExecution,
  lifecycle: RuntimeLifecycleContext = idleRuntimeExecution,
): Promise<RuntimeResponse> {
  const executionHost = scopedRuntimeHost(host, execution);
  try {
    switch (request.method) {
      case "initialize": {
        const result = await plugin.initialize(request.params, host, lifecycle);
        const protocolVersion = result.required_services?.length || result.provided_services?.length
          ? CAPABILITY_PROTOCOL_V3 : result.protocol_version;
        if ((protocolVersion ?? 1) === 1 && (result.capabilities?.length || result.model_adapters?.length)) {
          throw Object.assign(new Error("initialize() must return protocol_version: 2 or 3 when declaring capabilities or model_adapters; omission selects legacy v1"), { code: "invalid_capability_protocol" });
        }
        const offered = request.params.supported_host_features;
        const required = result.required_host_features;
        for (const features of [offered, required]) {
          if (features !== undefined && (!Array.isArray(features) || features.some(feature =>
            typeof feature !== "string" || !feature || feature.trim() !== feature || feature.includes("\0")))) {
            throw Object.assign(new Error("Host feature declarations must be arrays of exact nonempty names"), { code: "invalid_host_features" });
          }
        }
        const requirements = new Set(required);
        if (result.provided_services?.some(service => service.name === DOCUMENT_STORE_SERVICE
          && service.methods?.some(method => method.name === "authority"))) {
          requirements.add(DOCUMENT_AUTHORITY_HOST_FEATURE);
        }
        for (const feature of requirements) {
          if (!offered?.includes(feature)) {
            throw Object.assign(new Error(`Required host feature ${feature} is unavailable; refusing incompatible host semantics`), { code: "host_feature_unavailable" });
          }
        }
        return { id: request.id, result: {
          ...result,
          ...(requirements.size ? { required_host_features: [...requirements] } : {}),
          protocol_version: protocolVersion,
          lifecycle_version: 2,
        } };
      }
      case "prepare":
        await plugin.prepare?.(host, lifecycle);
        return { id: request.id, result: null };
      case "activate":
        await plugin.activate?.(host, lifecycle);
        return { id: request.id, result: null };
      case "capability.invoke":
        if (!plugin.invokeCapability) throw new Error("capability.invoke is not implemented");
        // Read the live signal after queue/lifecycle waiting, before user code.
        execution.signal.throwIfAborted();
        return { id: request.id, result: await plugin.invokeCapability(request.params, executionHost, execution) };
      case "service.invoke":
        if (!plugin.invokeService) throw new Error("service.invoke is not implemented");
        execution.signal.throwIfAborted();
        return { id: request.id, result: await plugin.invokeService(request.params, executionHost, execution) };
      case "service.changed":
        await plugin.serviceChanged?.(request.params, host);
        return { id: request.id, result: null };
      case "tool.execute":
        if (!plugin.executeTool) throw new Error("tool.execute is not implemented");
        execution.signal.throwIfAborted();
        return { id: request.id, result: await plugin.executeTool(request.params, executionHost, execution) };
      case "shutdown":
        await plugin.shutdown?.();
        return { id: request.id, result: null };
    }
  } catch (error) {
    const code = typeof error === "object" && error !== null && "code" in error
      && typeof error.code === "string" ? error.code.trim() : "";
    return { id: request.id, error: {
      ...(code ? { code } : {}),
      message: typeof error === "object" && error !== null && "message" in error && typeof error.message === "string"
        ? error.message : String(error),
    } };
  }
}

function scopedRuntimeHost(host: RuntimeHost, execution: RuntimeExecutionContext): RuntimeHost {
  return {
    supports: (method) => host.supports(method),
    call: (method, params, options) => {
      const signal = options?.cleanup ? options.signal : options?.signal
        ? AbortSignal.any([execution.signal, options.signal])
        : execution.signal;
      if (method === "host.service.continue") {
        if (options?.cleanup) return Promise.reject(new Error("Service continuation cannot run as cleanup"));
        const continuation = params as HostServiceContracts["host.service.continue"]["params"];
        return host.call("host.service.continue", {
          continuation_id: continuation.continuation_id, execution_id: execution.executionId,
        }, {signal});
      }
      const serviceParams = method === "host.service.call"
        ? params as HostServiceContracts["host.service.call"]["params"]
        : { service: KERNEL_SERVICE_NAMES[method as KernelHostServiceMethod], method: KERNEL_SERVICE_METHOD, params };
      return host.call("host.service.call", {
        ...serviceParams,
        ...(options?.cleanup ? { execution_id: undefined, cleanup: true } : execution.executionId ? { execution_id: execution.executionId } : {}),
      }, { signal });
    },
  } as RuntimeHost;
}

export interface JSONLInput extends AsyncIterable<Uint8Array | string> {}
export interface JSONLOutput { write(chunk: string): unknown }

const unavailableRuntimeHost: RuntimeHost = {
  supports: () => false,
  call: async (method) => { throw new Error(`host service ${method} is unavailable outside a runtime connection`); },
};
const idleRuntimeExecutionController = new AbortController();
const idleRuntimeExecution: RuntimeExecutionContext = {
  executionId: "",
  signal: idleRuntimeExecutionController.signal,
};

type PendingHostCall = {
  resolve(value: unknown): void;
  reject(error: unknown): void;
};

class JSONLRuntimeHost implements RuntimeHost {
  private sequence = 0;
  private closed = false;
  private supported = new Set<HostServiceMethod>();
  private readonly pending = new Map<string, PendingHostCall>();

  constructor(private readonly send: (value: unknown) => Promise<void>) {}

  configure(methods: readonly HostServiceMethod[] | undefined): void {
    this.supported = new Set(methods ?? []);
  }

  supports(method: HostServiceMethod): boolean {
    if (method === "host.service.continue") return this.supported.has(method);
    return this.supported.has("host.service.call") || this.supported.has(method);
  }

  call<M extends HostServiceMethod>(
    method: M,
    params: HostServiceContracts[M]["params"],
    options?: RuntimeHostCallOptions,
  ): Promise<HostServiceContracts[M]["result"]> {
    const signal = options?.signal;
    if (signal?.aborted) return Promise.reject(signal.reason);
    if (this.closed) return Promise.reject(new Error("runtime transport closed"));
    if (!this.supports(method)) {
      return Promise.reject(new Error(`host service ${method} is not supported`));
    }
    if (method === "host.service.continue" && options?.cleanup) {
      return Promise.reject(new Error("Service continuation cannot run as cleanup"));
    }
    const id = `plugin-${++this.sequence}`;
    return new Promise<HostServiceContracts[M]["result"]>((resolve, reject) => {
      const cleanup = (): void => {
        this.pending.delete(id);
        signal?.removeEventListener("abort", abort);
      };
      const abort = (): void => {
        cleanup();
        reject(signal!.reason);
      };
      const fail = (error: unknown): void => {
        cleanup();
        reject(error);
      };
      this.pending.set(id, {
        resolve(value) { cleanup(); resolve(value as HostServiceContracts[M]["result"]); },
        reject: fail,
      });
      signal?.addEventListener("abort", abort, { once: true });
      const frame = method === "host.service.continue"
        ? { id, method, params: params as HostServiceContracts["host.service.continue"]["params"] }
        : method === "host.service.call"
        ? { id, method: "host.service.call", params: params as HostServiceContracts["host.service.call"]["params"] }
        : kernelServiceCall(id, method as KernelHostServiceMethod, params as HostServiceContracts[KernelHostServiceMethod]["params"]);
      if (options?.cleanup && frame.method !== "host.service.continue") {
        frame.params = { ...frame.params, execution_id: undefined, cleanup: true };
      }
      void this.send(frame).catch(fail);
    });
  }

  route(value: unknown): boolean {
    if (typeof value !== "object" || value === null) return false;
    const response = value as { id?: unknown; method?: unknown; result?: unknown; error?: unknown };
    if (typeof response.id !== "string" || response.method !== undefined
      || (!("result" in response) && !("error" in response))) return false;
    const pending = this.pending.get(response.id);
    // Cancelled calls may still receive a late response; never dispatch it as a request.
    if (!pending) return true;
    this.pending.delete(response.id);
    if (response.error !== undefined) {
      const error = response.error as { code?: unknown; message?: unknown };
      pending.reject(new RuntimeHostServiceError({
        code: typeof error.code === "string" ? error.code : "host_error",
        message: typeof error.message === "string" ? error.message : "host service failed",
      }));
    } else {
      pending.resolve(response.result);
    }
    return true;
  }

  close(): void {
    this.closed = true;
    for (const pending of this.pending.values()) {
      pending.reject(new Error("runtime transport closed"));
    }
    this.pending.clear();
  }
}

// Bound tool bodies without consuming the separate reentrant callback lane.
const MAX_CONCURRENT_TOOLS = 8;

/** Runs a plugin over Wuu's full-duplex JSON-lines transport. */
export async function runJSONLRuntime(
  plugin: RuntimePlugin,
  streams: { input: JSONLInput; output: JSONLOutput },
): Promise<void> {
  let writes = Promise.resolve();
  const send = (value: unknown): Promise<void> => {
    const next = writes.then(async () => { await streams.output.write(`${JSON.stringify(value)}\n`); });
    writes = next.catch(() => undefined);
    return next;
  };
  const host = new JSONLRuntimeHost(send);
  const lifetime = new AbortController();
  const executions = new Map<string, Set<AbortController>>();
  const callerScopes = new Map<string, AbortController>();
  lifetime.signal.addEventListener("abort", () => {
    for (const controller of callerScopes.values()) controller.abort();
    callerScopes.clear();
  }, { once: true });
  const active = new Set<Promise<void>>();
  const concurrentServices = new Set(plugin.concurrentServices ?? []);
  const concurrentCapabilities = new Set(["model.adapter", "model.middleware", ...plugin.concurrentCapabilities ?? []]);
  const concurrentTools = new Set<string>();
  const queued: { run(): Promise<void>; canOverlap(): boolean }[] = [];
  let running = 0;
  let exclusive = false;
  // Only adjacent explicitly safe tools share slots. Classification happens
  // after preceding work, including initialize, publishes its registrations.
  const pump = (): void => {
    while (queued.length > 0 && !exclusive && running < MAX_CONCURRENT_TOOLS) {
      const next = queued[0];
      const safe = next.canOverlap();
      if (!safe && running > 0) return;
      queued.shift();
      running++;
      exclusive = !safe;
      const finished = (): void => {
        running--;
        if (!safe) exclusive = false;
        pump();
      };
      void next.run().then(finished, finished);
    }
  };
  const schedule = (run: () => Promise<void>, canOverlap = () => false): Promise<void> => {
    const task = new Promise<void>((resolve, reject) => {
      queued.push({ run: () => Promise.resolve().then(run).then(resolve, reject), canOverlap });
    });
    pump();
    return task;
  };
  let lifecycle = Promise.resolve();
  let shuttingDown = false;
  const track = (task: Promise<void>): void => {
    active.add(task);
    void task.then(() => active.delete(task), () => active.delete(task));
  };
  const enqueueResponse = (value: unknown): void => {
    track(schedule(() => send(value)));
  };
  const processLine = (line: string): void => {
    if (line.trim() === "") return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(line);
    } catch (error) {
      enqueueResponse({ id: "invalid", error: { message: error instanceof Error ? error.message : String(error) } });
      return;
    }
    if (host.route(parsed)) return;
    if (isExecutionCancelRequest(parsed)) {
      for (const controller of executions.get(parsed.params.execution_id) ?? []) controller.abort();
      callerScopes.get(parsed.params.execution_id)?.abort();
      callerScopes.delete(parsed.params.execution_id);
      return;
    }
    if (!isRuntimeRequest(parsed)) {
      enqueueResponse({ id: "invalid", error: { message: "invalid runtime request" } });
      return;
    }
    if (shuttingDown) {
      track(send({ id: parsed.id, error: { message: "runtime is shutting down" } }));
      return;
    }
    const isLifecycle = parsed.method === "initialize" || parsed.method === "prepare" || parsed.method === "activate" || parsed.method === "shutdown";
    if (parsed.method === "shutdown") {
      shuttingDown = true;
      // Background work may hold an active handler; stop it before waiting for the barrier.
      lifetime.abort();
    }
    const concurrent = (parsed.method === "service.invoke" && concurrentServices.has(parsed.params.service))
      || (parsed.method === "capability.invoke" && concurrentCapabilities.has(parsed.params.capability));
    const executionId = runtimeRequestExecutionId(parsed);
    const controller = new AbortController();
    // Even legacy requests without an execution id must stop on transport loss.
    let controllers = executions.get(executionId);
    if (!controllers) executions.set(executionId, controllers = new Set());
    controllers.add(controller);
    let caller: RuntimeExecutionContext["caller"];
    if (parsed.method === "service.invoke" && parsed.params.caller_scope) {
      const scope = parsed.params.caller_scope;
      let owner = callerScopes.get(scope.id);
      if (!owner) callerScopes.set(scope.id, owner = new AbortController());
      caller = { ...scope, signal: owner.signal };
    }
    const execution = { executionId, signal: controller.signal, caller } satisfies RuntimeExecutionContext;
    // A lifecycle frame waits for everything already admitted. Opted-in
    // handlers may bypass ordinary work, but never an earlier lifecycle
    // frame. Capture the barrier now so later frames cannot create a cycle.
    const ready = isLifecycle ? Promise.all([...active]) : concurrent ? lifecycle : Promise.resolve();
    const run = async (): Promise<void> => {
      await ready;
      if (parsed.method === "initialize") {
        host.configure(parsed.params.supported_host_services);
        concurrentTools.clear();
      }
      const response = await handleRuntimeRequest(plugin, parsed, host, execution, { signal: lifetime.signal });
      if (parsed.method === "initialize" && "result" in response) {
        const definition = response.result as RuntimeInitializeResult;
        for (const tool of definition.tools ?? []) {
          if (tool.activity?.concurrency_safe === true) concurrentTools.add(tool.id);
        }
      }
      await send(response);
    };
    const task = (concurrent ? run() : schedule(run,
      () => parsed.method === "tool.execute" && concurrentTools.has(parsed.params.tool_id)))
      .finally(() => {
        const controllers = executions.get(executionId);
        controllers?.delete(controller);
        if (controllers?.size === 0) executions.delete(executionId);
        controller.abort();
      });
    if (isLifecycle) lifecycle = task.catch(() => undefined);
    track(task);
  };
  const decoder = new TextDecoder();
  let buffered = "";
  try {
    for await (const chunk of streams.input) {
      buffered += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
      const lines = buffered.split("\n");
      buffered = lines.pop() ?? "";
      for (const line of lines) processLine(line);
    }
    buffered += decoder.decode();
    if (buffered.trim() !== "") processLine(buffered);
  } finally {
    lifetime.abort();
    host.close();
    for (const controllers of executions.values()) {
      for (const controller of controllers) controller.abort();
    }
    await Promise.all(active);
    await writes;
  }
}

function isExecutionCancelRequest(value: unknown): value is RuntimeExecutionCancelRequest {
  if (typeof value !== "object" || value === null) return false;
  const request = value as { id?: unknown; method?: unknown; params?: { execution_id?: unknown } };
  return typeof request.id === "string"
    && request.method === "execution.cancel"
    && typeof request.params?.execution_id === "string"
    && request.params.execution_id.trim() !== "";
}

function runtimeRequestExecutionId(request: RuntimeRequest): string {
  switch (request.method) {
    case "capability.invoke":
    case "service.invoke":
    case "tool.execute":
      return request.params.execution_id?.trim() ?? "";
    default:
      return "";
  }
}

function isRuntimeRequest(value: unknown): value is RuntimeRequest {
  if (typeof value !== "object" || value === null) return false;
  const request = value as { id?: unknown; method?: unknown; params?: unknown };
  if (typeof request.id !== "string" || typeof request.method !== "string") return false;
  if (request.method === "prepare" || request.method === "activate" || request.method === "shutdown") return true;
  return ["initialize", "capability.invoke", "service.invoke", "service.changed", "tool.execute"].includes(request.method)
    && typeof request.params === "object" && request.params !== null;
}

export interface Disposable {
  dispose(): void;
}

export const PRESENTATION_TARGETS = [
  "conversation.item", "conversation.process",
  "header.conversation", "header.workspace",
  "app.status", "content.preview", "settings",
] as const;
export type BuiltInPresentationTarget = (typeof PRESENTATION_TARGETS)[number];
/** Built-in targets plus future dotted, namespaced targets. */
export type PresentationTarget = BuiltInPresentationTarget | (string & {});
export type PresentationMode = "replace" | "wrap";

/** Sanitized attachment metadata. The opaque id is resolved only through host actions. */
export interface AttachmentDescriptorV1 {
  readonly id: string;
  readonly name: string;
  readonly mimeType?: string;
  readonly sizeBytes?: number;
  readonly width?: number;
  readonly height?: number;
}

export interface ConversationContentV1 {
  readonly type: "plain-text" | "markdown" | "code";
  readonly text: string;
  readonly language?: string;
}

export interface ConversationToolReferenceV1 {
  readonly id: string;
  readonly name?: string;
  readonly capability?: string;
  readonly status?: "pending" | ToolActivityStatus | "cancelled";
}

export interface ConversationProcessReferenceV1 {
  readonly id: string;
  readonly label?: string;
  readonly phase?: string;
  readonly status?: "pending" | "running" | "completed" | "failed" | "cancelled";
}

/** Public conversation item data, independent of renderer and thread storage types. */
export interface ConversationItemSnapshotV1 {
  readonly contractVersion: 1;
  readonly id: string;
  readonly kind: "user-message" | "assistant-message" | "reasoning" | "notice" | "attachment" | "tool-reference" | "process-reference";
  readonly status?: "pending" | "streaming" | "completed" | "failed" | "cancelled";
  readonly phase?: string;
  readonly text?: string;
  readonly contentType?: "plain-text" | "markdown" | "code";
  readonly content?: readonly ConversationContentV1[];
  readonly attachments?: readonly AttachmentDescriptorV1[];
  readonly toolReferences?: readonly ConversationToolReferenceV1[];
  readonly processReferences?: readonly ConversationProcessReferenceV1[];
  readonly createdAt?: string;
  /** Replacement-context body of a completed context-compaction notice. */
  readonly summary?: string;
  /** Raw user input when it differs from the displayed bubble text. */
  readonly inputText?: string;
}

export type ConversationProcessKindV1 = "reasoning" | "tool-group" | "mixed";
export type ConversationProcessStatusV1 = "running" | "completed" | "failed";

/** One ordered, sanitized entry in the process surface. */
export interface ConversationProcessItemV1 {
  readonly id: string;
  readonly kind: "reasoning" | "tool-activity";
  readonly status: ConversationProcessStatusV1;
  /** Reasoning text only. Tool arguments and results are intentionally excluded. */
  readonly text?: string;
  readonly toolName?: string;
  readonly displayLabel?: string;
  readonly capability?: string;
  readonly toolKind?: string;
  readonly error?: string;
}

/** Public data for the complete reasoning/tool process region of one turn. */
export interface ConversationProcessSnapshotV1 {
  readonly contractVersion: 1;
  readonly kind: ConversationProcessKindV1;
  readonly status: ConversationProcessStatusV1;
  readonly streaming: boolean;
  readonly active: boolean;
  /** Items preserve host display order and never contain private thread records. */
  readonly items: readonly ConversationProcessItemV1[];
}

export interface ComposerQueueSummaryV1 {
  readonly id: string;
  readonly text?: string;
  readonly attachmentCount?: number;
  readonly status?: "queued" | "pending";
}

export interface ModelSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly providerId?: string;
  readonly contextWindowTokens?: number;
}

export interface RuntimeSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly status?: "available" | "unavailable" | "starting" | "running" | "error";
}

export interface PermissionSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
}

export interface ContextUsageSummaryV1 {
  readonly usedTokens?: number;
  readonly limitTokens?: number;
  readonly percent?: number;
}

export type ComposerSubmissionModeV1 = "send" | "queue" | "steer";

/** One immutable request for an append-only suggestion in the host-owned composer. */
export interface ComposerInlineCompletionContext {
  readonly contractVersion: 1;
  /** Conversation kind, including before a side conversation has an ID. Split panes use main. */
  readonly kind: "main" | "side";
  /** Opaque identity for this mounted input and conversation/workspace context. */
  readonly composerId: string;
  /** Changes whenever input, selection, composition, focus, or conversation state changes. */
  readonly revision: number;
  readonly threadId?: string;
  /** Side-conversation identity; threadId remains its owning main conversation. */
  readonly sideThreadId?: string;
  /** Project ID is absent outside a project; the event-routing root is available for no-project conversations. */
  readonly workspaceId?: string;
  readonly workspaceRoot?: string;
  /** Counts only; providers never receive attachment bytes or queued draft content. */
  readonly attachmentCount?: number;
  readonly queuedCount?: number;
  readonly pendingCount?: number;
  readonly draftText: string;
  /** UTF-16 offsets into draftText. The host requests only at a collapsed end caret. */
  readonly selectionStart: number;
  readonly selectionEnd: number;
  /** Composer presentation activity; false does not prove the latest turn succeeded or settled. */
  readonly running: boolean;
}

export interface ComposerInlineCompletion {
  /** Optional provider-owned identity, preserved in interaction callbacks. */
  readonly id?: string;
  /** Literal text to append, never markup or a replacement of the current draft. */
  readonly text: string;
}

/** Refresh only the exact input observation that produced a cached result. */
export interface ComposerInlineCompletionRefresh {
  readonly composerId: string;
  readonly revision: number;
}

export interface ComposerInlineCompletionProviderHandle {
  dispose(): void;
  /**
   * Recheck eligible inputs after external data changes; omit scope to recheck all.
   * Coalesced at most once per 100 ms. A stale scope or disposed handle is ignored.
   * Refresh never revives an explicitly dismissed observation.
   */
  refresh(scope?: ComposerInlineCompletionRefresh): void;
}

/** Attribution only: interaction callbacks never receive a later draft's text. */
export type ComposerInlineCompletionSource = Pick<ComposerInlineCompletionContext,
  "kind" | "composerId" | "revision" | "threadId" | "sideThreadId">;

export type ComposerInlineCompletionInteraction = {
  /** Host identity for this presentation, shared by its subsequent interactions. */
  readonly interactionId: string;
  /** The provider's result id, when supplied. */
  readonly suggestionId?: string;
  readonly source: ComposerInlineCompletionSource;
} & (
  | { readonly type: "shown" | "accepted" | "dismissed" }
  | {
    /** The composer callback accepted dispatch, not confirmation of backend delivery. */
    readonly type: "dispatched";
    readonly mode: ComposerSubmissionModeV1;
    /** Draft text differs from the draft immediately after this acceptance. */
    readonly edited: boolean;
  }
);

export interface ComposerInlineCompletionProviderRegistration {
  readonly id: string;
  /** Lower orders run first; ties use plugin ID, then registration ID. */
  readonly order?: number;
  /**
   * Return null to let the next provider try. The host owns rendering and Tab
   * acceptance; providers own prompts, debouncing, and any context retrieval.
   * Stop work when signal aborts. Late results never change the current input.
   */
  provideCompletion(
    context: ComposerInlineCompletionContext,
    signal: AbortSignal,
  ): ComposerInlineCompletion | null | Promise<ComposerInlineCompletion | null>;
  /** Receives only this registration's interactions while its generation is active. */
  onDidInteract?(event: ComposerInlineCompletionInteraction): void | Promise<void>;
}

export interface ComposerSnapshotV1 {
  readonly contractVersion: 1;
  readonly draftText?: string;
  readonly attachments?: readonly AttachmentDescriptorV1[];
  readonly queued?: readonly ComposerQueueSummaryV1[];
  readonly pending?: readonly ComposerQueueSummaryV1[];
  readonly running?: boolean;
  readonly readOnly?: boolean;
  readonly threadId?: string;
  readonly variant?: string;
  readonly availableSubmissionModes?: readonly ComposerSubmissionModeV1[];
  readonly activeSubmissionMode?: ComposerSubmissionModeV1;
  readonly model?: ModelSummaryV1;
  readonly runtime?: RuntimeSummaryV1;
  readonly permission?: PermissionSummaryV1;
  readonly contextUsage?: ContextUsageSummaryV1;
  readonly disabledReason?: string;
}

export interface HeaderTabDescriptorV1 {
  readonly id: string;
  readonly title: string;
  readonly subtitle?: string;
  readonly kind?: string;
  readonly busy?: boolean;
  readonly dirty?: boolean;
  readonly disabled?: boolean;
  /** Whether this tab can be closed; omitted by older hosts. */
  readonly closable?: boolean;
}

export interface HeaderSnapshotV1 {
  readonly contractVersion: 1;
  readonly scope: "conversation" | "workspace";
  readonly title?: string;
  readonly subtitle?: string;
  readonly tabs?: readonly HeaderTabDescriptorV1[];
  readonly activeTabId?: string;
  readonly busy?: boolean;
  readonly dirty?: boolean;
  readonly canNavigateBack?: boolean;
  readonly canNavigateForward?: boolean;
}

export interface NavigationNodeV1 {
  readonly id: string;
  readonly kind: "section" | "project" | "thread" | "command";
  readonly label: string;
  readonly parentId?: string;
  readonly depth?: number;
  readonly description?: string;
  readonly icon?: string;
  readonly active?: boolean;
  readonly pinned?: boolean;
  readonly unread?: boolean;
  readonly running?: boolean;
  readonly disabled?: boolean;
}

export interface NavigationSnapshotV1 {
  readonly contractVersion: 1;
  /** Nodes are in host display order; parentId and depth describe hierarchy. */
  readonly nodes: readonly NavigationNodeV1[];
  readonly activeNodeId?: string;
}

export interface StatusItemV1 {
  readonly id: string;
  readonly label: string;
  readonly kind?: "info" | "progress" | "success" | "warning" | "error";
  readonly detail?: string;
  readonly icon?: string;
  readonly progress?: number;
  readonly busy?: boolean;
  readonly disabled?: boolean;
  readonly actionId?: string;
}

export interface StatusSnapshotV1 {
  readonly contractVersion: 1;
  readonly items: readonly StatusItemV1[];
}

export interface FileSelectionDescriptorV1 {
  readonly startOffset?: number;
  readonly endOffset?: number;
  readonly startLine?: number;
  readonly startColumn?: number;
  readonly endLine?: number;
  readonly endColumn?: number;
}

/** File data safe for presentation; it never contains filesystem or window handles. */
export interface FilePreviewSnapshotV1 {
  readonly contractVersion: 1;
  readonly resourceId: string;
  readonly workspaceRelativePath: string;
  readonly contentType?: string;
  readonly text?: string;
  readonly safeHostUrl?: string;
  readonly sizeBytes?: number;
  readonly binary?: boolean;
  /** The exposed text was shortened to the host preview limit, independently of safeHostUrl. */
  readonly truncated?: boolean;
  readonly readOnly?: boolean;
  readonly dirty?: boolean;
  readonly loading?: boolean;
  readonly error?: string;
  readonly selection?: FileSelectionDescriptorV1;
}

export interface SettingsPageSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
  readonly disabled?: boolean;
}

export interface SettingsPluginSummaryV1 {
  readonly id: string;
  readonly name: string;
  readonly version?: string;
  readonly enabled?: boolean;
  readonly status?: string;
}

export interface SettingsRuntimeSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly version?: string;
  readonly status?: string;
}

export interface SettingsProviderSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly configured?: boolean;
  readonly status?: string;
}

/** Sanitized settings navigation and service summaries; provider credentials are excluded. */
export interface SettingsSnapshotV1 {
  readonly contractVersion: 1;
  readonly activePageId?: string;
  readonly availablePages?: readonly SettingsPageSummaryV1[];
  readonly plugins?: readonly SettingsPluginSummaryV1[];
  readonly runtimes?: readonly SettingsRuntimeSummaryV1[];
  readonly providers?: readonly SettingsProviderSummaryV1[];
  readonly busy?: boolean;
  readonly error?: string;
}

export const CONVERSATION_ITEM_ACTIONS = {
  copy: "conversation.item.copy",
  edit: "conversation.item.edit",
  retry: "conversation.item.retry",
  openAttachment: "conversation.item.open-attachment",
  openTool: "conversation.item.open-tool",
  cancelProcess: "conversation.item.cancel-process",
} as const;
export type ConversationItemActionId = (typeof CONVERSATION_ITEM_ACTIONS)[keyof typeof CONVERSATION_ITEM_ACTIONS];

export const HEADER_ACTIONS = {
  selectTab: "header.select-tab",
  closeTab: "header.close-tab",
  navigateBack: "header.navigate-back",
  navigateForward: "header.navigate-forward",
} as const;
export type HeaderActionId = (typeof HEADER_ACTIONS)[keyof typeof HEADER_ACTIONS];

export const STATUS_ACTIONS = { activateItem: "status.activate-item" } as const;
export type StatusActionId = (typeof STATUS_ACTIONS)[keyof typeof STATUS_ACTIONS];

export const FILE_PREVIEW_ACTIONS = {
  open: "file-preview.open",
  reveal: "file-preview.reveal",
  select: "file-preview.select",
  save: "file-preview.save",
  reload: "file-preview.reload",
} as const;
export type FilePreviewActionId = (typeof FILE_PREVIEW_ACTIONS)[keyof typeof FILE_PREVIEW_ACTIONS];

export const SETTINGS_ACTIONS = {
  openPage: "settings.open-page",
  updateValue: "settings.update-value",
  refresh: "settings.refresh",
} as const;
export type SettingsActionId = (typeof SETTINGS_ACTIONS)[keyof typeof SETTINGS_ACTIONS];

export interface PresentationHost extends ViewHostAPI {
  readonly actions: readonly string[];
  invoke(action: string, input?: unknown): Promise<unknown>;
}

export interface PresenterProps {
  readonly contractVersion: 1;
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly snapshot: unknown;
  readonly host: PresentationHost;
  /** Original host rendering, unaffected by other presenters. */
  readonly original: React.ReactNode;
  /** Lazily continue with lower-priority presenters, optionally with a new snapshot. */
  readonly next: (input?: { readonly snapshot: unknown }) => React.ReactNode;
  /** Equivalent to next() with the current snapshot. */
  readonly fallback: React.ReactNode;
}

export interface PresenterDefinition {
  readonly id: string;
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly mode?: PresentationMode;
  readonly priority?: number;
  readonly render: (props: PresenterProps) => React.ReactNode;
}

export type ComposerStatusState = "running" | "queued" | "waiting" | "error" | "idle";

export interface ComposerStatusItem {
  readonly id: string;
  readonly label: string;
  readonly state?: ComposerStatusState;
  readonly secondaryText?: string;
  readonly tooltip?: string;
  readonly updatedAt?: string;
  readonly action?: Readonly<{ kind: "open-session"; sessionId: string }>;
}

export interface ComposerStatusContext {
  readonly threadId?: string;
  readonly mainConversation: true;
}

export interface ComposerStatusSourceRegistration {
  readonly id: string;
  readonly order?: number;
  getSnapshot(context: ComposerStatusContext): readonly ComposerStatusItem[];
  subscribe(context: ComposerStatusContext, listener: () => void): () => void;
}

export interface CommandRegistration extends PluginCommandPresentation {
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
  readonly id?: string;
  readonly threadId?: string;
  readonly title: string;
  readonly state?: unknown;
  readonly render: HostUIComponent<ConversationCardRenderProps>;
}

export interface ConversationCardHandle extends Disposable {
  readonly id: string;
  update(state: unknown): void;
  dismiss(): void;
}

export interface StyleRegistration {
  id: string;
  css: string;
  order?: number;
}

export interface LocaleRegistration {
  id: string;
  locale: string;
  entries: Readonly<Record<string, string>>;
  order?: number;
}

// ---------------------------------------------------------------------------
// Plugin manifest helpers
// ---------------------------------------------------------------------------

export function validateManifest(manifest: Record<string, unknown>): string[] {
  const errors: string[] = [];
  if (!manifest.id || typeof manifest.id !== "string") {
    errors.push("manifest.id is required (string)");
  }
  if (manifest.schema_version !== 1) {
    errors.push("manifest.schema_version must be 1");
  }
  if (!manifest.version || typeof manifest.version !== "string") {
    errors.push("manifest.version is required (string)");
  }
  return errors;
}

/** A reciprocal, host-executed one-time private storage ownership transfer. */
export interface StorageTransferDeclaration {
  id: string;
  plugin: string;
  scope: "user";
  domain: string;
  keyPrefix: string;
}

export interface StorageTransfers {
  exports?: StorageTransferDeclaration[];
  imports?: StorageTransferDeclaration[];
}

export function createManifest(options: {
  id: string;
  name?: string;
  version?: string;
  description?: string;
  icon?: PluginManifestIcon;
  requires?: string[];
  dependencies?: PluginDependency[];
  storageTransfers?: StorageTransfers;
}): Record<string, unknown> {
  return {
    schema_version: 1,
    id: options.id,
    name: options.name ?? options.id,
    version: options.version ?? "0.1.0",
    description: options.description ?? `A Wuu plugin: ${options.id}`,
    ...(options.icon ? { icon: options.icon } : {}),
    ...(options.storageTransfers ? { storageTransfers: options.storageTransfers } : {}),
    ...(options.requires ? { requires: options.requires } : {}),
    ...(options.dependencies ? { dependencies: options.dependencies } : {}),
  };
}

export * from "./bundle-contract.js";

export * from "./model.js";

export * from "./client.js";
export * from "./providers.js";
export * from "./coordination.js";

export * from "./tool-service.js";
export * from "./tool-batch.js";

export * from "./session-service.js";

export * from "./ui-composer.js";
export * from "./prompt-service.js";
export * from "./compaction-service.js";
export * from "./application-settings.js";
export * from "./ui-preview.js";

export * from "./title-service.js";
export * from "./ui-settings-controls.js";
export * from "./ui-navigation.js";
export * from "./ui-conversation-actions.js";
export * from "./ui-preview-content.js";
export * from "./ui-catalog.js";
export * from "./ui-conversation.js";
export * from "./ui-composer-runtime.js";
export * from "./ui-environment.js";
export * from "./ui-submission.js";
export * from "./ui-search.js";
export * from "./ui-terminals.js";
export * from "./ui-browser.js";
export * from "./ui-desktop.js";
export * from "./ui-projects.js";
export * from "./ui-file-selection.js";
export * from "./ui-side-conversation.js";
export * from "./ui-onboarding.js";

export * from "./ui-web-workspace.js";

export * from "./ui-artifacts.js";

export * from "./ui-composer-presentation.js";

export * from "./experiment.js";

export * from "./agent-policy.js";

export { scopeApplicationWorkbench } from "./ui-contributions.js";

export * from "./model-capacity.js";
export * from "./session-transport.js";
export * from "./desktop-session-transport.js";
export * from "./session-sharing.js";
export * from "./identity.js";
export * from "./resource-policy.js";

export * from "./execution-authorization.js";

export * from "./personal-memory.js";
