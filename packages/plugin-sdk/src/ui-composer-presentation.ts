import type { ComponentType } from "react";
import type { ComposerInlineCompletionProviderRegistration, ComposerInlineCompletionRefresh, ComposerStatusItem } from "./index.js";
import type { ApplicationSettingsProviderSummaryV1, SettingsReadonly } from "./application-settings.js";
import type { CatalogExtensionV1 } from "./ui-catalog.js";

export interface ApplicationComposerInlineProviderV1 extends Pick<ComposerInlineCompletionProviderRegistration, "provideCompletion" | "onDidInteract"> {
  subscribeRefresh(listener: (scope?: ComposerInlineCompletionRefresh) => void): () => void;
}
export interface ApplicationComposerConfigurationV1 {
  readonly provider: string;
  readonly model: string;
  readonly effort?: string;
  readonly variant?: string;
  readonly speed?: string;
  readonly permissions?: { readonly mode?: string; readonly approve_for_me?: boolean };
  readonly providers?: readonly SettingsReadonly<ApplicationSettingsProviderSummaryV1>[];
  readonly features?: { readonly safe_mode?: boolean };
  readonly extension_inventory?: readonly SettingsReadonly<CatalogExtensionV1>[];
}
export interface ApplicationComposerContextUsageV1 {
  readonly turnID: string; readonly used: number; readonly window: number;
  readonly inputTokens: number; readonly cacheCreationTokens: number; readonly cacheReadTokens: number;
  readonly requestContext?: {
    readonly stepIndex: number; readonly messageCount: number; readonly stablePrefix: number; readonly turnPrefix: number;
    readonly transientMessages: number; readonly hiddenMessages: number; readonly toolCount: number;
    readonly stablePrefixBytes: number; readonly turnPrefixBytes: number; readonly messageBytes: number;
    readonly dynamicBytes: number; readonly toolSchemaBytes: number;
    readonly promptCacheKey?: string; readonly stablePrefixHash?: string; readonly turnPrefixHash?: string;
    readonly toolSurfaceHash?: string; readonly loadableToolSurfaceHash?: string;
  };
}
export interface ApplicationComposerPresentationViewV1 {
  readonly ownerTabId: string;
  /** Retained while the selected shell is replaced. */
  readonly runtimeMenu: "model" | null;
  readonly threadId?: string;
  readonly available: boolean;
  readonly running: boolean;
  readonly turnRunning: boolean;
  readonly canSteer: boolean;
  readonly readOnly: boolean;
  readonly status: string;
  readonly turnId?: string;
  readonly configuration?: ApplicationComposerConfigurationV1;
  readonly contextUsage?: ApplicationComposerContextUsageV1;
  readonly queryHistory: readonly string[];
  readonly requestedHandoffIntent?: string;
  readonly skillRevision: string;
  readonly workspaceCapabilities: { readonly create: boolean; readonly open: boolean };
}
export interface ApplicationComposerTelemetryV1 {
  readonly tokensPerSecond: number; readonly source: "real" | "estimated" | "none";
  readonly sampledAt?: number; readonly inputTokens: number; readonly outputTokens: number;
  readonly contextUsage?: ApplicationComposerContextUsageV1;
}
export interface ApplicationComposerContributionContextV1 {
  readonly ownerTabId: string;
  readonly variant: "hero" | "dock" | "document";
  readonly mainConversation: boolean;
  readonly running: boolean;
  readonly readOnly: boolean;
  readonly hasDraft: boolean;
  readonly attachmentCount: number;
}
export interface ApplicationComposerContributionV1 {
  readonly key: string;
  readonly title?: string;
  /** The host retains the provider's render/error/translation lifecycle. */
  readonly View: ComponentType<{ readonly context: ApplicationComposerContributionContextV1 }>;
}
export interface ApplicationComposerStatusItemV1 extends ComposerStatusItem { readonly key: string }
export interface ApplicationComposerStatusSnapshotV1 {
  readonly items: readonly ApplicationComposerStatusItemV1[];
  readonly todoUpdate?: {
    readonly explanation?: string;
    readonly todos: readonly { readonly content: string; readonly status: "pending" | "in_progress" | "completed" }[];
  };
}

/** Explicit composer presentation mechanisms shared by default and independent composers. */
export interface ApplicationComposerPresentationV1 {
  /** Includes extension status and native agent activity; TODO exists only during an active turn. */
  getStatusSnapshot(threadId?: string): ApplicationComposerStatusSnapshotV1;
  /** Rebinds provider generations; unsubscribe or scope retirement releases every source. */
  subscribeStatus(threadId: string | undefined, listener: () => void): () => void;
  getSnapshot(ownerTabId: string): ApplicationComposerPresentationViewV1;
  setRuntimeMenu(ownerTabId: string, menu: "model" | null): void;
  subscribe(listener: () => void): () => void;
  getCommands(): readonly { readonly pluginId: string; readonly id: string }[];
  executeCommand(input: { readonly ownerTabId: string; readonly pluginId: string; readonly id: string; readonly args: string }): Promise<void>;
  getComposerInlineCompletionProviders(): readonly ApplicationComposerInlineProviderV1[];
  subscribeComposerInlineCompletionProviders(listener: () => void): () => void;
  getContributions(slot: "composer.above" | "composer.toolbar"): readonly ApplicationComposerContributionV1[];
  getTelemetry(turnId?: string): ApplicationComposerTelemetryV1;
  subscribeTelemetry(listener: () => void): () => void;
}
