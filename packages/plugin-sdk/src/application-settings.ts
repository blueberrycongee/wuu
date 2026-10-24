import type {
  CodexPetSettingsUpdate, CodexPetsSnapshot, EngineListResult, EngineUpdateParams,
  InitializeResult, RuntimeAdvancedSettingsUpdate, RuntimeConnectionUpdate,
  RuntimeGeneralSettingsUpdate, SettingsUsageResponse, Thread,
} from "../../protocol/src/index.js";

export type SettingsReadonly<T> = T extends readonly (infer Item)[]
  ? readonly SettingsReadonly<Item>[]
  : T extends object ? { readonly [Key in keyof T]: SettingsReadonly<T[Key]> } : T;

/** Workspace configuration presented by settings; excludes application composition bindings. */
export type ApplicationSettingsConfigurationV1 = SettingsReadonly<Pick<InitializeResult,
  "protocol_version" | "core" | "provider" | "model" | "effort" | "variant" | "speed"
  | "status" | "runtime_host" | "workspace_root" | "permissions" | "extension_trust"
  | "extension_inventory" | "model_roles" | "model_aliases" | "project_models"
  | "providers" | "catalog_providers" | "advanced_settings" | "general_settings" | "features"
>>;

export interface ApplicationArchivedThreadV1 {
  readonly id: string;
  readonly title?: string;
  readonly updated_at: string;
  readonly archive_project_id: string;
  readonly archive_project_name: string;
}

export interface ApplicationSettingsViewV1 {
  readonly configuration?: ApplicationSettingsConfigurationV1;
  readonly engineInventory?: SettingsReadonly<EngineListResult>;
  readonly engineInventoryError: string;
  readonly runningProviderNames: readonly string[];
  readonly archivedThreads: readonly ApplicationArchivedThreadV1[];
  readonly runtimeSwitchPending: boolean;
}

export interface ApplicationSettingsActionMapV1 {
  "settings.save-provider": { input: { readonly provider: string; readonly model: string; readonly effort?: string; readonly connection?: RuntimeConnectionUpdate; readonly variant?: string }; result: void };
  "settings.remove-provider": { input: { readonly provider: string; readonly fallbackProvider?: string; readonly fallbackModel?: string }; result: void };
  "settings.refresh-model-catalog": { input: Record<string, never>; result: { readonly modelCount: number } };
  "settings.reconcile-connections": { input: { readonly providers: SettingsReadonly<NonNullable<InitializeResult["providers"]>> }; result: void };
  "settings.reconcile-account-models": { input: { readonly provider: string; readonly accountId: string; readonly models: SettingsReadonly<NonNullable<NonNullable<InitializeResult["providers"]>[number]["models"]>> }; result: void };
  "settings.refresh-engines": { input: Record<string, never>; result: SettingsReadonly<EngineListResult> | undefined };
  "settings.update-engine": { input: EngineUpdateParams; result: SettingsReadonly<EngineListResult> };
  "settings.update-advanced": { input: RuntimeAdvancedSettingsUpdate; result: void };
  "settings.update-general": { input: RuntimeGeneralSettingsUpdate; result: void };
  "settings.read-usage": { input: Record<string, never>; result: SettingsReadonly<SettingsUsageResponse> };
  "settings.read-pets": { input: Record<string, never>; result: SettingsReadonly<CodexPetsSnapshot> };
  "settings.update-pets": { input: CodexPetSettingsUpdate; result: SettingsReadonly<CodexPetsSnapshot> };
  "settings.list-archived": { input: Record<string, never>; result: SettingsReadonly<Thread[]> };
  /** Only deletes a conversation that is still archived at the storage boundary. */
  "settings.delete-archived": { input: { readonly threadId: string }; result: void };
  "settings.unarchive": { input: { readonly threadId: string }; result: void };
}

/** Layout-owned geometry and interactions; contains no settings domain state. */
export interface ApplicationSettingsLayoutV1 {
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
