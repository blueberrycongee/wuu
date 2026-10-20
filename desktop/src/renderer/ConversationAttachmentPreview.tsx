import { createContext, useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { formatByteSize } from "./AttachmentFormat";
import { Download, LoaderCircle } from "./WuuIcons";

export type ConversationAttachmentRequest = {
  resourceId: string;
  name: string;
  mediaType: string;
  size: number;
  read: () => Promise<Blob>;
};
export const ConversationAttachmentPreviewContext = createContext<((request: ConversationAttachmentRequest) => void) | undefined>(undefined);
const rasterTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"]);
const textTypes = new Set(["application/json", "application/xml", "application/javascript", "image/svg+xml"]);

// These are remote verified byte snapshots, never workspace paths or artifact URIs.
// HTML, SVG and scripts are deliberately rendered as text, not embedded documents.
export function ConversationAttachmentPreview({ name, mediaType, size, read }: ConversationAttachmentRequest): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [result, setResult] = useState<{ url: string; text?: string }>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const raster = rasterTypes.has(mediaType);
  const text = mediaType.startsWith("text/") || textTypes.has(mediaType);
  const tooLarge = t("conversationAttachment.tooLarge");
  useEffect(() => {
    let live = true, url = "";
    setResult(undefined); setError("");
    void (async () => {
      try {
        const blob = await read();
        if (!live) return;
        if (blob.size > 20 * 1024 * 1024) throw new Error(tooLarge);
        const content = text ? await blob.slice(0, 512 * 1024).text() : undefined;
        if (!live) return;
        // Re-type only allowlisted raster bytes. All downloads remain inert.
        url = URL.createObjectURL(raster ? new Blob([blob], { type: mediaType }) : blob);
        setResult({ url, text: content });
      } catch (cause) { if (live) setError(cause instanceof Error ? cause.message : String(cause)); }
    })();
    return () => { live = false; if (url) URL.revokeObjectURL(url); };
  }, [read, mediaType, raster, text, attempt, tooLarge]);
  return <section className="conversation-attachment-preview" aria-label={name}>
    <header><span>{mediaType} · {formatByteSize(size, formatNumber)}</span>{result && <a className="settings-button" href={result.url} download={name}><Download />{t("conversationAttachment.download")}</a>}</header>
    {!result && !error && <p role="status"><LoaderCircle className="is-spinning" />{t("conversationAttachment.loading")}</p>}
    {error && <div role="alert"><p>{error}</p><button type="button" className="settings-button" onClick={() => setAttempt(value => value + 1)}>{t("conversationAttachment.retry")}</button></div>}
    {result && (raster ? <img src={result.url} alt={name} /> : text ? <>{size > 512 * 1024 && <p>{t("workspace.files.previewTruncated")}</p>}<pre>{result.text}</pre></> : <p>{t("conversationAttachment.unsupported")}</p>)}
  </section>;
}
