import type { ApplicationPreviewContentV1, PreviewContentOptionsV1, PreviewFileTargetV1 } from "../../../../packages/plugin-sdk/src/ui-preview-content";
import type { WuuDesktopApi } from "../../shared/protocol";

type PreviewHost = Pick<WuuDesktopApi, "listWorkspaceDirectory" | "readWorkspaceFile" | "revealWorkspaceItem" | "showWorkspaceItemMenu" | "exportWorkspaceFile" | "unsupportedMethods" | "platform">;

export class ApplicationPreviewContent implements ApplicationPreviewContentV1 {
  private disposed = false;
  readonly capabilities: ApplicationPreviewContentV1["capabilities"];

  constructor(private readonly api: PreviewHost) {
    this.capabilities = Object.freeze({
      nativeItemMenu: api.platform === "darwin" && !api.unsupportedMethods?.includes("showWorkspaceItemMenu"),
      revealItem: !api.unsupportedMethods?.includes("revealWorkspaceItem"),
      exportFile: Boolean(api.exportWorkspaceFile) && !api.unsupportedMethods?.includes("exportWorkspaceFile"),
    });
  }

  dispose(): void { this.disposed = true; }

  private async run<T>(operation: () => Promise<T>, options?: PreviewContentOptionsV1): Promise<T> {
    if (this.disposed || options?.signal?.aborted) throw new DOMException("Preview content operation cancelled", "AbortError");
    const result = await operation();
    // Native operations cannot always be cancelled; never publish their late result.
    if (this.disposed || options?.signal?.aborted) throw new DOMException("Preview content operation cancelled", "AbortError");
    return result;
  }

  listDirectory({ root, path }: PreviewFileTargetV1, options?: PreviewContentOptionsV1) {
    return this.run(async () => {
      const result = await this.api.listWorkspaceDirectory(path, root);
      return Object.freeze({ ...result, entries: Object.freeze(result.entries.map(entry => Object.freeze({ ...entry }))) });
    }, options);
  }

  readFile({ root, path }: PreviewFileTargetV1, options?: PreviewContentOptionsV1) {
    return this.run(async () => Object.freeze({ ...await this.api.readWorkspaceFile(path, root) }), options);
  }

  revealItem(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) {
    return this.run(async () => {
      if (!this.capabilities.revealItem) throw new Error("Reveal item unavailable");
      await this.api.revealWorkspaceItem(this.absolutePath(target));
    }, options);
  }

  showItemMenu(target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) {
    return this.run(async () => {
      if (!this.capabilities.nativeItemMenu) throw new Error("Native item menu unavailable");
      return Object.freeze({ ...await this.api.showWorkspaceItemMenu(this.absolutePath(target)) });
    }, options);
  }

  exportFile({ root, path }: PreviewFileTargetV1, options?: PreviewContentOptionsV1) {
    return this.run(async () => {
      if (!this.capabilities.exportFile || !this.api.exportWorkspaceFile) throw new Error("Export file unavailable");
      await this.api.exportWorkspaceFile(path, root);
    }, options);
  }

  private absolutePath({ root, path }: PreviewFileTargetV1): string {
    if (/^(?:[A-Za-z]:[\\/]|[/\\])/.test(path)) return path;
    return `${root.replace(/[\\/]+$/, "")}/${path}`;
  }
}
