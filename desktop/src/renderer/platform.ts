import type { DesktopPlatform } from "../shared/protocol";
import { translateCurrent } from "./i18n";

// JS-side mirror of the data-platform stamp the preload puts on <html>.
// window.wuu.platform is the truth (process.platform in the preload); the
// user-agent fallback only covers boot paths that replace the real preload
// with a mock (the e2e harness preloads), where the OS the test runs on is
// the right answer anyway.
export function desktopPlatform(): DesktopPlatform {
  const fromApi = window.wuu?.platform;
  if (fromApi === "darwin" || fromApi === "win32" || fromApi === "linux") {
    return fromApi;
  }
  const ua = navigator.userAgent;
  if (ua.includes("Windows")) return "win32";
  if (ua.includes("Macintosh") || ua.includes("Mac OS")) return "darwin";
  return "linux";
}

// Re-stamp for boots where the preload stamp was dropped. Idempotent: the
// preload's value wins when present so CSS and JS never disagree.
export function applyPlatformStamp(): void {
  const root = document.documentElement;
  root.dataset.hostKind = window.wuu?.hostKind ?? "desktop";
  if (!root.dataset.platform) {
    root.dataset.platform = desktopPlatform();
  }
}

// Shortcut hints only — key HANDLERS must keep accepting both metaKey and
// ctrlKey so a stale label never hides a working binding.
export function primaryShortcutLabel(key: string | number): string {
  return desktopPlatform() === "darwin" ? `⌘${key}` : `Ctrl+${key}`;
}

export function revealInFileManagerLabel(): string {
  switch (desktopPlatform()) {
    case "darwin":
      return translateCurrent("platform.revealFinder");
    case "win32":
      return translateCurrent("platform.revealExplorer");
    default:
      return translateCurrent("platform.revealFileManager");
  }
}
