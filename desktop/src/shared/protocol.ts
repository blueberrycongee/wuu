// The Wuu app-server protocol types live in packages/protocol — the single
// source of truth shared by every shell (desktop today, mobile and web
// clients next). This module re-exports them so desktop code keeps its
// existing `shared/protocol` import path.
export * from "../../../packages/protocol/src/index";

// Go reads proxy variables as http, https, or socks5 URLs; a bare host:port means http.
export function normalizeProxyURL(value: string): string | undefined {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  try {
    const url = new URL(trimmed.includes("://") ? trimmed : `http://${trimmed}`);
    if (!["http:", "https:", "socks5:"].includes(url.protocol) || !url.hostname) return undefined;
    return url.toString().replace(/\/$/, "");
  } catch {
    return undefined;
  }
}
