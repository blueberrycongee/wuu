import { createContext, useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { isMarkdownDocument, MarkdownDocumentReading } from "./MarkdownDocumentReading";
import { LoaderCircle } from "./WuuIcons";

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
  const { t } = useI18n();
  const [result, setResult] = useState<{ url?: string; text?: string }>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const raster = rasterTypes.has(mediaType);
  const text = mediaType.startsWith("text/") || textTypes.has(mediaType);
  const markdown = text && isMarkdownDocument(name, mediaType);
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
        // Only allowlisted raster bytes need an object URL; other content stays inert.
        if (raster) url = URL.createObjectURL(new Blob([blob], { type: mediaType }));
        setResult({ url, text: content });
      } catch (cause) { if (live) setError(cause instanceof Error ? cause.message : String(cause)); }
    })();
    return () => { live = false; if (url) URL.revokeObjectURL(url); };
  }, [read, mediaType, raster, text, attempt, tooLarge]);
  return <article className="conversation-attachment-preview workspace-file-preview readonly" aria-label={name}>
    {!result && !error && <div className="workspace-panel-empty" role="status"><LoaderCircle className="is-spinning" /><span>{t("conversationAttachment.loading")}</span></div>}
    {error && <div className="workspace-panel-empty" role="alert"><p>{error}</p><button type="button" className="settings-button" onClick={() => setAttempt(value => value + 1)}>{t("conversationAttachment.retry")}</button></div>}
    {result && text && size > 512 * 1024 && <p className="workspace-file-preview-notice" role="status">{t("workspace.files.previewTruncated")}</p>}
    {result && (raster ? <div className="workspace-file-image-preview"><img src={result.url} alt={name} /></div> : text ? (
      markdown ? <div className="workspace-file-editor-scroll markdown-reading"><MarkdownDocumentReading text={result.text ?? ""} linkPolicy="external-only" /></div> : <div className="workspace-file-code-scroll"><pre className="workspace-file-code">{result.text}</pre></div>
    ) : <div className="workspace-panel-empty" role="status"><span>{t("conversationAttachment.unsupported")}</span></div>)}
  </article>;
}
