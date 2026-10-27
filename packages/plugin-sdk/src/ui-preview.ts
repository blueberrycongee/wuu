import type { ApplicationActivityV1 } from "./ui.js";
import type { BrowserDockTargetV1, BrowserNavigationRequestV1 } from "./ui-browser.js";
export type PreviewReadonly<T> = T extends readonly (infer Item)[] ? readonly PreviewReadonly<Item>[]
  : T extends object ? { readonly [Key in keyof T]: PreviewReadonly<T[Key]> } : T;

export type ApplicationPreviewIconV1 = { readonly name: string; readonly path?: never; readonly light?: never; readonly dark?: never }
  | { readonly name?: never; readonly path: string; readonly light?: never; readonly dark?: never }
  | { readonly name?: never; readonly path?: never; readonly light: string; readonly dark: string };
export type ApplicationPreviewContextV1 = { readonly kind: "project"; readonly project_id: string; readonly cwd: string }
  | { readonly kind: "no_project"; readonly cwd: string };
export interface ApplicationDiffSelectionV1 {
  readonly artifactID?: string;
  readonly path: string;
  readonly cwd?: string;
  readonly action?: "create" | "update" | "delete" | "rename";
  readonly snapshotText?: string;
  readonly afterSha?: string;
  readonly additions: number;
  readonly deletions: number;
  readonly newFile: boolean;
  readonly diff?: {
    readonly path?: string; readonly newFile?: boolean; readonly truncated?: boolean;
    readonly lines?: number; readonly oldLines?: number; readonly newLines?: number; readonly summary?: string;
    readonly hunks: readonly { readonly oldStart: number; readonly newStart: number;
      readonly lines: readonly { readonly op: "equal" | "insert" | "delete"; readonly content: string }[] }[];
  };
}
export interface ApplicationArtifactV1 {
  readonly id: string; readonly itemId: string; readonly index: number;
  readonly type: string;
  readonly name: string; readonly mimeType: string; readonly data?: string; readonly remoteRef?: string;
  readonly text?: string; readonly foldText?: boolean; readonly foldPreview?: boolean; readonly uri?: string;
  readonly resource?: unknown; readonly placement: "inline" | "turn_end"; readonly ref?: string;
  readonly sha256?: string; readonly sizeBytes?: number; readonly delivered?: boolean;
}
export type ApplicationPreviewTabV1 =
  | { readonly kind: "new" | "side-thread" | "files" | "review" | "terminal" | "browser"; readonly id: string }
  | { readonly kind: "project"; readonly id: string; readonly projectID: string; readonly title: string }
  | { readonly kind: "file"; readonly id: string; readonly context: ApplicationPreviewContextV1; readonly path: string; readonly title: string;
      readonly selection?: { readonly startLineNumber: number; readonly startColumn: number; readonly endLineNumber?: number; readonly endColumn?: number }; readonly anchor?: string }
  | { readonly kind: "diff"; readonly id: string; readonly threadID: string; readonly path: string; readonly title: string; readonly selection: ApplicationDiffSelectionV1 }
  | { readonly kind: "artifact"; readonly id: string; readonly threadID: string; readonly cwd?: string; readonly title: string; readonly artifact: ApplicationArtifactV1; readonly page?: number; readonly pageRequest?: string }
  | { readonly kind: "plugin"; readonly id: string; readonly pluginId: string; readonly fingerprint: string; readonly viewTypeId: string; readonly title: string; readonly icon?: ApplicationPreviewIconV1 };
export interface ApplicationPreviewWorkspaceV1 {
  readonly ownerTabId: string;
  readonly activeContext?: ApplicationPreviewContextV1;
  readonly workspaceContext?: ApplicationPreviewContextV1;
  readonly threadId?: string;
  readonly conversationTitle: string;
  readonly fileRefreshKey?: string;
  readonly browserActivity?: ApplicationActivityV1;
  readonly browserDockTarget?: BrowserDockTargetV1;
  readonly browserNavigation?: BrowserNavigationRequestV1;
  readonly tabs: readonly ApplicationPreviewTabV1[];
  readonly activeTabId?: string;
  readonly activeFileTabId?: string;
  readonly conversationSelected: boolean;
}
export interface ApplicationPreviewActionMapV1 {
  "preview.open-url": { input: { readonly url: string; readonly modifiers?: { readonly metaKey?: boolean; readonly ctrlKey?: boolean; readonly altKey?: boolean; readonly button?: number } }; result: void };
  "preview.set-visibility": { input: { readonly open: boolean }; result: void };
  "preview.open-file": { input: { readonly context: ApplicationPreviewContextV1; readonly path: string }; result: void };
  "preview.open-diff": { input: { readonly threadId: string; readonly path: string; readonly selection: ApplicationDiffSelectionV1 }; result: void };
  "preview.open-artifact": { input: { readonly threadId: string; readonly artifactId: string; readonly sha256?: string; readonly page?: number }; result: void };
  "preview.open-tool": { input: { readonly toolId: "files" | "review" | "terminal" | "browser"; readonly activate?: boolean }; result: void };
  "preview.open-plugin": { input: { readonly entryId: string; readonly pluginId: string; readonly generation: string; readonly viewTypeId: string; readonly title: string; readonly icon?: ApplicationPreviewIconV1 }; result: void };
  "preview.open-project": { input: { readonly projectId: string; readonly title: string }; result: void };
  "preview.open-side-thread": { input: Record<string, never>; result: void };
  "preview.new-tab": { input: Record<string, never>; result: { readonly tabId: string } };
  "preview.focus-tab": { input: { readonly tabId?: string }; result: void };
  "preview.close-tab": { input: { readonly tabId: string }; result: void };
  "preview.reorder-tab": { input: { readonly tabId: string; readonly overTabId: string }; result: void };
  "preview.close-tabs": { input: { readonly tabIds: readonly string[] }; result: void };
  "preview.follow-project": { input: { readonly project?: { readonly id: string; readonly title: string } }; result: void };
  "preview.show-conversation": { input: Record<string, never>; result: void };
}
