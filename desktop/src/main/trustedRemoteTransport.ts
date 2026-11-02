import type { AppServerResponse, RuntimeContext, ServerEvent } from "../shared/protocol";
import type { TrustedTransportPeer, TrustedTransportPrincipal } from "../../../packages/protocol/src/trusted-transport";
import type { AppServerClientPool, BoundAppServerRequest, OwnedAppServerOperation } from "./appServerClients";

export type TransportPrincipal = TrustedTransportPrincipal;
export type TrustedRemotePeer = TrustedTransportPeer;
export type RemoteReceipt = { token: string; check: () => Promise<void>; generation?: string; release?: () => Promise<void> };
export type ScopedRemoteResponse = Pick<AppServerResponse, "result" | "error"> & { receipt?: RemoteReceipt };
// A wire result cannot manufacture the process-local check closure.
export function isScopedRemoteResponse(value: unknown): value is ScopedRemoteResponse {
  return !!value && typeof value === "object" && "receipt" in value && !!value.receipt && typeof value.receipt === "object" && "check" in value.receipt && typeof value.receipt.check === "function";
}
type ReceiptOwnership = { release: () => Promise<void>; forget: () => void };
const retainedPeers = new WeakMap<AppServerClientPool, Map<string, ReceiptOwnership>>();
function retainPeer(pool:AppServerClientPool,peer:TrustedRemotePeer,generation:string,ownership:ReceiptOwnership):()=>Promise<void> {
  const peers=retainedPeers.get(pool)??new Map<string,ReceiptOwnership>();retainedPeers.set(pool,peers);
  const key=JSON.stringify([peer.peer_id,generation]);const existing=peers.get(key);
  if(existing){if(existing!==ownership)ownership.forget();return existing.release;}
  const release=ownership.release;
  ownership.release=async()=>{if(peers.get(key)===ownership)peers.delete(key);await release();};
  peers.set(key,ownership);return ownership.release;
}
type Frame = { generation: string; version: 1; message: ScopedRemoteResponse; receipt: string };

export function trustedRemotePeer(value: unknown): value is TrustedRemotePeer {
  if (!value || typeof value !== "object") return false;
  const p = value as TrustedRemotePeer;
  return p.version === 1 && typeof p.owner === "boolean" && typeof p.peer_id === "string" && p.peer_id.length > 0 && p.peer_id.length <= 256 &&
    !!p.validation && typeof p.validation.address === "string" && typeof p.validation.token === "string" && p.validation.token.length >= 32 &&
    !!p.principal && ["user", "device", "agent"].includes(p.principal.kind) &&
    [p.principal.issuer, p.principal.tenant, p.principal.subject].every(v => typeof v === "string" && !!v.trim() && v.length <= 4096 && !v.includes("\0"));
}

/** Uses the already-resident session owner, never creates a principal-specific
 * runtime. Receipt rechecks go back through the same routing contract; losing a
 * process expires its receipts rather than upgrading to the new owner's pipe. */
export async function requestTrustedRemote(
  pool: AppServerClientPool, context: RuntimeContext, peer: TrustedRemotePeer,
  method: string, params: unknown, reply?: (response: ScopedRemoteResponse) => void,
): Promise<ScopedRemoteResponse> {
  const fields = params && typeof params === "object" ? params as Record<string, unknown> : {};
  const session = fields.thread_id ?? fields.session_id ?? fields.main_thread_id;
  const call = typeof session === "string" && session
    ? pool.bindSessionRequest(context, session, { localOnly: true })
    : pool.bindContextRequest(context, { localOnly: true });
  return requestTrustedRemoteOnConnection(pool, call, peer, method, params, reply);
}

/** Reuse an already captured receiver generation without resolving a new core. */
export async function requestTrustedRemoteOnConnection(
  pool: AppServerClientPool, call: BoundAppServerRequest, peer: TrustedRemotePeer,
  method: string, params: unknown, reply?: (response: ScopedRemoteResponse) => void,
  own?: (release: () => Promise<void>, forget: () => void) => void,
): Promise<ScopedRemoteResponse> {
  let operation: OwnedAppServerOperation<Frame>;
  const ownership:ReceiptOwnership={release:async()=>{await operation.release();},forget:()=>{queueMicrotask(()=>operation.forget());}};
  const unwrap = (frame: Frame): ScopedRemoteResponse => {
    if (frame?.version !== 1 || typeof frame.receipt !== "string" || !frame.receipt || !frame.generation || !frame.message) throw new Error("Trusted core response missing authorization receipt");
    return { ...frame.message, receipt: { token: frame.receipt, generation: frame.generation, release: retainPeer(pool,peer,frame.generation,ownership), check: async () => {
      await call("transport/check", { version: 1, peer_id: peer.peer_id, receipt: frame.receipt });
    } } };
  };
  operation = call.owned<Frame>("transport/dispatch", { ...peer, method, params }, {method:"transport/release",params:{version:1,peer_id:peer.peer_id},retainAfterResult:true}, response => {
    if (response.error) return;
    reply?.(unwrap(response.result as Frame));
  });
  own?.(() => ownership.release(), () => operation.forget());
  try { return unwrap(await operation.result); } catch(error) { if (!own) operation.forget(); throw error; }
}

/** Capture the emitting process now, before any async delivery queue yields. */
export function bindRemoteEventAuthorization(
  pool: AppServerClientPool, event: ServerEvent, peer: TrustedRemotePeer,
): () => Promise<RemoteReceipt> {
  if (event.kind !== "notification") throw new Error("Notification source required");
  const call = pool.bindEventRequest(event, { localOnly: true });
  const { method, params } = event.message;
  return async () => {
    const operation = call.owned<{ version: number; receipt: string; generation: string }>("transport/event", { ...peer, method, params }, {method:"transport/release",params:{version:1,peer_id:peer.peer_id},retainAfterResult:true});
    let result:{version:number;receipt:string;generation:string};
    try {result=await operation.result;} catch(error){operation.forget();throw error;}
    if (result?.version !== 1 || !result.receipt || !result.generation) throw new Error("Event authorization receipt missing");
    return { token: result.receipt, generation: result.generation, release: retainPeer(pool,peer,result.generation,operation), check: async () => {
      await call("transport/check", { version: 1, peer_id: peer.peer_id, receipt: result.receipt });
    } };
  };
}
