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
export interface PreviewFileSaveV1 extends PreviewFileTargetV1 {
  readonly text: string;
  readonly base_mtime_ms: number;
  readonly base_sha256: string;
}
export interface PreviewFileSaveResultV1 {
  readonly status: "saved" | "conflict";
  readonly file: PreviewFileV1;
}
/** Invalidation, not an ordered filesystem journal. Re-read the captured target. */
export type PreviewFileChangeV1 = PreviewFileTargetV1 & (
  { readonly type: "changed" } | { readonly type: "error"; readonly message: string }
);
/** The default preview retains docking and sizing around this replaceable region. */
export interface WorkspaceFileTreeContextV1 extends Readonly<Record<string, unknown>> {
  readonly root?: string;
  readonly visible: boolean;
  readonly selectedFilePath?: string;
}
export interface PreviewContentOptionsV1 { readonly signal?: AbortSignal }
/** Workspace file browsing uses the host's existing path and content policies. */
export interface ApplicationPreviewContentV1 {
  readonly capabilities: {
    readonly nativeItemMenu: boolean;
    readonly revealItem: boolean;
    readonly exportFile: boolean;
    /** Optional on older application providers. */
    readonly saveFile?: boolean;
    readonly watch?: boolean;
  };
  /** Resolve an embedded image through the host transport; unavailable resources return undefined. */
  resolveResourceURL(target: { readonly source: string; readonly cwd?: string }): string | undefined;
  openFile(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<void>;
  listDirectory(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<PreviewDirectoryV1>;
  readFile(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<PreviewFileV1>;
  /** Save an existing complete text file; a conflict returns the current file without overwriting it.
   * Cancellation cannot roll back a completed write. Check capabilities before calling. */
  saveFile?(input: PreviewFileSaveV1, options?: PreviewContentOptionsV1): Promise<PreviewFileSaveResultV1>;
  /** Observe one existing file or directory's entry changes, not descendants recursively.
   * Resolves when observation is installed; read the initial state afterwards.
   * Dispose on unmount. Abort, provider retirement and terminal errors also stop observation. */
  watch?(target: PreviewFileTargetV1, listener: (change: PreviewFileChangeV1) => void,
    options?: PreviewContentOptionsV1): Promise<() => void>;
  revealItem(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<void>;
  showItemMenu(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<{ readonly action: "none" }>;
  exportFile(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1): Promise<void>;
}
