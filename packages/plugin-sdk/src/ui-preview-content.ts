/** A file target retains its workspace across navigation and asynchronous work. */
export interface PreviewFileTargetV1 {
  readonly root: string;
  readonly path: string;
}
export interface PreviewDirectoryEntryV1 {
  readonly name: string;
  readonly path: string;
  readonly kind: "directory" | "file";
}
export interface PreviewDirectoryV1 extends PreviewFileTargetV1 {
  readonly entries: readonly PreviewDirectoryEntryV1[];
  readonly truncated: boolean;
}
export interface PreviewFileV1 extends PreviewFileTargetV1 {
  readonly absolute_path: string;
  readonly size_bytes: number;
  readonly mtime_ms: number;
  readonly sha256: string;
  readonly binary: boolean;
  readonly truncated: boolean;
  readonly text?: string;
  readonly renderable_url?: string;
  readonly renderable_kind?: "image" | "pdf" | "video";
}
export interface PreviewContentOptionsV1 { readonly signal?: AbortSignal }
/** Workspace file browsing uses the host's existing path and content policies. */
export interface ApplicationPreviewContentV1 {
  readonly capabilities: {
    readonly nativeItemMenu: boolean;
    readonly revealItem: boolean;
    readonly exportFile: boolean;
  };
  listDirectory(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<PreviewDirectoryV1>;
  readFile(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<PreviewFileV1>;
  revealItem(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<void>;
  showItemMenu(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<{ readonly action: "none" }>;
  exportFile(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<void>;
}
