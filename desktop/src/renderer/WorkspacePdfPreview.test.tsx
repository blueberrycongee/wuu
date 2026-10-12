import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const pdfMocks = vi.hoisted(() => {
  const globalWorkerOptions: { workerSrc?: string } = {};
  const loadingTask = {
    promise: Promise.resolve({ numPages: 3 }),
    destroy: vi.fn(() => Promise.resolve()),
  };
  const viewer = {
    currentScale: 1.25,
    currentScaleValue: "",
    currentPageNumber: 1,
    pagesCount: 3,
    setDocument: vi.fn(),
    pagesPromise: Promise.resolve() as Promise<void>,
    pdfDocument: { getPage: vi.fn(async (_page: number) => ({})) },
    cleanup: vi.fn(),
    decreaseScale: vi.fn(),
    increaseScale: vi.fn(),
  };
  return {
    eventBus: undefined as
      | {
          dispatch: (name: string, value: unknown) => void;
        }
      | undefined,
    getDocument: vi.fn((_options: { url: string; isEvalSupported: boolean; rangeChunkSize: number }) => loadingTask),
    globalWorkerOptions,
    loadingTask,
    viewer,
    viewerSignals: [] as AbortSignal[],
  };
});

vi.mock("pdfjs-dist", () => ({
  GlobalWorkerOptions: pdfMocks.globalWorkerOptions,
  getDocument: pdfMocks.getDocument,
}));

vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({
  default: "pdf-worker.mjs",
}));

vi.mock("pdfjs-dist/web/pdf_viewer.css?inline", () => ({
  default: ".pdfViewer {}",
}));

vi.mock("./styles/workspace-pdf-preview.css?inline", () => ({
  default: ".workspace-pdf-shell {}",
}));

vi.mock("pdfjs-dist/web/pdf_viewer.mjs", () => ({
  EventBus: class {
    private handlers = new Map<string, Set<(value: unknown) => void>>();

    constructor() {
      pdfMocks.eventBus = this;
    }

    on(name: string, handler: (value: unknown) => void): void {
      const handlers = this.handlers.get(name) ?? new Set();
      handlers.add(handler);
      this.handlers.set(name, handlers);
    }

    off(name: string, handler: (value: unknown) => void): void {
      this.handlers.get(name)?.delete(handler);
    }

    dispatch(name: string, value: unknown): void {
      for (const handler of this.handlers.get(name) ?? []) handler(value);
    }
  },
  PDFViewer: class {
    constructor(options: { abortSignal: AbortSignal }) {
      pdfMocks.viewerSignals.push(options.abortSignal);
      return pdfMocks.viewer;
    }
  },
}));

vi.mock("./i18n", () => ({
  useI18n: () => ({ t: (key: string) => key }),
  translateCurrent: (key: string) => key,
}));

import { WorkspacePdfPreview } from "./WorkspacePdfPreview";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  pdfMocks.getDocument.mockReset();
  pdfMocks.getDocument.mockReturnValue(pdfMocks.loadingTask);
  pdfMocks.viewerSignals.length = 0;
  pdfMocks.loadingTask.destroy.mockClear();
  pdfMocks.viewer.setDocument.mockReset();
  pdfMocks.viewer.pagesPromise = Promise.resolve();
  pdfMocks.viewer.pdfDocument.getPage.mockReset().mockResolvedValue({});
  pdfMocks.viewer.cleanup.mockClear();
  pdfMocks.viewer.decreaseScale.mockClear();
  pdfMocks.viewer.increaseScale.mockClear();
  pdfMocks.viewer.currentScaleValue = "";
  pdfMocks.viewer.currentPageNumber = 1;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe("WorkspacePdfPreview", () => {
  it("loads through PDF.js and exposes lazy viewer zoom controls in its shadow root", async () => {
    await act(async () => {
      root.render(
        <WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" />,
      );
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(pdfMocks.globalWorkerOptions.workerSrc).toBe("pdf-worker.mjs");
    expect(pdfMocks.getDocument).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "wuu-file://local/document",
        isEvalSupported: false,
        rangeChunkSize: 256 * 1024,
      }),
    );
    expect(pdfMocks.viewer.setDocument).toHaveBeenCalledWith({ numPages: 3 });

    await act(async () => {
      pdfMocks.eventBus?.dispatch("pagesinit", {});
      pdfMocks.eventBus?.dispatch("scalechanging", { scale: 1.5 });
    });

    const host = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]");
    const shadowRoot = host?.shadowRoot;
    expect(shadowRoot?.textContent).toContain("1 / 3");
    expect(shadowRoot?.textContent).toContain("150%");
    expect(pdfMocks.viewer.currentScaleValue).toBe("page-width");

    const zoomIn = shadowRoot?.querySelector<HTMLButtonElement>(
      'button[aria-label="imagePreview.zoomIn"]',
    );
    act(() => zoomIn?.click());
    expect(pdfMocks.viewer.increaseScale).toHaveBeenCalledOnce();
  });

  it("destroys PDF.js resources when the preview closes", async () => {
    await act(async () => {
      root.render(
        <WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" />,
      );
      await Promise.resolve();
    });

    act(() => root.unmount());
    root = createRoot(container);

    expect(pdfMocks.viewer.cleanup).toHaveBeenCalledOnce();
    expect(pdfMocks.loadingTask.destroy).toHaveBeenCalledOnce();
  });

  it("retries a failed load once in place and preserves the latest requested page", async () => {
    const failed = { promise: Promise.reject(new Error("Temporary read failure")), destroy: vi.fn(async () => {}) };
    let resolve!: (document: { numPages: number }) => void;
    const retry = { promise: new Promise<{ numPages: number }>(done => { resolve = done; }), destroy: vi.fn(async () => {}) };
    pdfMocks.getDocument.mockReturnValueOnce(failed).mockReturnValueOnce(retry);
    const source = { workspace: "/project", path: "document.pdf", revision: "observed-revision" };
    await act(async () => root.render(
      <WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" source={source} initialPage={2} pageRequest="first" />,
    ));
    const host = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!;
    const shadow = host.shadowRoot!;
    expect(shadow.querySelector("[role=alert]")?.textContent).toContain("Temporary read failure");
    const button = shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!;
    expect(button).not.toBeNull();
    await act(async () => { button.click(); button.click(); });
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(2);
    expect(failed.destroy).toHaveBeenCalledOnce();
    expect(pdfMocks.viewerSignals[0].aborted).toBe(true);
    expect(pdfMocks.viewer.setDocument).toHaveBeenCalledWith(null);
    expect(shadow.querySelector("[role=alert]")).toBeNull();
    expect(shadow.querySelector("[role=status]")).not.toBeNull();
    expect(shadow.querySelector<HTMLButtonElement>('button[aria-label="imagePreview.zoomIn"]')?.disabled).toBe(true);
    expect(container.querySelector("[data-workspace-pdf-preview]")).toBe(host);
    expect(pdfMocks.getDocument.mock.calls.map(call => call[0])).toEqual([
      expect.objectContaining({ url: "wuu-file://local/document" }),
      expect.objectContaining({ url: "wuu-file://local/document" }),
    ]);
    await act(async () => root.render(
      <WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" source={source} initialPage={3} pageRequest="newer" />,
    ));
    await act(async () => resolve({ numPages: 3 }));
    await act(async () => pdfMocks.eventBus?.dispatch("pagesinit", {}));
    expect(pdfMocks.viewer.currentPageNumber).toBe(3);
    expect(shadow.querySelector("[role=status]")).toBeNull();
    expect(source.revision).toBe("observed-revision");
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(2);
  });

  it.each(["initialization", "navigation", "render"])("recovers a %s failure after the document loads", async (phase) => {
    let failPages!: (error: Error) => void;
    pdfMocks.viewer.pagesPromise = new Promise<void>((_, reject) => { failPages = reject; });
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" initialPage={2} />));
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    await act(async () => {
      if (phase === "initialization") failPages(new Error("Page initialization failed"));
      else {
        pdfMocks.eventBus?.dispatch("pagesinit", {});
        if (phase === "navigation") pdfMocks.viewer.pdfDocument.getPage.mockRejectedValueOnce(new Error("Page fetch failed"));
        pdfMocks.eventBus?.dispatch("pagechanging", { pageNumber: 3 });
        if (phase === "render") pdfMocks.eventBus?.dispatch("pagerendered", { pageNumber: 3, error: new Error("Page render failed") });
      }
    });
    expect(shadow.querySelector("[role=alert]")?.textContent).toContain("failed");
    expect(shadow.querySelector<HTMLButtonElement>('button[aria-label="imagePreview.zoomIn"]')?.disabled).toBe(true);
    pdfMocks.viewer.pagesPromise = Promise.resolve();
    await act(async () => shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!.click());
    await act(async () => pdfMocks.eventBus?.dispatch("pagesinit", {}));
    expect(pdfMocks.viewer.currentPageNumber).toBe(phase === "initialization" ? 2 : 3);
    expect(shadow.querySelector("[role=alert]")).toBeNull();
    expect(shadow.querySelector("[role=status]")).toBeNull();
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(2);
  });

  it("waits for refreshed source metadata and keeps refresh failures retryable", async () => {
    pdfMocks.getDocument.mockReturnValueOnce({ promise: Promise.reject(new Error("Retry me")), destroy: vi.fn(async () => {}) });
    let rejectRefresh!: (error: Error) => void;
    const refresh = vi.fn((_signal: AbortSignal) => new Promise<boolean>((_, reject) => { rejectRefresh = reject; }));
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" onRefresh={refresh} />));
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    await act(async () => {
      const retry = shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!;
      retry.click(); retry.click();
    });
    expect(refresh).toHaveBeenCalledOnce();
    expect(pdfMocks.getDocument).toHaveBeenCalledOnce();
    expect(shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')?.disabled).toBe(true);
    await act(async () => rejectRefresh(new Error("Metadata unavailable")));
    expect(shadow.querySelector("[role=alert]")?.textContent).toContain("Metadata unavailable");
    refresh.mockResolvedValueOnce(true);
    await act(async () => shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!.click());
    await act(async () => pdfMocks.eventBus?.dispatch("pagesinit", {}));
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(2);
    expect(shadow.querySelector("[role=alert]")).toBeNull();
  });

  it.each(["close", "source"])("cancels a metadata refresh on %s before starting another PDF load", async (action) => {
    pdfMocks.getDocument.mockReturnValueOnce({ promise: Promise.reject(new Error("Retry me")), destroy: vi.fn(async () => {}) });
    let resolveRefresh!: (retry: boolean) => void;
    const refresh = vi.fn((_signal: AbortSignal) => new Promise<boolean>(resolve => { resolveRefresh = resolve; }));
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" onRefresh={refresh} />));
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    await act(async () => shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!.click());
    await act(async () => {
      if (action === "close") root.render(null);
      else root.render(<WorkspacePdfPreview url="wuu-file://local/replacement" title="replacement.pdf" />);
    });
    expect(refresh.mock.calls[0][0].aborted).toBe(true);
    await act(async () => resolveRefresh(true));
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(action === "close" ? 1 : 2);
    expect(pdfMocks.getDocument.mock.calls.filter(call => call[0].url === "wuu-file://local/document")).toHaveLength(1);
  });

  it("keeps a corrupt PDF retryable and releases each failed attempt", async () => {
    pdfMocks.getDocument.mockImplementation(() => ({
      promise: Promise.reject(new Error("Invalid PDF")), destroy: vi.fn(async () => {}),
    }));
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/corrupt" title="corrupt.pdf" />));
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    for (let attempt = 0; attempt < 3; attempt += 1) {
      expect(shadow.querySelector("[role=alert]")?.textContent).toContain("Invalid PDF");
      await act(async () => shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!.click());
    }
    expect(pdfMocks.getDocument).toHaveBeenCalledTimes(4);
    expect(pdfMocks.viewerSignals.slice(0, 3).every(signal => signal.aborted)).toBe(true);
    expect(shadow.querySelector('button[aria-label="appState.retryAction"]')).not.toBeNull();
  });

  it.each(["resolve", "reject"])("ignores a late %s after a source revision changes", async (outcome) => {
    let resolve!: (document: { numPages: number }) => void;
    let reject!: (error: Error) => void;
    const old = { promise: new Promise<{ numPages: number }>((yes, no) => { resolve = yes; reject = no; }), destroy: vi.fn(async () => {}) };
    pdfMocks.getDocument.mockReturnValueOnce(old);
    const source = { workspace: "/project", path: "document.pdf", revision: "old" };
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" source={source} />));
    const oldEvents = pdfMocks.eventBus;
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" source={{ ...source, revision: "new" }} />));
    expect(old.destroy).toHaveBeenCalledOnce();
    expect(pdfMocks.viewerSignals[0].aborted).toBe(true);
    await act(async () => {
      if (outcome === "resolve") resolve({ numPages: 99 }); else reject(new Error("Old failure"));
      oldEvents?.dispatch("pagesinit", {});
      oldEvents?.dispatch("pagechanging", { pageNumber: 99 });
    });
    expect(pdfMocks.viewer.setDocument).not.toHaveBeenCalledWith({ numPages: 99 });
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    expect(shadow.querySelector("[role=alert]")).toBeNull();
    expect(shadow.querySelector<HTMLButtonElement>('button[aria-label="imagePreview.zoomIn"]')?.disabled).toBe(true);
    await act(async () => pdfMocks.eventBus?.dispatch("pagesinit", {}));
    expect(shadow.querySelector(".workspace-pdf-page-count")?.textContent).toBe("1 / 3");
  });

  it("cancels a pending retry when the preview closes", async () => {
    let resolve!: (document: { numPages: number }) => void;
    const pending = { promise: new Promise<{ numPages: number }>(done => { resolve = done; }), destroy: vi.fn(async () => {}) };
    pdfMocks.getDocument.mockReturnValueOnce({ promise: Promise.reject(new Error("Retry me")), destroy: vi.fn(async () => {}) }).mockReturnValueOnce(pending);
    await act(async () => root.render(<WorkspacePdfPreview url="wuu-file://local/document" title="document.pdf" />));
    const shadow = container.querySelector<HTMLElement>("[data-workspace-pdf-preview]")!.shadowRoot!;
    await act(async () => shadow.querySelector<HTMLButtonElement>('button[aria-label="appState.retryAction"]')!.click());
    act(() => root.unmount());
    root = createRoot(container);
    expect(pending.destroy).toHaveBeenCalledOnce();
    expect(pdfMocks.viewerSignals.every(signal => signal.aborted)).toBe(true);
    await act(async () => resolve({ numPages: 99 }));
    expect(pdfMocks.viewer.setDocument).not.toHaveBeenCalledWith({ numPages: 99 });
    expect(container.querySelector("[data-workspace-pdf-preview]")).toBeNull();
  });

});
