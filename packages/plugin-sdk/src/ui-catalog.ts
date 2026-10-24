/** Public plugin and skill catalog data; no renderer state or callbacks. */
export type CatalogKindV1 = "skill" | "command" | "mcp" | "hook" | "plugin";

export type CatalogStateV1 = "active" | "read_only" | "pending" | "granted" | "rejected" | "changed";

export type CatalogApprovalStateV1 = "official" | "pending" | "granted" | "changed" | "rejected";

export type CatalogRuntimeStateV1 = "inactive" | "starting" | "active" | "failed" | "stopping" | "stopped";

export type CatalogCommandDescriptorV1 = {
  id: string;
  title: string;
  description?: string;
  kind: "prompt_template" | "runtime_action" | "side_conversation";
  template?: string;
  contexts?: string[];
  aliases?: string[];
  keywords?: string[];
};

export interface CatalogDesktopDescriptorV1 {
  entry: string;
}

export interface CatalogThemeDescriptorV1 {
  id: string;
  name: string;
  base: "light" | "dark";
  tokens: Record<string, string>;
  syntax?: Record<string, string>;
}

export interface CatalogSkinVariantV1 {
  background: string;
  foreground: string;
  image?: string;
  position?: "center" | "top" | "bottom" | "left" | "right";
  overlay?: string;
  hover?: string;
  active?: string;
}

export interface CatalogSkinDescriptorV1 {
  id: string;
  name: string;
  light: CatalogSkinVariantV1;
  dark: CatalogSkinVariantV1;
}

export type CatalogSettingTypeV1 = "boolean" | "string" | "number" | "enum" | "secret";
export type CatalogSettingScopeV1 = "user" | "workspace";
export type CatalogSettingApplyModeV1 = "live" | "restart";

export interface CatalogSettingDescriptorV1 {
  id: string;
  type: CatalogSettingTypeV1;
  title: string;
  description?: string;
  default: boolean | string | number;
  enum?: string[];
  scope: CatalogSettingScopeV1;
  apply: CatalogSettingApplyModeV1;
  credential_origins?: string[];
}

export type CatalogContributionsV1 = {
  commands?: CatalogCommandDescriptorV1[];
  themes?: CatalogThemeDescriptorV1[];
  skins?: CatalogSkinDescriptorV1[];
  settings?: CatalogSettingDescriptorV1[];
  slots?: CatalogSlotContributionDescriptorV1[];
  surfaces?: CatalogSurfaceContributionDescriptorV1[];
  presenters?: CatalogPresenterContributionDescriptorV1[];
  navigation?: CatalogViewEntryDescriptorV1[];
  workspace_tools?: CatalogViewEntryDescriptorV1[];
  settings_pages?: CatalogViewEntryDescriptorV1[];
};

export interface CatalogViewEntryDescriptorV1 {
  id: string;
  view: string;
  title: string;
  description?: string;
  icon?: CatalogIconDescriptorV1;
  order?: number;
}

export type CatalogIconDescriptorV1 =
  | { name: string; path?: never; light?: never; dark?: never }
  | { name?: never; path: string; light?: never; dark?: never }
  | { name?: never; path?: never; light: string; dark: string };

export interface CatalogSlotContributionDescriptorV1 {
  id: string;
  target: string;
  order?: number;
  title?: string;
}

export interface CatalogSurfaceContributionDescriptorV1 {
  id: string;
  target: string;
  mode: "replace" | "wrap";
  order?: number;
  title?: string;
}

export interface CatalogPresenterContributionDescriptorV1 {
  id: string;
  target: string;
  mode: "replace" | "wrap";
  priority?: number;
  title?: string;
}

export type CatalogPendingUpdateV1 = {
  version?: string;
  fingerprint: string;
  active_fingerprint: string;
  requested_permissions?: string[];
  effective_permissions?: string[];
};

export type CatalogPluginActivationIssueV1 = {
  kind: "missing_requirement" | "conflict" | "version_mismatch" | "optional_cycle";
  related_plugin_id: string;
};

export type CatalogProvenanceV1 = {
  kind: CatalogKindV1;
  source: string;
  scope: string;
  path?: string;
  plugin_id?: string;
  official?: boolean;
};

export type CatalogExtensionV1 = {
  id: string;
  // What people see: a plugin manifest's interface displayName and
  // shortDescription, then its name and description, then the id.
  name: string;
  description?: string;
  long_description?: string;
  developer?: string;
  icon?: CatalogIconDescriptorV1;
  kind: CatalogKindV1;
  provenance: CatalogProvenanceV1;
  state: CatalogStateV1;
  executable?: boolean;
  fingerprint?: string;
  graph_generation?: string;
  package_source?: "bundled" | "user" | "project" | "dev";
  grant_scope?: "action" | "session" | "project" | "user";
  requested_permissions?: string[];
  unsupported_fields?: string[];
  parent_id?: string;
  approval_state?: CatalogApprovalStateV1;
  runtime_state?: CatalogRuntimeStateV1;
  last_error?: string;
  requires?: string[];
  dependencies?: Array<{ id: string; version?: string; optional?: boolean }>;
  resolved_dependencies?: string[];
  breaks?: string[];
  conflicts?: string[];
  activation_issues?: CatalogPluginActivationIssueV1[];
  enabled?: boolean;
  // Explicit user policy; absent means the package default applies.
  enabled_override?: boolean;
  desktop?: CatalogDesktopDescriptorV1;
  contributions?: CatalogContributionsV1;
  pending_update?: CatalogPendingUpdateV1;
};

export type CatalogPackageActionV1 =
  | "grant"
  | "reject"
  | "revoke"
  | "enable"
  | "disable"
  | "promote_update"
  | "reject_update";

export type CatalogSkillProjectIdentityV1 = {
  root: string;
  path: string;
};

export type CatalogSkillV1 = {
  name: string;
  description?: string;
  when_to_use?: string;
  trigger_condition?: string;
  source: string;
  path?: string;
  project?: CatalogSkillProjectIdentityV1;
  argument_hint?: string;
  model?: string;
  context?: string;
  agent?: string;
  allowed_tools?: string[];
  required_context?: string[];
  examples?: string[];
  verification_checklist?: string[];
  progressive_disclosure?: string;
  user_invocable: boolean;
  disable_model_invoke: boolean;
  paths?: string[];
  effort?: string;
  version?: string;
};


export interface CatalogPackageUpdateV1 {
 readonly id: string;
 readonly fingerprint?: string;
 readonly action: CatalogPackageActionV1;
}
export interface CatalogPackageResultV1 {
 readonly skills: CatalogSkillV1[];
 readonly extension_inventory: CatalogExtensionV1[];
 readonly package?: { readonly id: string };
}
export interface ApplicationCatalogSnapshotV1 {
 readonly contextKey: string;
 readonly inventory: readonly CatalogExtensionV1[];
 readonly capabilities: { readonly install: boolean; readonly update: boolean; readonly remove: boolean };
}
export interface CatalogCallOptionsV1 { readonly signal?: AbortSignal; }
/** All calls retain their captured application generation. Failed effects must not be replayed. */
export interface ApplicationCatalogV1 {
 getSnapshot(): ApplicationCatalogSnapshotV1;
 listSkills(options?: CatalogCallOptionsV1): Promise<readonly CatalogSkillV1[]>;
 readSkill(input: {readonly name:string; readonly source:string}, options?: CatalogCallOptionsV1): Promise<{readonly content:string}>;
 refresh(options?: CatalogCallOptionsV1): Promise<readonly CatalogSkillV1[] | undefined>;
 update(input: CatalogPackageUpdateV1, options?: CatalogCallOptionsV1): Promise<void>;
 /** Picker cancellation or a changed source workspace yields undefined. */
 install(options?: CatalogCallOptionsV1): Promise<CatalogPackageResultV1 | undefined>;
 remove(id: string, options?: CatalogCallOptionsV1): Promise<CatalogPackageResultV1 | undefined>;
 loadIcon(id: string, fingerprint: string, path: string, options?: CatalogCallOptionsV1): Promise<string>;
}
