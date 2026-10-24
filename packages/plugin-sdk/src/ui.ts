import type { ApplicationSettingsActionMapV1, ApplicationSettingsViewV1 } from "./application-settings.js";
import type { ApplicationComposerActionMapV1, ApplicationDraftsSnapshotV1 } from "./ui-composer.js";
import type { ComponentType, ReactNode } from "react";
import type {
  ComposerSnapshotV1, ComposerSubmissionModeV1, ConversationItemSnapshotV1,
  ConversationProcessSnapshotV1, Disposable, FilePreviewSnapshotV1,
  NavigationSnapshotV1, PluginWorkspaceSnapshot, SettingsSnapshotV1,
  SettingsValueMapV1, StatusSnapshotV1,
} from "./index.js";

/** Declare this service in plugin.json desktop.services to offer an application shell. */
export const APPLICATION_SHELL_SERVICE = {
  name: "ui.shell", version: "1.0.0",
  methods: [{ name: "describe", input_schema: "ui.shell.describe.input.v1", output_schema: "ui.shell.describe.output.v1" }],
} satisfies import("./index.js").ServiceDescriptor;

/** Immutable public application state. Snapshots retain identity until a change. */
export interface ApplicationSnapshotV1 {
  readonly contractVersion: 1;
  readonly revision: number;
  readonly phase: "starting" | "ready" | "error";
  readonly connection: { readonly status: "connecting" | "connected" | "disconnected" | "error"; readonly message?: string };
  readonly workspaces: PluginWorkspaceSnapshot;
  readonly navigation: NavigationSnapshotV1 & {
    readonly section: "conversations" | "catalog";
    readonly catalogSection: "plugins" | "skills";
    readonly catalogPluginId: string;
  };
  readonly tabs: {
    readonly activeTabId: string;
    readonly items: readonly ApplicationTabV1[];
    readonly pending?: { readonly kind: "tab" | "workspace" | "thread" | "runtime"; readonly targetId: string; readonly requestId?: number; readonly contextSwitching?: boolean; readonly contentReady?: boolean };
  };
  readonly conversation: {
    readonly threadId?: string;
    readonly title?: string;
    readonly turns: readonly ApplicationTurnV1[];
    readonly loading: boolean;
    readonly error?: string;
  };
  readonly composer: ComposerSnapshotV1;
  readonly drafts: ApplicationDraftsSnapshotV1;
  readonly settings: SettingsSnapshotV1 & ApplicationSettingsViewV1 & { readonly open: boolean };
  readonly preview: FilePreviewSnapshotV1 | null;
  readonly appearance: ApplicationAppearanceV1;
  readonly locale: string;
  readonly status: StatusSnapshotV1;
}

export interface ApplicationTabV1 {
  readonly id: string;
  readonly kind: "draft" | "thread" | "skills";
  readonly title: string;
  readonly workspaceId?: string;
  readonly workspaceRoot: string;
  readonly threadId?: string;
  readonly project: boolean;
  readonly draftText: string;
  readonly hasAttachments: boolean;
}

export interface ApplicationTurnV1 {
  readonly id: string;
  readonly status: "pending" | "running" | "completed" | "failed" | "cancelled";
  readonly items: readonly ConversationItemSnapshotV1[];
  readonly process?: ConversationProcessSnapshotV1;
}

export interface ApplicationAppearanceV1 {
  readonly theme: "system" | "light" | "dark";
  readonly resolvedTheme: "light" | "dark";
  readonly uiFontSize: number;
  readonly codeFontSize: number;
}

/** Commands are scoped to the generation that captured this application handle. */
export interface ApplicationActionMapV1 extends ApplicationComposerActionMapV1, ApplicationSettingsActionMapV1 {
  "workspace.retarget-draft": { input: { readonly workspaceId?: string }; result: void };
  "workspace.start-draft": { input: { readonly workspaceId?: string }; result: { readonly activated: boolean; readonly tabId?: string } };
  "workspace.create": { input: { readonly kind: "blank" | "folder" }; result: void };
  /** Remove a workspace after the caller has confirmed the user's intent. */
  "workspace.remove": { input: { readonly workspaceId: string }; result: void };
  "workspace.relocate": { input: { readonly workspaceId: string }; result: void };
  "thread.activate": { input: { readonly threadId: string; readonly workspaceId?: string }; result: void };
  "draft.start": { input: Record<string, never>; result: { readonly tabId: string } };
  "tab.pop-out": { input: { readonly tabId: string }; result: void };
  "navigation.reveal-complete": { input: { readonly requestId: number }; result: void };
  "tab.select": { input: { readonly tabId: string }; result: void };
  "tab.close": { input: { readonly tabId: string }; result: void };
  "tab.reorder": { input: { readonly tabId: string; readonly beforeTabId: string }; result: void };
  "tab.new": { input: { readonly workspaceId?: string; readonly project?: boolean }; result: { readonly tabId: string } };
  "draft.rename": { input: { readonly tabId: string; readonly title: string }; result: void };
  "draft.set-project-mode": { input: { readonly project: boolean }; result: void };
  "navigation.show-section": { input: { readonly section: "conversations" | "catalog" }; result: void };
  "catalog.select-section": { input: { readonly section: "plugins" | "skills" }; result: void };
  "catalog.select-plugin": { input: { readonly pluginId: string }; result: void };
  "workspace.select": { input: { readonly workspaceId: string }; result: void };
  "thread.select": { input: { readonly threadId: string }; result: void };
  "thread.create": { input: { readonly workspaceId?: string }; result: { readonly threadId: string } };
  "thread.rename": { input: { readonly threadId: string; readonly title: string }; result: void };
  "thread.archive": { input: { readonly threadId: string; readonly archived: boolean }; result: void };
  "composer.set-draft": { input: { readonly text: string; readonly ownerTabId?: string }; result: void };
  "composer.submit": { input: { readonly mode?: ComposerSubmissionModeV1 }; result: void };
  "composer.stop": { input: Record<string, never>; result: void };
  "composer.configure": { input: { readonly modelId?: string; readonly runtimeId?: string; readonly permissionId?: string }; result: void };
  "settings.open": { input: { readonly pageId?: string }; result: void };
  "settings.close": { input: Record<string, never>; result: void };
  "settings.update-value": { input: { [K in keyof SettingsValueMapV1]: { readonly key: K; readonly value: SettingsValueMapV1[K] } }[keyof SettingsValueMapV1]; result: void };
  "appearance.set": { input: Partial<Pick<ApplicationAppearanceV1, "theme" | "uiFontSize" | "codeFontSize">>; result: void };
  "preview.open": { input: { readonly resourceId: string }; result: void };
  "preview.close": { input: Record<string, never>; result: void };
  "runtime.refresh": { input: Record<string, never>; result: void };
}

export interface ApplicationServiceV1 {
  readonly contractVersion: 1;
  getSnapshot(): ApplicationSnapshotV1;
  /** Unsubscribe is idempotent; generation disposal also removes every listener. */
  subscribe(listener: () => void): () => void;
  /** A rejection reports failure; the caller must not automatically replay effects. */
  invoke<Action extends keyof ApplicationActionMapV1>(
    action: Action,
    input: ApplicationActionMapV1[Action]["input"],
    options?: { readonly signal?: AbortSignal },
  ): Promise<ApplicationActionMapV1[Action]["result"]>;
}

/** Slots are qualified as `${ownerPluginId}/${localId}`. */
export interface UISlotDefinition {
  readonly id: string;
  readonly cardinality: "one" | "many";
  /** Select a single surface through the committed service graph, without fallback. */
  readonly service?: string;
}
export interface UISlotProps {
  readonly name: string;
  readonly context?: Readonly<Record<string, unknown>>;
  readonly fallback?: ReactNode;
}
export interface UICompositionV1 {
  /** Nested contributions use the same component; recursive slot cycles fail visibly. */
  readonly Slot: ComponentType<UISlotProps>;
}
export interface UISlotRenderProps {
  readonly application: ApplicationServiceV1;
  readonly slots: UICompositionV1;
  readonly context: Readonly<Record<string, unknown>>;
}
export interface UIContributionDefinition {
  readonly id: string;
  readonly target: string;
  readonly order?: number;
  readonly render: ComponentType<UISlotRenderProps>;
}
export interface ApplicationShellRenderProps {
  readonly application: ApplicationServiceV1;
  readonly slots: UICompositionV1;
}
export interface ApplicationShellDefinition {
  /** Local provider ID used with the owning plugin ID and generation in ui.shell binding. */
  readonly id: string;
  readonly render: ComponentType<ApplicationShellRenderProps>;
}
export interface ApplicationGenerationAPI {
  readonly application: ApplicationServiceV1;
  registerApplicationShell(definition: ApplicationShellDefinition): Disposable;
  registerUISlot(definition: UISlotDefinition): Disposable;
  registerUIContribution(definition: UIContributionDefinition): Disposable;
}
