import { useId, useLayoutEffect, type RefObject } from "react";

/** Keep the captured source readable when the annotation editor takes focus.
 * The build-only comparison adapter installs this hook in the PDF menu. */
export function usePdfSelectionHighlight(
  hostRef: RefObject<HTMLDivElement | null>,
  range: Range | undefined,
): void {
  const id = `pdf-selection-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  useLayoutEffect(() => {
    const root = hostRef.current?.shadowRoot;
    const api = window as unknown as {
      Highlight?: new (...ranges: Range[]) => unknown;
      CSS?: { highlights?: Map<string, unknown> };
    };
    const highlights = api.CSS?.highlights;
    if (!range || !root || !api.Highlight || !highlights) return;
    const highlight = new api.Highlight(range);
    const start = range.startContainer, end = range.endContainer;
    const startOffset = range.startOffset, endOffset = range.endOffset;
    const quote = range.toString();
    let valid = true;
    const style = document.createElement("style");
    style.dataset.pdfSelectionHighlight = id;
    // PDF.js renders the glyphs on canvas; its selectable text stays invisible.
    // Scope the rule inside the text layer's shadow tree, not document.head.
    style.textContent = `.textLayer ::highlight(${id}) { background-color: var(--selection-bg, Highlight); color: transparent; }`;
    root.appendChild(style);
    const clear = () => { highlights.delete(id); };
    const update = () => {
      const inTextLayer = (node: Node): boolean => {
        const element = node instanceof Element ? node : node.parentElement;
        return node.isConnected && node.getRootNode() === root && Boolean(element?.closest(".textLayer"));
      };
      // DOM Ranges reparent their endpoints when text is removed. Once that
      // happens, a still-connected range can refer to different source text.
      valid &&= range.startContainer === start && range.endContainer === end
        && range.startOffset === startOffset && range.endOffset === endOffset
        && range.toString() === quote && inTextLayer(start) && inTextLayer(end);
      if (document.hasFocus() && valid && !range.collapsed) {
        highlights.set(id, highlight);
      } else clear();
    };
    update();
    window.addEventListener("blur", clear);
    window.addEventListener("focus", update);
    // The range follows scrolling and scaling natively. If PDF.js recycles its
    // text nodes, stop painting rather than marking an unrelated page.
    const observer = new MutationObserver(update);
    observer.observe(root, { childList: true, characterData: true, subtree: true });
    return () => {
      observer.disconnect();
      window.removeEventListener("blur", clear);
      window.removeEventListener("focus", update);
      clear();
      style.remove();
    };
  }, [hostRef, range, id]);
}
