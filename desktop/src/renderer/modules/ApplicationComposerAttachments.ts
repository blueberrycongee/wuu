import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationAttachmentV1 } from "../../../../packages/plugin-sdk/src/ui-composer";
import type { ComposerAttachmentPreparation, ComposerFile, ComposerImage } from "../ComposerMessages";

type Attachment = ComposerImage | ComposerFile;

/** Remove this adapter when the attachment tray and send helpers consume SDK resources directly. */
export function createApplicationComposerAttachments(
  application: ApplicationServiceV1,
  changed: (kind: ApplicationAttachmentV1["kind"], value: Attachment) => void,
): { read: (descriptor: ApplicationAttachmentV1) => Attachment; dispose: () => void; resume: () => void; nameFor: (id: string) => string | undefined; retainVisible: (ids: readonly string[]) => void } {
  const values = new Map<string, Attachment>();
  const names = new Map<string, string>();
  let active = true;
  function encoded(descriptor: ApplicationAttachmentV1): Attachment {
    const content = { id: descriptor.id, media_type: descriptor.mediaType, data: descriptor.data ?? "",
      ...(descriptor.remoteRef ? { remote_ref: descriptor.remoteRef } : {}) };
    return descriptor.kind === "file" ? { ...content, filename: descriptor.name } : {
      ...content, ...(descriptor.width === undefined ? {} : { width: descriptor.width }),
      ...(descriptor.height === undefined ? {} : { height: descriptor.height }),
    };
  }
  function read(descriptor: ApplicationAttachmentV1): Attachment {
    names.set(descriptor.id, descriptor.name);
    const existing = values.get(descriptor.id);
    if (existing) return existing.preparation?.result ?? existing;
    if (descriptor.status === "ready") {
      const value = encoded(descriptor);
      values.set(descriptor.id, value);
      return value;
    }
    const preparation: ComposerAttachmentPreparation<Attachment> = {
      filename: descriptor.name,
      promise: undefined!,
      error: descriptor.error,
      restart: () => { start(true); },
      retry: async () => { await start(true).catch(() => {}); },
    };
    const value: Attachment = { ...encoded(descriptor), preparation,
      ...(descriptor.kind === "image" && descriptor.previewUrl
        ? { previewSrc: descriptor.previewUrl, applicationOwnedPreview: true as const } : {}) };
    values.set(descriptor.id, value);
    function start(retry: boolean): Promise<Attachment> {
      preparation.error = undefined;
      const pending = application.invoke(retry ? "composer.retry-attachment" : "composer.resolve-attachment", { attachmentId: descriptor.id })
        .then(result => {
          const ready = encoded(result);
          preparation.result = ready;
          preparation.retry = undefined;
          if (active) changed(descriptor.kind, ready);
          return ready;
        }, error => {
          preparation.error = error instanceof Error ? error.message : String(error);
          if (active) changed(descriptor.kind, { ...value, encodePromise: undefined });
          throw error;
        });
      preparation.promise = pending;
      value.encodePromise = pending;
      // The tray can display the failure before a send awaits the same promise.
      void pending.catch(() => {});
      return pending;
    }
    start(false);
    return value;
  }
  return { read, nameFor: id => names.get(id),
    retainVisible: ids => { const visible = new Set(ids); for (const id of values.keys()) if (!visible.has(id)) values.delete(id); },
    dispose: () => { active = false; values.clear(); }, resume: () => { active = true; } };
}
