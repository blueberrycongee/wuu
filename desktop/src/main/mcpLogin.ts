import { randomUUID } from "node:crypto";
import { createServer, type Server } from "node:http";
import type { MCPAuthStartResult, MCPListResult, MCPLoginPollResult, MCPLoginStartResult } from "../shared/protocol";

type Request = (method: string, params?: unknown) => Promise<unknown>;
type Owner = {
  id: number;
  isDestroyed(): boolean;
  once(event: "destroyed", listener: () => void): unknown;
  removeListener(event: "destroyed", listener: () => void): unknown;
};
type Attempt = {
  owner: number;
  name: string;
  request: Request;
  state: string;
  cancelled: boolean;
  exchanging: boolean;
  result: MCPLoginPollResult;
  dispose(): void;
};

/** A loopback callback and all credential writes stay on the initiating server. */
export class MCPLoginCoordinator {
  private readonly attempts = new Map<string, Attempt>();
  constructor(
    private readonly openExternal: (url: string) => Promise<void>,
    private readonly onSuccess: (senderID: number) => void,
  ) {}

  async start(owner: Owner, name: string, bind: () => Request): Promise<MCPLoginStartResult> {
    if (owner.isDestroyed()) throw new Error("MCP sign-in window is closed.");
    if ([...this.attempts.values()].some(a => a.owner === owner.id && a.result.status === "pending")) {
      throw new Error("Finish or cancel the current MCP sign-in first.");
    }
    const loginID = randomUUID();
    let listener: Server | undefined;
    let expiry: ReturnType<typeof setTimeout> | undefined;
    const attempt: Attempt = {
      owner: owner.id, name, request: bind(), state: "", cancelled: false, exchanging: false,
      result: { status: "pending", phase: "starting" },
      dispose: () => {
        listener?.close();
        listener?.closeAllConnections();
        clearTimeout(expiry);
        owner.removeListener("destroyed", destroyed);
      },
    };
    const destroyed = () => { void this.cancel(owner.id, loginID).catch(() => {}); };
    this.attempts.set(loginID, attempt);
    owner.once("destroyed", destroyed);
    expiry = setTimeout(() => {
      void this.cancel(owner.id, loginID).catch(() => {});
    }, 10 * 60 * 1000);
    // Admission returns the cancellation handle before metadata or OAuth work.
    // Presentation replacement may detach observers, never this native attempt.
    void (async () => {
      try {
        const list = await attempt.request("mcp/list") as MCPListResult;
        if (attempt.cancelled || owner.isDestroyed()) throw new Error("MCP sign-in cancelled.");
        const definition = list.servers.find(server => server.name === name);
        if (!definition) throw new Error("MCP server is no longer available.");
        const fixed = definition.oauth_redirect_uri;
        const redirect = new URL(fixed || "http://127.0.0.1:0/mcp/callback");
        if (redirect.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(redirect.hostname)
          || redirect.username || redirect.password || redirect.search || redirect.hash) {
          throw new Error("This server requires manual authorization with its configured redirect URI.");
        }
        listener = createServer((request, response) => {
          response.setHeader("Content-Type", "text/plain; charset=utf-8");
          response.setHeader("Cache-Control", "no-store");
          response.setHeader("Referrer-Policy", "no-referrer");
          const callback = new URL(request.url || "/", redirect.origin);
          if (request.method !== "GET" || request.headers.host !== redirect.host || callback.pathname !== redirect.pathname) {
            response.writeHead(404).end("Not found");
            return;
          }
          if (!attempt.state || callback.searchParams.get("state") !== attempt.state) {
            response.writeHead(400).end("Invalid authorization state. Return to Wuu and try again.");
            return;
          }
          if (attempt.cancelled || attempt.exchanging || attempt.result.status !== "pending") {
            response.writeHead(409).end("This sign-in is no longer waiting for a callback.");
            return;
          }
          const code = callback.searchParams.get("code");
          if (callback.searchParams.has("error") || !code) {
            attempt.result = { status: "error", error: "Authorization was declined or no authorization code was returned." };
            response.writeHead(400).end("Authorization did not complete. Return to Wuu.");
            void this.cancelAuthorization(attempt).catch(() => {});
            listener?.close();
            return;
          }
          attempt.exchanging = true;
          response.end("Authorization received. You can return to Wuu.");
          listener?.close();
          void (async () => {
            try {
              await attempt.request("mcp/auth/finish", { name, state: attempt.state, code });
              if (attempt.cancelled) return;
              await attempt.request("mcp/connect", { name });
              if (attempt.cancelled) return;
              attempt.result = { status: "success" };
              if (!owner.isDestroyed()) this.onSuccess(owner.id);
            } catch (error) {
              if (!attempt.cancelled) attempt.result = { status: "error", error: error instanceof Error ? error.message : String(error) };
            }
          })();
        });
        await new Promise<void>((resolve, reject) => {
          listener!.once("error", reject);
          listener!.listen(Number(redirect.port || 80), redirect.hostname.replace(/^\[|\]$/g, ""), () => {
            listener!.removeListener("error", reject);
            resolve();
          });
        });
        const address = listener.address();
        if (!address || typeof address === "string") throw new Error("MCP callback listener did not start.");
        redirect.port = String(address.port);
        if (attempt.cancelled) throw new Error("MCP sign-in cancelled.");
        const auth = await attempt.request("mcp/auth/start", { name, redirect_uri: redirect.href }) as MCPAuthStartResult;
        attempt.state = auth.state;
        if (attempt.cancelled || owner.isDestroyed()) throw new Error("MCP sign-in cancelled.");
        const authorization = new URL(auth.authorization_url);
        if (!["http:", "https:"].includes(authorization.protocol) || authorization.username || authorization.password) {
          throw new Error("MCP server returned an invalid authorization URL.");
        }
        await this.openExternal(authorization.href);
        if (!attempt.cancelled && attempt.result.status === "pending") attempt.result = { status: "pending", phase: "waiting" };
      } catch (error) {
        listener?.close();
        listener?.closeAllConnections();
        if (!attempt.cancelled) {
          attempt.cancelled = true;
          attempt.result = { status: "error", error: error instanceof Error ? error.message : String(error) };
        }
        // Keep startup failures pollable until consumed or expired. A cancelled
        // start still cleans a state that arrived after its handle was removed.
        await this.cancelAuthorization(attempt).catch(() => {});
      }
    })();
    return { login_id: loginID };
  }

  poll(senderID: number, loginID: string): MCPLoginPollResult {
    const attempt = this.attempts.get(loginID);
    if (!attempt || attempt.owner !== senderID) throw new Error("MCP sign-in expired; start again.");
    if (attempt.result.status !== "pending") {
      attempt.dispose();
      this.attempts.delete(loginID);
    }
    return attempt.result;
  }

  async cancel(senderID: number, loginID: string): Promise<{ ok: boolean }> {
    const attempt = this.attempts.get(loginID);
    if (!attempt || attempt.owner !== senderID) return { ok: false };
    await this.stop(loginID, attempt);
    return { ok: true };
  }

  private async stop(loginID: string, attempt: Attempt): Promise<void> {
    attempt.cancelled = true;
    attempt.dispose();
    this.attempts.delete(loginID);
    await this.cancelAuthorization(attempt);
  }

  private async cancelAuthorization(attempt: Attempt): Promise<void> {
    const state = attempt.state;
    attempt.state = "";
    // Clearing before dispatch deduplicates simultaneous cancellation. A late
    // start receipt supplies its own state and still uses the original requester.
    if (state) await attempt.request("mcp/auth/cancel", { name: attempt.name, state });
  }
}
