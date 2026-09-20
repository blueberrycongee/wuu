import type { BackgroundEffect } from "./preferences";

export function processBackground(image: Blob, effect: BackgroundEffect, light: boolean, signal?: AbortSignal): Promise<Blob> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException("Aborted", "AbortError")); return; }
    const worker = new Worker(new URL("./image.worker.ts", import.meta.url), { type: "module" });
    const timer = setTimeout(() => { cleanup(); reject(new Error("Background processing timed out")); }, 30_000);
    const cleanup = () => { clearTimeout(timer); worker.terminate(); signal?.removeEventListener("abort", abort); };
    const abort = () => { cleanup(); reject(new DOMException("Aborted", "AbortError")); };
    signal?.addEventListener("abort", abort, { once: true });
    worker.onmessage = (event: MessageEvent<{ image?: Blob }>) => {
      cleanup();
      if (event.data.image) resolve(event.data.image);
      else reject(new Error("Invalid background image"));
    };
    worker.onerror = () => { cleanup(); reject(new Error("Background processing failed")); };
    worker.postMessage({ image, effect, light });
  });
}
