import { PdfSelectionMenu } from "./PdfSelectionMenu";
import type { PdfPreviewSource } from "./PdfSelection";
import { Minus, Plus, RefreshCw } from "./WuuIcons";
import {
  type ReactNode,
  type RefObject,
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
type PageRenderedEvent = { error?: unknown };

// Return false when the owner replaces the source instead of retrying this one.
type RefreshPdfSource = (signal: AbortSignal) => Promise<boolean>;

export function WorkspacePdfPreview({
  url,
  title, source, active = true, initialPage, pageRequest, onRefresh,
}: {
  url: string;
  title: string;
  source?: PdfPreviewSource;
  active?: boolean;
  initialPage?: number;
  pageRequest?: string;
  onRefresh?: RefreshPdfSource;
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
      tabIndex={-1}
      className="workspace-file-pdf-preview"
      data-workspace-pdf-preview
      data-selectable
      data-wuu-component="workspace-pdf-preview"
    >
      {shadowRoot
        ? createPortal(
            <>
              <style>{pdfViewerCSS}</style>
              <style>{previewCSS}</style>
              <WorkspacePdfSurface key={JSON.stringify([url, source])} url={url} title={title}
                hostRef={hostRef} source={source} active={active} initialPage={initialPage} pageRequest={pageRequest} onRefresh={onRefresh} />
            </>,
            shadowRoot,
          )
        : null}
    </div>
  );
}

function WorkspacePdfSurface({ url, title, hostRef, source, active, initialPage, pageRequest, onRefresh }: {
  url: string;
  title: string;
  hostRef: RefObject<HTMLDivElement | null>;
  source?: PdfPreviewSource;
  active: boolean;
  initialPage?: number;
  pageRequest?: string;
  onRefresh?: RefreshPdfSource;
}): JSX.Element {
  const { t } = useI18n();
  const containerRef = useRef<HTMLDivElement>(null);
  const viewerElementRef = useRef<HTMLDivElement>(null);
  const viewerRef = useRef<PDFViewer | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retryPendingRef = useRef(false);
  const requestedPageRef = useRef(initialPage);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const [zoom, setZoom] = useState(100);
  const [pageNumber, setPageNumber] = useState(1);
  const [pageCount, setPageCount] = useState(0);

  useLayoutEffect(() => {
    requestedPageRef.current = initialPage;
  }, [initialPage, pageRequest]);

  useEffect(() => {
    const container = containerRef.current;
    const viewerElement = viewerElementRef.current;
    if (!container || !viewerElement) return;

    setReady(false); setError(""); setPageCount(0); setPageNumber(1);
    let active = true;
    let failed = false;
    let initialized = false;
    const lifetime = new AbortController();
    const eventBus = new EventBus();
    // PDF.js 5.4 supports abortSignal but does not yet declare it in its types.
    const viewerOptions = {
      container,
      viewer: viewerElement,
      eventBus,
      enableHWA: true,
      removePageBorders: true,
      abortSignal: lifetime.signal,
    };
    const viewer = new PDFViewer(viewerOptions);
    let loadingTask: ReturnType<typeof getDocument> | undefined;
    viewerRef.current = viewer;

    const onError = (nextError: unknown): void => {
      if (!active || failed) return;
      failed = true;
      retryPendingRef.current = false;
      setReady(false);
      setError(nextError instanceof Error ? nextError.message : translateCurrent("workspace.files.openFailed"));
    };
    const onPagesInit = (): void => {
      if (!active || failed) return;
      const page = requestedPageRef.current;
      viewer.currentScaleValue = "page-width";
      setZoom(Math.round(viewer.currentScale * 100));
      initialized = true;
      if (page && Number.isSafeInteger(page) && page >= 1 && page <= viewer.pagesCount) viewer.currentPageNumber = page;
      retryPendingRef.current = false;
      setReady(true);
    };
    const onScaleChanging = ({ scale }: ScaleChangingEvent): void => {
      if (!active) return;
      setZoom(Math.round(scale * 100));
    };
    const onPageChanging = ({ pageNumber: nextPage }: PageChangingEvent): void => {
      if (!active || failed) return;
      if (initialized) {
        requestedPageRef.current = nextPage;
        // PDF.js logs lazy page-fetch failures without dispatching pagerendered.
        // Observe the same cached request for the page the user navigated to.
        void viewer.pdfDocument!.getPage(nextPage).catch(onError);
      }
      setPageNumber(nextPage);
    };
    const onPageRendered = ({ error }: PageRenderedEvent): void => {
      if (error) onError(error);
    };
    eventBus.on("pagerendered", onPageRendered);
    eventBus.on("pagesinit", onPagesInit);
    eventBus.on("scalechanging", onScaleChanging);
    eventBus.on("pagechanging", onPageChanging);

    // PDF.js observes container height, but its fit preset must be reapplied
    // when the workspace panel changes width. Numeric zoom stays user-owned.
    let observedWidth = -1;
    let resizeFrame = 0;
    const resizeObserver = new ResizeObserver(() => {
      const width = container.clientWidth;
      if (width === observedWidth) return;
      observedWidth = width;
      cancelAnimationFrame(resizeFrame);
      if (width <= 0) return;
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = 0;
        if (active && container.clientWidth > 0 && viewer.currentScaleValue === "page-width") {
          viewer.currentScaleValue = "page-width";
        }
      });
    });
    resizeObserver.observe(container);

    const load = async (): Promise<void> => {
      // Workspace URLs address mutable paths. Let their owner refresh metadata
      // before loading bytes instead of retaining the pre-failure revision.
      if (attempt > 0 && onRefresh && !await onRefresh(lifetime.signal)) return;
      if (!active) return;
      loadingTask = getDocument({ url, isEvalSupported: false, rangeChunkSize: 256 * 1024 });
      const document = await loadingTask.promise;
      if (!active) return;
      setPageCount(document.numPages);
      viewer.setDocument(document);
      // setDocument is void; PDF.js reports asynchronous initialization errors
      // through pagesPromise, after loadingTask has already succeeded.
      await viewer.pagesPromise;
    };
    void load().catch(onError);

    return () => {
      active = false;
      eventBus.off("pagerendered", onPageRendered);
      eventBus.off("pagesinit", onPagesInit);
      eventBus.off("scalechanging", onScaleChanging);
      eventBus.off("pagechanging", onPageChanging);
      resizeObserver.disconnect();
      cancelAnimationFrame(resizeFrame);
      viewer.cleanup();
      // PDF.js accepts null to cancel renders and clear its document listeners.
      // Remove this suppression when its setDocument types include teardown.
      // @ts-expect-error PDF.js 5.4 omits the supported null teardown argument.
      viewer.setDocument(null);
      lifetime.abort();
      void loadingTask?.destroy().catch((error: unknown) => {
        console.warn("Could not destroy PDF preview loading task", error);
      });
      viewerRef.current = null;
    };
  }, [url, attempt]);

  useEffect(() => {
    const viewer = viewerRef.current;
    if (ready && viewer && initialPage && Number.isSafeInteger(initialPage) && initialPage >= 1 && initialPage <= viewer.pagesCount) viewer.currentPageNumber = initialPage;
  }, [initialPage, pageRequest]);

  function retry(): void {
    // A second click can arrive before React commits the loading state.
    if (!error || retryPendingRef.current) return;
    retryPendingRef.current = true;
    setError("");
    setReady(false);
    setAttempt(value => value + 1);
  }

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
    <>
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
        <div key={attempt} ref={containerRef} className="workspace-pdf-container">
          <div ref={viewerElementRef} className="pdfViewer" />
        </div>
        {!ready || error ? (
          <div className={`workspace-pdf-status${error ? " error" : ""}`} role={error ? "alert" : "status"}>
            {error ? <>
              <strong>{t("workspace.files.openFailedTitle")}</strong>
              <span>{error}</span>
            </> : <span>{t("workspace.files.opening")}</span>}
            {error || attempt > 0 ? (
              <button type="button" className="workspace-pdf-retry" aria-label={t("appState.retryAction")}
                title={t("appState.retryAction")} disabled={!error} onClick={retry}>
                <RefreshCw size={15} />
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
    </section>
    {/* The surface and menu share a revision-keyed lifetime. A refreshed source
        cannot label the previous viewer's text while its replacement loads. */}
    <PdfSelectionMenu hostRef={hostRef} source={source} active={active && ready && !error} />
    </>
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
