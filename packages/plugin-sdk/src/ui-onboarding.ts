import type { ApplicationSettingsControlsV1 } from "./ui-settings-controls.js";
export interface OnboardingWorkspaceV1 {
  readonly path: string; readonly name: string; readonly sources: readonly ("codex" | "claude")[];
  readonly lastUsedAt: string; readonly sessionCount: number; readonly isRepository: boolean;
}
export interface OnboardingWorkspaceScanV1 { readonly candidates: readonly OnboardingWorkspaceV1[]; readonly incomplete: boolean }
export interface ApplicationOnboardingWorkflowV1 {
  readonly step: "welcome" | "plugins" | "runtime" | "provider" | "permissions" | "browser" | "ready";
  readonly selectedPluginIDs: readonly string[]; readonly initializedPluginSelection: boolean;
  readonly applyingPlugins: boolean; readonly savingProvider: boolean; readonly connected: boolean;
  readonly savingRuntime: boolean; readonly finishing: boolean; readonly importingBrowser: string;
  readonly browserImportError: string;
  readonly browserImportResults: Readonly<Record<string, Awaited<ReturnType<ApplicationSettingsControlsV1["importBrowserCookies"]>>>>;
  readonly browserImported: boolean; readonly workspaceScan?: OnboardingWorkspaceScanV1;
  readonly scanningWorkspaces: boolean; readonly choosingWorkspace: boolean; readonly selectedPaths: readonly string[];
  readonly scanAttempt: number; readonly scannedRevision: number;
  readonly error: { readonly title: string; readonly detail?: string } | null;
  readonly selectedEngine: string;
}
export interface ApplicationOnboardingViewV1 {
  readonly revision: number; readonly completed: boolean; readonly workflow?: ApplicationOnboardingWorkflowV1;
  readonly capabilities: { readonly discoverWorkspaces: boolean; readonly chooseWorkspace: boolean; readonly browserImport: boolean };
}
export interface OnboardingCallOptionsV1 { readonly signal?: AbortSignal }
export interface ApplicationOnboardingV1 {
  getSnapshot(): ApplicationOnboardingViewV1;
  subscribe(listener: () => void): () => void;
  /** Rejects a stale revision; only this setup flow can be checkpointed. */
  checkpoint(input: { readonly revision: number; readonly workflow: ApplicationOnboardingWorkflowV1 }): void;
  discoverWorkspaces(options?: OnboardingCallOptionsV1): Promise<OnboardingWorkspaceScanV1>;
  chooseWorkspace(options?: OnboardingCallOptionsV1): Promise<string | null>;
  /** Import and persistence errors are visible and are never automatically replayed. */
  complete(input: { readonly paths: readonly string[] }, options?: OnboardingCallOptionsV1): Promise<void>;
}
