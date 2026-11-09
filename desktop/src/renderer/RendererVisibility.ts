const RENDERER_HIDDEN_ATTRIBUTE = "data-renderer-hidden";

export function syncRendererVisibility(
  root: HTMLElement,
  visibilityState: DocumentVisibilityState,
): void {
  root.toggleAttribute(RENDERER_HIDDEN_ATTRIBUTE, visibilityState === "hidden");
}

export function startRendererVisibilitySync(): () => void {
  const sync = (): void => {
    syncRendererVisibility(document.documentElement, document.visibilityState);
  };
  sync();
  document.addEventListener("visibilitychange", sync);
  return () => document.removeEventListener("visibilitychange", sync);
}
