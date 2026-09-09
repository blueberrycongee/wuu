import { useEffect, useRef, useState } from "react";
import type { InputImage } from "../shared/protocol";
import { imageSource } from "./ComposerMessages";
import { useI18n } from "./i18n";

export function AttachmentImage({ image, label, className, previewDisabled, onOpen }: {
  image: InputImage;
  label: string;
  className?: string;
  previewDisabled?: boolean;
  onOpen: (src: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [data, setData] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState("");
  const element = useRef<HTMLButtonElement & HTMLImageElement>(null);
  const generation = useRef(0);
  useEffect(() => {
    generation.current++; setData(""); setPreview(""); setError(""); setLoading(false);
    return () => { generation.current++; };
  }, [image.remote_ref]);
  useEffect(() => {
    if (!image.remote_ref?.startsWith("thread:") || image.data || !window.wuu.readRemoteAttachmentPreview || !element.current || typeof IntersectionObserver === "undefined") return;
    const requestGeneration = generation.current;
    const observer = new IntersectionObserver(entries => {
      if (!entries.some(entry => entry.isIntersecting)) return;
      observer.disconnect();
      void window.wuu.readRemoteAttachmentPreview!(image.remote_ref!).then(src => {
        if (generation.current === requestGeneration) setPreview(src);
      }).catch(() => { /* Original loading remains available for unsupported previews. */ });
    }, { rootMargin: "160px" });
    observer.observe(element.current);
    return () => observer.disconnect();
  }, [image.remote_ref, image.data]);
  const remote = Boolean(image.remote_ref && !image.data && !data);
  const labelOpen = t("composer.enlargeNamed", { name: label });
  const src = remote ? preview : imageSource(data ? { ...image, data } : image);
  async function open(): Promise<void> {
    if (previewDisabled || loading) return;
    if (!remote) { onOpen(src); return; }
    const requestGeneration = generation.current;
    setLoading(true); setError("");
    try {
      if (!window.wuu.readRemoteAttachment) throw new Error("Remote attachment loading is unavailable");
      const loaded = await window.wuu.readRemoteAttachment(image.remote_ref!);
      if (generation.current !== requestGeneration) return;
      setData(loaded);
      onOpen(imageSource({ ...image, data: loaded }));
    } catch (cause) {
      if (generation.current !== requestGeneration) return;
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally { if (generation.current === requestGeneration) setLoading(false); }
  }
  if (remote && !preview) return <>
    <button ref={element} type="button" className={className} disabled={loading || previewDisabled} aria-label={labelOpen} onClick={() => void open()}>
      {loading ? t("common.loadingEllipsis") : label}
    </button>
    {error ? <span role="alert">{error}</span> : null}
  </>;
  return <><img ref={element} className={className} src={src} alt={label} role={previewDisabled ? undefined : "button"}
    aria-busy={loading || undefined}
    tabIndex={previewDisabled ? -1 : 0} aria-label={previewDisabled ? undefined : labelOpen}
    onClick={() => void open()} onKeyDown={event => {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); void open(); }
    }} />{error ? <span role="alert">{error}</span> : null}</>;
}
