import { createContext, useCallback, useContext, useRef, type ReactNode } from "react";
import type { ImagePreviewItem } from "./ImagePreview";

type Gallery = Map<HTMLElement, ImagePreviewItem>;
const GalleryContext = createContext<Gallery | null>(null);
const galleries = new WeakMap<HTMLElement, Gallery>();

export function ImagePreviewGallery({ children }: { children: ReactNode }): JSX.Element {
  const gallery = useRef<Gallery>(new Map());
  return <GalleryContext.Provider value={gallery.current}>{children}</GalleryContext.Provider>;
}

export function useImagePreviewRegistration(item: ImagePreviewItem | null): (node: HTMLElement | null) => void {
  const gallery = useContext(GalleryContext);
  const element = useRef<HTMLElement | null>(null);
  return useCallback((node: HTMLElement | null) => {
    if (element.current) {
      gallery?.delete(element.current);
      galleries.delete(element.current);
    }
    element.current = node;
    if (node && gallery && item) {
      gallery.set(node, item);
      galleries.set(node, gallery);
    }
  }, [gallery, item]);
}

export function imagePreviewSelection(item: ImagePreviewItem, origin?: HTMLElement): { items: ImagePreviewItem[]; index: number } {
  const gallery = origin && galleries.get(origin);
  if (!gallery) return { items: [item], index: 0 };
  // Mount order changes when older history is prepended. Snapshot display order
  // on open so streaming output cannot move the reader's position mid-preview.
  const entries = Array.from(gallery).filter(([node]) => node.isConnected);
  entries.sort(([a], [b]) => {
    if (a === b) return 0;
    return a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1;
  });
  const index = entries.findIndex(([node]) => node === origin);
  if (index < 0) return { items: [item], index: 0 };
  const items = entries.map(([, entry]) => entry);
  items[index] = item;
  return { items, index };
}
