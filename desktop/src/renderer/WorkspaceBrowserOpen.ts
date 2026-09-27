import { createContext, useContext } from "react";

import { parseLinkTarget } from "./LinkTargets";

export type WorkspaceBrowserOpenTarget = {
  url: string;
  reuseKey: string;
};

export type WorkspaceBrowserOpenIntent = {
  target?: WorkspaceBrowserOpenTarget;
  external?: boolean;
};

export type WorkspaceBrowserFocusDecision = {
  navigate: boolean;
  stealFocus: boolean;
};

export type WorkspaceBrowserFocusState = {
  rightPanelOpen: boolean;
  activeTabID?: string;
  browserForegroundOccupied: boolean;
};

export type WorkspaceBrowserOpenModifiers = {
  metaKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
  button?: number;
};

const PAGE_PROTOCOLS = new Set(["http:", "https:"]);

export function workspaceBrowserOpenTarget(
  raw: string | undefined,
): WorkspaceBrowserOpenTarget | undefined {
  const parsed = parseLinkTarget(raw);
  if (parsed.kind !== "external") {
    return undefined;
  }
  let url: URL;
  try {
    url = new URL(parsed.url);
  } catch {
    return undefined;
  }
  if (!PAGE_PROTOCOLS.has(url.protocol.toLowerCase())) {
    return undefined;
  }
  return {
    url: url.toString(),
    reuseKey: workspaceBrowserReuseKey(url),
  };
}

export function workspaceBrowserOpenIntent(
  raw: string | undefined,
  modifiers: WorkspaceBrowserOpenModifiers = {},
): WorkspaceBrowserOpenIntent {
  const target = workspaceBrowserOpenTarget(raw);
  if (!target) {
    return {};
  }
  return {
    target,
    external: prefersSystemBrowser(modifiers),
  };
}

export function prefersSystemBrowser(
  modifiers: WorkspaceBrowserOpenModifiers | undefined,
): boolean {
  return (
    modifiers?.metaKey === true ||
    modifiers?.ctrlKey === true ||
    modifiers?.altKey === true ||
    modifiers?.button === 1
  );
}

export function workspaceBrowserFocusDecision(
  state: WorkspaceBrowserFocusState,
): WorkspaceBrowserFocusDecision {
  if (state.browserForegroundOccupied) {
    return { navigate: true, stealFocus: false };
  }
  if (state.rightPanelOpen && state.activeTabID !== undefined && state.activeTabID !== "browser") {
    return { navigate: true, stealFocus: false };
  }
  return { navigate: true, stealFocus: true };
}

export function workspaceBrowserClickModifiers(
  event: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number },
): WorkspaceBrowserOpenModifiers | undefined {
  const modifiers: WorkspaceBrowserOpenModifiers = {
    metaKey: event.metaKey === true,
    ctrlKey: event.ctrlKey === true,
    altKey: event.altKey === true,
    button: event.button,
  };
  return prefersSystemBrowser(modifiers) ? modifiers : undefined;
}

export const WorkspaceBrowserOpenContext = createContext<
  ((url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void) | undefined
>(undefined);

export function useWorkspaceBrowserOpen(
  fallback?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void,
): (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void {
  const open = useContext(WorkspaceBrowserOpenContext);
  return (url, modifiers) => {
    if (open) {
      open(url, modifiers);
      return;
    }
    fallback?.(url, modifiers);
  };
}

export function openWorkspaceBrowserOrExternal(
  url: string,
  modifiers: WorkspaceBrowserOpenModifiers | undefined,
  openWorkspace: (target: WorkspaceBrowserOpenTarget) => void,
): void {
  const intent = workspaceBrowserOpenIntent(url, modifiers);
  if (!intent.target) {
    return;
  }
  if (intent.external) {
    void window.wuu?.openExternal?.(intent.target.url);
    return;
  }
  openWorkspace(intent.target);
}

export function openExternalURL(url: string): void {
  if (typeof window !== "undefined") {
    void window.wuu?.openExternal?.(url);
  }
}

function workspaceBrowserReuseKey(url: URL): string {
  const protocol = url.protocol.toLowerCase();
  const host = url.host.toLowerCase();
  let pathname = url.pathname;
  if (pathname.endsWith("/")) {
    pathname = pathname.slice(0, -1);
  }
  return `${protocol}//${host}${pathname}${url.search}${url.hash}`;
}
