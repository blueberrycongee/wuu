export type WebWorkspaceHistoryEntry = {
  id: string;
  url: string;
  title: string;
  visitedAt: number;
};

export type WebWorkspaceBookmark = {
  id: string;
  url: string;
  title: string;
  createdAt: number;
};

export type WebWorkspaceLibraryState = {
  history: WebWorkspaceHistoryEntry[];
  bookmarks: WebWorkspaceBookmark[];
  error?: string;
};

export type WebWorkspaceSavedSession = {
  tabs: { id: string; url: string; title: string; pinned?: boolean; zoomFactor?: number }[];
  activeTabId: string;
  homeURL?: string;
  /** Retained until legacy ribbon preferences are migrated, including interrupted writes. */
  migratedTabGroups?: Record<string, string[]>;
};

export type WebWorkspaceLibraryApi = {
  getState: () => Promise<WebWorkspaceLibraryState>;
  setBookmark: (url: string, title: string, bookmarked: boolean) => Promise<WebWorkspaceLibraryState>;
  clearHistory: () => Promise<WebWorkspaceLibraryState>;
  onState: (handler: (state: WebWorkspaceLibraryState) => void) => () => void;
};

function sensitiveParameter(key: string): boolean {
  const normalized = key.toLowerCase().replace(/[^a-z0-9]/g, "");
  return /(?:token|secret|password|passwd|signature|credential|assertion)$/.test(normalized)
    || /^(?:auth|authorization|apikey|key|code|state|session|sessionid|sid|sig|ticket|samlrequest|samlresponse|relaystate|csrf|xsrf|codeverifier|codechallenge)$/.test(normalized);
}

/** Keep normal addresses intact, but never save known authentication links. */
export function persistentWebWorkspaceURL(value: string): string | undefined {
  if (typeof value !== "string" || value.length > 8192) return undefined;
  try {
    const url = new URL(value);
    if (!/^https?:$/.test(url.protocol) || url.username || url.password) return undefined;
    // Fragments can carry OAuth tokens or query strings inside hash routes.
    // Decode nested redirect parameters too, rather than saving a credential
    // indirectly in a return_to or redirect_uri address.
    let address = url.href;
    for (let depth = 0; depth < 3; depth++) {
      if (/https?:\/\/[^/?#\s]+@/i.test(address)) return undefined;
      if (/\/(?:magic[-_]?link|reset[-_]?password|password[-_]?reset|verify[-_]?email|email[-_]?verification|auth[-_]?callback)\/[^/?#]+/i.test(address)) return undefined;
      for (const match of address.matchAll(/(?:^|[?&#;])([^?&#;=]+)=/g)) {
        if (sensitiveParameter(match[1])) return undefined;
      }
      let decoded: string;
      try { decoded = decodeURIComponent(address); } catch { break; }
      if (decoded === address) break;
      address = decoded;
    }
    return url.href;
  } catch {
    return undefined;
  }
}
