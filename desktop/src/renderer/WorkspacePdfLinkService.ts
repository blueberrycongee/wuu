import { isValidExplicitDest, type PDFDocumentProxy } from "pdfjs-dist";
import { EventBus, PDFLinkService, PDFViewer } from "pdfjs-dist/web/pdf_viewer.mjs";
import { parseLinkTarget } from "./LinkTargets";

export class WorkspacePdfLinkService extends PDFLinkService {
  private navigation = 0;

  constructor(eventBus: EventBus, private readonly isReady: () => boolean) {
    super({ eventBus, ignoreDestinationZoom: true });
  }

  override setDocument(document: PDFDocumentProxy | null): void {
    this.cancelNavigation();
    super.setDocument(document);
  }

  cancelNavigation(): void {
    this.navigation += 1;
  }

  override async goToDestination(destination: string | unknown[]): Promise<void> {
    const document = this.pdfDocument as PDFDocumentProxy | null;
    const viewer = this.pdfViewer as PDFViewer | null;
    const navigation = ++this.navigation;
    if (!document || !viewer || !this.isReady()) return;
    const isCurrent = (): boolean => this.isReady() && navigation === this.navigation
      && document === this.pdfDocument && viewer === this.pdfViewer;

    try {
      const resolved = typeof destination === "string" ? await document.getDestination(destination) : destination;
      if (!isCurrent() || !isValidExplicitDest(resolved)) return;
      // PDF.js validates the destination shape; non-finite coordinates must not
      // reach its scroll arithmetic, even when a malformed PDF supplies them.
      const explicit = resolved as unknown[];
      if (explicit.slice(2).some(value => typeof value === "number" && !Number.isFinite(value))) return;
      const target = explicit[0];
      const pageNumber = typeof target === "number" ? target + 1
        : document.cachedPageNumber(target as { num: number; gen: number })
          ?? (await document.getPageIndex(target as { num: number; gen: number })) + 1;
      if (!isCurrent() || !Number.isSafeInteger(pageNumber) || pageNumber < 1 || pageNumber > document.numPages) return;
      viewer.scrollPageIntoView({ pageNumber, destArray: explicit, ignoreDestinationZoom: true });
    } catch {
      // Missing names/references and a destroyed loading task leave the reader
      // at the current page. A bad link is not a document rendering failure.
    }
  }

  override addLinkAttributes(link: HTMLAnchorElement, url: string): void {
    const target = parseLinkTarget(url);
    if (target.kind !== "external") {
      link.removeAttribute("href");
      link.onclick = link.onauxclick = event => { event.preventDefault(); };
      return;
    }
    super.addLinkAttributes(link, target.url, true);
    const open = (event: MouseEvent): void => {
      event.preventDefault();
      if (event.type === "auxclick" && event.button !== 1) return;
      if (!this.isReady() || !this.externalLinkEnabled) return;
      // The main-process external URL policy remains authoritative. No renderer
      // navigation or window creation is needed for a PDF annotation click.
      void window.wuu?.openExternal?.(target.url).catch(error => {
        console.warn("Could not open PDF link", error);
      });
    };
    link.onclick = open;
    link.onauxclick = open;
  }

  // Connecting a document must not enable PDF actions that this read-only
  // preview did not previously support (named actions or layer visibility).
  override executeNamedAction(_action: string): void {}
  override async executeSetOCGState(_action: object): Promise<void> {}
}
