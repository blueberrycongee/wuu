import { useEffect, useRef, useState } from "react";
import { Film, Play } from "./WuuIcons";
import type { InputFile } from "../shared/protocol";
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
  const videoRef = useRef<HTMLVideoElement>(null);
  const [activeSource, setActiveSource] = useState<string>();
  const [failedSource, setFailedSource] = useState<string>();
  const filename = file.filename || "video";
  const active = Boolean(src && activeSource === src);
  const failed = Boolean(src && failedSource === src);
  return (
    <div className="video-attachment">
      {failed ? (
        <p className="video-attachment-error" role="alert">{t("composer.attachment.videoPlaybackFailed")}</p>
      ) : src ? (
        <>
          <video
            key={src}
            ref={videoRef}
            src={src}
            controls={active}
            tabIndex={active ? 0 : -1}
            preload="metadata"
            playsInline
            aria-label={t("composer.attachment.playVideo", { name: filename })}
            onLoadedMetadata={event => {
              // Chromium may leave metadata-only previews blank until a seek.
              event.currentTarget.currentTime = 0.001;
            }}
            onError={() => setFailedSource(src)}
          />
          {!active ? (
            <button
              type="button"
              className="video-attachment-play"
              aria-label={t("composer.attachment.playVideo", { name: filename })}
              onClick={() => {
                const video = videoRef.current!;
                // Reveal native controls before moving keyboard focus to the player.
                video.controls = true;
                setActiveSource(src);
                video.focus();
                void video.play().catch(() => setFailedSource(src));
              }}
            >
              <span className="video-attachment-play-icon"><Play size={22} aria-hidden="true" /></span>
            </button>
          ) : null}
        </>
      ) : <Film size={24} aria-hidden="true" />}
    </div>
  );
}
