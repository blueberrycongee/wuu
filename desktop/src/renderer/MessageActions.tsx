import { VideoAttachment } from "./VideoAttachment";
import {
  AlertCircle,
  Check,
  ChevronDown,
  ChevronUp,
  Copy,
  FileText,
  PencilLine,
  Split,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { InputFile, InputImage } from "../shared/protocol";
import { AttachmentImage } from "./AttachmentImage";
import { useImagePreview } from "./ImagePreview";
import { useI18n } from "./i18n";

function fileNameParts(filename: string): { stem: string; extension: string } {
  const dot = filename.lastIndexOf(".");
  if (dot <= 0 || dot === filename.length - 1) {
    return { stem: filename, extension: "" };
  }
  return { stem: filename.slice(0, dot), extension: filename.slice(dot) };
}

function base64ByteLength(data: string): number {
  const payload = data.includes(",") ? data.slice(data.indexOf(",") + 1) : data;
  const padding = payload.endsWith("==") ? 2 : payload.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((payload.length * 3) / 4) - padding);
}

function formatFileSize(data: string): string {
  const bytes = base64ByteLength(data);
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const kilobytes = bytes / 1024;
  if (kilobytes < 1024) {
    return `${kilobytes < 10 ? kilobytes.toFixed(1) : Math.round(kilobytes)} KB`;
  }
  const megabytes = kilobytes / 1024;
  return `${megabytes < 10 ? megabytes.toFixed(1) : Math.round(megabytes)} MB`;
}

export function AgentMessageActions({
  getText,
  onFork,
  placement,
  showFork = true,
}: {
  getText: () => string;
  onFork?: () => void;
  placement: "overlay" | "persistent";
  showFork?: boolean;
}): JSX.Element {
  const { t } = useI18n();

  return (
    <div
      className="message-actions agent-message-actions"
      data-wuu-component="message-actions"
      data-wuu-placement={placement}
      aria-label={t("message.assistantActions")}
    >
      <MessageCopyButton getText={getText} className="message-action-button" iconSize={15} />
      {showFork ? <MessageForkButton onFork={onFork} /> : null}
    </div>
  );
}

export function MessageForkButton({ onFork }: { onFork?: () => void }): JSX.Element {
  const { t } = useI18n();
  return (
    <button
      className="message-action-button"
      type="button"
      aria-label={t("message.fork")}
      title={t("message.fork")}
      disabled={!onFork}
      onClick={onFork}
    >
      <Split size={15} />
    </button>
  );
}

export function MessageCopyButton({
  getText,
  className = "",
  iconSize = 14,
  idleLabel,
  copiedLabel,
  failedLabel,
}: {
  getText: () => string;
  className?: string;
  iconSize?: number;
  idleLabel?: string;
  copiedLabel?: string;
  failedLabel?: string;
}): JSX.Element {
  const { t } = useI18n();
  const resolvedIdleLabel = idleLabel ?? t("message.copy");
  const resolvedCopiedLabel = copiedLabel ?? t("message.copied");
  const resolvedFailedLabel = failedLabel ?? t("common.copyFailed");
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const resetTimerRef = useRef<number | undefined>(undefined);
  const label = copyState === "copied" ? resolvedCopiedLabel : copyState === "failed" ? resolvedFailedLabel : resolvedIdleLabel;

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== undefined) {
        window.clearTimeout(resetTimerRef.current);
      }
    };
  }, []);

  function showCopyState(nextState: "copied" | "failed"): void {
    if (resetTimerRef.current !== undefined) {
      window.clearTimeout(resetTimerRef.current);
    }
    setCopyState(nextState);
    resetTimerRef.current = window.setTimeout(() => {
      setCopyState("idle");
      resetTimerRef.current = undefined;
    }, 1200);
  }

  async function handleCopy(): Promise<void> {
    const text = getText();
    if (text.trim() === "") {
      showCopyState("failed");
      return;
    }
    try {
      const clipboard = navigator.clipboard;
      if (!clipboard?.writeText) {
        throw new Error("Clipboard API unavailable");
      }
      await clipboard.writeText(text);
      showCopyState("copied");
    } catch {
      showCopyState("failed");
    }
  }

  return (
    <button
      className={`message-copy-button ${className} ${copyState}`}
      type="button"
      aria-label={label}
      title={label}
      onClick={() => void handleCopy()}
    >
      {copyState === "copied" ? (
        <Check size={iconSize} />
      ) : copyState === "failed" ? (
        <AlertCircle size={iconSize} />
      ) : (
        <Copy size={iconSize} />
      )}
    </button>
  );
}

export function MessageEditButton({
  onEdit,
  className = "",
  iconSize = 14
}: {
  onEdit: () => void;
  className?: string;
  iconSize?: number;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <button
      className={`message-edit-button ${className}`}
      type="button"
      aria-label={t("message.editAndRetry")}
      title={t("message.editAndRetry")}
      onClick={onEdit}
    >
      <PencilLine size={iconSize} />
    </button>
  );
}

export function MessageImageGrid({
  images,
  onRemove,
  collapsedLimit,
}: {
  images: InputImage[];
  /** When provided, each image gets a remove button (used inside the inline editor). */
  onRemove?: (index: number) => void;
  /** Static message grids collapse after this many previews. Editors always show all images. */
  collapsedLimit?: number;
}): JSX.Element {
  const { t } = useI18n();
  const { openPreview } = useImagePreview();
  const [expanded, setExpanded] = useState(false);
  const effectiveLimit = onRemove ? undefined : collapsedLimit;
  const hasOverflow = Boolean(effectiveLimit && images.length > effectiveLimit);
  const collapsed = hasOverflow && !expanded;
  const visibleImages = collapsed && effectiveLimit ? images.slice(0, effectiveLimit) : images;
  const hiddenCount = effectiveLimit ? images.length - effectiveLimit : 0;

  useEffect(() => {
    setExpanded(false);
  }, [images.length]);

  return (
    <div className={`message-images${onRemove ? " message-images-editable" : ""}`}>
      {visibleImages.map((image, index) => {
        const label = t("composer.imageNumber", { number: index + 1 });
        const overflowPreview = collapsed && index === visibleImages.length - 1;
        return (
          <div className="message-image-frame" key={`${image.media_type}-${index}`}>
            <AttachmentImage image={image} label={label} className="message-image" previewDisabled={overflowPreview}
              onOpen={src => openPreview({ src, alt: label, title: label })} />
            {overflowPreview ? (
              <button
                type="button"
                className="message-image-overflow"
                aria-label={t("message.showMoreImages", { count: hiddenCount })}
                aria-expanded={false}
                onClick={() => setExpanded(true)}
              >
                +{hiddenCount}
              </button>
            ) : null}
            {onRemove ? (
              <button
                type="button"
                className="message-image-remove"
                aria-label={t("composer.removeImage", { number: index + 1 })}
                title={t("common.remove")}
                onClick={(event) => {
                  event.stopPropagation();
                  onRemove(index);
                }}
              >
                <X size={12} aria-hidden="true" />
              </button>
            ) : null}
          </div>
        );
      })}
      {hasOverflow && expanded ? (
        <button
          type="button"
          className="message-attachment-collapse"
          aria-expanded={true}
          onClick={() => setExpanded(false)}
        >
          <ChevronUp aria-hidden="true" />
          <span>{t("message.collapseImages")}</span>
        </button>
      ) : null}
    </div>
  );
}

export function MessageFileList({
  files,
  onRemove,
  collapsedLimit,
}: {
  files: InputFile[];
  /** When provided, each file gets a remove button (used inside the inline editor). */
  onRemove?: (index: number) => void;
  /** Static message lists collapse after this many files. Editors always show all files. */
  collapsedLimit?: number;
}): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const effectiveLimit = onRemove ? undefined : collapsedLimit;
  const hasOverflow = Boolean(effectiveLimit && files.length > effectiveLimit);
  const visibleFiles = hasOverflow && !expanded && effectiveLimit
    ? files.slice(0, effectiveLimit)
    : files;
  const hiddenCount = effectiveLimit ? files.length - effectiveLimit : 0;

  useEffect(() => {
    setExpanded(false);
  }, [files.length]);

  return (
    <div className={`message-files${onRemove ? " message-files-editable" : ""}`}>
      {visibleFiles.map((file, index) => {
        const filename = file.filename?.trim() || t("message.fileNumber", { number: index + 1 });
        const { stem, extension } = fileNameParts(filename);
        const kind = extension ? extension.slice(1).toUpperCase() : file.media_type.split("/").pop()?.toUpperCase();
        return (
          <div className="message-file-frame" key={`${file.media_type}-${file.filename ?? index}-${index}`}>
            {file.media_type.startsWith("video/") ? <VideoAttachment file={file} /> : <div className="message-file" title={filename}>
              <FileText className="icon" aria-hidden="true" />
              <span className="message-file-details">
                <span className="message-file-name">
                  <span className="message-file-name-stem">{stem}</span>
                  {extension ? <span className="message-file-name-extension">{extension}</span> : null}
                </span>
                <span className="message-file-meta">{kind ? `${kind} · ` : ""}{formatFileSize(file.data)}</span>
              </span>
            </div>}
            {onRemove ? (
              <button
                type="button"
                className="message-file-remove"
                aria-label={t("message.removeFileNamed", { name: filename })}
                title={t("common.remove")}
                onClick={() => onRemove(index)}
              >
                <X size={12} aria-hidden="true" />
              </button>
            ) : null}
          </div>
        );
      })}
      {hasOverflow ? (
        <button
          type="button"
          className="message-attachment-more"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
          <span>
            {expanded
              ? t("message.collapseFiles")
              : t("message.showMoreFiles", { count: hiddenCount })}
          </span>
        </button>
      ) : null}
    </div>
  );
}
