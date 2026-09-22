import {
  createContext,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type MouseEvent as ReactMouseEvent,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import { Download, Maximize2, Minus, RotateCw, X, ZoomIn } from "./WuuIcons";
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

type View = { scale: number | null; x: number; y: number };
const fittedView: View = { scale: null, x: 0, y: 0 };

function ImagePreviewOverlay({ item, onClose }: {
  item: ImagePreviewItem;
  onClose: () => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [view, setView] = useState<View>(fittedView);
  const [rotation, setRotation] = useState(0);
  const [imageSize, setImageSize] = useState({ width: 0, height: 0 });
  const [stageSize, setStageSize] = useState({ width: 0, height: 0 });
  const [loadStatus, setLoadStatus] = useState<"loading" | "loaded" | "error">("loading");
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const [dragging, setDragging] = useState(false);
  const overlayRef = useRef<HTMLDivElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<HTMLDivElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const suppressClick = useRef(false);
  const rotated = rotation % 180 !== 0;
  const width = rotated ? imageSize.height : imageSize.width;
  const height = rotated ? imageSize.width : imageSize.height;
  const fitScale = width && height
    ? Math.min(1, Math.max(1, stageSize.width - 32) / width, Math.max(1, stageSize.height - 32) / height)
    : 1;
  const minScale = Math.min(0.05, fitScale);
  const scale = view.scale ?? fitScale;
  const ready = loadStatus === "loaded";

  const constrain = useCallback((next: View): View => {
    const zoom = next.scale ?? fitScale;
    const maxX = Math.max(0, (width * zoom - stageSize.width) / 2 + 16);
    const maxY = Math.max(0, (height * zoom - stageSize.height) / 2 + 16);
    return { ...next, x: Math.max(-maxX, Math.min(maxX, next.x)), y: Math.max(-maxY, Math.min(maxY, next.y)) };
  }, [fitScale, width, height, stageSize]);
  const visibleView = constrain(view);

  const zoomAt = useCallback((factor: number, clientX?: number, clientY?: number, dx = 0, dy = 0) => {
    const bounds = stageRef.current!.getBoundingClientRect();
    const x = clientX == null ? 0 : clientX - bounds.left - bounds.width / 2;
    const y = clientY == null ? 0 : clientY - bounds.top - bounds.height / 2;
    setView(previous => {
      const current = constrain(previous);
      const currentScale = current.scale ?? fitScale;
      const nextScale = Math.max(minScale, Math.min(8, currentScale * factor));
      const ratio = nextScale / currentScale;
      return constrain({ scale: nextScale, x: x - (x - current.x) * ratio + dx, y: y - (y - current.y) * ratio + dy });
    });
  }, [constrain, fitScale, minScale]);

  const pan = useCallback((x: number, y: number) => {
    setView(previous => {
      const current = constrain(previous);
      return constrain({ ...current, x: current.x + x, y: current.y + y });
    });
  }, [constrain]);

  const rotate = useCallback(() => {
    setRotation(current => (current + 90) % 360);
    setView(fittedView);
  }, []);

  const saveImage = useCallback(async () => {
    if (!window.wuu?.saveArtifactFile || savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setSaveError("");
    const source = item.svg == null ? item.src : `data:image/svg+xml;charset=utf-8,${encodeURIComponent(item.svg)}`;
    try {
      await window.wuu.saveArtifactFile(item.title || (item.svg == null ? "image" : "image.svg"), source);
    } catch (error) {
      setSaveError(String(error));
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }, [item]);

  useEffect(() => {
    setView(fittedView);
    setRotation(0);
    setSaveError("");
    setImageSize({ width: 0, height: 0 });
    setLoadStatus("loading");
    pointers.current.clear();
    setDragging(false);
    if (item.svg != null) {
      const svg = svgRef.current!.querySelector("svg")!;
      const box = svg.getAttribute("viewBox")?.trim().split(/[\s,]+/).map(Number);
      const bounds = svg.getBoundingClientRect();
      setImageSize({ width: box?.[2] || bounds.width, height: box?.[3] || bounds.height });
      setLoadStatus("loaded");
    }
  }, [item.src, item.svg]);

  useEffect(() => {
    const stage = stageRef.current!;
    const measure = () => setStageSize({ width: stage.clientWidth, height: stage.clientHeight });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(stage);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    overlayRef.current!.focus();
    return () => {
      document.body.style.overflow = previousOverflow;
      previousFocus?.focus();
    };
  }, []);

  useEffect(() => {
    const stage = stageRef.current!;
    function wheel(event: WheelEvent): void {
      event.preventDefault();
      if (!ready) return;
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? stage.clientHeight : 1;
      if (event.ctrlKey || event.metaKey) {
        // Chromium reports a trackpad pinch as Ctrl + wheel, with small deltas.
        zoomAt(Math.exp(-event.deltaY * unit * 0.01), event.clientX, event.clientY);
      } else {
        pan(-event.deltaX * unit, -event.deltaY * unit);
      }
    }
    stage.addEventListener("wheel", wheel, { passive: false });
    return () => stage.removeEventListener("wheel", wheel);
  }, [ready, zoomAt, pan]);

  useEffect(() => {
    function keydown(event: KeyboardEvent): void {
      // Keep preview shortcuts (especially Escape) away from the conversation.
      event.stopPropagation();
      if (event.key === "Tab") {
        const buttons = Array.from(overlayRef.current!.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
        const next = event.shiftKey ? (index <= 0 ? buttons.length - 1 : index - 1) : (index + 1) % buttons.length;
        event.preventDefault();
        buttons[next]?.focus();
        return;
      }
      if (event.key === "Escape") { event.preventDefault(); onClose(); return; }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        if (ready) void saveImage();
        return;
      }
      if (!ready || event.metaKey || event.ctrlKey || event.altKey) return;
      switch (event.key) {
        case "+": case "=": zoomAt(1.25); break;
        case "-": zoomAt(1 / 1.25); break;
        case "0": setView(fittedView); break;
        case "1": setView({ scale: 1, x: 0, y: 0 }); break;
        case "r": case "R": rotate(); break;
        case "ArrowLeft": pan(60, 0); break;
        case "ArrowRight": pan(-60, 0); break;
        case "ArrowUp": pan(0, 60); break;
        case "ArrowDown": pan(0, -60); break;
        default: return;
      }
      event.preventDefault();
    }
    document.addEventListener("keydown", keydown, true);
    return () => document.removeEventListener("keydown", keydown, true);
  }, [onClose, ready, saveImage, zoomAt, pan, rotate]);

  function pointerDown(event: ReactPointerEvent<HTMLDivElement>): void {
    if (!ready || event.button !== 0) return;
    suppressClick.current = event.target !== event.currentTarget;
    pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
  }

  function pointerMove(event: ReactPointerEvent<HTMLDivElement>): void {
    const previous = pointers.current.get(event.pointerId);
    if (!previous) return;
    const next = { x: event.clientX, y: event.clientY };
    const other = Array.from(pointers.current.entries()).find(([id]) => id !== event.pointerId)?.[1];
    pointers.current.set(event.pointerId, next);
    if (previous.x === next.x && previous.y === next.y) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    suppressClick.current = true;
    setDragging(true);
    if (other) {
      const before = Math.hypot(previous.x - other.x, previous.y - other.y);
      const after = Math.hypot(next.x - other.x, next.y - other.y);
      if (before > 0) zoomAt(after / before, (previous.x + other.x) / 2, (previous.y + other.y) / 2,
        (next.x - previous.x) / 2, (next.y - previous.y) / 2);
    } else {
      pan(next.x - previous.x, next.y - previous.y);
    }
  }

  function endPointer(event: ReactPointerEvent<HTMLDivElement>): void {
    pointers.current.delete(event.pointerId);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (!pointers.current.size) setDragging(false);
  }

  function backgroundClick(event: ReactMouseEvent<HTMLDivElement>): void {
    if (event.target === event.currentTarget && !suppressClick.current) onClose();
    suppressClick.current = false;
  }

  const transform = `translate(${visibleView.x}px, ${visibleView.y}px) scale(${scale}) rotate(${rotation}deg)`;
  const canPan = width * scale > stageSize.width - 32 || height * scale > stageSize.height - 32;
  return (
    <div ref={overlayRef} className="image-preview-overlay" role="dialog" aria-modal="true"
      aria-label={t("imagePreview.label")} tabIndex={-1}>
      <div className="image-preview-toolbar">
        <div className="image-preview-toolbar-actions">
          {window.wuu?.saveArtifactFile && <button type="button" className="image-preview-toolbar-button"
            onClick={() => void saveImage()} disabled={!ready || saving}
            aria-label={t("imagePreview.saveAs")} title={t("imagePreview.saveAs")}>
            <Download className="icon" aria-hidden="true" />
          </button>}
          <button type="button" className="image-preview-toolbar-button" onClick={() => zoomAt(1 / 1.25)}
            disabled={!ready || scale <= minScale} aria-label={t("imagePreview.zoomOut")} title={t("imagePreview.zoomOut")}>
            <Minus className="icon" aria-hidden="true" />
          </button>
          <span className="image-preview-zoom-readout">
            {formatNumber(scale, { style: "percent", maximumFractionDigits: 0 })}
          </span>
          <button type="button" className="image-preview-toolbar-button" onClick={() => zoomAt(1.25)}
            disabled={!ready || scale >= 8} aria-label={t("imagePreview.zoomIn")} title={t("imagePreview.zoomIn")}>
            <ZoomIn className="icon" aria-hidden="true" />
          </button>
          <button type="button" className="image-preview-toolbar-button" onClick={() => setView(fittedView)}
            disabled={!ready} aria-label={t("imagePreview.fit")} title={t("imagePreview.fit")}>
            <Maximize2 className="icon" aria-hidden="true" />
          </button>
          <button type="button" className="image-preview-toolbar-button" onClick={() => setView({ scale: 1, x: 0, y: 0 })}
            disabled={!ready} aria-label={t("imagePreview.actualSize")} title={t("imagePreview.actualSize")}>
            <span className="image-preview-actual-size" aria-hidden="true">1:1</span>
          </button>
          <button type="button" className="image-preview-toolbar-button" onClick={rotate}
            disabled={!ready} aria-label={t("imagePreview.rotate")} title={t("imagePreview.rotate")}>
            <RotateCw className="icon" aria-hidden="true" />
          </button>
          <button type="button" className="image-preview-toolbar-button" onClick={onClose}
            aria-label={t("imagePreview.close")} title={t("imagePreview.closeShortcut")}>
            <X className="icon" aria-hidden="true" />
          </button>
        </div>
      </div>
      {saveError && <p className="image-preview-save-error" role="alert">{saveError}</p>}
      <div ref={stageRef} className="image-preview-stage" style={{ cursor: dragging ? "grabbing" : canPan ? "grab" : "zoom-in" }}
        onClick={backgroundClick} onPointerDown={pointerDown} onPointerMove={pointerMove}
        onPointerUp={endPointer} onPointerCancel={endPointer} onLostPointerCapture={endPointer}
        onDoubleClick={event => {
          if (!ready || event.target === event.currentTarget) return;
          if (scale > fitScale + 0.001) setView(fittedView);
          else zoomAt(Math.max(2, 1 / fitScale), event.clientX, event.clientY);
        }}>
        {loadStatus !== "loaded" && <div className={`image-preview-status${loadStatus === "error" ? " error" : ""}`}>
          {t(loadStatus === "error" ? "imagePreview.loadFailed" : "imagePreview.loading")}
        </div>}
        {item.svg != null ? (
          <div ref={svgRef} className="image-preview-image image-preview-svg loaded" role="img" aria-label={item.alt ?? ""}
            style={{ transform, width: imageSize.width || undefined, height: imageSize.height || undefined }}
            dangerouslySetInnerHTML={{ __html: item.svg }} />
        ) : (
          <img className={`image-preview-image${ready ? " loaded" : ""}`} src={item.src} alt={item.alt ?? ""}
            draggable={false} style={{ transform, width: imageSize.width || undefined, height: imageSize.height || undefined }}
            onLoad={event => {
              setImageSize({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight });
              setLoadStatus("loaded");
            }} onError={() => setLoadStatus("error")} />
        )}
      </div>
    </div>
  );
}
