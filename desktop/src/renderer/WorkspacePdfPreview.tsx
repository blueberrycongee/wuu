import { Minus, Plus } from "./WuuIcons";
import {
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import {
  GlobalWorkerOptions,
  getDocument,
} from "pdfjs-dist";
import pdfWorkerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import pdfViewerCSS from "pdfjs-dist/web/pdf_viewer.css?inline";
import { EventBus, PDFViewer } from "pdfjs-dist/web/pdf_viewer.mjs";
import previewCSS from "./styles/workspace-pdf-preview.css?inline";
import { translateCurrent, useI18n } from "./i18n";

GlobalWorkerOptions.workerSrc = pdfWorkerURL;

type ScaleChangingEvent = { scale: number };
type PageChangingEvent = { pageNumber: number };

export function WorkspacePdfPreview({
  url,
  title,
}: {
  url: string;
  title: string;
}): JSX.Element {
  const hostRef = useRef<HTMLDivElement>(null);
  const [shadowRoot, setShadowRoot] = useState<ShadowRoot>();

  useLayoutEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setShadowRoot(host.shadowRoot ?? host.attachShadow({ mode: "open" }));
  }, []);

  return (
    <div
      ref={hostRef}
      className="workspace-file-pdf-preview"
      data-workspace-pdf-preview
      data-wuu-component="workspace-pdf-preview"
    >
      {shadowRoot
        ? createPortal(
            <>
              <style>{pdfViewerCSS}</style>
              <style>{previewCSS}</style>
              <WorkspacePdfSurface url={url} title={title} />
            </>,
            shadowRoot,
          )
        : null}
    </div>
  );
}

function WorkspacePdfSurface({ url, title }: { url: string; title: string }): JSX.Element {
  const { t } = useI18n();
  const containerRef = useRef<HTMLDivElement>(null);
  const viewerElementRef = useRef<HTMLDivElement>(null);
  const viewerRef = useRef<PDFViewer | null>(null);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const [zoom, setZoom] = useState(100);
  const [pageNumber, setPageNumber] = useState(1);
  const [pageCount, setPageCount] = useState(0);

  useEffect(() => {
    const container = containerRef.current;
    const viewerElement = viewerElementRef.current;
    if (!container || !viewerElement) return;

    let active = true;
    const eventBus = new EventBus();
    const viewer = new PDFViewer({
      container,
      viewer: viewerElement,
      eventBus,
      enableHWA: true,
      removePageBorders: true,
    });
    const loadingTask = getDocument({
      url,
      isEvalSupported: false,
      rangeChunkSize: 256 * 1024,
    });
    viewerRef.current = viewer;

    const onPagesInit = (): void => {
      viewer.currentScaleValue = "page-width";
      setZoom(Math.round(viewer.currentScale * 100));
      setReady(true);
    };
    const onScaleChanging = ({ scale }: ScaleChangingEvent): void => {
      setZoom(Math.round(scale * 100));
    };
    const onPageChanging = ({ pageNumber: nextPage }: PageChangingEvent): void => {
      setPageNumber(nextPage);
    };
    eventBus.on("pagesinit", onPagesInit);
    eventBus.on("scalechanging", onScaleChanging);
    eventBus.on("pagechanging", onPageChanging);

    void loadingTask.promise
      .then((document) => {
        if (!active) return;
        setPageCount(document.numPages);
        viewer.setDocument(document);
      })
      .catch((nextError: unknown) => {
        if (!active) return;
        setError(
          nextError instanceof Error
            ? nextError.message
            : translateCurrent("workspace.files.openFailed"),
        );
      });

    return () => {
      active = false;
      eventBus.off("pagesinit", onPagesInit);
      eventBus.off("scalechanging", onScaleChanging);
      eventBus.off("pagechanging", onPageChanging);
      viewer.cleanup();
      void loadingTask.destroy();
      viewerRef.current = null;
    };
  }, [url]);

  function updateScale(action: "decrease" | "increase" | "fit"): void {
    const viewer = viewerRef.current;
    if (!viewer) return;
    if (action === "decrease") {
      viewer.decreaseScale();
    } else if (action === "increase") {
      viewer.increaseScale();
    } else {
      viewer.currentScaleValue = "page-width";
    }
  }

  return (
    <section className="workspace-pdf-shell" aria-label={title}>
      <header className="workspace-pdf-toolbar">
        <span className="workspace-pdf-page-count" aria-live="polite">
          {pageCount > 0 ? `${pageNumber} / ${pageCount}` : ""}
        </span>
        <div className="workspace-pdf-zoom-controls">
          <PdfToolbarButton
            label={t("imagePreview.zoomOut")}
            disabled={!ready}
            onClick={() => updateScale("decrease")}
          >
            <Minus size={15} />
          </PdfToolbarButton>
          <button
            type="button"
            className="workspace-pdf-zoom-value"
            disabled={!ready}
            title={t("imagePreview.resetZoom")}
            onClick={() => updateScale("fit")}
          >
            {zoom}%
          </button>
          <PdfToolbarButton
            label={t("imagePreview.zoomIn")}
            disabled={!ready}
            onClick={() => updateScale("increase")}
          >
            <Plus size={15} />
          </PdfToolbarButton>
        </div>
      </header>
      <div className="workspace-pdf-viewport">
        <div ref={containerRef} className="workspace-pdf-container">
          <div ref={viewerElementRef} className="pdfViewer" />
        </div>
        {!ready && !error ? (
          <div className="workspace-pdf-status" role="status">
            {t("workspace.files.opening")}
          </div>
        ) : null}
        {error ? (
          <div className="workspace-pdf-status error" role="alert">
            <strong>{t("workspace.files.openFailedTitle")}</strong>
            <span>{error}</span>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function PdfToolbarButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}): JSX.Element {
  return (
    <button type="button" aria-label={label} disabled={disabled} onClick={onClick}>
      {children}
    </button>
  );
}
