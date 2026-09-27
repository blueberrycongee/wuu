import { useEffect, useState } from "react";
import { Film } from "./WuuIcons";
import type { InputFile } from "../shared/protocol";
import { formatFileSize } from "./AttachmentFormat";
import { useI18n } from "./i18n";
import "./styles/video-attachment.css";

/** Stored bytes also back history previews; object URLs never enter persisted drafts. */
export function useVideoObjectURL(file: InputFile): string | undefined {
  const [src, setSrc] = useState<string>();
  useEffect(() => {
    if (!file.data) { setSrc(undefined); return; }
    const decoded = atob(file.data);
    const bytes = new Uint8Array(decoded.length);
    for (let i = 0; i < decoded.length; i++) bytes[i] = decoded.charCodeAt(i);
    const url = URL.createObjectURL(new Blob([bytes], { type: file.media_type }));
    setSrc(url);
    return () => URL.revokeObjectURL(url);
  }, [file.data, file.media_type]);
  return src;
}

export function formatVideoDuration(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, "0")}`;
}

export function VideoAttachment({ file }: { file: InputFile }): JSX.Element {
  const { t } = useI18n();
  const src = useVideoObjectURL(file);
  const [duration, setDuration] = useState(0);
  const filename = file.filename || "video";
  useEffect(() => setDuration(0), [src]);
  return <div className="video-attachment">
    {src ? <video src={src} controls preload="metadata" playsInline aria-label={t("composer.attachment.playVideo", { name: filename })}
      onLoadedMetadata={event => setDuration(event.currentTarget.duration)} /> : <Film size={20} aria-hidden="true" />}
    <span className="video-attachment-caption"><span title={filename}>{filename}</span><small>
      {duration > 0 && Number.isFinite(duration) ? `${formatVideoDuration(duration)} · ` : ""}
      {formatFileSize(file.data)}
    </small></span>
  </div>;
}
