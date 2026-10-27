import { createServer, type Server, type Socket } from "node:net";
import { projectRemoteHistory } from "./remoteHistory";
import { RemoteAttachments, attachmentReadTarget, threadContentParams } from "./remoteAttachments";
import { randomBytes, timingSafeEqual } from "node:crypto";
import type { AppServerResponse, ExtensionInventoryRecord, PluginDesktopActivationParams, ServerEvent } from "../shared/protocol";
import { isScopedRemoteResponse, trustedRemotePeer, type TrustedRemotePeer, type RemoteReceipt, type ScopedRemoteResponse } from "./trustedRemoteTransport";
import { PluginDesktopActivations } from "./pluginDesktopActivations";

export type RemoteAppServerEndpoint = { address: string; token: string };
type Request = { id: string | number; method: string; params?: unknown; workdir?: string };
const MAX_LINE_BYTES = 64 * 1024 * 1024;

/** Local authenticated transport into the desktop's existing Go service pool.
 * Connections own subscriptions and response routes, never task lifetimes. */
export class RemoteAppServerBridge {
  private server?: Server;
  private endpoint?: RemoteAppServerEndpoint;
  private defaultWorkdir = "";
  private sockets = new Set<Socket>();
  private peerSockets = new Map<number,Socket>();
  private nextPeerID = 1;
  private readonly activationOwners = new Map<number, PluginDesktopActivations>();
  private subscribers = new Set<Socket>();
  private readonly compactSubscribers = new Set<Socket>();
  private readonly attachments = new Map<Socket, RemoteAttachments>();
  private readonly releases = new Map<Socket, Map<string, () => Promise<void>>>();
  private readonly peers = new Map<Socket, TrustedRemotePeer>();
  private readonly receipts = new Map<Socket, Map<string, RemoteReceipt>>();
  private readonly deliveryBudget = new Map<Socket, { count: number; bytes: number }>();
  private readonly deliveries = new Map<Socket, Promise<void>>();
  constructor(private readonly request: (workdir: string, method: string, params: unknown, reply: (response: Pick<AppServerResponse, "result" | "error">) => void, peerID: number, activations: PluginDesktopActivations, peer: TrustedRemotePeer) => Promise<unknown>, private readonly onPeerClose?: (peerID:number, peer?: TrustedRemotePeer)=>void, private readonly authorizeEvent?: (event: ServerEvent, peer: TrustedRemotePeer) => () => Promise<RemoteReceipt>) {}

  notifyPeer(peerID:number,method:string,params:unknown):void { const socket=this.peerSockets.get(peerID); if(socket && this.peers.get(socket)?.owner) this.send(socket,{method,params}); }

  currentEndpoint(): RemoteAppServerEndpoint | undefined { return this.endpoint; }

  async start(workdir: string): Promise<RemoteAppServerEndpoint> {
    this.defaultWorkdir = workdir;
    if (this.endpoint) return this.endpoint;
    const token = randomBytes(32).toString("hex");
    const server = createServer(socket => this.accept(socket, token, this.defaultWorkdir));
    this.server = server;
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", () => { server.off("error", reject); resolve(); });
    });
    server.on("error", () => this.stop());
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("Local app-server listener unavailable");
    this.endpoint = { address: `127.0.0.1:${address.port}`, token };
    return this.endpoint;
  }

  publish(event: ServerEvent): void {
    if (event.kind === "server-exit") {
      // A fresh attach obtains new snapshots after the core restarts.
      for (const socket of this.subscribers) socket.destroy();
    }
    if (event.kind !== "notification") return;
    if (event.message.method === "plugin/inventory/changed") {
      const params = event.message.params as { extension_inventory?: ExtensionInventoryRecord[] } | undefined;
      if (params?.extension_inventory) {
        for (const owner of this.activationOwners.values()) owner.observeInventory(event.workdir, params.extension_inventory);
      }
    }
    for (const socket of this.subscribers) this.publishTo(socket, event);
  }

  stop(): void {
    this.endpoint = undefined;
    for (const socket of this.sockets) socket.destroy();
    this.subscribers.clear();
    this.compactSubscribers.clear();
    this.attachments.clear();
    this.server?.close();
    this.server = undefined;
  }

  private publishTo(socket: Socket, event: Extract<ServerEvent, { kind: "notification" }>): void {
    const peer = this.peers.get(socket);
    if (!peer) return;
    const { workdir, message: { method, params } } = event;
    // Without a scoped backend, only explicitly owner-bound legacy clients may
    // receive legacy events. Production always supplies authorizeEvent.
    if (!this.authorizeEvent) { if(peer.owner) this.send(socket, { method, params, workdir }); return; }
    let authorize: () => Promise<RemoteReceipt>;
    try { authorize = this.authorizeEvent(event, peer); } catch { return; }
    this.queue(socket, async () => {
      const receipt = await authorize();
      if (socket.destroyed) { await receipt.release?.(); return; }
      if (receipt.generation && receipt.release) this.releases.get(socket)?.set(receipt.generation, receipt.release);
      await receipt.check();
      this.send(socket, { method, params, workdir }, receipt);
    }, Buffer.byteLength(JSON.stringify({method,params,workdir})));
  }

  private queue(socket: Socket, deliver: () => Promise<void>, bytes = 0): void {
    const budget = this.deliveryBudget.get(socket) ?? { count: 0, bytes: 0 };
    if (budget.count >= 256 || budget.bytes + bytes > MAX_LINE_BYTES) { socket.destroy(); return; }
    budget.count++; budget.bytes += bytes; this.deliveryBudget.set(socket, budget);
    const previous = this.deliveries.get(socket) ?? Promise.resolve();
    const next = previous.then(async () => { if (!socket.destroyed) await deliver(); }).catch(() => {}).finally(() => { budget.count--; budget.bytes -= bytes; });
    this.deliveries.set(socket, next);
  }

  private send(socket: Socket, message: unknown, receipt?: RemoteReceipt): void {
    if (socket.destroyed) return;
    if (socket.writableLength > MAX_LINE_BYTES) { socket.destroy(); return; }
    const projected = this.compactSubscribers.has(socket) ? this.attachments.get(socket)!.project(projectRemoteHistory(message)) : message;
    if (receipt) {
      const receipts = this.receipts.get(socket)!;
      receipts.set(receipt.token, receipt);
      if (receipt.generation && receipt.release) this.releases.get(socket)?.set(receipt.generation, receipt.release);
      if (receipts.size > 65536) receipts.delete(receipts.keys().next().value!);
      socket.write(JSON.stringify({ transport_frame: 1, receipt: receipt.token, message: projected }) + "\n");
    } else socket.write(JSON.stringify(projected) + "\n");
  }

  private accept(socket: Socket, token: string, defaultWorkdir: string): void {
    const peerID=this.nextPeerID++;
    this.peerSockets.set(peerID,socket);
    const activations = new PluginDesktopActivations(`remote:${peerID}`);
    const request = async (cwd: string, method: string, params: unknown, reply: (response: Pick<AppServerResponse, "result" | "error">) => void) => {
      if (socket.destroyed) throw new Error("Remote connection closed");
      // ACK ownership comes from this authenticated socket, never browser fields
      // or a fresh lookup of the currently selected workspace/process.
      if (method === "plugin/desktop-activation") {
        await activations.report(params as PluginDesktopActivationParams);
        return { ok: true };
      }
      const peer = this.peers.get(socket)!;
      if (method.startsWith("transport/")) throw new Error("Remote transport methods are forbidden");
      return this.request(cwd, method, params, reply, peerID, activations, peer);
    };
    const attachments = new RemoteAttachments();
    this.attachments.set(socket, attachments);
    this.sockets.add(socket);
    socket.on("error", () => socket.destroy());
    socket.once("close", () => {
      this.activationOwners.delete(peerID);
      const peer = this.peers.get(socket);
      for (const release of this.releases.get(socket)?.values() ?? []) void release().catch(() => {});
      this.releases.delete(socket);
      this.peers.delete(socket); this.receipts.delete(socket); this.deliveries.delete(socket); this.deliveryBudget.delete(socket); this.attachments.delete(socket);
      void activations.disconnect().catch(error => console.error("Remote renderer disconnect failed", error));
      this.peerSockets.delete(peerID);this.onPeerClose?.(peerID, peer);this.sockets.delete(socket); this.subscribers.delete(socket); this.compactSubscribers.delete(socket);
    });
    socket.setTimeout(5000, () => socket.destroy());
    socket.setEncoding("utf8");
    let buffer = "", authenticated = false;
    // Duplicate ids on a live attachment share the original execution/result.
    const completed: string[] = [];
    const requests = new Map<string, { signature: string; reply?: unknown; receipt?: RemoteReceipt; waiting: number }>();
    socket.on("data", (chunk: string) => {
      buffer += chunk;
      if (Buffer.byteLength(buffer) > MAX_LINE_BYTES) { socket.destroy(); return; }
      for (;;) {
        const end = buffer.indexOf("\n");
        if (end < 0) break;
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        let input: Request & { token?: string; transport?: unknown; transport_check?: { id: string; receipt: string } };
        try { input = JSON.parse(line); } catch { socket.destroy(); return; }
        if (!input || typeof input !== "object") { socket.destroy(); return; }
        if (!authenticated) {
          const provided = Buffer.from(typeof input.token === "string" ? input.token : "");
          const expected = Buffer.from(token);
          if (provided.length !== expected.length || !timingSafeEqual(provided, expected)) { socket.destroy(); return; }
          if (!trustedRemotePeer(input.transport)) { socket.destroy(); return; }
          this.peers.set(socket, Object.freeze({ ...input.transport, validation: Object.freeze({ ...input.transport.validation }), principal: Object.freeze({ ...input.transport.principal }) }));
          this.receipts.set(socket, new Map());
          this.releases.set(socket, new Map());
          authenticated = true;
          this.activationOwners.set(peerID, activations);
          socket.setTimeout(0);
          this.subscribers.add(socket);
          this.send(socket, { ready: true, transport_version: 1 });
          continue;
        }
        if (input.transport_check) {
          const check = input.transport_check;
          const receipt = this.receipts.get(socket)?.get(check.receipt);
          void Promise.resolve().then(async () => {
            if (!receipt) throw new Error("Receipt expired");
            await receipt.check();
          }).then(() => this.send(socket, { transport_checked: check.id, allowed: true }),
            () => this.send(socket, { transport_checked: check.id, allowed: false }));
          continue;
        }
        if ((typeof input.id !== "string" && typeof input.id !== "number") || typeof input.method !== "string") {
          socket.destroy(); return;
        }
        const { id, method, params } = input;
        if (method === "workspace/list" && (params as { remote_delivery?: number } | undefined)?.remote_delivery === 1) {
          this.compactSubscribers.add(socket);
        }
        const cwd = typeof input.workdir === "string" && input.workdir ? input.workdir : defaultWorkdir;
        const key = JSON.stringify(id), signature = JSON.stringify([cwd, method, params]);
        let entry = requests.get(key);
        if (entry && entry.signature !== signature) { socket.destroy(); return; }
        if (entry) {
          if (entry.reply !== undefined) { const replay = entry; this.queue(socket, async () => { try { await replay.receipt?.check(); this.send(socket, replay.reply, replay.receipt); } catch { this.send(socket, { id, error: { code: "access_denied", message: "Cached response authorization expired" } }); } }); }
          else entry.waiting++;
          continue;
        }
        if (requests.size >= 4096) { socket.destroy(); return; }
        entry = { signature, waiting: 1 };
        requests.set(key, entry);
        const pending = entry;
        const finish = (response: ScopedRemoteResponse) => {
          if (pending.reply !== undefined) return;
          const { receipt, ...body } = response;
          if (socket.destroyed) { void receipt?.release?.().catch(() => {}); return; }
          if (receipt?.generation && receipt.release) this.releases.get(socket)?.set(receipt.generation, receipt.release);
          pending.reply = { ...body, id }; pending.receipt = receipt;
          if (!receipt && !this.peers.get(socket)!.owner && !response.error) {
            pending.reply = { id, error: { code: "access_denied", message: "Scoped backend required" } };
          }
          if (receipt) this.queue(socket, async () => { try { await receipt.check(); for (let n = 0; n < pending.waiting; n++) this.send(socket, pending.reply, receipt); } catch { this.send(socket, { id, error: { code: "access_denied", message: "Response authorization expired" } }); } });
          else for (let n = 0; n < pending.waiting; n++) this.send(socket, pending.reply);
          completed.push(key);
          if (completed.length > 256) requests.delete(completed.shift()!);
        };
        void Promise.resolve().then(async () => {
          if(socket.destroyed)throw new Error("Remote connection closed");
          if (method.startsWith("transport/")) throw new Error("Remote transport methods are forbidden");
          if (!this.peers.get(socket)!.owner && (method.startsWith("desktop/") || method.startsWith("plugin/") || method.startsWith("remote/attachment/"))) throw new Error("This operation requires a scoped provider");
          if (method.startsWith("auth/chatgpt/login/")) {
            throw new Error("ChatGPT sign-in must be started on the local desktop");
          }
          if (method === "engine/authenticate" && (params as { method_id?: unknown } | undefined)?.method_id === "local-cli") {
            throw new Error("CLI sign-in must be started on the local desktop");
          }
          if (!this.compactSubscribers.has(socket)) return request(cwd, method, params, finish);
          if (method === "remote/content/read") {
            const input = params as { ref: string; offset?: number };
            return request(cwd, "thread/content/read", { ...threadContentParams(input.ref), offset: input.offset ?? 0 }, finish);
          }
          if (method === "remote/history/read") return request(cwd, "thread/history/read", params, finish);
          if (method === "remote/attachment/read" || method === "remote/attachment/preview") {
            const input = params as { ref?: unknown; offset?: unknown };
            const source = typeof input?.ref === "string" ? attachmentReadTarget(input.ref) : undefined;
            if (source) return request(cwd, source.method, { ...source.params, offset: input.offset ?? 0, preview: method.endsWith("preview") }, finish);
            if (method.endsWith("preview")) throw new Error("Preview is unavailable for this attachment");
            return attachments.read(params);
          }
          if (!this.peers.get(socket)!.owner) {
            const assertScoped = (value: unknown): void => {
              if (!value || typeof value !== "object") return;
              if ("remote_ref" in value && typeof value.remote_ref === "string" && !value.remote_ref.startsWith("thread:")) throw new Error("Scoped attachment reference required");
              for (const child of Object.values(value)) assertScoped(child);
            };
            assertScoped(params);
          }
          const hydrated = await attachments.hydrateRemote(params, async ref => {
            const source = attachmentReadTarget(ref)!;
            const chunks: string[] = [];
            let offset = 0, total = 1;
            while (offset < total) {
              const response = await request(cwd, source.method, { ...source.params, offset }, () => {});
              const chunk = (isScopedRemoteResponse(response) ? response.result : response) as { data: string; total: number; offset: number };
              if (!chunk.data || chunk.offset !== offset || chunk.total > 64 * 1024 * 1024) throw new Error("Invalid attachment response");
              chunks.push(chunk.data); offset += chunk.data.length; total = chunk.total;
            }
            return chunks.join("");
          });
          return request(cwd, method, method === "thread/resume" ? { ...(hydrated as object), history_page: true } : hydrated, finish);
        }).then(
          result => finish(isScopedRemoteResponse(result) ? result : { result }),
          error => finish({ error: { code: "error", message: error instanceof Error ? error.message : String(error) } }),
        );
      }
    });
  }
}
