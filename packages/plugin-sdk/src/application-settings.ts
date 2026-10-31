import type { NavigationIconV1 } from "./ui-navigation.js";

export type SettingsReadonly<T> = T extends readonly (infer Item)[]
  ? readonly SettingsReadonly<Item>[]
  : T extends object ? { readonly [Key in keyof T]: SettingsReadonly<T[Key]> } : T;

/** Workspace settings data; excludes session state and application composition. */
export interface ApplicationSettingsConfigurationV1 {
 readonly protocol_version: string;
 readonly provider: string;
 readonly model: string;
 readonly effort?: string;
 readonly variant?: string;
 readonly workspace_root: string;
 readonly core?: SettingsReadonly<ApplicationSettingsCoreBuildInfoV1>;
 readonly providers?: SettingsReadonly<ApplicationSettingsProviderSummaryV1[]>;
 readonly catalog_providers?: SettingsReadonly<ApplicationSettingsCatalogProviderSummaryV1[]>;
 readonly model_aliases?: SettingsReadonly<Record<string, ApplicationSettingsModelAliasSummaryV1>>;
 readonly project_models?: SettingsReadonly<ApplicationSettingsProjectModelsConfigV1>;
 readonly advanced_settings?: SettingsReadonly<ApplicationSettingsAdvancedSettingsSummaryV1>;
 readonly general_settings?: SettingsReadonly<ApplicationSettingsGeneralSettingsSummaryV1>;
 readonly features?: { readonly project_agent?: boolean; readonly browser?: boolean; readonly safe_mode?: boolean };
 readonly extension_inventory?: SettingsReadonly<ApplicationSettingsExtensionInventoryRecordV1[]>;
}

export interface ApplicationArchivedThreadV1 {
  readonly id: string;
  readonly title?: string;
  readonly updated_at: string;
  readonly archive_project_id: string;
  readonly archive_project_name: string;
}

export interface ApplicationSettingsViewV1 {
  readonly configuration?: ApplicationSettingsConfigurationV1;
  readonly engineInventory?: SettingsReadonly<ApplicationSettingsEngineListResultV1>;
  readonly engineInventoryError: string;
  readonly runningProviderNames: readonly string[];
  readonly archivedThreads: readonly ApplicationArchivedThreadV1[];
  readonly runtimeSwitchPending: boolean;
}

export interface ApplicationSettingsActionMapV1 {
  "settings.save-provider": { input: { readonly provider: string; readonly model: string; readonly effort?: string; readonly connection?: ApplicationSettingsRuntimeConnectionUpdateV1; readonly variant?: string }; result: void };
  "settings.remove-provider": { input: { readonly provider: string; readonly fallbackProvider?: string; readonly fallbackModel?: string }; result: void };
  "settings.refresh-model-catalog": { input: Record<string, never>; result: { readonly modelCount: number } };
  "settings.reconcile-connections": { input: { readonly providers: SettingsReadonly<ApplicationSettingsProviderSummaryV1[]> }; result: void };
  "settings.reconcile-account-models": { input: { readonly provider: string; readonly accountId: string; readonly models: SettingsReadonly<ApplicationSettingsProviderModelSummaryV1[]> }; result: void };
  "settings.refresh-engines": { input: Record<string, never>; result: SettingsReadonly<ApplicationSettingsEngineListResultV1> | undefined };
  "settings.update-engine": { input: ApplicationSettingsEngineUpdateParamsV1; result: SettingsReadonly<ApplicationSettingsEngineListResultV1> };
  "settings.update-advanced": { input: ApplicationSettingsRuntimeAdvancedSettingsUpdateV1; result: void };
  "settings.update-general": { input: ApplicationSettingsRuntimeGeneralSettingsUpdateV1; result: void };
  "settings.read-usage": { input: Record<string, never>; result: SettingsReadonly<ApplicationSettingsUsageResponseV1> };
  "settings.read-pets": { input: Record<string, never>; result: SettingsReadonly<ApplicationSettingsCodexPetsSnapshotV1> };
  "settings.update-pets": { input: ApplicationSettingsCodexPetSettingsUpdateV1; result: SettingsReadonly<ApplicationSettingsCodexPetsSnapshotV1> };
  "settings.list-archived": { input: Record<string, never>; result: readonly { readonly id: string; readonly archived?: boolean }[] };
  /** Only deletes a conversation that is still archived at the storage boundary. */
  "settings.delete-archived": { input: { readonly threadId: string }; result: void };
  "settings.unarchive": { input: { readonly threadId: string }; result: void };
}

/** Layout-owned geometry and interactions; contains no settings domain state. */
export interface ApplicationSettingsLayoutV1 {
  /** @deprecated Default settings now uses the shared notice queue; layouts need not arbitrate notices. */
  readonly onNoticeVisibilityChange?: (visible: boolean) => void;
  readonly browserSettingsAllowed: boolean;
  readonly embedded: boolean;
  readonly embeddedDrawerPhase: "closed" | "open" | "closing" | "docking";
  readonly onSidebarPointerEnter: () => void;
  readonly onSidebarPointerLeave: (event: import("react").PointerEvent<HTMLElement>) => void;
  readonly sidebarWidth: number;
  readonly resizingSidebar: boolean;
  readonly shellRef: import("react").RefObject<HTMLDivElement | null>;
  readonly sidebarCollapsed: boolean;
  readonly sidebarAnimating: boolean;
  readonly onToggleSidebar: () => void;
  readonly onSidebarResizeStart: (event: import("react").PointerEvent<HTMLDivElement>) => void;
  readonly onSidebarResizerPointer: (event: import("react").PointerEvent<HTMLDivElement>) => void;
  readonly onSidebarSeparatorKey: (event: import("react").KeyboardEvent<HTMLDivElement>) => void;
}


/** Settings wire data owned by this public SDK; no host protocol dependency. */
export type ApplicationSettingsCoreBuildInfoV1 = {
  version?: string;
  commit?: string;
  date?: string;
  dirty?: boolean;
};

export type ApplicationSettingsProviderSummaryV1 = {
  chatgpt_account_id?: string;
  auth_status?: ApplicationSettingsChatGPTAuthStatusV1;
  name: string;
  type: string;
  model: string;
  base_url?: string;
  api_key_configured?: boolean;
  connection_locked?: boolean;
  auto_discovered?: boolean;
  reuse_codex_credentials?: boolean;
  // Model catalog service the endpoint belongs to; absent when unrecognized.
  catalog_id?: string;
  catalog_name?: string;
  // Choices removed from this provider, sorted by ID.
  hidden_models?: string[];
  // Present when a local ChatGPT/Codex OAuth session exists. "wuu-auth-store"
  // is Wuu's own login; "codex-cli" is a read-only Codex CLI login on this
  // machine. Discovery does not imply Wuu is allowed to use it yet.
  codex_credential_source?: string;
  models?: ApplicationSettingsProviderModelSummaryV1[];
  latest_request?: ApplicationSettingsEngineLatestRequestV1;
  local_usage?: ApplicationSettingsSubscriptionUsageV1;
  quota?: ApplicationSettingsSubscriptionQuotaV1;
};

export type ApplicationSettingsChatGPTAuthStatusV1 = "connected" | "reauth_required" | "permission_required";

export type ApplicationSettingsProviderModelSummaryV1 = {
  fast_mode?: boolean;
  default_speed?: string;
  id: string;
  display_name?: string;
  default_effort?: string;
  default_variant?: string;
  supported_efforts?: string[];
  variants?: ApplicationSettingsProviderModelVariantSummaryV1[];
  capabilities?: ApplicationSettingsModelCapabilitySummaryV1;
  behavior?: ApplicationSettingsModelBehaviorSummaryV1;
  source?: string;
};

export type ApplicationSettingsProviderModelVariantSummaryV1 = {
  id: string;
  options?: Record<string, ApplicationSettingsJsonValueV1>;
};

export type ApplicationSettingsJsonValueV1 =
  | null
  | boolean
  | number
  | string
  | ApplicationSettingsJsonValueV1[]
  | { [key: string]: ApplicationSettingsJsonValueV1 };

export type ApplicationSettingsModelCapabilitySummaryV1 = {
  chat: boolean;
  responses?: boolean;
  tools: boolean;
  tool_calling?: string;
  structured_output: boolean;
  streaming: boolean;
  streaming_tool_args?: boolean;
  freeform_tool?: boolean;
  parallel_tool_calls?: boolean;
  system_role: boolean;
  developer_role?: boolean;
  reasoning: boolean;
  context_window?: number;
  input_limit?: number;
  output_limit?: number;
  image_input?: boolean;
  file_input?: boolean;
  video_input?: boolean;
  video_input_known?: boolean;
  prompt_cache?: boolean;
  cache_granularity?: string;
  protocol_family?: string;
  retry_safe_error_categories?: string[];
};

export type ApplicationSettingsModelBehaviorSummaryV1 = {
  family?: string;
  default_write_mode?: string;
  preferred_edit_primitive?: string;
  exact_edit_reliability?: number;
  whole_file_reliability?: number;
  json_reliability?: number;
  long_horizon_score?: number;
  default_max_autonomous_steps?: number;
  default_search_budget?: number;
  needs_read_before_write?: boolean;
  allow_parallel_read_only?: boolean;
  allow_direct_shell?: boolean;
  latency?: string;
  suitable?: {
    main?: boolean;
    review?: boolean;
    compact?: boolean;
    title?: boolean;
    worker?: boolean;
    fallback?: boolean;
  };
};

export type ApplicationSettingsEngineLatestRequestV1 = {
  status?: string;
  error?: string;
  at?: string;
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  cache_creation_tokens?: number;
  cache_read_tokens?: number;
  usage_reported?: boolean;
};

export type ApplicationSettingsSubscriptionUsageV1 = {
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reported_turns: number;
};

export type ApplicationSettingsSubscriptionQuotaV1 = {
  status: "available" | "stale" | "unavailable" | "sign_in" | "unsupported";
  kind?: "subscription" | "plan" | "balance";
  account?: { id: string; label?: string; source?: string };
  plan?: string;
  checked_at: string;
  observed_at?: string;
  expires_at?: string;
  error_code?: "network" | "rate_limited" | "sign_in" | "invalid_response" | "unsupported";
  windows?: {
    id: string;
    label?: string;
    used_percent?: number;
    window_minutes?: number;
    resets_at?: string;
    display?: string;
    model?: string;
    scope?: string;
    unlimited?: boolean;
  }[];
  balances?: { currency: string; amount: string }[];
  reset_credits?: number;
};

export type ApplicationSettingsCatalogProviderSummaryV1 = {
  id: string;
  name: string;
  type: string;
  base_url: string;
  api_key_env?: string;
  model_count: number;
  default_model: string;
  /** Present only when a single provider was requested. */
  models?: ApplicationSettingsCatalogModelSummaryV1[];
};

export type ApplicationSettingsCatalogModelSummaryV1 = {
  id: string;
  name?: string;
  release_date?: string;
  context_window?: number;
  tool_call?: boolean;
};

export type ApplicationSettingsModelAliasSummaryV1 = {
  provider: string;
  model: string;
  effort?: string;
  variant?: string;
};

export type ApplicationSettingsProjectModelsConfigV1 = {
  technical_lead?: Partial<ApplicationSettingsModelAliasSummaryV1>;
  executor?: Partial<ApplicationSettingsModelAliasSummaryV1>;
  side?: Partial<ApplicationSettingsModelAliasSummaryV1>;
  worker?: Partial<ApplicationSettingsModelAliasSummaryV1>;
};

export type ApplicationSettingsAdvancedSettingsSummaryV1 = {
  max_steps: number;
  max_context_tokens: number;
  temperature: number;
  compact_threshold_pct?: number;
  compact_keep_recent_tokens?: number;
  disable_auto_compact: boolean;
  provider_context_window?: number;
  context_window_tokens?: number;
  context_window_source?: string;
  input_limit_tokens?: number;
  output_reserve_tokens?: number;
  compact_threshold_tokens?: number;
};

export type ApplicationSettingsGeneralSettingsSummaryV1 = {
  execution_environments?: ApplicationSettingsExecutionEnvironmentSettingsV1;
  ptc?: ApplicationSettingsPTCSettingsV1;
  git_attribution_enabled?: boolean;
  mcp_server_enabled: Record<string, boolean>;
};

export type ApplicationSettingsExecutionEnvironmentSettingsV1 = {
  default?: string;
  profiles?: Record<string, ApplicationSettingsExecutionEnvironmentProfileV1>;
};

export type ApplicationSettingsExecutionEnvironmentProfileV1 = {
 user?: string;
 python?: string;
 known_hosts_file?: string;
 host_workspace?: string;
 mount_read_only?: boolean;
  backend: "docker" | "ssh" | "singularity" | "modal" | "daytona" | "vercel_sandbox" | "command";
  image?: string;
  host?: string;
  port?: number;
  identity_file?: string;
  workspace?: string;
  worker?: string;
  shared?: boolean;
  persistent?: boolean;
  network?: "enabled" | "none";
  cpus?: number;
  memory_mb?: number;
  lifetime_seconds?: number;
  /** Total EOF drain/cleanup budget in milliseconds; zero selects the backend default. */
  shutdown_timeout_ms?: number;
  forward_env?: string[];
  command?: string[];
};

export type ApplicationSettingsPTCSettingsV1 = {
  enabled: boolean;
};

export type ApplicationSettingsExtensionInventoryRecordV1 = {
 id: string; name: string; description?: string; kind: ApplicationSettingsExtensionKindV1; provenance: ApplicationSettingsExtensionProvenanceV1; state: ApplicationSettingsExtensionStateV1;
 fingerprint?: string;
 package_source?: "bundled" | "user" | "project" | "dev";
 enabled_override?: boolean;
 icon?: NavigationIconV1;
 approval_state?: "official" | "pending" | "granted" | "changed" | "rejected";
 enabled?: boolean; contributions?: { settings?: ApplicationSettingsExtensionSettingDescriptorV1[] };
};

export type ApplicationSettingsExtensionKindV1 = "skill" | "command" | "mcp" | "hook" | "plugin";

export type ApplicationSettingsExtensionProvenanceV1 = {
  kind: ApplicationSettingsExtensionKindV1;
  source: string;
  scope: string;
  path?: string;
  plugin_id?: string;
  official?: boolean;
};

export type ApplicationSettingsExtensionStateV1 = "active" | "read_only" | "pending" | "granted" | "rejected" | "changed";

export interface ApplicationSettingsExtensionSettingDescriptorV1 {
  id: string;
  type: ApplicationSettingsExtensionSettingTypeV1;
  title: string;
  description?: string;
  default: boolean | string | number;
  enum?: string[];
  scope: ApplicationSettingsExtensionSettingScopeV1;
  apply: ApplicationSettingsExtensionSettingApplyModeV1;
  credential_origins?: string[];
}

export type ApplicationSettingsExtensionSettingTypeV1 = "boolean" | "string" | "number" | "enum" | "secret";

export type ApplicationSettingsExtensionSettingScopeV1 = "user" | "workspace";

export type ApplicationSettingsExtensionSettingApplyModeV1 = "live" | "restart";

export type ApplicationSettingsEngineListResultV1 = {
  subscription_providers?: ApplicationSettingsProviderSummaryV1[];
  engines: ApplicationSettingsEngineInfoV1[];
  settings?: ApplicationSettingsEngineSettingsConfigV1;
};

export type ApplicationSettingsEngineInfoV1 = {
  id: string;
  display_name?: string;
  protocol?: string;
  install_url?: string;
  /** Legacy descriptor version; not necessarily the installed CLI version. */
  version?: string;
  /** Observed installed CLI version; omitted when unknown or unsupported. */
  cli_version?: string;
  /** Vendor guide only; Wuu does not manage the CLI installation. */
  update_url?: string;
  capabilities?: string[];
  /** Selected implementation's client readiness/model source; not backend authorization. Omitted uses legacy client admission. */
  requires_host_model?: boolean;
  enabled: boolean;
  binary_path?: string;
  binary_ok: boolean;
  error?: string;
  models?: ApplicationSettingsEngineModelInfoV1[];
  models_status?: "ready" | "empty" | "unsupported" | "error" | "partial" | "stale";
  models_error?: string;
  permission_modes?: ApplicationSettingsEnginePermissionModeInfoV1[];
  latest_request?: ApplicationSettingsEngineLatestRequestV1;
  quota?: ApplicationSettingsSubscriptionQuotaV1;
  local_usage?: ApplicationSettingsSubscriptionUsageV1;
};

export type ApplicationSettingsEngineModelInfoV1 = {
  options?: Array<{ id: string; label: string; type: "select" | "boolean"; default_value: string; choices: Array<{ value: string; label: string }> }>;
  /** CLI-reported target; id remains the value passed back to the engine. */
  resolved_model?: string;
  fast_mode?: boolean;
  default_speed?: string;
  id: string;
  display_name?: string;
  default_effort?: string;
  supported_efforts?: string[];
  is_default?: boolean;
};

export type ApplicationSettingsEnginePermissionModeInfoV1 = {
  mode: "standard" | "read_only" | "unconfined";
  id?: string;
  label?: string;
};

export type ApplicationSettingsEngineSettingsConfigV1 = {
  default_engine?: string;
  codex?: ApplicationSettingsEngineBinarySettingsV1;
  claude?: ApplicationSettingsEngineBinarySettingsV1;
  cursor?: ApplicationSettingsEngineBinarySettingsV1;
  devin?: ApplicationSettingsEngineBinarySettingsV1;
  dsh?: ApplicationSettingsEngineBinarySettingsV1;
  grok?: ApplicationSettingsEngineBinarySettingsV1;
  hermes?: ApplicationSettingsEngineBinarySettingsV1;
  pi?: ApplicationSettingsEngineBinarySettingsV1;
  opencode?: ApplicationSettingsEngineBinarySettingsV1;
  antigravity?: ApplicationSettingsEngineBinarySettingsV1;
};

export type ApplicationSettingsEngineBinarySettingsV1 = {
  enabled?: boolean;
  binary_path?: string;
};

export type ApplicationSettingsRuntimeConnectionUpdateV1 = {
  chatgpt_account_id?: string;
  // Remove a model from this provider's choices, including future catalog refreshes.
  remove_model?: string;
  // Restore a removed choice or add a model ID the catalog does not list.
  add_model?: string;
  // Save the provider without making it the workspace default. Settings edit
  // services this way; choosing the default is a separate update.
  keep_selection?: boolean;
  base_url?: string;
  api_key?: string;
  auth_token?: string;
  // Optional provider protocol type used when creating a new provider.
  // Supported values: "openai", "openai-compatible", "anthropic", "claude",
  // "anthropic-official", "xai-subscription", "openai-codex". Omitted or empty
  // keeps "openai-compatible". Codex requires explicit credential reuse consent.
  type?: string;
  create_provider?: boolean;
  reuse_codex_credentials?: boolean;
};

export type ApplicationSettingsEngineUpdateParamsV1 = ApplicationSettingsEngineSettingsConfigV1;

export type ApplicationSettingsRuntimeAdvancedSettingsUpdateV1 = {
  max_steps?: number;
  max_context_tokens?: number;
  temperature?: number;
  compact_threshold_pct?: number;
  compact_keep_recent_tokens?: number;
  disable_auto_compact?: boolean;
  provider_context_window?: number;
  model_aliases?: Record<string, ApplicationSettingsModelAliasSummaryV1>;
  project_models?: ApplicationSettingsProjectModelsConfigV1;
  verification_model?: ApplicationSettingsModelAliasSummaryV1;
};

export type ApplicationSettingsRuntimeGeneralSettingsUpdateV1 = {
  execution_environments?: ApplicationSettingsExecutionEnvironmentSettingsV1;
  ptc?: ApplicationSettingsPTCSettingsV1;
  git_attribution_enabled?: boolean;
  mcp_enabled_toggles?: Record<string, boolean>;
};

export type ApplicationSettingsUsageResponseV1 = {
  total_sessions: number;
  generated_at: string;
  metrics: ApplicationSettingsUsageMetricsV1;
  model_breakdowns: ApplicationSettingsModelUsageV1[];
  skill_usage: ApplicationSettingsSkillUsageV1[];
  days: ApplicationSettingsUsageDayV1[];
};

export type ApplicationSettingsUsageMetricsV1 = {
  prompt_tokens: number;
  context_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  cache_hit_rate: number;
  turns: number;
  agents: number;
  date_range: [string, string];
  active_days: number;
};

export type ApplicationSettingsModelUsageV1 = {
  provider: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  sessions: number;
};

export type ApplicationSettingsSkillUsageV1 = {
  name: string;
  count: number;
};

export type ApplicationSettingsUsageDayV1 = {
  date: string;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  cache_hit_rate: number;
  turns: number;
  agents: number;
};

export type ApplicationSettingsCodexPetsSnapshotV1 = ApplicationSettingsCodexPetSettingsV1 & {
  home: string;
  pets: ApplicationSettingsCodexPetV1[];
  errors: string[];
};

export type ApplicationSettingsCodexPetSettingsV1 = {
  enabled: boolean;
  selected_id: string;
  size?: ApplicationSettingsCodexPetSizeV1;
  scale?: number;
};

export type ApplicationSettingsCodexPetSizeV1 = "small" | "default" | "large" | "extra-large";

export type ApplicationSettingsCodexPetV1 = {
  id: string;
  display_name: string;
  description: string;
  manifest_path: string;
  spritesheet_path: string;
  spritesheet_url: string;
};

export type ApplicationSettingsCodexPetSettingsUpdateV1 = Partial<ApplicationSettingsCodexPetSettingsV1>;
