export const RENDERER_RELOAD_COOLDOWN_MS = 5_000;

const RELOADABLE_RENDERER_GONE_REASONS = new Set([
  "crashed",
  "oom",
  "abnormal-exit",
  "launch-failed",
]);

export function shouldReloadAfterRendererGone(reason: string): boolean {
  return RELOADABLE_RENDERER_GONE_REASONS.has(reason);
}

export function allowRendererReload(
  now: number,
  lastReloadAt: number | undefined,
  cooldownMs = RENDERER_RELOAD_COOLDOWN_MS,
): boolean {
  return lastReloadAt === undefined || now - lastReloadAt >= cooldownMs;
}

export function isDisposedWebFrameError(error: unknown): boolean {
  return error instanceof Error && error.message.includes("Render frame was disposed");
}
