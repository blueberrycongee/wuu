import { cubicBezier, motionDurationMs, motionEasing, prefersReducedMotion, subscribeReducedMotion } from "./motion";
import "./ArtifactPreviewMotion.css";

type Box = { left: number; top: number; width: number; height: number };
type Frame = { surface: Box; title: Box; image?: Box };

export type ArtifactPreviewMotion = {
  origin: WeakRef<HTMLElement>;
  attach: (panel: HTMLElement) => () => void;
  close: () => void;
  cancel: () => void;
};

let cancelFlight: (() => void) | undefined;

function visible(element: HTMLElement): boolean {
  const rect = element.getBoundingClientRect();
  return element.isConnected && rect.width > 0 && rect.height > 0
    && rect.bottom > 0 && rect.top < innerHeight && rect.right > 0 && rect.left < innerWidth
    && !element.closest('[inert], [aria-hidden="true"]');
}

function interpolate(from: Box, to: Box, progress: number): Box {
  return {
    left: from.left + (to.left - from.left) * progress,
    top: from.top + (to.top - from.top) * progress,
    width: from.width + (to.width - from.width) * progress,
    height: from.height + (to.height - from.height) * progress,
  };
}

function place(element: HTMLElement, box: Box): void {
  element.style.transform = `translate3d(${box.left}px, ${box.top}px, 0)`;
  element.style.width = `${box.width}px`;
  element.style.height = `${box.height}px`;
}

/** A per-click handle. Tabs retain only weak references to conversation DOM. */
export function createArtifactPreviewMotion(origin: HTMLElement, name: string): ArtifactPreviewMotion {
  const source = new WeakRef(origin);
  const thumbnail = origin instanceof HTMLImageElement ? origin : origin.querySelector<HTMLImageElement>("img");
  const imageSource = thumbnail?.complete && thumbnail.naturalWidth > 0 ? thumbnail.currentSrc : undefined;
  const aspect = thumbnail && thumbnail.naturalHeight > 0 ? thumbnail.naturalWidth / thumbnail.naturalHeight : 1;
  const sourceTitle = (): HTMLElement | null => source.deref()?.querySelector(
    ".turn-edit-summary-overview-path, .turn-output-summary-name",
  ) ?? null;
  const sourceFrame = (): Frame | undefined => {
    const node = source.deref();
    if (!node || !visible(node)) return undefined;
    const surface = node.getBoundingClientRect();
    return {
      surface,
      title: sourceTitle()?.getBoundingClientRect() ?? { left: surface.left, top: surface.bottom - 24, width: surface.width, height: 24 },
      image: imageSource ? (node instanceof HTMLImageElement ? node : node.querySelector("img"))?.getBoundingClientRect() : undefined,
    };
  };
  const initial = sourceFrame();
  let panel: WeakRef<HTMLElement> | undefined;
  let entered = false;
  let closing = false;
  let stop: (() => void) | undefined;
  let current: Frame | undefined;

  const panelFrame = (): Frame | undefined => {
    const node = panel?.deref();
    if (!node?.isConnected) return undefined;
    const body = node.querySelector<HTMLElement>(".artifact-preview-body")!;
    const bounds = body.getBoundingClientRect();
    const width = Math.min(bounds.width, bounds.height * aspect);
    const height = width / aspect;
    return {
      surface: node.getBoundingClientRect(),
      title: node.querySelector(".artifact-preview-toolbar strong")!.getBoundingClientRect(),
      image: imageSource ? { left: bounds.left + (bounds.width - width) / 2, top: bounds.top + (bounds.height - height) / 2, width, height } : undefined,
    };
  };

  const play = (from: Frame, returning: boolean): void => {
    cancelFlight?.();
    if (prefersReducedMotion() || !sourceFrame()) return;
    const duration = returning
      ? motionDurationMs("--motion-slow", 280)
      : motionDurationMs("--motion-slower", 440);
    if (duration <= 0) return;
    const ease = motionEasing("--ease-out", cubicBezier(0.22, 1, 0.36, 1));
    const ghost = document.createElement("div");
    ghost.className = "artifact-preview-flight";
    ghost.setAttribute("aria-hidden", "true");
    const surface = document.createElement("div");
    surface.className = "artifact-preview-flight-surface";
    const title = document.createElement("div");
    title.className = "artifact-preview-flight-title";
    title.textContent = name;
    ghost.append(surface, title);
    const image = imageSource ? document.createElement("img") : undefined;
    if (image) {
      image.src = imageSource!;
      image.alt = "";
      ghost.append(image);
    }
    document.body.append(ghost);
    // Only decorative peers disappear; buttons remain available throughout.
    const sourceNode = source.deref();
    const hidden = [sourceTitle(), sourceNode instanceof HTMLImageElement ? sourceNode : sourceNode?.querySelector("img"),
      panel?.deref()?.querySelector(".artifact-preview-image"),
      panel?.deref()?.querySelector(".artifact-preview-toolbar strong")].filter((node): node is HTMLElement => node instanceof HTMLElement);
    for (const node of hidden) node.classList.add("artifact-preview-motion-hidden");
    let raf = 0;
    const finish = (): void => {
      cancelAnimationFrame(raf);
      ghost.remove();
      for (const node of hidden) node.classList.remove("artifact-preview-motion-hidden");
      window.removeEventListener("resize", finish);
      window.removeEventListener("wheel", finish, true);
      document.removeEventListener("visibilitychange", finish);
      unsubscribe();
      if (cancelFlight === finish) cancelFlight = undefined;
      if (stop === finish) stop = undefined;
      current = undefined;
    };
    const unsubscribe = subscribeReducedMotion((reduced) => { if (reduced) finish(); });
    window.addEventListener("resize", finish);
    window.addEventListener("wheel", finish, { capture: true, passive: true });
    document.addEventListener("visibilitychange", finish);
    cancelFlight = stop = finish;
    const start = performance.now();
    const draw = (now: number): void => {
      const target = returning ? sourceFrame() : panelFrame();
      if (!target || !sourceFrame()) { finish(); return; }
      const elapsed = Math.min(1, (now - start) / duration);
      const progress = ease(elapsed);
      current = {
        surface: interpolate(from.surface, target.surface, progress),
        title: interpolate(from.title, target.title, progress),
        image: from.image && target.image ? interpolate(from.image, target.image, progress) : undefined,
      };
      place(surface, current.surface);
      place(title, current.title);
      if (image && current.image) place(image, current.image);
      // Reveal live document content as the surface reaches the existing pane.
      surface.style.opacity = String(returning ? 0.8 * (1 - progress) : 1 - progress);
      title.style.opacity = String(imageSource ? (returning ? 1 - progress : progress) : 1);
      if (elapsed >= 1) { finish(); return; }
      raf = requestAnimationFrame(draw);
    };
    draw(start);
  };

  return {
    origin: source,
    cancel() { stop?.(); },
    attach(node) {
      panel = new WeakRef(node);
      closing = false;
      if (!entered) {
        entered = true;
        if (initial) play(initial, false);
      }
      return () => {
        // An explicit close owns its detached return flight. Tab switches cancel.
        if (!closing) stop?.();
        panel = undefined;
      };
    },
    close() {
      if (closing) return;
      closing = true;
      const from = current ?? panelFrame();
      const node = source.deref();
      if (node && visible(node) && panel?.deref()?.closest(".workspace-right-panel")?.contains(document.activeElement)) {
        node.focus({ preventScroll: true });
      }
      stop?.();
      if (from) play(from, true);
    },
  };
}
