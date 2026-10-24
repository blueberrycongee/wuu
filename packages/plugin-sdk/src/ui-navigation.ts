/** Navigation data remains available when no application shell is mounted. */
export type NavigationReadonly<T> = T extends readonly (infer U)[] ? readonly NavigationReadonly<U>[] : T extends object ? { readonly [K in keyof T]: NavigationReadonly<T[K]> } : T;
export type NavigationIconV1 = { name: string; path?: never; light?: never; dark?: never } | { path: string; name?: never; light?: never; dark?: never } | { light: string; dark: string; name?: never; path?: never };
export interface NavigationThreadV1 {
  readonly id: string;
  readonly title?: string;
  readonly title_revision?: number;
  readonly preview: string;
  readonly cwd: string;
  readonly workspace_id?: string;
  readonly workspace_kind?: "project" | "scratch";
  readonly parent_id?: string;
  readonly agent_path?: string;
  readonly source?: string;
  readonly project_id?: string;
  readonly project_exists?: boolean;
  readonly project_role?: "side" | "worker";
  readonly model_provider: string;
  readonly model: string;
  readonly engine_id?: string;
  readonly status: "idle" | "in_progress";
  readonly pinned?: boolean;
  readonly folder_id?: string;
  readonly archived?: boolean;
  readonly archive_reason?: string;
  readonly ephemeral?: boolean;
  readonly read_only?: boolean;
  readonly session_control?: { readonly manager_id: string; readonly manager_name: string; readonly state: "active" | "paused" | "taken_over"; readonly revision: number };
  readonly forked_from_id?: string;
  readonly forked_from_turn_id?: string;
  readonly forked_from_item_id?: string;
  readonly created_at: string;
  readonly updated_at: string;
  readonly latest_completed_turn_id?: string;
  readonly turns: readonly { readonly id: string; readonly status: "in_progress" | "completed" | "failed" | "interrupted"; readonly started_at: string; readonly completed_at?: string; readonly duration_ms?: number }[];
  readonly turn_count: number;
  readonly child_agents?: readonly { readonly id: string; readonly status: string; readonly type?: string; readonly task_name?: string; readonly agent_profile?: string; readonly agent_path?: string; readonly parent_id?: string; readonly description?: string; readonly nested_count?: number; readonly nested_running_count?: number; readonly started_at?: string; readonly completed_at?: string | null; readonly pinned?: boolean; readonly archived?: boolean }[];
  readonly worktree?: { readonly path: string; readonly base_head?: string; readonly base_repo?: string; readonly dirty?: boolean; readonly changed_files?: readonly string[] };
}
export interface NavigationWorkspaceV1 {
  readonly id: string; readonly name: string; readonly path: string; readonly missing?: boolean;
  readonly created_at: string; readonly updated_at: string;
}
export interface NavigationPluginDestinationV1 {
  readonly id: string; readonly pluginId: string; readonly generation: string; readonly view: string;
  readonly title: string; readonly icon?: NavigationIconV1; readonly instanceId?: string; readonly navigationId?: string;
}
export interface NavigationWebsiteV1 {
  readonly id: string; readonly title: string; readonly url: string; readonly reopenedFrom?: string;
  readonly pinned?: boolean; readonly audible?: boolean; readonly muted?: boolean; readonly loading?: boolean; readonly favicon?: string; readonly startPage?: boolean;
}
export type NavigationUpdateV1 = { readonly kind: "unsupported" | "idle" | "checking" | "prompting" | "installing" } | { readonly kind: "available" | "ready" | "error"; readonly version: string } | { readonly kind: "downloading"; readonly version: string; readonly percent: number };
export interface ApplicationNavigationViewV1 {
  readonly initialized: boolean;
  readonly activeContextKind?: "project" | "no_project";
  readonly selectedWorkspaceId?: string;
  readonly threads: readonly NavigationThreadV1[];
  readonly workspaces: readonly NavigationWorkspaceV1[];
  readonly loadingWorkspaceIds: readonly string[];
  readonly lastViewedTurnByThreadId: Readonly<Record<string, string>>;
  readonly pendingConversations: readonly { readonly id: string; readonly title: string; readonly context: { readonly kind: "project"; readonly project_id: string; readonly cwd: string } | { readonly kind: "no_project"; readonly cwd: string } }[];
  readonly folders: readonly { readonly id: string; readonly name: string }[];
  readonly plugins: readonly { readonly id: string; readonly name: string; readonly pluginId: string; readonly fingerprint: string; readonly icon?: NavigationIconV1; readonly enabled: boolean; readonly kind: "plugin" | "skill" }[];
  readonly destinations: readonly NavigationPluginDestinationV1[];
  readonly activePluginView?: { readonly id: string; readonly pluginId: string; readonly title: string; readonly webPage: boolean };
  readonly canRevealThread: boolean;
  readonly canCreateWorkspace: boolean;
  readonly canChooseWorkspace: boolean;
  readonly projectAgentEnabled: boolean;
  readonly accountAvailable: boolean;
  readonly update: NavigationUpdateV1;
  readonly browser?: { readonly tabs: readonly NavigationWebsiteV1[]; readonly activeTabId?: string; readonly canCreate: boolean };
  readonly activeWebsiteId?: string;
  readonly searchOpen: boolean;
  readonly accountOpen: boolean;
  readonly focusedWorkspaceId?: string;
  readonly composerFocusRequest?: { readonly id: number; readonly tabId: string };
}
export interface ApplicationNavigationActionMapV1 {
  "navigation.refresh": { input: Record<string, never>; result: void };
  "navigation.load-workspace": { input: { readonly workspaceId: string }; result: void };
  "navigation.mark-viewed": { input: { readonly threadIds: readonly string[] }; result: void };
  "navigation.pin-thread": { input: { readonly threadId: string; readonly pinned: boolean }; result: void };
  "navigation.archive-thread": { input: { readonly threadId: string; readonly force?: boolean }; result: { readonly ok: true } | { readonly ok: false; readonly error: string; readonly forceRetryable?: boolean } };
  "navigation.delete-thread": { input: { readonly threadId: string }; result: void };
  "navigation.adopt-thread": { input: { readonly projectId: string; readonly threadId: string }; result: void };
  "navigation.create-folder": { input: { readonly name: string; readonly threadId?: string }; result: void };
  "navigation.rename-folder": { input: { readonly folderId: string; readonly name: string }; result: void };
  "navigation.delete-folder": { input: { readonly folderId: string }; result: void };
  "navigation.reorder-folders": { input: { readonly folderIds: readonly string[] }; result: void };
  "navigation.move-to-folder": { input: { readonly threadId: string; readonly folderId?: string }; result: void };
  "navigation.open-plugin": { input: { readonly pluginId: string; readonly viewTypeId: string; readonly instanceId?: string }; result: void };
  "navigation.show-native": { input: Record<string, never>; result: void };
  "navigation.show-search": { input: { readonly open: boolean }; result: void };
  "navigation.show-account": { input: { readonly open: boolean }; result: void };
  "navigation.focus-composer": { input: { readonly tabId: string }; result: void };
  "navigation.focus-workspace": { input: { readonly workspaceId?: string }; result: void };
  "navigation.reveal-thread": { input: { readonly threadId: string }; result: void };
  "navigation.open-external": { input: { readonly url: string }; result: void };
  "navigation.load-icon": { input: { readonly pluginId: string; readonly fingerprint: string; readonly path: string }; result: string };
  "navigation.account-status": { input: Record<string, never>; result: { readonly username?: string; readonly display_name?: string; readonly server?: string; readonly localLogoutOnly?: boolean } };
  "navigation.account-logout": { input: Record<string, never>; result: { readonly localLogoutOnly?: boolean } };
  "navigation.install-update": { input: Record<string, never>; result: void };
  "navigation.create-browser-tab": { input: Record<string, never>; result: void };
  "navigation.select-browser-tab": { input: { readonly tabId: string }; result: void };
  "navigation.close-browser-tab": { input: { readonly tabId: string }; result: void };
  "navigation.pin-browser-tab": { input: { readonly tabId: string; readonly pinned: boolean }; result: void };
  "navigation.mute-browser-tab": { input: { readonly tabId: string; readonly muted: boolean }; result: void };
  "navigation.reorder-browser-tabs": { input: { readonly tabIds: readonly string[] }; result: void };
}
