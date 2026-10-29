import { validateSessionRoute, isSessionRouteResult, unknownSessionRouteResult, SessionTransportError, type SessionRouteRequest, type SessionRouteResult } from "./session-transport.js";
import type { RuntimeHost, ServiceDescriptor, ServiceRequirement } from "./index.js";
import type { ModelImageV1, ModelFileV1 } from "./model.js";

/** Only negotiated on the local desktop owner's admitted stdio connection.
 * Neither a renderer field nor a remote transport principal confers this role. */
export interface DesktopSessionHostIdentityV1 {
  version: 1;
  host_id: string;
  /** Ed25519 public key, standard base64. Changes with the host generation. */
  public_key: string;
}
export interface DesktopSessionDispatchTicketV1 {
  version: 1;
  source_host: string;
  ticket_id: string;
}
/** UTF-8 payload bytes are signed. Relays MUST forward this exact string;
 * parsing and reserializing it changes the authenticated message. */
export interface DesktopSessionExportV1 {
  version: 1;
  payload: string;
  signature: string;
}
export interface DesktopSessionExportPayloadV1 {
  version: 1;
  source: {
    host_id: string;
    public_key: string;
    user_space_id?: string;
    product_domain: string;
    plugin_id: string;
  };
  /** Stable logical address remains unchanged through resolution. Desktop
   * discovery uses execution_host="desktop"; project results use
   * "desktop-project:<registered ID>" and existing scratch contexts use an
   * opaque "desktop-context:<scope hash>". These are not filesystem grants. */
  request: SessionRouteRequest;
  /** Absent in the preliminary export; required for import. The source binds
   * its ticket once to this exact resolved generation and signs a new payload. */
  target?: DesktopSessionHostIdentityV1;
  message?: {
    prompt: string;
    digest: string;
    images?: Array<ModelImageV1 & { sha256: string }>;
    files?: Array<ModelFileV1 & { sha256: string }>;
  };
}
export const DESKTOP_SESSION_DISPATCH_METHOD = "session/desktop/dispatch";

/** Public replacement-provider gateway. The source host mints tickets for its
 * selected provider; caller-created IDs or another provider's IDs are rejected. */
export const DESKTOP_SESSION_GATEWAY_SERVICE = "host.desktop.session";
export const DESKTOP_SESSION_GATEWAY_DESCRIPTOR: ServiceDescriptor = {
  name: DESKTOP_SESSION_GATEWAY_SERVICE, version: "1.0.0",
  methods: [{ name: "dispatch", input_schema: "desktop.session.ticket.v1", output_schema: "session.transport.dispatch.output.v1" }],
};
export function requireDesktopSessionGateway(): ServiceRequirement {
  return { name: DESKTOP_SESSION_GATEWAY_SERVICE, major_version: 1, required: true };
}

/** Adapter for a selected session.transport provider. Only the host-injected
 * ticket crosses the owner gateway; a provider cannot replace source text,
 * attachments, destination or policy by changing request fields. The host still
 * authenticates ticket ownership and expiry. This adapter never mints a ticket,
 * selects another provider, or automatically retries an uncertain dispatch.
 * Aborting after dispatch stops waiting, not already-admitted target work. */
export function desktopSessionTransport(host: RuntimeHost) {
  return {
    async dispatch(request: SessionRouteRequest, signal?: AbortSignal): Promise<SessionRouteResult> {
      validateSessionRoute(request);
      signal?.throwIfAborted();
      const ticket = request.transport_ticket;
      if (!ticket || ![ticket.id, ticket.source_host].every(value => typeof value === "string" && value.trim() !== "" && value.length <= 256 && !value.includes("\0"))) {
        throw new TypeError("Desktop transport requires a host-issued ticket from the selected provider invocation");
      }
      if (!host.supports("host.service.call")) throw new SessionTransportError("session_host_unavailable", "The selected host does not provide the desktop session gateway");
      let onAbort: (() => void) | undefined;
      try {
        const response = host.call("host.service.call", {
          service: DESKTOP_SESSION_GATEWAY_SERVICE, method: "dispatch", params: { ticket_id: ticket.id },
        }, { signal });
        const aborted = new Promise<SessionRouteResult>(resolve => {
          onAbort = () => resolve(unknownSessionRouteResult(request, "session_wait_cancelled"));
          signal?.addEventListener("abort", onAbort, { once: true });
          if (signal?.aborted) onAbort();
        });
        const result = await (signal ? Promise.race([response, aborted]) : response);
        return isSessionRouteResult(result, request) ? result : unknownSessionRouteResult(request, "session_invalid_receipt");
      } catch (error) {
        // A structured unavailable response is a gateway rejection, not an
        // excuse to substitute the local or remote default transport.
        if (error instanceof Error && "code" in error && error.code === "service_unavailable") throw error;
        return unknownSessionRouteResult(request, signal?.aborted ? "session_wait_cancelled" : "session_delivery_unknown");
      } finally {
        if (onAbort) signal?.removeEventListener("abort", onAbort);
      }
    },
  };
}
