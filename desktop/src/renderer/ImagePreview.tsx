import {
  createContext,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type UIEvent as ReactUIEvent,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import { Download, Minus, RotateCcw, X, ZoomIn } from "lucide-react";
import { useI18n } from "./i18n";

export type ImagePreviewItem =
  | { src: string; alt?: string; title?: string; svg?: undefined }
  | { src?: undefined; alt?: string; title?: string; svg: string };

export type ImagePreviewContextValue = {
  openPreview: (item: ImagePreviewItem) => void;
  closePreview: () => void;
};

const ImagePreviewContext = createContext<ImagePreviewContextValue | null>(null);

export function useImagePreview(): ImagePreviewContextValue {
  const value = useContext(ImagePreviewContext);
  if (!value) {
    throw new Error("useImagePreview must be used within ImagePreviewProvider");
  }
  return value;
}

export function useOptionalImagePreview(): ImagePreviewContextValue | null {
  return useContext(ImagePreviewContext);
}

const MIN_SCALE = 0.5;
const MAX_SCALE = 8;
const SCALE_STEP = 0.5;

function clampScale(value: number): number {
  if (!Number.isFinite(value)) {
    return 1;
  }
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, value));
}

function nextScale(current: number, direction: 1 | -1): number {
  const target = direction > 0 ? current + SCALE_STEP : current - SCALE_STEP;
  return clampScale(target);
}

export function ImagePreviewProvider({ children }: { children: ReactNode }): JSX.Element {
  const [item, setItem] = useState<ImagePreviewItem | null>(null);

  const openPreview = useCallback((next: ImagePreviewItem) => {
    setItem(next);
  }, []);
  const closePreview = useCallback(() => setItem(null), []);

  const value = useMemo<ImagePreviewContextValue>(
    () => ({ openPreview, closePreview }),
    [openPreview, closePreview]
  );

  return (
    <ImagePreviewContext.Provider value={value}>
      {children}
      {item ? <ImagePreviewOverlay item={item} onClose={closePreview} /> : null}
    </ImagePreviewContext.Provider>
  );
}

function ImagePreviewOverlay({
  item,
  onClose
}: {
  item: ImagePreviewItem;
  onClose: () => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [loadStatus, setLoadStatus] = useState<"loading" | "loaded" | "error">("loading");
  const [saveError, setSaveError] = useState("");
  const saveImage = (): void => {
    const source = item.svg == null ? item.src : `data:image/svg+xml;charset=utf-8,${encodeURIComponent(item.svg)}`;
    if (!source || !window.wuu?.saveArtifactFile) return;
    setSaveError("");
    void window.wuu.saveArtifactFile(item.title || (item.svg == null ? "image.png" : "image.svg"), source)
      .catch(error => setSaveError(String(error)));
  };
  const dragState = useRef<{ pointerId: number; startX: number; startY: number; baseX: number; baseY: number } | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setScale(1);
    setOffset({ x: 0, y: 0 });
    setLoadStatus("loading");
  }, [item.src, item.svg]);

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, []);

  useEffect(() => {
    function handleKey(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    }
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, [onClose]);

  const resetTransform = useCallback(() => {
    setScale(1);
    setOffset({ x: 0, y: 0 });
  }, []);

  const zoomIn = useCallback(() => {
    setScale((current) => nextScale(current, 1));
  }, []);
  const zoomOut = useCallback(() => {
    setScale((current) => nextScale(current, -1));
  }, []);

  const handleWheel = useCallback((event: WheelEvent) => {
    event.preventDefault();
    const direction = event.deltaY > 0 ? -1 : 1;
    setScale((current) => nextScale(current, direction));
  }, []);

  useEffect(() => {
    const node = viewportRef.current;
    if (!node) {
      return;
    }
    node.addEventListener("wheel", handleWheel, { passive: false });
    return () => node.removeEventListener("wheel", handleWheel);
  }, [handleWheel]);

  const handlePointerDown = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (scale <= 1) {
      return;
    }
    if (event.button !== 0) {
      return;
    }
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    dragState.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      baseX: offset.x,
      baseY: offset.y
    };
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>): void => {
    const drag = dragState.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    setOffset({
      x: drag.baseX + (event.clientX - drag.startX),
      y: drag.baseY + (event.clientY - drag.startY)
    });
  };

  const endDrag = (event: ReactPointerEvent<HTMLDivElement>): void => {
    const drag = dragState.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    dragState.current = null;
  };

  const handleBackgroundClick = (event: ReactUIEvent<HTMLDivElement>): void => {
    if (event.target === event.currentTarget) {
      onClose();
    }
  };

  const handleDoubleClick = (): void => {
    if (scale > 1) {
      resetTransform();
    } else {
      setScale(2);
    }
  };

  const handleImageLoad = (): void => setLoadStatus("loaded");
  const handleImageError = (): void => setLoadStatus("error");

  const transform = `translate(${offset.x}px, ${offset.y}px) scale(${scale})`;
  const cursor = scale > 1 ? "grab" : "zoom-in";

  return (
    <div
      ref={viewportRef}
      className="image-preview-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={t("imagePreview.label")}
      onClick={handleBackgroundClick}
    >
      <div className="image-preview-toolbar" onClick={(event) => event.stopPropagation()}>
        <div className="image-preview-toolbar-actions">
          {window.wuu?.saveArtifactFile && <button type="button" className="image-preview-toolbar-button" onClick={saveImage} aria-label={t("artifacts.downloadNamed", {name:item.title || "image"})}><Download className="icon" aria-hidden="true" /></button>}
          <button
            type="button"
            className="image-preview-toolbar-button"
            onClick={zoomOut}
            disabled={scale <= MIN_SCALE}
            aria-label={t("imagePreview.zoomOut")}
            title={t("imagePreview.zoomOut")}
          >
            <Minus className="icon" aria-hidden="true" />
          </button>
          <span className="image-preview-zoom-readout" aria-live="polite">
            {formatNumber(scale, {
              style: "percent",
              maximumFractionDigits: 0,
            })}
          </span>
          <button
            type="button"
            className="image-preview-toolbar-button"
            onClick={zoomIn}
            disabled={scale >= MAX_SCALE}
            aria-label={t("imagePreview.zoomIn")}
            title={t("imagePreview.zoomIn")}
          >
            <ZoomIn className="icon" aria-hidden="true" />
          </button>
          <button
            type="button"
            className="image-preview-toolbar-button"
            onClick={resetTransform}
            disabled={scale === 1 && offset.x === 0 && offset.y === 0}
            aria-label={t("imagePreview.resetZoom")}
            title={t("imagePreview.resetZoom")}
          >
            <RotateCcw className="icon" aria-hidden="true" />
          </button>
          <button
            type="button"
            className="image-preview-toolbar-button"
            onClick={onClose}
            aria-label={t("imagePreview.close")}
            title={t("imagePreview.closeShortcut")}
          >
            <X className="icon" aria-hidden="true" />
          </button>
        </div>
      </div>
      {saveError && <p className="image-preview-status error" role="alert">{saveError}</p>}
      <div
        className="image-preview-stage"
        style={{ cursor }}
        onClick={handleBackgroundClick}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onDoubleClick={handleDoubleClick}
      >
        {item.svg == null && loadStatus === "loading" ? (
          <div className="image-preview-status">{t("imagePreview.loading")}</div>
        ) : null}
        {item.svg == null && loadStatus === "error" ? (
          <div className="image-preview-status error">
            {t("imagePreview.loadFailed")}
          </div>
        ) : null}
        {item.svg != null ? (
          <div
            className="image-preview-image image-preview-svg loaded"
            role="img"
            aria-label={item.alt ?? ""}
            style={{ transform }}
            dangerouslySetInnerHTML={{ __html: item.svg }}
          />
        ) : (
          <img
            className={`image-preview-image${loadStatus === "loaded" ? " loaded" : ""}`}
            src={item.src}
            alt={item.alt ?? ""}
            draggable={false}
            style={{ transform }}
            onLoad={handleImageLoad}
            onError={handleImageError}
          />
        )}
      </div>
    </div>
  );
}
