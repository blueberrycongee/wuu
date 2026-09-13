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
export type ViewPersistence = "session" | "durable";

/** Artwork declared in plugin.json for the package or a host-owned entry. */
export type PluginManifestIcon =
  | PublicIconName
  | Readonly<{ path: string }>
  | Readonly<{ light: string; dark: string }>;

export interface ViewTypeDefinition {
  id: ViewTypeId;
  title: string;
  icon?: PublicIconName;
  defaultRegion?: ViewPlacementRegion;
  persistence?: ViewPersistence;
  render: unknown; // React.ComponentType<ViewRenderProps> — opaque in SDK
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

export interface SettingsValueMapV1 {
  readonly "runtime.modelAliases": Readonly<Record<string, SettingsModelAliasV1>>;
}

export type SettingsValueKeyV1 = keyof SettingsValueMapV1;

/** Narrow settings service exposed only to plugin views mounted as Settings pages. */
export interface SettingsPageHostAPI {
  readonly contractVersion: 1;
  getValue<Key extends SettingsValueKeyV1>(key: Key): SettingsValueMapV1[Key];
  updateValue<Key extends SettingsValueKeyV1>(key: Key, value: SettingsValueMapV1[Key]): Promise<void>;
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
  readonly children?: unknown;
  readonly className?: string;
  readonly [property: string]: unknown;
}

export interface PluginUIPageProps extends PluginUIContainerProps {
  readonly density?: "comfortable" | "compact";
}

export interface PluginUISectionProps extends PluginUIContainerProps {
  readonly title?: unknown;
  readonly description?: unknown;
}

export interface PluginUIStackProps extends PluginUIContainerProps {
  readonly gap?: "small" | "medium" | "large";
}

export interface PluginUIButtonProps extends PluginUIContainerProps {
  readonly variant?: "primary" | "secondary" | "ghost" | "danger";
  readonly type?: "button" | "submit" | "reset";
  readonly disabled?: boolean;
  readonly onClick?: (event: unknown) => void;
}

export interface PluginUIToolbarToggleProps extends PluginUIContainerProps {
  readonly pressed: boolean;
  readonly type?: "button" | "submit" | "reset";
  readonly disabled?: boolean;
  readonly title?: string;
  readonly "aria-label": string;
  readonly onClick?: (event: unknown) => void;
}

export interface PluginUITextInputProps extends PluginUIContainerProps {
  readonly label: unknown;
  readonly description?: unknown;
  readonly value?: string | number;
  readonly defaultValue?: string | number;
  readonly placeholder?: string;
  readonly disabled?: boolean;
  readonly onChange?: (event: unknown) => void;
}

export interface PluginUITextAreaProps extends PluginUIContainerProps {
  readonly label: unknown;
  readonly description?: unknown;
  readonly value?: string;
  readonly placeholder?: string;
  readonly disabled?: boolean;
  readonly onChange?: (event: unknown) => void;
}

export interface PluginUICheckboxProps extends PluginUIContainerProps {
  readonly label: unknown;
  readonly description?: unknown;
  readonly checked?: boolean;
  readonly disabled?: boolean;
  readonly onChange?: (event: unknown) => void;
}

export interface PluginUISelectProps extends PluginUIContainerProps {
  readonly label: unknown;
  readonly description?: unknown;
  readonly value?: string | number;
  readonly defaultValue?: string | number;
  readonly disabled?: boolean;
  readonly onChange?: (event: unknown) => void;
}

export interface PluginUIEmptyStateProps extends PluginUIContainerProps {
  readonly title: unknown;
  readonly description?: unknown;
  readonly actions?: unknown;
}


export interface PluginUILoadingStateProps extends PluginUIContainerProps {
  readonly title?: unknown;
  readonly description?: unknown;
}

export interface PluginUIErrorStateProps extends PluginUIContainerProps {
  readonly title: unknown;
  readonly description?: unknown;
  readonly actions?: unknown;
}

export interface PluginUILiveDurationProps extends PluginUIContainerProps {
  readonly elapsedMs: number;
  readonly runningSinceMs?: number;
  readonly active?: boolean;
}

export type HostUIComponent<Props> = (props: Props) => unknown;

/** Stable host-owned primitives for plugin views. */
export interface PluginUIKit {
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

export interface InspectorSectionHostAPI {
  executeCommand(commandId: string, input?: unknown): Promise<unknown>;
  openView(viewTypeId: ViewTypeId, options?: OpenViewOptions): Promise<void>;
}

export interface InspectorSectionRenderProps {
  readonly snapshot: InspectorSnapshotV1;
  readonly host: InspectorSectionHostAPI;
}

export interface InspectorSectionDefinition {
  readonly id: string;
  readonly title: string;
  readonly priority?: number;
  readonly render: HostUIComponent<InspectorSectionRenderProps>;
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

export interface ToolActivityPresenterProps {
  readonly activity: ToolActivitySnapshot;
  readonly host: ViewHostAPI;
  readonly fallback: unknown;
}

export interface ToolActivityPresenterDefinition {
  readonly id: string;
  /** Exact stable dispatch identity, such as a tool display capability. */
  readonly key: string;
  readonly render: (props: ToolActivityPresenterProps) => unknown;
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
  render: unknown; // React.ComponentType<RendererProps> — opaque in SDK
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
  type: "boolean" | "string" | "number" | "enum";
  title: string;
  description?: string;
  default: unknown;
  enum?: string[];
  scope: "user" | "workspace";
  apply: "live" | "restart";
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

export interface PluginGenerationApi {
  /** Host-owned React runtime. Executable desktop bundles must use this instance. */
  readonly react: HostReact;
  /** Host-owned primitives that inherit the active Wuu theme and spacing rhythm. */
  readonly ui: PluginUIKit;
  readonly pluginId: string;
  readonly generation: string;
  /** Invoke one method owned by this plugin's active runtime generation. */
  invokeRuntime(method: string, input?: unknown, options?: PluginRuntimeInvokeOptions): Promise<unknown>;
  /** List registered project workspaces that a trusted extension may target. */
  listWorkspaces(): Promise<PluginWorkspaceSnapshot>;
  /** Observe host lifecycle events; disposal is bound to this generation. */
  onHostEvent(handler: (event: unknown) => void): Disposable;
  registerSlot(slotId: string, contribution: SlotRegistration): Disposable;
  registerSurface(surfaceId: string, contribution: SurfaceRegistration): Disposable;
  registerCommand(command: CommandRegistration): Disposable;
  /** Show a non-persistent card at the bottom of a conversation. */
  showConversationCard(card: ConversationCardRegistration): ConversationCardHandle;
  registerStyle(style: StyleRegistration): Disposable;
  registerLocale(locale: LocaleRegistration): Disposable;
  registerComposerStatusSource(source: ComposerStatusSourceRegistration): Disposable;
  registerCleanup(cleanup: () => void): Disposable;
  registerViewType(definition: ViewTypeDefinition): Disposable;
  registerViewPlacement(contribution: ViewPlacementContribution): Disposable;
  registerInspectorSection(definition: InspectorSectionDefinition): Disposable;
  registerRenderer(definition: RendererDefinition): Disposable;
  registerThemeTokens(tokens: ThemeTokens): Disposable;
  registerCSSSnippet(snippet: CSSSnippet): Disposable;
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

/** Minimal type for the host-owned React instance; plugins never bundle React. */
export interface HostReact {
  readonly Fragment: unknown;
  useState<T>(initial: T | (() => T)): [T, (next: T | ((current: T) => T)) => void];
  useEffect(effect: () => void | (() => void), dependencies?: readonly unknown[]): void;
  useMemo<T>(factory: () => T, dependencies: readonly unknown[]): T;
  useCallback<T>(callback: T, dependencies: readonly unknown[]): T;
  useRef<T>(initial: T): { current: T };
  useSyncExternalStore<T>(
    subscribe: (notify: () => void) => () => void,
    getSnapshot: () => T,
    getServerSnapshot?: () => T,
  ): T;
  createElement(
    type: string | ((props: Readonly<Record<string, unknown>>) => unknown),
    props: Readonly<Record<string, unknown>> | null,
    ...children: unknown[]
  ): unknown;
}

// ---------------------------------------------------------------------------
// Executable runtime contract
// ---------------------------------------------------------------------------

export const RUNTIME_PROTOCOL_V1 = 1 as const;
export const CAPABILITY_PROTOCOL_V2 = 2 as const;
export const CAPABILITY_PROTOCOL_V3 = 3 as const;
export const REQUEST_TRANSFORM_CAPABILITY = "agent.request.transform" as const;
export const AGENT_PRE_STEP_CAPABILITY = "agent.pre_step" as const;
export const SYSTEM_PROMPT_SECTION_CAPABILITY = "agent.system_prompt.section" as const;
/** @experimental No distributed first-party consumer has proven this contract. */
export const COMPACTION_CAPABILITY = "agent.compaction" as const;
export const PLUGIN_CLIENT_REQUEST_CAPABILITY = "plugin.client.request" as const;

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
  "host.session.create",
  "host.session.send",
  "host.session.list",
  "host.session.cancel",
  "host.session.inspect",
  "host.session.history.read",
  "host.session.history.search",
  "host.workspace.status",
  "host.workspace.apply",
  "host.workspace.discard",
  "host.service.call",
] as const;

export type HostServiceMethod = (typeof HOST_SERVICE_METHODS)[number];

export const KERNEL_SERVICE_METHOD = "call" as const;
export const EXECUTION_UPDATE_SERVICE = "execution.update" as const;
export const INVOKE_TOOL_SERVICE = "execution.invoke-tool" as const;
export const USER_QUESTION_ASK_SERVICE = "host.user-question.ask" as const;
export const ARTIFACT_IMPORT_SERVICE = "host.artifact.import" as const;
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
  "host.session.create": "host.session.create",
  "host.session.send": "host.session.send",
  "host.session.list": "host.session.list",
  "host.session.cancel": "host.session.cancel",
  "host.session.inspect": "host.session.inspect",
  "host.session.history.read": "host.session.history.read",
  "host.session.history.search": "host.session.history.search",
  "host.workspace.status": "host.workspace.status",
  "host.workspace.apply": "host.workspace.apply",
  "host.workspace.discard": "host.workspace.discard",
} as const satisfies Record<Exclude<HostServiceMethod, "host.service.call">, string>;

export type KernelHostServiceMethod = keyof typeof KERNEL_SERVICE_NAMES;

/** Preserve the former host-call input while emitting the Service Registry gateway frame. */
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

export interface HostServiceDescriptor {
  id: HostServiceMethod | (string & {});
  required?: boolean;
}

/** One typed method of a provided service. */
export interface ServiceMethodDescriptor {
  name: string;
  input_schema: string;
  output_schema: string;
}

/** A versioned service a plugin provides; consumers resolve by name and major version. */
export interface ServiceDescriptor {
  name: string;
  version: string;
  methods: ServiceMethodDescriptor[];
}

export const AUTHORIZATION_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: SECURITY_AUTHORIZE_SERVICE,
  version: "1.0.0",
  methods: [{ name: SECURITY_AUTHORIZE_METHOD, input_schema: "security.authorize.input.v1", output_schema: "security.authorize.output.v1" }],
};

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

/** One validated, host-routed call delivered to a service provider. */
export interface ServiceInvokeParams<TParams = unknown> {
  service: string;
  method: string;
  caller: string;
  execution_id?: string;
  params?: TParams;
}

/** Notification that a consumed service resolution changed. */
export interface ServiceChangedParams {
  service: string;
  reason?: string;
}

export interface RuntimeInitializeParams {
  protocol_version: number;
  plugin_id: string;
  plugin_root: string;
  project_root: string;
  wuu_home: string;
  capability_protocol_version?: number;
  supported_host_services?: HostServiceMethod[];
  lifecycle_version?: 1;
}

export interface RuntimeInitializeResult {
  tools?: ToolRegistration[];
  protocol_version?: 1 | 2 | 3;
  capabilities?: CapabilityDescriptor[];
  required_host_services?: HostServiceDescriptor[];
  provided_services?: ServiceDescriptor[];
  required_services?: ServiceRequirement[];
  lifecycle_version?: 1;
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

export interface ProcessSandboxRequest {
  argv: string[];
  policy: ProcessSandboxPolicy;
}

export interface ProcessSandboxPolicy {
  mode: "read-only" | "workspace-write";
  writable_roots?: string[];
}

export interface ProcessSandboxResult {
  argv: string[];
  enforcement: "full" | "partial";
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
  id: string;
  description: string;
  input_schema: JSONSchemaObject;
  execution_scopes?: Array<"root" | "child" | "collaboration">;
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
  session_id?: string;
  thread_id?: string;
  cwd?: string;
  provider?: string;
  model?: string;
  step_index: number;
  messages: readonly ModelMessageViewV1[];
}

export interface AgentPreStepMessage {
  id: string;
  content: string;
}

export interface AgentPreStepOutput {
  append_messages?: readonly AgentPreStepMessage[];
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

export interface HostServiceContracts {
  "host.storage.get": { params: { scope: "user" | "workspace"; key: string }; result: { value: string | null } };
  "host.storage.set": { params: { scope: "user" | "workspace"; key: string; value: string }; result: Record<string, never> };
  "host.storage.delete": { params: { scope: "user" | "workspace"; key: string }; result: Record<string, never> };
  "host.storage.keys": { params: { scope: "user" | "workspace" }; result: { keys: string[] } };
  "host.storage.compare_exchange": {
    params: { scope: "user" | "workspace"; key: string; expected: string | null; value: string | null };
    result: { swapped: boolean; value: string | null };
  };
  "host.settings.get": { params: { key: string }; result: { value: unknown } };
  "host.settings.list": { params: Record<string, never>; result: { entries: Record<string, unknown> } };
  "host.session.create": {
    params: {
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
      launch?: { revision?: number; kind?: string; intent?: string };
    };
    result: { session_id: string; created: boolean; workspace_root?: string };
  };
  "host.session.send": {
    params: {
      request_id: string;
      session_id: string;
      input: {
        prompt: string;
        context_blocks?: Array<{ kind?: string; title?: string; source?: string; content: string }>;
      };
      presentation?: { kind: "query_bubble"; text: string; name?: string; related_session_id?: string };
      cause?: string;
      if_running?: "queue" | "steer";
      reply_to_turn_id?: string;
    };
    result: { state: string; session_id: string; turn_id?: string; queue_id?: string; steered?: boolean };
  };
  "host.session.list": {
    params: { parent_session_id?: string; scope?: "owned" | "shared" };
    result: {
      sessions: Array<{
        session_id: string;
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
    params: { session_id: string; turn_id?: string; queue_id?: string };
    result: { session_id: string; turn_id?: string; queue_id?: string; cancelled: boolean };
  };
  "host.session.inspect": {
    params: {
      session_id: string;
      turn_id?: string;
      request_id?: string;
      wait?: "none" | "terminal";
      timeout_ms?: number;
    };
    result: {
      session: {
        session_id: string;
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
    };
  };
  "host.session.history.read": {
    params: {
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
        seq: number;
        role: string;
        name?: string;
        content?: string;
        display_content?: string;
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
    params: {
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
      matches?: Array<{ seq: number; role: string; name?: string; excerpt?: string }>;
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
    params: { service: string; method: string; execution_id?: string; params?: unknown };
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

export interface RuntimeHost {
  supports(method: HostServiceMethod): boolean;
  call<M extends HostServiceMethod>(
    method: M,
    params: HostServiceContracts[M]["params"],
  ): Promise<HostServiceContracts[M]["result"]>;
}

export interface RuntimeExecutionContext {
  readonly executionId: string;
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

export interface RuntimePlugin {
  initialize(params: RuntimeInitializeParams, host: RuntimeHost): RuntimeInitializeResult | Promise<RuntimeInitializeResult>;
  activate?(host: RuntimeHost): void | Promise<void>;
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
  | RuntimeActivateRequest
  | RuntimeCapabilityRequest
  | RuntimeServiceInvokeRequest
  | RuntimeServiceChangedRequest
  | RuntimeToolRequest
  | RuntimeShutdownRequest;

export type RuntimeResponse =
  | { id: string; result: unknown }
  | { id: string; error: { message: string } };

export async function handleRuntimeRequest(
  plugin: RuntimePlugin,
  request: RuntimeRequest,
  host: RuntimeHost = unavailableRuntimeHost,
  execution: RuntimeExecutionContext = idleRuntimeExecution,
): Promise<RuntimeResponse> {
  const executionHost = scopedRuntimeHost(host, execution.executionId);
  try {
    switch (request.method) {
      case "initialize":
        return { id: request.id, result: { ...await plugin.initialize(request.params, host), lifecycle_version: 1 } };
      case "activate":
        await plugin.activate?.(host);
        return { id: request.id, result: null };
      case "capability.invoke":
        if (!plugin.invokeCapability) throw new Error("capability.invoke is not implemented");
        return { id: request.id, result: await plugin.invokeCapability(request.params, executionHost, execution) };
      case "service.invoke":
        if (!plugin.invokeService) throw new Error("service.invoke is not implemented");
        return { id: request.id, result: await plugin.invokeService(request.params, executionHost, execution) };
      case "service.changed":
        await plugin.serviceChanged?.(request.params, host);
        return { id: request.id, result: null };
      case "tool.execute":
        if (!plugin.executeTool) throw new Error("tool.execute is not implemented");
        return { id: request.id, result: await plugin.executeTool(request.params, executionHost, execution) };
      case "shutdown":
        await plugin.shutdown?.();
        return { id: request.id, result: null };
    }
  } catch (error) {
    return { id: request.id, error: { message: error instanceof Error ? error.message : String(error) } };
  }
}

function scopedRuntimeHost(host: RuntimeHost, executionId: string): RuntimeHost {
  if (executionId === "") return host;
  return {
    supports: (method) => host.supports(method),
    call: (method, params) => host.call(method, method === "host.service.call"
      ? { ...params, execution_id: executionId }
      : params),
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
  reject(error: Error): void;
};

class JSONLRuntimeHost implements RuntimeHost {
  private sequence = 0;
  private supported = new Set<HostServiceMethod>();
  private readonly pending = new Map<string, PendingHostCall>();

  constructor(private readonly send: (value: unknown) => Promise<void>) {}

  configure(methods: readonly HostServiceMethod[] | undefined): void {
    this.supported = new Set(methods ?? []);
  }

  supports(method: HostServiceMethod): boolean {
    return this.supported.has(method);
  }

  call<M extends HostServiceMethod>(
    method: M,
    params: HostServiceContracts[M]["params"],
  ): Promise<HostServiceContracts[M]["result"]> {
    if (!this.supports(method)) {
      return Promise.reject(new Error(`host service ${method} is not supported`));
    }
    const id = `plugin-${++this.sequence}`;
    return new Promise<HostServiceContracts[M]["result"]>((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      void this.send({ id, method, params }).catch((error: unknown) => {
        this.pending.delete(id);
        reject(error instanceof Error ? error : new Error(String(error)));
      });
    });
  }

  route(value: unknown): boolean {
    if (typeof value !== "object" || value === null) return false;
    const response = value as { id?: unknown; method?: unknown; result?: unknown; error?: unknown };
    if (typeof response.id !== "string" || response.method !== undefined) return false;
    const pending = this.pending.get(response.id);
    if (!pending) return false;
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
    for (const pending of this.pending.values()) {
      pending.reject(new Error("runtime transport closed"));
    }
    this.pending.clear();
  }
}

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
  const executions = new Map<string, AbortController>();
  const active = new Set<Promise<void>>();
  let requests = Promise.resolve();
  const track = (task: Promise<void>): void => {
    requests = task.catch(() => undefined);
    active.add(task);
    void task.then(() => active.delete(task), () => active.delete(task));
  };
  const enqueueResponse = (value: unknown): void => {
    track(requests.then(() => send(value)));
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
      executions.get(parsed.params.execution_id)?.abort();
      return;
    }
    if (!isRuntimeRequest(parsed)) {
      enqueueResponse({ id: "invalid", error: { message: "invalid runtime request" } });
      return;
    }
    if (parsed.method === "initialize") host.configure(parsed.params.supported_host_services);
    const executionId = runtimeRequestExecutionId(parsed);
    const controller = new AbortController();
    if (executionId !== "") executions.set(executionId, controller);
    const execution = { executionId, signal: controller.signal } satisfies RuntimeExecutionContext;
    const task = requests
      .then(() => handleRuntimeRequest(plugin, parsed, host, execution))
      .then(send)
      .finally(() => {
        if (executionId !== "" && executions.get(executionId) === controller) executions.delete(executionId);
      });
    track(task);
  };
  const decoder = new TextDecoder();
  let buffered = "";
  for await (const chunk of streams.input) {
    buffered += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
    const lines = buffered.split("\n");
    buffered = lines.pop() ?? "";
    for (const line of lines) processLine(line);
  }
  buffered += decoder.decode();
  if (buffered.trim() !== "") processLine(buffered);
  host.close();
  for (const controller of executions.values()) controller.abort();
  await Promise.all(active);
  await writes;
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
  if (request.method === "activate" || request.method === "shutdown") return true;
  return ["initialize", "capability.invoke", "service.invoke", "service.changed", "tool.execute"].includes(request.method)
    && typeof request.params === "object" && request.params !== null;
}

export interface Disposable {
  dispose(): void;
}

export const PRESENTATION_TARGETS = [
  "conversation.item", "conversation.process", "conversation.tool-activity",
  "conversation.composer", "header.conversation", "header.workspace",
  "navigation.primary", "app.status", "content.preview", "settings",
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
  readonly kind: "section" | "project" | "thread" | "room" | "command";
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

export const COMPOSER_ACTIONS = {
  setDraft: "conversation.composer.set-draft",
  addAttachment: "conversation.composer.add-attachment",
  removeAttachment: "conversation.composer.remove-attachment",
  setSubmissionMode: "conversation.composer.set-submission-mode",
  submit: "conversation.composer.submit",
  stop: "conversation.composer.stop",
} as const;
export type ComposerActionId = (typeof COMPOSER_ACTIONS)[keyof typeof COMPOSER_ACTIONS];

export const HEADER_ACTIONS = {
  selectTab: "header.select-tab",
  closeTab: "header.close-tab",
  navigateBack: "header.navigate-back",
  navigateForward: "header.navigate-forward",
} as const;
export type HeaderActionId = (typeof HEADER_ACTIONS)[keyof typeof HEADER_ACTIONS];

export const NAVIGATION_ACTIONS = {
  activateNode: "navigation.activate-node",
  pinNode: "navigation.pin-node",
  unpinNode: "navigation.unpin-node",
} as const;
export type NavigationActionId = (typeof NAVIGATION_ACTIONS)[keyof typeof NAVIGATION_ACTIONS];

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
  readonly fallback: unknown;
}

export interface PresenterDefinition {
  readonly id: string;
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly mode?: PresentationMode;
  readonly priority?: number;
  readonly render: (props: PresenterProps) => unknown;
}

export interface SlotRegistration {
  id: string;
  order?: number;
  render(context: Readonly<Record<string, unknown>>): unknown;
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

export type SurfaceMode = "replace" | "wrap";

export interface SurfaceRegistration {
  id: string;
  mode: SurfaceMode;
  order?: number;
  render(context: Readonly<Record<string, unknown>>, fallback: unknown): unknown;
}

export interface CommandRegistration {
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

export function createManifest(options: {
  id: string;
  name?: string;
  version?: string;
  description?: string;
  icon?: PluginManifestIcon;
}): Record<string, unknown> {
  return {
    schema_version: 1,
    id: options.id,
    name: options.name ?? options.id,
    version: options.version ?? "0.1.0",
    description: options.description ?? `A Wuu plugin: ${options.id}`,
    ...(options.icon ? { icon: options.icon } : {}),
  };
}

export * from "./bundle-contract.js";
