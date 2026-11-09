import type { ApplicationSettingsEngineInfoV1, ApplicationSettingsProviderSummaryV1, SettingsReadonly } from "./application-settings.js";

export type ComposerPermissionModeV1 = "standard" | "read_only" | "unconfined";

export interface ApplicationComposerRuntimeSelectionV1 {
  readonly engine: string;
  readonly provider: string;
  /** Empty native model/effort/speed values select the engine's own default. */
  readonly model: string;
  readonly effort: string;
  readonly variant: string;
  readonly speed: string;
  readonly modelOptions: Readonly<Record<string, string>>;
  readonly permissionMode: string;
  readonly approveForMe: boolean;
}

export interface ApplicationComposerRuntimeOwnerV1 {
  readonly ownerTabId: string;
  readonly threadId?: string;
  readonly cwd: string;
  readonly available: boolean;
  readonly readOnly: boolean;
  readonly busy: boolean;
  readonly engineLocked: boolean;
  readonly engineSelectionPending: boolean;
  readonly permissionLocked: boolean;
  readonly selection: ApplicationComposerRuntimeSelectionV1;
  readonly nativeDefaults: {
    readonly model: string;
    readonly effort: string;
    readonly speed: string;
    readonly modelOptions: Readonly<Record<string, string>>;
  };
  readonly worktree: { readonly available: boolean; readonly enabled: boolean; readonly startBranch: string; readonly branches: readonly string[] };
  readonly engines: readonly SettingsReadonly<ApplicationSettingsEngineInfoV1>[];
  readonly providers: readonly SettingsReadonly<ApplicationSettingsProviderSummaryV1>[];
  readonly providerModels: readonly { readonly id: string; readonly label: string; readonly efforts: readonly string[]; readonly defaultEffort: string; readonly supportedInAPI: boolean }[];
  readonly engineModelsLoading: boolean;
  readonly engineModelsError: string;
  readonly providerModelsLoading: boolean;
  readonly providerModelsError: string;
  readonly selectionError: string;
}

export interface ApplicationComposerRuntimeViewV1 {
  readonly activeOwnerTabId?: string;
  readonly owners: readonly ApplicationComposerRuntimeOwnerV1[];
}

export type ApplicationComposerRuntimeChoiceV1 =
  | { readonly kind: "engine"; readonly engine: string }
  | { readonly kind: "provider-model"; readonly provider: string; readonly model: string; readonly variant?: string }
  | { readonly kind: "effort"; readonly variant: string }
  | { readonly kind: "speed"; readonly speed: string }
  | { readonly kind: "native-model"; readonly model: string; readonly effort: string }
  | { readonly kind: "native-effort"; readonly effort: string }
  | { readonly kind: "native-options"; readonly modelOptions: Readonly<Record<string, string>> };

/** Selections belong to a source tab, never to whichever tab happens to be active later. */
export interface ApplicationComposerRuntimeActionMapV1 {
  "composer.choose-runtime": { input: { readonly ownerTabId: string; readonly selection: ApplicationComposerRuntimeChoiceV1 }; result: { readonly accepted: boolean } };
  "composer.choose-permission": { input: { readonly ownerTabId: string; readonly mode: ComposerPermissionModeV1; readonly approveForMe?: boolean }; result: { readonly accepted: boolean } };
  "composer.choose-worktree": { input: { readonly ownerTabId: string; readonly enabled: boolean; readonly startBranch?: string }; result: { readonly accepted: boolean } };
  "composer.refresh-engine-models": { input: { readonly ownerTabId: string; readonly force?: boolean }; result: void };
  "composer.refresh-provider-models": { input: { readonly ownerTabId: string; readonly provider?: string; readonly force?: boolean }; result: void };
}
