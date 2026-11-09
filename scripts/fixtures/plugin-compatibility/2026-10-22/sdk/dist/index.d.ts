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
import type { PluginDependency } from "./bundle-contract.js";
import type { ModelAdapterDescriptor } from "./model.js";
export * from "./process.js";
import type { PublicIconName, PublicSyntaxTokenName, PublicThemeTokenName } from "./theme-contract.generated.js";
export { LEGACY_THEME_TOKEN_ALIASES, PUBLIC_ICON_NAMES, PUBLIC_SYNTAX_TOKEN_NAMES, PUBLIC_THEME_TOKEN_NAMES, canonicalThemeTokenName, isPublicIconName, isPublicSyntaxTokenName, isPublicThemeTokenName, type PublicIconName, type PublicSyntaxTokenName, type PublicThemeTokenName, } from "./theme-contract.generated.js";
/** Unique identifier for a view type. */
export type ViewTypeId = string;
/** Where a view instance appears. */
/** Stable host-owned semantic regions available for View placement. */
export declare const VIEW_PLACEMENT_REGIONS: readonly ["navigation", "primary", "auxiliary", "inspector", "settings", "overlay"];
export type ViewPlacementRegion = (typeof VIEW_PLACEMENT_REGIONS)[number];
/** Stable host action locations; commands without placements remain programmatic. */
export type PluginCommandPlacement = "view.title" | "conversation.message.actions";
/** Public action input. Direct or palette invocations may still omit input. */
export type PluginCommandActionContext = Readonly<{
    contractVersion: 1;
    target: "view.title";
    viewId: string;
    viewTypeId: string;
    viewPluginId: string;
    region: ViewPlacementRegion;
}> | Readonly<{
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
export type PluginManifestIcon = PublicIconName | Readonly<{
    path: string;
}> | Readonly<{
    light: string;
    dark: string;
}>;
export interface ViewTypeDefinition {
    id: ViewTypeId;
    title: string;
    icon?: PublicIconName;
    defaultRegion?: ViewPlacementRegion;
    persistence?: ViewPersistence;
    render: unknown;
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
/** A desktop Chromium page inside an existing plugin View. */
export interface PluginUIWebPageProps {
    /** Initial HTTP(S) destination. Changing it navigates this page. */
    readonly url: string;
    readonly title?: string;
}
export type HostUIComponent<Props> = (props: Props) => unknown;
/** Composer accessory shell with shared sizing, scrolling and keyboard dismissal.
 * Render in composer.above. Empty-state visibility and expansion belong to the caller.
 */
export interface PluginUIComposerDrawerProps extends PluginUIContainerProps {
    readonly expanded: boolean;
    readonly onExpandedChange: (expanded: boolean) => void;
    readonly toggleLabel: string;
    /** Feedback displayed in both collapsed and expanded states. */
    readonly notice?: unknown;
    readonly icon?: unknown;
    readonly summary: unknown;
    readonly actions?: unknown;
    readonly tone?: "default" | "muted" | "warning";
}
/** Stable host-owned primitives for plugin views. */
export interface PluginUIKit {
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
    render: unknown;
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
    apply: "live" | "restart";
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
export interface PluginGenerationApi {
    /** Host-owned React runtime. Executable desktop bundles must use this instance. */
    readonly react: HostReact;
    /** Host-owned primitives that inherit the active Wuu theme and spacing rhythm. */
    readonly ui: PluginUIKit;
    readonly pluginId: string;
    readonly generation: string;
    /** Aborted when staging fails, is superseded, or this generation is disposed. */
    readonly signal: AbortSignal;
    /** Invoke one method owned by this plugin's active runtime generation. */
    invokeRuntime(method: string, input?: unknown, options?: PluginRuntimeInvokeOptions): Promise<unknown>;
    /** Read bounded, display-only completed turns from the addressed conversation. */
    readVisibleHistory(request: PluginVisibleHistoryRequest): Promise<PluginVisibleHistoryResult>;
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
    /** Supply append-only text for the host-owned composer ghost and Tab action. */
    registerComposerInlineCompletionProvider(provider: ComposerInlineCompletionProviderRegistration): ComposerInlineCompletionProviderHandle;
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
interface PluginVisibleHistoryScope {
    readonly threadId: string;
    readonly workspaceRoot: string;
    readonly workspaceId?: string;
}
export type PluginVisibleHistoryRequest = PluginVisibleHistoryScope & ({
    readonly kind: "main";
    readonly beforeTurnId: string;
    readonly sideThreadId?: never;
} | {
    readonly kind: "side";
    readonly sideThreadId: string;
    readonly beforeTurnId?: never;
});
export interface PluginVisibleHistoryResult {
    readonly turns: readonly {
        readonly id: string;
        readonly userText: string;
        readonly assistantText: string;
        readonly imageCount: number;
        readonly fileCount: number;
    }[];
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
/** Minimal type for the host-owned React instance; plugins never bundle React. */
export interface HostReact {
    readonly Fragment: unknown;
    useState<T>(initial: T | (() => T)): [T, (next: T | ((current: T) => T)) => void];
    useEffect(effect: () => void | (() => void), dependencies?: readonly unknown[]): void;
    useMemo<T>(factory: () => T, dependencies: readonly unknown[]): T;
    useCallback<T>(callback: T, dependencies: readonly unknown[]): T;
    useRef<T>(initial: T): {
        current: T;
    };
    useSyncExternalStore<T>(subscribe: (notify: () => void) => () => void, getSnapshot: () => T, getServerSnapshot?: () => T): T;
    createElement(type: string | ((props: Readonly<Record<string, unknown>>) => unknown), props: Readonly<Record<string, unknown>> | null, ...children: unknown[]): unknown;
}
export declare const RUNTIME_PROTOCOL_V1: 1;
export declare const CAPABILITY_PROTOCOL_V2: 2;
export declare const CAPABILITY_PROTOCOL_V3: 3;
export declare const REQUEST_TRANSFORM_CAPABILITY: "agent.request.transform";
export declare const AGENT_PRE_STEP_CAPABILITY: "agent.pre_step";
export declare const AGENT_TURN_COMPLETED_CAPABILITY: "agent.turn.completed";
export declare const AGENT_TURN_LIFECYCLE_CAPABILITY: "agent.turn.lifecycle";
export declare const AGENT_TURN_INTERRUPTED_CAPABILITY: "agent.turn.interrupted";
export declare const SYSTEM_PROMPT_SECTION_CAPABILITY: "agent.system_prompt.section";
/** @experimental No distributed first-party consumer has proven this contract. */
export declare const COMPACTION_CAPABILITY: "agent.compaction";
export declare const PLUGIN_CLIENT_REQUEST_CAPABILITY: "plugin.client.request";
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
export declare const HOST_SERVICE_METHODS: readonly ["host.storage.get", "host.storage.set", "host.storage.delete", "host.storage.keys", "host.storage.compare_exchange", "host.settings.get", "host.settings.list", "host.credentials.request", "host.session.create", "host.session.send", "host.session.list", "host.session.cancel", "host.session.inspect", "host.session.control", "host.session.history.read", "host.session.history.search", "host.workspace.status", "host.workspace.apply", "host.workspace.discard", "host.service.call"];
export type HostServiceMethod = (typeof HOST_SERVICE_METHODS)[number];
export declare const KERNEL_SERVICE_METHOD: "call";
export declare const EXECUTION_UPDATE_SERVICE: "execution.update";
export declare const INVOKE_TOOL_SERVICE: "execution.invoke-tool";
export declare const USER_QUESTION_ASK_SERVICE: "host.user-question.ask";
export declare const USER_QUESTION_OFFER_SERVICE: "host.user-question.offer";
export declare const ARTIFACT_IMPORT_SERVICE: "host.artifact.import";
export declare const FILE_LOCK_SERVICE: "host.file-lock";
export declare const MODEL_REQUEST_SERVICE: "host.model.request";
export declare const SECURITY_AUTHORIZE_SERVICE: "security.authorize";
export declare const PROCESS_SANDBOX_SERVICE: "sandbox.process";
export declare const SECURITY_AUTHORIZE_METHOD: "authorize";
export declare const PROCESS_SANDBOX_METHOD: "confine";
export declare const KERNEL_SERVICE_NAMES: {
    readonly "host.storage.get": "host.storage.get";
    readonly "host.storage.set": "host.storage.set";
    readonly "host.storage.delete": "host.storage.delete";
    readonly "host.storage.keys": "host.storage.keys";
    readonly "host.storage.compare_exchange": "host.storage.compare-exchange";
    readonly "host.settings.get": "host.settings.get";
    readonly "host.settings.list": "host.settings.list";
    readonly "host.credentials.request": "host.credentials.request";
    readonly "host.session.create": "host.session.create";
    readonly "host.session.send": "host.session.send";
    readonly "host.session.list": "host.session.list";
    readonly "host.session.cancel": "host.session.cancel";
    readonly "host.session.inspect": "host.session.inspect";
    readonly "host.session.control": "host.session.control";
    readonly "host.session.history.read": "host.session.history.read";
    readonly "host.session.history.search": "host.session.history.search";
    readonly "host.workspace.status": "host.workspace.status";
    readonly "host.workspace.apply": "host.workspace.apply";
    readonly "host.workspace.discard": "host.workspace.discard";
};
export type KernelHostServiceMethod = keyof typeof KERNEL_SERVICE_NAMES;
/** Build a typed kernel service call through the Service Registry gateway. */
export declare function kernelServiceCall<M extends KernelHostServiceMethod>(id: string, method: M, params: HostServiceContracts[M]["params"]): {
    id: string;
    method: "host.service.call";
    params: {
        service: {
            readonly "host.storage.get": "host.storage.get";
            readonly "host.storage.set": "host.storage.set";
            readonly "host.storage.delete": "host.storage.delete";
            readonly "host.storage.keys": "host.storage.keys";
            readonly "host.storage.compare_exchange": "host.storage.compare-exchange";
            readonly "host.settings.get": "host.settings.get";
            readonly "host.settings.list": "host.settings.list";
            readonly "host.credentials.request": "host.credentials.request";
            readonly "host.session.create": "host.session.create";
            readonly "host.session.send": "host.session.send";
            readonly "host.session.list": "host.session.list";
            readonly "host.session.cancel": "host.session.cancel";
            readonly "host.session.inspect": "host.session.inspect";
            readonly "host.session.control": "host.session.control";
            readonly "host.session.history.read": "host.session.history.read";
            readonly "host.session.history.search": "host.session.history.search";
            readonly "host.workspace.status": "host.workspace.status";
            readonly "host.workspace.apply": "host.workspace.apply";
            readonly "host.workspace.discard": "host.workspace.discard";
        }[M];
        method: "call";
        params: HostServiceContracts[M]["params"];
    };
};
export declare function requireKernelService(method: KernelHostServiceMethod): ServiceRequirement;
export declare function requireArtifactImportService(): ServiceRequirement;
export declare function requireModelRequestService(): ServiceRequirement;
/** Bootstrap service choices; independent of runtime protocol and product version. */
export interface ProductComposition {
    version: 1;
    services?: ServiceBinding[];
}
/** Selects a provider without fallback when it fails or is removed. */
export interface ServiceBinding {
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
}
/** A versioned service a plugin provides; consumers resolve by name and major version. */
export interface ServiceDescriptor {
    name: string;
    version: string;
    methods: ServiceMethodDescriptor[];
}
/** Optional plugin-owned durable user notebook; declare major 1 with required: false. */
export declare const MEMORY_NOTEBOOK_SERVICE = "memory.notebook";
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
    source: {
        scope: "user_notebook";
        file: string;
    };
    found: boolean;
    /** Opaque full-source revision. Present for empty files, absent for missing files. */
    revision?: string;
    content: string;
    truncated: boolean;
}
export declare const AUTHORIZATION_SERVICE_DESCRIPTOR: ServiceDescriptor;
export declare const PROCESS_SANDBOX_SERVICE_DESCRIPTOR: ServiceDescriptor;
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
    workspace_id?: string;
    wuu_home: string;
    workspace_state_dir?: string;
    capability_protocol_version?: number;
    supported_host_services?: HostServiceMethod[];
    lifecycle_version?: 2;
}
export interface RuntimeInitializeResult {
    model_adapters?: readonly ModelAdapterDescriptor[];
    tools?: ToolRegistration[];
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
export interface HostServiceContracts {
    "host.credentials.request": {
        params: CredentialRequestParams;
        result: CredentialRequestResult;
    };
    "host.storage.get": {
        params: {
            scope: "user" | "workspace";
            key: string;
        };
        result: {
            value: string | null;
        };
    };
    "host.storage.set": {
        params: {
            scope: "user" | "workspace";
            key: string;
            value: string;
        };
        result: Record<string, never>;
    };
    "host.storage.delete": {
        params: {
            scope: "user" | "workspace";
            key: string;
        };
        result: Record<string, never>;
    };
    "host.storage.keys": {
        params: {
            scope: "user" | "workspace";
        };
        result: {
            keys: string[];
        };
    };
    "host.storage.compare_exchange": {
        params: {
            scope: "user" | "workspace";
            key: string;
            expected: string | null;
            value: string | null;
        };
        result: {
            swapped: boolean;
            value: string | null;
        };
    };
    "host.settings.get": {
        params: {
            key: string;
        };
        result: {
            value: unknown;
        };
    };
    "host.settings.list": {
        params: Record<string, never>;
        result: {
            entries: Record<string, unknown>;
        };
    };
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
            speed?: string;
            permission_mode?: string;
            instructions?: string;
            tool_policy?: {
                allow?: string[];
                deny?: string[];
            };
            seed?: {
                version: 1;
                id?: string;
                body: string;
                source: {
                    session_id: string;
                    through_seq: number;
                };
                references?: Array<{
                    id: string;
                    label?: string;
                    history: {
                        snapshot: {
                            session_id: string;
                            through_seq: number;
                        };
                        start_seq: number;
                        end_seq: number;
                    };
                }>;
                artifacts?: Array<{
                    id: string;
                    label?: string;
                }>;
                provenance: {
                    producer: string;
                    source_model?: string;
                    created_at?: string;
                };
            };
            launch?: {
                revision?: number;
                kind?: string;
                intent?: string;
            };
        };
        result: {
            session_id: string;
            created: boolean;
            workspace_root?: string;
        };
    };
    "host.session.send": {
        params: {
            request_id: string;
            session_id: string;
            input: {
                prompt: string;
                context_blocks?: Array<{
                    kind?: string;
                    title?: string;
                    source?: string;
                    content: string;
                }>;
            };
            presentation?: {
                kind: "query_bubble" | "session_message";
                text: string;
                name?: string;
                related_session_id?: string;
            };
            cause?: string;
            if_running?: "queue" | "steer";
            reply_to_turn_id?: string;
            control_revision?: number;
        };
        result: {
            state: string;
            session_id: string;
            turn_id?: string;
            queue_id?: string;
            steered?: boolean;
        };
    };
    "host.session.list": {
        params: {
            parent_session_id?: string;
            scope?: "owned" | "shared";
        };
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
        params: {
            session_id: string;
            turn_id?: string;
            queue_id?: string;
            control_revision?: number;
        };
        result: {
            session_id: string;
            turn_id?: string;
            queue_id?: string;
            cancelled: boolean;
        };
    };
    "host.session.control": {
        params: {
            session_id: string;
            state?: "active" | "paused" | "released";
            revision?: number;
        };
        result: {
            session_id: string;
            manager_id?: string;
            state?: string;
            revision: number;
        };
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
            workspace?: {
                kind: "shared" | "worktree";
                dirty?: boolean;
                changed_files?: string[];
            };
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
            matches?: Array<{
                seq: number;
                role: string;
                name?: string;
                excerpt?: string;
            }>;
            has_more?: boolean;
            budget_exhausted?: boolean;
            next_cursor?: string;
        };
    };
    "host.workspace.status": {
        params: {
            session_id: string;
        };
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
        params: {
            session_id: string;
        };
        result: {
            session_id: string;
            applied: boolean;
            changed_files?: string[];
            discarded: boolean;
        };
    };
    "host.workspace.discard": {
        params: {
            session_id: string;
        };
        result: {
            session_id: string;
            discarded: boolean;
        };
    };
    "host.service.call": {
        params: {
            service: string;
            method: string;
            execution_id?: string;
            params?: unknown;
        };
        result: unknown;
    };
}
export type HostServiceCall<M extends HostServiceMethod = HostServiceMethod> = M extends HostServiceMethod ? {
    id: string;
    method: M;
    params?: HostServiceContracts[M]["params"];
} : never;
export interface HostServiceError {
    code: string;
    message: string;
}
export type HostServiceResponse<M extends HostServiceMethod = HostServiceMethod> = {
    id: string;
    result?: HostServiceContracts[M]["result"];
    error?: never;
} | {
    id: string;
    result?: never;
    error: HostServiceError;
};
export type HostServiceRequest<M extends HostServiceMethod = HostServiceMethod> = HostServiceCall<M>;
export type HostServiceResult<M extends HostServiceMethod = HostServiceMethod> = HostServiceResponse<M>;
export declare class RuntimeHostServiceError extends Error {
    readonly code: string;
    constructor(error: HostServiceError);
}
export interface RuntimeHostCallOptions {
    /** Stop waiting for this call. This does not undo effects already accepted by the host. */
    signal?: AbortSignal;
}
export interface RuntimeHost {
    supports(method: HostServiceMethod): boolean;
    call<M extends HostServiceMethod>(method: M, params: HostServiceContracts[M]["params"], options?: RuntimeHostCallOptions): Promise<HostServiceContracts[M]["result"]>;
}
/** Lifetime of background work started by initialize or activate. */
export interface RuntimeLifecycleContext {
    /** Aborts on shutdown or transport loss. Background tasks must stop when it aborts. */
    readonly signal: AbortSignal;
}
export interface RuntimeExecutionContext {
    readonly executionId: string;
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
export declare function invokeTool(host: RuntimeHost, params: InvokeToolParams): Promise<ToolResult>;
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
export declare function requestModel(host: RuntimeHost, input: ModelRequestParams): Promise<ModelRequestResult>;
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
export declare function reportExecutionUpdate(host: RuntimeHost, update: ExecutionUpdate): Promise<void>;
export declare function askUserQuestions(host: RuntimeHost, questions: readonly UserQuestion[]): Promise<UserQuestionAnswer>;
/** Offer a non-blocking question; any answer arrives through the conversation later. */
export declare function offerUserQuestions(host: RuntimeHost, questions: readonly UserQuestion[]): Promise<UserQuestionOfferResult>;
/** Copy a generated file into storage owned by the current tool execution's thread. */
export declare function importArtifact(host: RuntimeHost, artifact: ArtifactImportParams): Promise<ImportedArtifact>;
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
export declare function acquireFileLock(host: RuntimeHost, params: FileLockAcquireParams, executionId?: string): Promise<FileLockAcquireResult>;
/**
 * Release a lock idempotently. Use the host retained from initialize/activate
 * for cleanup after execution cancellation; an execution-scoped host is aborted.
 */
export declare function releaseFileLock(host: RuntimeHost, lockId: string, options?: RuntimeHostCallOptions): Promise<FileLockReleaseResult>;
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
    params: {
        execution_id: string;
    };
}
export type RuntimeRequest = RuntimeInitializeRequest | RuntimePrepareRequest | RuntimeActivateRequest | RuntimeCapabilityRequest | RuntimeServiceInvokeRequest | RuntimeServiceChangedRequest | RuntimeToolRequest | RuntimeShutdownRequest;
export type RuntimeResponse = {
    id: string;
    result: unknown;
} | {
    id: string;
    error: {
        message: string;
    };
};
export declare function handleRuntimeRequest(plugin: RuntimePlugin, request: RuntimeRequest, host?: RuntimeHost, execution?: RuntimeExecutionContext, lifecycle?: RuntimeLifecycleContext): Promise<RuntimeResponse>;
export interface JSONLInput extends AsyncIterable<Uint8Array | string> {
}
export interface JSONLOutput {
    write(chunk: string): unknown;
}
/** Runs a plugin over Wuu's full-duplex JSON-lines transport. */
export declare function runJSONLRuntime(plugin: RuntimePlugin, streams: {
    input: JSONLInput;
    output: JSONLOutput;
}): Promise<void>;
export interface Disposable {
    dispose(): void;
}
export declare const PRESENTATION_TARGETS: readonly ["conversation.item", "conversation.process", "conversation.tool-activity", "conversation.composer", "header.conversation", "header.workspace", "navigation.primary", "app.status", "content.preview", "settings"];
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
export type ComposerInlineCompletionSource = Pick<ComposerInlineCompletionContext, "kind" | "composerId" | "revision" | "threadId" | "sideThreadId">;
export type ComposerInlineCompletionInteraction = {
    /** Host identity for this presentation, shared by its subsequent interactions. */
    readonly interactionId: string;
    /** The provider's result id, when supplied. */
    readonly suggestionId?: string;
    readonly source: ComposerInlineCompletionSource;
} & ({
    readonly type: "shown" | "accepted" | "dismissed";
} | {
    /** The composer callback accepted dispatch, not confirmation of backend delivery. */
    readonly type: "dispatched";
    readonly mode: ComposerSubmissionModeV1;
    /** Draft text differs from the draft immediately after this acceptance. */
    readonly edited: boolean;
});
export interface ComposerInlineCompletionProviderRegistration {
    readonly id: string;
    /** Lower orders run first; ties use plugin ID, then registration ID. */
    readonly order?: number;
    /**
     * Return null to let the next provider try. The host owns rendering and Tab
     * acceptance; providers own prompts, debouncing, and any context retrieval.
     * Stop work when signal aborts. Late results never change the current input.
     */
    provideCompletion(context: ComposerInlineCompletionContext, signal: AbortSignal): ComposerInlineCompletion | null | Promise<ComposerInlineCompletion | null>;
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
export declare const CONVERSATION_ITEM_ACTIONS: {
    readonly copy: "conversation.item.copy";
    readonly edit: "conversation.item.edit";
    readonly retry: "conversation.item.retry";
    readonly openAttachment: "conversation.item.open-attachment";
    readonly openTool: "conversation.item.open-tool";
    readonly cancelProcess: "conversation.item.cancel-process";
};
export type ConversationItemActionId = (typeof CONVERSATION_ITEM_ACTIONS)[keyof typeof CONVERSATION_ITEM_ACTIONS];
export declare const COMPOSER_ACTIONS: {
    readonly setDraft: "conversation.composer.set-draft";
    readonly addAttachment: "conversation.composer.add-attachment";
    readonly removeAttachment: "conversation.composer.remove-attachment";
    readonly setSubmissionMode: "conversation.composer.set-submission-mode";
    readonly submit: "conversation.composer.submit";
    readonly stop: "conversation.composer.stop";
};
export type ComposerActionId = (typeof COMPOSER_ACTIONS)[keyof typeof COMPOSER_ACTIONS];
export declare const HEADER_ACTIONS: {
    readonly selectTab: "header.select-tab";
    readonly closeTab: "header.close-tab";
    readonly navigateBack: "header.navigate-back";
    readonly navigateForward: "header.navigate-forward";
};
export type HeaderActionId = (typeof HEADER_ACTIONS)[keyof typeof HEADER_ACTIONS];
export declare const NAVIGATION_ACTIONS: {
    readonly activateNode: "navigation.activate-node";
    readonly pinNode: "navigation.pin-node";
    readonly unpinNode: "navigation.unpin-node";
};
export type NavigationActionId = (typeof NAVIGATION_ACTIONS)[keyof typeof NAVIGATION_ACTIONS];
export declare const STATUS_ACTIONS: {
    readonly activateItem: "status.activate-item";
};
export type StatusActionId = (typeof STATUS_ACTIONS)[keyof typeof STATUS_ACTIONS];
export declare const FILE_PREVIEW_ACTIONS: {
    readonly open: "file-preview.open";
    readonly reveal: "file-preview.reveal";
    readonly select: "file-preview.select";
    readonly save: "file-preview.save";
    readonly reload: "file-preview.reload";
};
export type FilePreviewActionId = (typeof FILE_PREVIEW_ACTIONS)[keyof typeof FILE_PREVIEW_ACTIONS];
export declare const SETTINGS_ACTIONS: {
    readonly openPage: "settings.open-page";
    readonly updateValue: "settings.update-value";
    readonly refresh: "settings.refresh";
};
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
    readonly original: unknown;
    /** Lazily continue with lower-priority presenters, optionally with a new snapshot. */
    readonly next: (input?: {
        readonly snapshot: unknown;
    }) => unknown;
    /** Equivalent to next() with the current snapshot. */
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
    readonly action?: Readonly<{
        kind: "open-session";
        sessionId: string;
    }>;
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
export declare function validateManifest(manifest: Record<string, unknown>): string[];
export declare function createManifest(options: {
    id: string;
    name?: string;
    version?: string;
    description?: string;
    icon?: PluginManifestIcon;
    requires?: string[];
    dependencies?: PluginDependency[];
}): Record<string, unknown>;
export * from "./bundle-contract.js";
export * from "./model.js";
