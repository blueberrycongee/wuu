import { useEffect, useRef, useState } from "react";
import { useI18n } from "./i18n";
import "./styles/video-preview.css";

export function VideoPreview({ src, title, active = true }: {
  src: string;
  title: string;
  active?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const videoRef = useRef<HTMLVideoElement>(null);
  const [failedSource, setFailedSource] = useState<string>();

  useEffect(() => {
    if (!active) videoRef.current?.pause();
  }, [active]);

  return (
    <div className="video-preview">
      {failedSource === src ? (
        <p className="video-preview-error" role="alert">{t("artifacts.videoUnavailable")}</p>
      ) : (
        <video
          key={src}
          ref={videoRef}
          src={src}
          aria-label={title}
          controls
          playsInline
          preload="metadata"
          onError={() => setFailedSource(src)}
        />
      )}
    </div>
  );
}
