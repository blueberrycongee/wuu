import {
  desktopSessionTransport, DESKTOP_SESSION_GATEWAY_DESCRIPTOR,
  requireDesktopSessionGateway, SESSION_TRANSPORT_DESCRIPTOR,
  type RuntimeHost, type SessionRouteRequest, type SessionTransportTicketV1,
  type DesktopSessionExportV1, type DesktopSessionExportPayloadV1,
  type DesktopSessionHostIdentityV1,
} from "@wuu/plugin-sdk";

/** External replacement of the selected session.transport provider. Ticket
 * issuance and the original user-material export remain entirely host-owned. */
export function externalDesktopProvider(host: RuntimeHost) {
  const transport = desktopSessionTransport(host);
  return {
    provided: [SESSION_TRANSPORT_DESCRIPTOR],
    required: [requireDesktopSessionGateway()],
    gateway: DESKTOP_SESSION_GATEWAY_DESCRIPTOR,
    dispatch(request: SessionRouteRequest & { transport_ticket: SessionTransportTicketV1 }, signal?: AbortSignal) {
      return transport.dispatch(request, signal);
    },
  };
}

// Wire authoring uses only public packed types; no appserver or desktop-private
// module is reachable from this independently compiled author project.
export function resolvedIdentity(exported: DesktopSessionExportV1): DesktopSessionHostIdentityV1 | undefined {
  const payload = JSON.parse(exported.payload) as DesktopSessionExportPayloadV1;
  return payload.target;
}
