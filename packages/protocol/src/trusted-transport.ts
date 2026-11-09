/** Private authenticated embedding-hop protocol, v1. Never forward these
 * messages from remote RPC clients. Only a host's explicitly trusted local
 * stdio may present provenance; identity fields alone are not credentials. */
export interface TrustedTransportPrincipal {
  kind: "user" | "device" | "agent";
  issuer: string;
  tenant: string;
  subject: string;
}
export interface TrustedTransportPeer {
  version: 1;
  peer_id: string;
  principal: TrustedTransportPrincipal;
  owner: boolean;
  /** Private loopback POST /v1/validate, Authorization: Bearer <token>.
   * Return 204 iff the original authenticated connection remains valid. */
  validation: { address: string; token: string };
}
export interface TrustedTransportDispatch extends TrustedTransportPeer {
  method: string;
  params?: unknown;
}
export interface TrustedTransportReceipt {
  version: 1;
  generation: string;
  receipt: string;
}
export interface TrustedTransportCheck {
  version: 1;
  peer_id: string;
  receipt: string;
}
export interface TrustedTransportResponse extends TrustedTransportReceipt {
  message: { id?: string | number; result?: unknown; error?: { code: string; message: string } };
}
