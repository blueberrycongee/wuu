import type { ApplicationAttachmentV1, ApplicationComposerDraftV1 } from "../../../../packages/plugin-sdk/src/ui-composer";
import type { ComposerDraftState } from "../AppState";
import { composerFilePlaceholder, composerImagePlaceholder, isComposerImageFile,
  revokeComposerImagePreview, type ComposerFile, type ComposerImage } from "../ComposerMessages";

type Resource = { referenced?: boolean; kind: "image" | "file"; value: ComposerImage | ComposerFile };

/** Own asynchronous byte preparation independently of mounted composers. */
export class ApplicationDraftResources {
  private readonly resources = new Map<string, Resource>();
  constructor(private readonly changed: () => void) {}

  attach(files: readonly File[]): readonly ApplicationAttachmentV1[] {
    const resources = files.map(file => {
      const kind = isComposerImageFile(file) ? "image" : "file";
      const value = kind === "image" ? composerImagePlaceholder(file) : composerFilePlaceholder(file);
      this.remember(kind, value);
      return this.describe(value.id);
    });
    this.changed();
    return Object.freeze(resources);
  }

  remember(kind: Resource["kind"], value: Resource["value"]): void {
    const previous = this.resources.get(value.id);
    // Draft snapshots may retain a pending placeholder after its bytes settle.
    if (previous?.value === value || value.preparation !== undefined && previous?.value.preparation === value.preparation) return;
    this.resources.set(value.id, { kind, value, referenced: previous?.referenced });
    const pending = value.preparation?.promise ?? value.encodePromise;
    if (pending) void pending.then(() => this.changed(), () => this.changed());
  }

  import(attachments: readonly ApplicationAttachmentV1[]): void {
    // Validate the complete batch before publishing any resource.
    const batch = new Map<string, ApplicationAttachmentV1>();
    for (const attachment of attachments) {
      if (!attachment.id.trim()) throw new Error("Attachment identity cannot be empty");
      if (attachment.status !== "ready" || attachment.data === undefined && !attachment.remoteRef) throw new Error("Only ready attachment content can be imported");
      const existing = batch.get(attachment.id) ?? (this.resources.has(attachment.id) ? this.describe(attachment.id) : undefined);
      if (existing) {
        for (const key of ["kind", "name", "mediaType", "data", "remoteRef", "width", "height"] as const) {
          if ((key === "data" ? existing.data ?? "" : existing[key]) !== (key === "data" ? attachment.data ?? "" : attachment[key])) throw new Error(`Composer attachment identity conflict: ${attachment.id}`);
        }
      }
      batch.set(attachment.id, attachment);
    }
    for (const attachment of batch.values()) {
      if (this.resources.has(attachment.id)) continue;
      this.remember(attachment.kind, { id: attachment.id, media_type: attachment.mediaType, data: attachment.data ?? "", remote_ref: attachment.remoteRef,
        ...(attachment.kind === "file" ? { filename: attachment.name } : { width: attachment.width, height: attachment.height }) });
    }
  }

  reconcile(references: ReadonlySet<string>): void {
    for (const [id, resource] of this.resources) {
      if (references.has(id)) resource.referenced = true;
      else if (resource.referenced) this.release([id]);
    }
  }

  release(ids: readonly string[]): void {
    for (const id of ids) {
      const resource = this.resources.get(id);
      if (resource?.kind === "image") revokeComposerImagePreview(resource.value as ComposerImage);
      this.resources.delete(id);
    }
  }

  private require(id: string): Resource {
    const resource = this.resources.get(id);
    if (!resource) throw new Error(`Unknown composer attachment: ${id}`);
    return resource;
  }

  describe(id: string): ApplicationAttachmentV1 {
    return this.describeResource(id, this.require(id));
  }

  private describeResource(id: string, { kind, value }: Resource): ApplicationAttachmentV1 {
    const preparation = value.preparation;
    const ready = preparation?.result ?? value;
    const error = preparation?.error;
    const pending = Boolean((preparation || value.encodePromise) && !preparation?.result && !error);
    return Object.freeze({ id, kind, name: "filename" in ready && ready.filename || preparation?.filename || "image",
      mediaType: ready.media_type, status: error ? "failed" : pending ? "pending" : "ready",
      previewUrl: "previewSrc" in value ? value.previewSrc : undefined, error,
      data: pending || error ? undefined : ready.data, remoteRef: ready.remote_ref,
      width: "width" in ready ? ready.width : undefined, height: "height" in ready ? ready.height : undefined });
  }

  async resolve(id: string, retry = false): Promise<ApplicationAttachmentV1> {
    const resource = this.require(id);
    const { value } = resource;
    const preparation = value.preparation;
    if (retry && preparation?.error) {
      if (preparation.retry) await preparation.retry();
      else preparation.restart();
      this.changed();
    }
    const pending = preparation?.promise ?? value.encodePromise;
    if (pending) await pending;
    // An admitted read retains its bytes even after the draft releases its lookup.
    return this.describeResource(id, resource);
  }

  read(input: ApplicationComposerDraftV1): ComposerDraftState {
    const resolve = (id: string, kind: Resource["kind"]): Resource["value"] => {
      const resource = this.require(id);
      if (resource.kind !== kind) throw new Error(`Attachment ${id} is not a ${kind}`);
      return resource.value;
    };
    return { prompt: input.text, images: input.imageIds.map(id => resolve(id, "image") as ComposerImage),
      files: input.fileIds.map(id => resolve(id, "file") as ComposerFile),
      selections: input.selections.map(selection => ({ ...selection, source: { ...selection.source } })) };
  }

  project(draft: ComposerDraftState): ApplicationComposerDraftV1 {
    return Object.freeze({ text: draft.prompt, imageIds: Object.freeze(draft.images.map(image => image.id)),
      fileIds: Object.freeze(draft.files.map(file => file.id)),
      selections: Object.freeze((draft.selections ?? []).map(selection => Object.freeze({ ...selection, source: Object.freeze({ ...selection.source }) }))) });
  }
}
