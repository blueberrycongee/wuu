import { useEffect, useState } from "react";
import { Film } from "./WuuIcons";
import type { InputFile } from "../shared/protocol";
import { useI18n } from "./i18n";
import "./styles/video-attachment.css";

/** Stored bytes also back history previews; object URLs never enter persisted drafts. */
export function VideoAttachment({ file }: { file: InputFile }): JSX.Element {
  const { t } = useI18n();
  const [src, setSrc] = useState<string>();
  const [duration, setDuration] = useState(0);
  const filename = file.filename || "video";
  const bytes = file.data.length * 3 / 4 - (file.data.endsWith("==") ? 2 : file.data.endsWith("=") ? 1 : 0);
  const size = bytes < 1024 * 1024 ? `${Math.ceil(bytes / 1024)} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  useEffect(() => {
    setDuration(0);
    if (!file.data) { setSrc(undefined); return; }
    const decoded = atob(file.data);
    const bytes = new Uint8Array(decoded.length);
    for (let i = 0; i < decoded.length; i++) bytes[i] = decoded.charCodeAt(i);
    const url = URL.createObjectURL(new Blob([bytes], { type: file.media_type }));
    setSrc(url);
    return () => URL.revokeObjectURL(url);
  }, [file.data, file.media_type]);
  return <div className="video-attachment">
    {src ? <video src={src} controls preload="metadata" playsInline aria-label={t("composer.attachment.playVideo", { name: filename })}
      onLoadedMetadata={event => setDuration(event.currentTarget.duration)} /> : <Film size={20} aria-hidden="true" />}
    <span className="video-attachment-caption"><span title={filename}>{filename}</span><small>
      {duration > 0 && Number.isFinite(duration) ? `${Math.floor(duration / 60)}:${String(Math.floor(duration % 60)).padStart(2, "0")} · ` : ""}
      {size}
    </small></span>
  </div>;
}
