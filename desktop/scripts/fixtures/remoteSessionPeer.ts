// Synthetic encrypted full-control peer for desktop acceptance; no real account.
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { appendFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { b64decode, b64encode } from "../../../clients/core/src/b64";
import { bytesEqual, randomBytes, utf8Decode, utf8Encode } from "../../../clients/core/src/bytes";
import { RemoteClient, type Credentials, type WebSocketFactory, type WebSocketLike } from "../../../clients/core/src/client";
import type { ProtocolEnvelope } from "../../../clients/core/src/rpc";
import { type Channel, type HS1, Identity, acceptHandshake, encodeKey, verifyRelayAuth } from "../../../clients/core/src/secure";
import { type E2EMsg, KIND_HANDSHAKE, KIND_SEALED, type RelayMsg, TYPE_AUTH, TYPE_AUTH_OK, TYPE_CHALLENGE, TYPE_FRAME, TYPE_HELLO, splitPayload, wrapPayload } from "../../../clients/core/src/wire";
import type { SessionConnectionHooks, RemoteSessionRoute } from "../../src/main/sessionRoutes";
// --- In-memory socket ----------------------------------------------------------

type Listener = (ev: unknown) => void;

class FakeSocket implements WebSocketLike {
  private listeners = new Map<string, Listener[]>();
  closed = false;
  onServerReceive: (data: string) => void = () => {};

  constructor() {
    queueMicrotask(() => {
      if (!this.closed) this.emit("open", {});
    });
  }

  send(data: string): void {
    if (this.closed) throw new Error("socket closed");
    queueMicrotask(() => {
      if (!this.closed) this.onServerReceive(data);
    });
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    queueMicrotask(() => this.emit("close", {}));
  }

  serverSend(data: string): void {
    queueMicrotask(() => {
      if (!this.closed) this.emit("message", { data });
    });
  }

  addEventListener(type: "open" | "message" | "close" | "error", listener: (ev: never) => void): void {
    const arr = this.listeners.get(type) ?? [];
    arr.push(listener as Listener);
    this.listeners.set(type, arr);
  }

  private emit(type: string, ev: unknown): void {
    for (const l of this.listeners.get(type) ?? []) l(ev);
  }
}

// --- Fake relay + host ----------------------------------------------------------

class FakeHost {
  onCall: (env: ProtocolEnvelope) => void = () => {};
  readonly host = Identity.generate();
  phonePub: Uint8Array | null = null;
  session = "sess-1";
  private sessionCounter = 1;
  private channel: Channel | null = null;
  private seq = 0;
  private spool: Array<{ seq: number; line: unknown }> = [];
  spoolLost = false;
  lastAck = 0;
  attachCount = 0;
  compressionEnabled = false;
  compressionAccepted = false;
  suppressPong = false;
  pingCount = 0;
  attachProfiles: Array<string | undefined> = [];
  readonly sockets: FakeSocket[] = [];
  readonly uplinkEnvelopes: ProtocolEnvelope[] = [];
  respondToCalls = true;
  handleCall: (env: ProtocolEnvelope) => unknown = (env) => ({ echo: env.method });

  readonly factory: WebSocketFactory = () => {
    const sock = new FakeSocket();
    this.sockets.push(sock);
    let authNonce: Uint8Array | null = null;
    sock.onServerReceive = (data) => {
      const msg = JSON.parse(data) as RelayMsg;
      switch (msg.type) {
        case TYPE_HELLO: {
          authNonce = randomBytes(32);
          sock.serverSend(JSON.stringify({ type: TYPE_CHALLENGE, nonce: b64encode(authNonce) } satisfies RelayMsg));
          return;
        }
        case TYPE_AUTH: {
          const okSig =
            authNonce !== null &&
            this.phonePub !== null &&
            verifyRelayAuth(this.phonePub, authNonce, b64decode(msg.sig ?? ""), "phone");
          sock.serverSend(
            JSON.stringify(
              okSig
                ? ({ type: TYPE_AUTH_OK, online: true } satisfies RelayMsg)
                : ({ type: "auth_err", code: "bad_signature" } satisfies RelayMsg),
            ),
          );
          return;
        }
        case TYPE_FRAME: {
          const { kind, body } = splitPayload(b64decode(msg.payload ?? ""));
          if (kind === KIND_HANDSHAKE) this.onHandshake(sock, JSON.parse(utf8Decode(body)) as HS1);
          else if (kind === KIND_SEALED) this.onSealed(sock, body);
          return;
        }
        default:
          return;
      }
    };
    return sock;
  };

  current(): FakeSocket {
    return this.sockets[this.sockets.length - 1];
  }

  endAppConnection(): void {
    this.sendSealed(this.current(), { t: "bye", reason: "app-server connection closed" });
  }

  drop(): void {
    this.current().close();
  }

  private onHandshake(sock: FakeSocket, hs1: HS1): void {
    const { channel, hs2 } = acceptHandshake(this.host, hs1, (dev) =>
      this.phonePub !== null && bytesEqual(dev, this.phonePub),
    );
    this.channel = channel;
    const body = utf8Encode(JSON.stringify({ t: "hs2", ...hs2 }));
    sock.serverSend(
      JSON.stringify({
        type: TYPE_FRAME,
        from: encodeKey(this.host.public_()),
        payload: b64encode(wrapPayload(KIND_HANDSHAKE, body)),
      } satisfies RelayMsg),
    );
  }

  private onSealed(sock: FakeSocket, body: Uint8Array): void {
    if (!this.channel) return;
    const msg = JSON.parse(utf8Decode(this.channel.open(body))) as E2EMsg;
    switch (msg.t) {
      case "attach": {
        this.attachCount++;
        this.compressionAccepted = msg.accept_line_compression === "gzip";
        this.attachProfiles.push(msg.client_profile);
        const recv = msg.recv ?? 0;
        const gap = this.spool.length > 0 && recv < this.spool[0].seq - 1;
        const canResume = !this.spoolLost && msg.prev === this.session && !gap && recv <= this.seq;
        if (canResume) {
          this.sendSealed(sock, { t: "attached", session: this.session, resumed: true });
          for (const item of this.spool) {
            if (item.seq > recv) this.sendSealed(sock, { t: "rpc", seq: item.seq, line: item.line });
          }
        } else {
          this.sessionCounter++;
          this.session = `sess-${this.sessionCounter}`;
          this.seq = 0;
          this.spool = [];
          this.spoolLost = false;
          this.sendSealed(sock, { t: "attached", session: this.session, resumed: false });
        }
        return;
      }
      case "rpc": {
        const env = msg.line as ProtocolEnvelope;
        this.uplinkEnvelopes.push(env);
        this.onCall(env);
        if (this.respondToCalls && env.method && env.id !== undefined) {
          this.sendLine({ id: env.id, result: this.handleCall(env) });
        }
        return;
      }
      case "ack": {
        this.lastAck = msg.recv ?? 0;
        this.spool = this.spool.filter((i) => i.seq > this.lastAck);
        return;
      }
      case "ping":
        this.pingCount++;
        if (this.suppressPong) return;
        this.sendSealed(sock, { t: "pong" });
        return;
      default:
        return;
    }
  }

  /** Queues one downlink app-server line; delivers live when a channel and
   *  socket exist, spools for replay otherwise (mirrors the host spool). */
  sendLine(line: unknown): void {
    this.seq++;
    this.spool.push({ seq: this.seq, line });
    const sock = this.sockets[this.sockets.length - 1];
    if (this.channel && sock && !sock.closed) {
      this.sendSealed(sock, { t: "rpc", seq: this.seq, line });
    }
  }

  sendState(ver: number): void {
    const sock = this.current();
    if (this.channel && sock && !sock.closed) {
      this.sendSealed(sock, { t: "state", ver, host: { name: "fake host" }, running: [] });
    }
  }

  private sendSealed(sock: FakeSocket, msg: E2EMsg): void {
    if (!this.channel) return;
    if (this.compressionEnabled && this.compressionAccepted && msg.t === "rpc") {
      const line = JSON.stringify(msg.line);
      if (line.length > 4096) msg = { ...msg, line: undefined, line_gzip: b64encode(gzipSync(line)) };
    }
    const sealed = this.channel.seal(utf8Encode(JSON.stringify(msg)));
    sock.serverSend(
      JSON.stringify({
        type: TYPE_FRAME,
        from: encodeKey(this.host.public_()),
        payload: b64encode(wrapPayload(KIND_SEALED, sealed)),
      } satisfies RelayMsg),
    );
  }
}


let fixture: { core: string; home: string; workdir: string; log: string };
const peer = new FakeHost();
let child: ChildProcessWithoutNullStreams;
const outputMethods: string[] = [];
export const hostID = encodeKey(peer.host.public_());
export function configure(options: typeof fixture) {
  fixture = options;
  child = spawn(fixture.core, ["app-server", "--desktop-execution-defaults", "--workdir", fixture.workdir], {
    cwd: fixture.workdir,
    env: { ...process.env, WUU_HOME: fixture.home, WUU_NODE_EXECUTABLE: process.execPath, ELECTRON_RUN_AS_NODE: "1", WUU_ENABLE_CUA_MAC: "0", WUU_ENABLE_BROWSER: "0" },
    stdio: ["pipe", "pipe", "pipe"],
  });
  child.stderr.on("data", chunk => appendFileSync(fixture.log, chunk));
  createInterface({ input: child.stdout }).on("line", line => {
    const env: ProtocolEnvelope = JSON.parse(line);
    if (env.method) outputMethods.push(env.method);
    if (env.error) appendFileSync(fixture.log, JSON.stringify({ responseError: env.error }) + "\n");
    peer.sendLine(env);
  });
  peer.respondToCalls = false;
  peer.onCall = env => {
    if (env.method === "plugin/desktop-activation") appendFileSync(fixture.log, JSON.stringify({ activation: env.params }) + "\n");
    child.stdin.write(`${JSON.stringify(env)}\n`);
  };
}
export function connectDesktopSession({ route, hooks }: { route: RemoteSessionRoute; hooks: SessionConnectionHooks }) {
  if (route.execution_host !== hostID) throw new Error("Synthetic peer identity mismatch");
  const phone = Identity.generate();
  peer.phonePub = phone.public_();
  const creds: Credentials = { v: 1, device_seed: b64encode(phone.seed()), host_pub: hostID, relay_url: "ws://synthetic-memory-relay" };
  return new RemoteClient(creds, { ...hooks, wsFactory: peer.factory, reconnectMinMs: 5, reconnectMaxMs: 50, pingIntervalMs: 60000 });
}
export function status() {
  return { running: child && child.exitCode === null && !child.killed,
    shutdowns: peer.uplinkEnvelopes.filter(env => env.method === "shutdown").length,
    methods: peer.uplinkEnvelopes.filter(env => env.method).map(env => env.method),
    turnCompletions: outputMethods.filter(method => method === "turn/completed").length,
    activations: peer.uplinkEnvelopes.filter(env => env.method === "plugin/desktop-activation").map(env => env.params),
    attaches: peer.attachCount, encryptedFrames: peer.lastAck, childPID: child?.pid };
}
export async function stopFixture() { child?.kill("SIGTERM"); }
