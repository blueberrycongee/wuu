import type { WindowOpenHandlerResponse } from "electron";

type WindowOpenDetails = { url: string; frameName?: string };

export type ExternalURLOpener = (url: string) => Promise<void>;

export type ExternalNavigationWindow = {
  webContents: {
    setWindowOpenHandler: (
      handler: (details: WindowOpenDetails) => WindowOpenHandlerResponse,
    ) => void;
    on: (
      event: "will-navigate",
      listener: (event: { preventDefault: () => void }, url: string) => void,
    ) => void;
  };
};

const EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);

export function normalizeExternalURL(rawURL: unknown): string | undefined {
  if (typeof rawURL !== "string") {
    return undefined;
  }
  const raw = rawURL.trim();
  if (!raw) {
    return undefined;
  }

  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return undefined;
  }
  if (!EXTERNAL_PROTOCOLS.has(url.protocol.toLowerCase())) {
    return undefined;
  }
  return url.toString();
}

export async function openExternalURL(
  rawURL: unknown,
  opener: ExternalURLOpener,
): Promise<boolean> {
  const url = normalizeExternalURL(rawURL);
  if (!url) {
    return false;
  }
  await opener(url);
  return true;
}

export function wireExternalNavigationGuards(
  window: ExternalNavigationWindow,
  {
    rendererURL,
    openExternal,
    openWindow,
  }: {
    rendererURL: string;
    openExternal: (url: string) => Promise<boolean>;
    openWindow?: (details: WindowOpenDetails) => WindowOpenHandlerResponse | undefined;
  },
): void {
  window.webContents.setWindowOpenHandler((details) => {
    const handled = openWindow?.(details);
    if (handled) return handled;
    openExternalSafely(openExternal, details.url);
    return { action: "deny" };
  });
  window.webContents.on("will-navigate", (event, url) => {
    if (isAllowedRendererNavigation(url, rendererURL)) {
      return;
    }
    event.preventDefault();
    openExternalSafely(openExternal, url);
  });
}

function openExternalSafely(
  openExternal: (url: string) => Promise<boolean>,
  url: string,
): void {
  void openExternal(url).catch((error) => {
    console.error(`[external-navigation] failed to open ${url}`, error);
  });
}

export function isAllowedRendererNavigation(url: string, rendererURL: string): boolean {
  let target: URL;
  let renderer: URL;
  try {
    target = new URL(url);
    renderer = new URL(rendererURL);
  } catch {
    return false;
  }

  if (renderer.protocol === "file:") {
    return target.protocol === "file:" && target.pathname === renderer.pathname;
  }
  return target.origin === renderer.origin;
}
