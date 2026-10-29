import { createHash } from "node:crypto";
import { resolve } from "node:path";
import type { RuntimeContext, ServerEvent } from "../shared/protocol";
import { validateSessionRoute, type SessionRouteRequest, type SessionRouteResult } from "../../../packages/plugin-sdk/src/session-transport";
import { DESKTOP_SESSION_DISPATCH_METHOD, type DesktopSessionExportV1, type DesktopSessionExportPayloadV1, type DesktopSessionHostIdentityV1 } from "../../../packages/plugin-sdk/src/desktop-session-bridge";
import type { AppServerClientPool, BoundLocalAppServerHost } from "./appServerClients";
import { AppServerResponseError } from "./appServerErrors";

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function token(value: unknown): value is string {
  return typeof value === "string" && value.trim() !== "" && value.length <= 256 && !value.includes("\0");
}
function identity(initialized: unknown): DesktopSessionHostIdentityV1 {
  const value = record(initialized) ? initialized.desktop_session_transport : undefined;
  if (!record(value) || value.version !== 1 || !token(value.host_id) || !token(value.public_key)) throw new Error("Local host has not negotiated desktop session transport v1");
  return { version: 1, host_id: value.host_id, public_key: value.public_key };
}
function logicalHost(context: RuntimeContext): string {
  if (context.kind === "project") return `desktop-project:${context.project_id}`;
  if (context.kind === "no_project") return `desktop-context:${createHash("sha256").update(resolve(context.cwd)).digest("hex")}`;
  throw new Error("Desktop session target must be a registered Coding workspace");
}
function parseExport(value: DesktopSessionExportV1): DesktopSessionExportPayloadV1 {
  if (!record(value) || value.version !== 1 || typeof value.payload !== "string" || !token(value.signature)) throw new Error("Invalid desktop session export");
  const payload = JSON.parse(value.payload);
  if (!record(payload) || payload.version !== 1 || !record(payload.source)) throw new Error("Invalid desktop session export payload");
  validateSessionRoute(payload.request);
  return payload as unknown as DesktopSessionExportPayloadV1;
}

/** Main-process relay, not an RPC proxy. Logical discovery never pins all listed
 * hosts. Each operation resolves one registered context, retains its exact local
 * generation, then asks the source to bind and sign that resolved target. */
export class DesktopSessionBridge {
  constructor(private readonly pool: AppServerClientPool, private readonly contexts: () => RuntimeContext[]) {}

  async handleServerRequest(event: ServerEvent): Promise<void> {
    if (event.kind !== "server-request" || !event.message.method.startsWith("session/desktop/")) return;
    const reply = this.pool.bindMainServerRequest(event);
    try {
      if (event.message.method !== DESKTOP_SESSION_DISPATCH_METHOD) throw new Error("Unsupported desktop session bridge method");
      // Capture before yielding: wire workdir/locator fields are descriptive,
      // never substitutes for the pool's actual source connection identity.
      const source = this.pool.bindLocalEventHost(event);
      if (source.context.kind !== "user") throw new Error("Desktop session source requires an owner-admitted user host");
      const params = event.message.params;
      if (!record(params) || params.version !== 1 || !token(params.ticket_id) || !token(params.source_host)) throw new Error("Invalid desktop session ticket");
      const trustedSource = identity(await source.initialized);
      if (params.source_host !== trustedSource.host_id) throw new Error("Desktop session source host identity mismatch");
      const exported = await source.request<DesktopSessionExportV1>("session/desktop/export", { ticket_id: params.ticket_id });
      const payload = parseExport(exported);
      if (payload.source.host_id !== trustedSource.host_id || payload.source.public_key !== trustedSource.public_key
        || payload.source.user_space_id !== source.context.user_space_id || payload.source.product_domain !== source.context.product_domain
        || !token(payload.source.plugin_id)) throw new Error("Desktop session export source identity or scope mismatch");
      if (payload.request.operation === "create") throw new Error("Desktop session bridge does not create sessions");
      const result = payload.request.operation === "list"
        ? await this.list(payload.request, source)
        : await this.dispatch(payload, params.ticket_id, trustedSource, source);
      reply.respond(result);
    } catch (error) {
      reply.reject(error instanceof Error ? error.message : String(error));
    }
  }

  private registeredContexts(): Map<string, RuntimeContext> {
    const contexts = new Map<string, RuntimeContext>();
    for (const context of this.contexts()) {
      const host = logicalHost(context);
      const existing = contexts.get(host);
      if (existing && resolve(existing.cwd) !== resolve(context.cwd)) throw new Error("Registered desktop session scope is ambiguous");
      // A resident context can describe the thread previously viewed there.
      // Discovery addresses the registered scope, not that unrelated thread;
      // preserve its execution/storage host fence while clearing the hint.
      contexts.set(host, context.session_locator
        ? { ...context, session_locator: { ...context.session_locator, session_id: undefined } }
        : context);
    }
    return contexts;
  }

  private async list(request: SessionRouteRequest, source: BoundLocalAppServerHost): Promise<SessionRouteResult> {
    if (request.locator.execution_host !== "desktop") throw new Error("Desktop session list requires the local desktop scope");
    const sessions: unknown[] = [];
    // Read and release each host before warming the next. Returned addresses
    // identify registered logical scopes, not expiring subprocess identities.
    for (const [host, context] of this.registeredContexts()) {
      source.check();
      const target = this.pool.bindLocalContextHost(context);
      const release = target.retain();
      try {
        const targetIdentity = identity(await target.initialized);
        source.check();
        const result = await target.request<{ sessions: unknown[] }>("session/desktop/list", {});
        if (!record(result) || !Array.isArray(result.sessions)) throw new Error("Local session list response is invalid");
        for (const session of result.sessions) {
          if (!record(session) || !record(session.locator) || session.locator.version !== 1
            || session.locator.execution_host !== `desktop:${targetIdentity.host_id}` || !token(session.locator.session_id)
            || session.session_id !== session.locator.session_id) throw new Error("Local session list returned an invalid host locator");
          sessions.push({ ...session, locator: { ...session.locator, execution_host: host } });
        }
      } finally { release(); }
    }
    return { version: 1, operation_id: request.operation_id, locator: request.locator, status: "completed", result: { sessions } };
  }

  private async dispatch(payload: DesktopSessionExportPayloadV1, ticketID: string, trustedSource: DesktopSessionHostIdentityV1, source: BoundLocalAppServerHost): Promise<SessionRouteResult> {
    const request = payload.request;
    const host = request.locator.execution_host;
    const context = this.registeredContexts().get(host);
    if (!context) throw new Error("The located desktop host is no longer registered or available");
    source.check();
    const target = this.pool.bindLocalSessionHost(context, request.locator.session_id!);
    const release = target.retain();
    try {
      const targetIdentity = identity(await target.initialized);
      source.check();
      // The source is the only signer. Its immutable ticket binds this selected
      // generation once; desktop never rewrites the signed logical request.
      const boundExport = await source.request<DesktopSessionExportV1>("session/desktop/export", { ticket_id: ticketID, target: targetIdentity });
      const resolved = parseExport(boundExport);
      const { target: _previousTarget, ...original } = payload;
      const { target: resolvedTarget, ...bound } = resolved;
      if (!resolvedTarget || resolvedTarget.version !== 1 || resolvedTarget.host_id !== targetIdentity.host_id || resolvedTarget.public_key !== targetIdentity.public_key
        || JSON.stringify(original) !== JSON.stringify(bound)) throw new Error("Source export changed its immutable request or resolved target binding");
      source.check();
      target.check();
      // A removed/moved registration cannot borrow the former context's owner.
      const current = this.registeredContexts().get(host);
      if (!current || resolve(current.cwd) !== resolve(context.cwd)) throw new Error("Desktop target registration changed before dispatch");
      try {
        const result = await target.request<SessionRouteResult>(DESKTOP_SESSION_DISPATCH_METHOD, { export: boundExport, trusted_source: trustedSource });
        if (!record(result) || result.version !== 1 || result.operation_id !== request.operation_id
          || !record(result.locator) || result.locator.execution_host !== host || result.locator.session_id !== request.locator.session_id
          || !["completed", "pending", "unknown"].includes(result.status)) throw new Error("Target returned an invalid session receipt");
        return result;
      } catch (error) {
        if (error instanceof AppServerResponseError) throw error;
        return { version: 1, operation_id: request.operation_id, locator: request.locator, status: "unknown",
          error: { code: "session_outcome_unknown", message: "Target dispatch was interrupted before its receipt was confirmed; inspect the same operation before retrying." } };
      }
    } finally { release(); }
  }
}
