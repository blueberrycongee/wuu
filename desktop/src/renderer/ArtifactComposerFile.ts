import type { ArtifactPreviewRequest } from "./ArtifactPreviewContext";
import { COMPOSER_ATTACHMENT_MAX_BYTES, composerFileFromFile, type ComposerFile } from "./ComposerMessages";

/** Create only at submission, with the destination thread captured by the caller. */
export function createArtifactComposerFile(request: ArtifactPreviewRequest, threadID: string): ComposerFile {
  const { artifact } = request;
  if (!threadID || request.threadID !== threadID) {
    throw new Error("The preview belongs to a different conversation. Open it in its original conversation to continue.");
  }
  const uri = artifact.uri;
  const source = uri ? new URL(uri) : undefined;
  // The host's managed-artifact protocol verifies the manifest and snapshot
  // digest. Bind its existing read capability to this submission's owner.
  if (artifact.mimeType !== "application/pdf" || !source || source.protocol !== "wuu-artifact:"
    || decodeURIComponent(source.pathname.split("/")[1] ?? "") !== threadID) {
    throw new Error("This preview cannot be attached as a PDF from the current conversation.");
  }
  if (artifact.sizeBytes !== undefined && artifact.sizeBytes > COMPOSER_ATTACHMENT_MAX_BYTES) {
    throw new Error("The preview exceeds the 20MB attachment limit.");
  }
  const filename = artifact.name;
  const id = `artifact:${JSON.stringify([threadID, uri])}`;
  const file: ComposerFile = { id, filename, media_type: "application/pdf", data: "" };
  const encodePromise = (async () => {
      const response = await fetch(uri!);
      if (!response.ok || !response.body) {
        throw new Error(`Could not read the PDF preview (${response.status}). Reopen it and try again.`);
      }
      const reader = response.body.getReader();
      const chunks: Uint8Array<ArrayBuffer>[] = [];
      let size = 0;
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > COMPOSER_ATTACHMENT_MAX_BYTES) {
            await reader.cancel();
            throw new Error("The preview exceeds the 20MB attachment limit.");
          }
          chunks.push(new Uint8Array(value));
        }
      } finally {
        reader.releaseLock();
      }
      const encoded = await composerFileFromFile(new File(chunks, filename, { type: file.media_type }));
      return { ...encoded, id };
    })();
  // Queue admission may await other work before it consumes these bytes. Mark
  // an early rejection observed without replacing the promise the sender awaits.
  void encodePromise.catch(() => undefined);
  return { ...file, encodePromise };
}
