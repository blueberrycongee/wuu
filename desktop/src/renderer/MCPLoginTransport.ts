import type { MCPServerStatus } from "../shared/protocol";

export function usesAutomaticMCPLogin(server: MCPServerStatus, available: boolean): boolean {
  let uri: URL | null;
  try { uri = server.oauth_redirect_uri ? new URL(server.oauth_redirect_uri) : null; }
  catch { return false; }
  return available && (!uri || (uri.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(uri.hostname)
    && !uri.username && !uri.password && !uri.search && !uri.hash));
}
