/**
 * Session restoration reserves image layout, but a restored conversation can
 * still reveal an empty image box while the bytes decode. Waiting for the
 * images near the viewport before releasing the loading cover keeps the first
 * painted frame complete. The wait is bounded because remote sources may be
 * unavailable or arbitrarily slow.
 */
import type { Thread } from "../shared/protocol";

export const CONVERSATION_MEDIA_SETTLE_MS = 240;
export const CONVERSATION_MEDIA_DISCOVERY_MS = 48;
const NEAR_VIEWPORT_MARGIN = 1;
const CONVERSATION_IMAGE_SELECTOR = [
  "img.rich-image",
  "img.message-image",
  ".turn-artifact-inline-image img",
  ".turn-artifact-image-preview img",
].join(",");

function remainingMs(deadline: number): number {
  return Math.max(0, deadline - performance.now());
}

function isRelevant(node: Element, viewport: HTMLElement): boolean {
  const pane = node.closest(".cached-conversation-pane");
  if (pane && pane.getAttribute("data-active") !== "true") return false;
  const rect = node.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return false;
  const viewportRect = viewport.getBoundingClientRect();
  if (viewportRect.height <= 0) return false;
  const margin = viewportRect.height * NEAR_VIEWPORT_MARGIN;
  return rect.bottom >= viewportRect.top - margin && rect.top <= viewportRect.bottom + margin;
}

function waitUntil(deadline: number, signal?: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    if (signal?.aborted) {
      resolve();
      return;
    }
    const timer = window.setTimeout(finish, remainingMs(deadline));
    function finish(): void {
      window.clearTimeout(timer);
      signal?.removeEventListener("abort", finish);
      resolve();
    }
    signal?.addEventListener("abort", finish, { once: true });
  });
}

function decodeImage(image: HTMLImageElement): Promise<void> {
  return typeof image.decode === "function"
    ? image.decode().catch(() => undefined)
    : Promise.resolve();
}

function waitForImage(image: HTMLImageElement): Promise<void> {
  if (image.complete) {
    return image.naturalWidth > 0 ? decodeImage(image) : Promise.resolve();
  }
  if (image.loading === "lazy") image.loading = "eager";
  return new Promise<void>(resolve => {
    const finish = (): void => {
      image.removeEventListener("load", finish);
      image.removeEventListener("error", finish);
      resolve();
    };
    image.addEventListener("load", finish);
    image.addEventListener("error", finish);
    // A cached image can finish between the `complete` read and listener setup.
    if (image.complete) finish();
  }).then(() => image.naturalWidth > 0 ? decodeImage(image) : undefined);
}

function relevantPlaceholders(viewport: HTMLElement): HTMLElement[] {
  return Array.from(viewport.querySelectorAll<HTMLElement>("[data-conversation-image-pending]"))
    .filter(node => isRelevant(node, viewport));
}

function relevantImages(viewport: HTMLElement): HTMLImageElement[] {
  return Array.from(viewport.querySelectorAll<HTMLImageElement>(CONVERSATION_IMAGE_SELECTOR))
    .filter(image => isRelevant(image, viewport));
}

/** Synchronous fast path: avoid deferring a switch that has no media to wait for. */
export function hasPendingConversationMedia(viewport: HTMLElement): boolean {
  return relevantPlaceholders(viewport).length > 0
    || relevantImages(viewport).some(image => !image.complete
      || image.naturalWidth > 0 && typeof image.decode === "function");
}

/** The resumed thread can contain media that must reach the DOM before settling. */
export function threadHasConversationMedia(thread: Thread | undefined): boolean {
  if (!thread) return false;
  return (thread.turns ?? []).some(turn => (turn.items ?? []).some(item =>
    Boolean(item.images?.length || item.markdown_images?.length
      || item.result_detail?.content?.some(part => part.type === "image"
        || Boolean(part.mime_type?.startsWith("image/")))
      || item.text?.includes("![") && item.text.includes("]("))));
}

/** Wait briefly for media components that mount after the first stable layout. */
export function waitForConversationMedia(
  viewport: HTMLElement,
  timeoutMs = CONVERSATION_MEDIA_DISCOVERY_MS,
  signal?: AbortSignal,
): Promise<void> {
  if (hasPendingConversationMedia(viewport)) return Promise.resolve();
  const deadline = performance.now() + Math.max(0, timeoutMs);
  return new Promise<void>(resolve => {
    let observer: MutationObserver;
    let timer = 0;
    let finished = false;
    const finish = (): void => {
      if (finished) return;
      finished = true;
      observer.disconnect();
      window.clearTimeout(timer);
      signal?.removeEventListener("abort", finish);
      resolve();
    };
    observer = new MutationObserver(() => {
      if (hasPendingConversationMedia(viewport)) finish();
    });
    observer.observe(viewport, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["src", "data-conversation-image-pending"],
    });
    timer = window.setTimeout(finish, remainingMs(deadline));
    if (signal?.aborted) finish();
    else signal?.addEventListener("abort", finish, { once: true });
  });
}

async function waitForPendingPlaceholders(
  viewport: HTMLElement,
  deadline: number,
  signal?: AbortSignal,
): Promise<void> {
  const placeholders = new Set(relevantPlaceholders(viewport));
  if (placeholders.size === 0) return;
  await new Promise<void>(resolve => {
    let observer: MutationObserver;
    let timer = 0;
    let finished = false;
    const finish = (): void => {
      if (finished) return;
      finished = true;
      observer.disconnect();
      window.clearTimeout(timer);
      signal?.removeEventListener("abort", finish);
      resolve();
    };
    observer = new MutationObserver(() => {
      for (const node of placeholders) {
        if (!node.isConnected || !node.hasAttribute("data-conversation-image-pending")) {
          placeholders.delete(node);
        }
      }
      if (placeholders.size === 0) finish();
    });
    observer.observe(viewport, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["data-conversation-image-pending"],
    });
    timer = window.setTimeout(finish, remainingMs(deadline));
    if (signal?.aborted) finish();
    else signal?.addEventListener("abort", finish, { once: true });
  });
}

async function waitForImages(
  viewport: HTMLElement,
  deadline: number,
  signal?: AbortSignal,
): Promise<void> {
  const images = relevantImages(viewport);
  if (images.length === 0) return;
  await Promise.race([
    Promise.all(images.map(waitForImage)),
    waitUntil(deadline, signal),
  ]);
}

export async function settleConversationMedia(
  viewport: HTMLElement,
  timeoutMs = CONVERSATION_MEDIA_SETTLE_MS,
  signal?: AbortSignal,
): Promise<void> {
  const deadline = performance.now() + Math.max(0, timeoutMs);
  await waitForPendingPlaceholders(viewport, deadline, signal);
  if (signal?.aborted || remainingMs(deadline) <= 0) return;
  await waitForImages(viewport, deadline, signal);
}
