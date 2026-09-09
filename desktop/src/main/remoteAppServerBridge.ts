import { createServer, type Server, type Socket } from "node:net";
import { projectRemoteHistory } from "./remoteHistory";
import { RemoteAttachments, threadAttachmentParams, threadContentParams } from "./remoteAttachments";
import { randomBytes, timingSafeEqual } from "node:crypto";
import type { AppServerResponse, ServerEvent } from "../shared/protocol";

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
  private subscribers = new Set<Socket>();
  private readonly compactSubscribers = new Set<Socket>();
  private readonly attachments = new RemoteAttachments();
  constructor(private readonly request: (workdir: string, method: string, params: unknown, reply: (response: Pick<AppServerResponse, "result" | "error">) => void, peerID: number) => Promise<unknown>, private readonly onPeerClose?: (peerID:number)=>void) {}

  notifyPeer(peerID:number,method:string,params:unknown):void {const socket=this.peerSockets.get(peerID);if(socket)this.send(socket,{method,params});}

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
    for (const socket of this.subscribers) this.send(socket, { ...event.message, workdir: event.workdir });
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

  private send(socket: Socket, message: unknown): void {
    if (socket.destroyed) return;
    if (socket.writableLength > MAX_LINE_BYTES) { socket.destroy(); return; }
    const projected = this.compactSubscribers.has(socket) ? this.attachments.project(projectRemoteHistory(message)) : message;
    socket.write(JSON.stringify(projected) + "\n");
  }

  private accept(socket: Socket, token: string, defaultWorkdir: string): void {
    const peerID=this.nextPeerID++;
    this.peerSockets.set(peerID,socket);
    const request=(cwd:string,method:string,params:unknown,reply:(response:Pick<AppServerResponse,"result"|"error">)=>void)=>this.request(cwd,method,params,reply,peerID);
    this.sockets.add(socket);
    socket.on("error", () => socket.destroy());
    socket.once("close", () => { this.peerSockets.delete(peerID);this.onPeerClose?.(peerID);this.sockets.delete(socket); this.subscribers.delete(socket); this.compactSubscribers.delete(socket); });
    socket.setTimeout(5000, () => socket.destroy());
    socket.setEncoding("utf8");
    let buffer = "", authenticated = false;
    // Duplicate ids on a live attachment share the original execution/result.
    const completed: string[] = [];
    const requests = new Map<string, { signature: string; reply?: unknown; waiting: number }>();
    socket.on("data", (chunk: string) => {
      buffer += chunk;
      if (Buffer.byteLength(buffer) > MAX_LINE_BYTES) { socket.destroy(); return; }
      for (;;) {
        const end = buffer.indexOf("\n");
        if (end < 0) break;
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        let input: Request & { token?: string };
        try { input = JSON.parse(line); } catch { socket.destroy(); return; }
        if (!input || typeof input !== "object") { socket.destroy(); return; }
        if (!authenticated) {
          const provided = Buffer.from(typeof input.token === "string" ? input.token : "");
          const expected = Buffer.from(token);
          if (provided.length !== expected.length || !timingSafeEqual(provided, expected)) { socket.destroy(); return; }
          authenticated = true;
          socket.setTimeout(0);
          this.subscribers.add(socket);
          this.send(socket, { ready: true });
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
          if (entry.reply !== undefined) this.send(socket, entry.reply);
          else entry.waiting++;
          continue;
        }
        if (requests.size >= 4096) { socket.destroy(); return; }
        entry = { signature, waiting: 1 };
        requests.set(key, entry);
        const pending = entry;
        const finish = (response: Pick<AppServerResponse, "result" | "error">) => {
          if (pending.reply !== undefined) return;
          pending.reply = { ...response, id };
          for (let n = 0; n < pending.waiting; n++) this.send(socket, pending.reply);
          completed.push(key);
          if (completed.length > 256) requests.delete(completed.shift()!);
        };
        void Promise.resolve().then(async () => {
          if(socket.destroyed)throw new Error("Remote connection closed");
          if (!this.compactSubscribers.has(socket)) return request(cwd, method, params, finish);
          if (method === "remote/content/read") {
            const input = params as { ref: string; offset?: number };
            return request(cwd, "thread/content/read", { ...threadContentParams(input.ref), offset: input.offset ?? 0 }, finish);
          }
          if (method === "remote/history/read") return request(cwd, "thread/history/read", params, finish);
          if (method === "remote/attachment/read" || method === "remote/attachment/preview") {
            const input = params as { ref?: unknown; offset?: unknown };
            const source = typeof input?.ref === "string" ? threadAttachmentParams(input.ref) : undefined;
            if (source) return request(cwd, "thread/attachment/read", { ...source, offset: input.offset ?? 0, preview: method.endsWith("preview") }, finish);
            if (method.endsWith("preview")) throw new Error("Preview is unavailable for this attachment");
            return this.attachments.read(params);
          }
          const hydrated = await this.attachments.hydrateRemote(params, async ref => {
            const source = threadAttachmentParams(ref)!;
            const chunks: string[] = [];
            let offset = 0, total = 1;
            while (offset < total) {
              const chunk = await request(cwd, "thread/attachment/read", { ...source, offset }, () => {}) as { data: string; total: number; offset: number };
              if (!chunk.data || chunk.offset !== offset || chunk.total > 64 * 1024 * 1024) throw new Error("Invalid attachment response");
              chunks.push(chunk.data); offset += chunk.data.length; total = chunk.total;
            }
            return chunks.join("");
          });
          return request(cwd, method, method === "thread/resume" ? { ...(hydrated as object), history_page: true } : hydrated, finish);
        }).then(
          result => finish({ result }),
          error => finish({ error: { code: "error", message: error instanceof Error ? error.message : String(error) } }),
        );
      }
    });
  }
}
