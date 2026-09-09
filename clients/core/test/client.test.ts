// End-to-end client semantics against an in-memory fake relay+host that
// implements the reference wire behavior: challenge auth, host-side
// handshake, seq+spool with cumulative acks, attach/resume with exact replay,
// and the app-server line dialect. Mirrors the shape of the Go host
// integration test, but in-process.

import { describe, expect, it, vi } from "vitest";
import { gzipSync } from "node:zlib";

import { b64decode, b64encode } from "../src/b64.js";
import { bytesEqual, randomBytes, utf8Decode, utf8Encode } from "../src/bytes.js";
import { Credentials, RemoteClient, WebSocketFactory, WebSocketLike, pair } from "../src/client.js";
import { ProtocolEnvelope } from "../src/rpc.js";
import { Channel, HS1, Identity, Pairing, acceptHandshake, encodeKey, verifyRelayAuth } from "../src/secure.js";
import {
  CLIENT_PROFILE_MOBILE_CHAT,
  E2EMsg,
  KIND_HANDSHAKE,
  KIND_SEALED,
  RelayMsg,
  TYPE_AUTH,
  TYPE_AUTH_OK,
  TYPE_CHALLENGE,
  TYPE_FRAME,
  TYPE_HELLO,
  TYPE_PAIR_ANSWER,
  TYPE_PAIR_OFFER,
  splitPayload,
  wrapPayload,
} from "../src/wire.js";

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

// --- helpers ---------------------------------------------------------------------

async function until(cond: () => boolean, ms = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > ms) throw new Error("condition not met in time");
    await new Promise((r) => setTimeout(r, 5));
  }
}

function makeClient(fake: FakeHost, extra: Partial<ConstructorParameters<typeof RemoteClient>[1]> = {}) {
  const phone = Identity.generate();
  fake.phonePub = phone.public_();
  const creds: Credentials = {
    v: 1,
    device_seed: b64encode(phone.seed()),
    host_pub: encodeKey(fake.host.public_()),
    relay_url: "ws://fake-relay",
  };
  const notifications: Array<{ method: string; params: unknown }> = [];
  const attaches: Array<{ session: string; resumed: boolean }> = [];
  const client = new RemoteClient(creds, {
    wsFactory: fake.factory,
    reconnectMinMs: 5,
    reconnectMaxMs: 50,
    dialTimeoutMs: 500,
    ackIntervalMs: 10,
    pingIntervalMs: 60_000,
    onNotification: (method, params) => notifications.push({ method, params }),
    onAttach: (ev) => attaches.push(ev),
    ...extra,
  });
  return { client, notifications, attaches };
}

// --- tests -----------------------------------------------------------------------

describe("pair()", () => {
  it("completes the exchange over a fake relay", async () => {
    const host = Identity.generate();
    const pairing = Pairing.generate();
    let seenDevicePub = "";
    const factory: WebSocketFactory = () => {
      const sock = new FakeSocket();
      sock.onServerReceive = (data) => {
        const msg = JSON.parse(data) as RelayMsg;
        if (msg.type !== TYPE_PAIR_OFFER) return;
        expect(msg.pairing_id).toBe(pairing.id);
        const { offer, hostPairing } = pairing.openPairOffer(b64decode(msg.payload ?? ""));
        seenDevicePub = encodeKey(offer.devicePub);
        const sealed = hostPairing.sealPairAnswer(host, "fake host", offer.devicePub);
        sock.serverSend(JSON.stringify({ type: TYPE_PAIR_ANSWER, payload: b64encode(sealed) } satisfies RelayMsg));
      };
      return sock;
    };

    const uri = pairing.uri("ws://fake-relay", host.public_());
    const creds = await pair(uri, "test phone", { wsFactory: factory, platform: "test" });
    expect(creds.host_pub).toBe(encodeKey(host.public_()));
    expect(creds.host_name).toBe("fake host");
    expect(creds.relay_url).toBe("ws://fake-relay");
    expect(creds.device_name).toBe("test phone");
    // The credentials' seed derives the device key the host registered.
    expect(encodeKey(Identity.fromSeed(b64decode(creds.device_seed)).public_())).toBe(seenDevicePub);
  });
});

describe("RemoteClient", () => {
  it("applies compressed snapshots and raw deltas in order across replay", async () => {
    const fake = new FakeHost();
    fake.compressionEnabled = true;
    const snapshot = { text: "workspace history ".repeat(20_000) };
    fake.handleCall = () => snapshot;
    const { client, notifications, attaches } = makeClient(fake);
    client.start();
    try {
      expect(await client.call("thread/resume", {})).toEqual(snapshot);
      expect(fake.compressionAccepted).toBe(true);
      fake.drop();
      fake.sendLine({ method: "snapshot", params: snapshot });
      fake.sendLine({ method: "delta", params: { text: "next" } });
      await until(() => notifications.length === 2);
      expect(notifications).toEqual([
        { method: "snapshot", params: snapshot },
        { method: "delta", params: { text: "next" } },
      ]);
      expect(attaches.at(-1)?.resumed).toBe(true);
      await until(() => fake.lastAck === 3);
    } finally { await client.stop(); }
  });

  it("uses uncompressed lines when the runtime has no streaming decoder", async () => {
    vi.stubGlobal("DecompressionStream", undefined);
    const fake = new FakeHost();
    fake.compressionEnabled = true;
    const { client } = makeClient(fake);
    client.start();
    try {
      expect(await client.call("initialize", {})).toEqual({ echo: "initialize" });
      expect(fake.compressionAccepted).toBe(false);
    } finally { await client.stop(); vi.unstubAllGlobals(); }
  });

  it("attaches and completes an rpc round trip", async () => {
    const fake = new FakeHost();
    fake.handleCall = (env) => ({ protocolVersion: "wuu-app-server/v0.1", method: env.method });
    const { client, attaches } = makeClient(fake);
    client.start();
    try {
      const result = await client.call<{ protocolVersion: string }>("initialize", {});
      expect(result.protocolVersion).toBe("wuu-app-server/v0.1");
      expect(attaches).toEqual([{ session: "sess-2", resumed: false }]);
      expect(client.isAttached()).toBe(true);
    } finally {
      await client.stop();
    }
  });

  it("does not send a call after its attach wait times out", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake);

    await expect(client.call("turn/start", { prompt: "too late" }, 5)).rejects.toThrow(/attach timeout/);

    client.start();
    try {
      await client.waitAttached(3000);
      expect(fake.uplinkEnvelopes).toEqual([]);
    } finally {
      await client.stop();
    }
  });

  it("applies the call timeout after attach when the host never responds", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake);
    client.start();
    try {
      await client.waitAttached(3000);
      fake.respondToCalls = false;

      await expect(client.call("thread/list", undefined, 20)).rejects.toThrow("rpc timeout: thread/list");
      expect(fake.uplinkEnvelopes.map((env) => env.method)).toEqual(["thread/list"]);

      fake.respondToCalls = true;
      await expect(client.call<{ echo: string }>("participant/list", undefined, 3000)).resolves.toEqual({
        echo: "participant/list",
      });
    } finally {
      await client.stop();
    }
  });

  it("rejects attach waiters when stopped", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake);
    const waiting = client.waitAttached();
    const rejected = expect(waiting).rejects.toThrow(/client stopped/);

    await client.stop();

    await rejected;
  });

  it("sends an optional client profile with attach", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake, { clientProfile: CLIENT_PROFILE_MOBILE_CHAT });
    client.start();
    try {
      await client.waitAttached(3000);
      expect(fake.attachProfiles).toEqual([CLIENT_PROFILE_MOBILE_CHAT]);
    } finally {
      await client.stop();
    }
  });

  it("routes notifications and state frames", async () => {
    const fake = new FakeHost();
    const states: number[] = [];
    const { client, notifications } = makeClient(fake, { onState: (s) => states.push(s.ver) });
    client.start();
    try {
      await client.waitAttached(3000);
      fake.sendLine({ method: "turn/started", params: { turn: "t1" } });
      fake.sendLine({ method: "item/agentMessage/delta", params: { delta: "你好" } });
      fake.sendState(7);
      await until(() => notifications.length === 2 && states.length === 1);
      expect(notifications[0]).toEqual({ method: "turn/started", params: { turn: "t1" } });
      expect(notifications[1].params).toEqual({ delta: "你好" });
      expect(states).toEqual([7]);
      expect(client.latestState()?.host.name).toBe("fake host");
    } finally {
      await client.stop();
    }
  });

  it("answers server requests through the sealed channel", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake, {
      onServerRequest: (req) => {
        expect(req.method).toBe("tool/approval/request");
        return { result: { decision: "approved", reason: "test policy" } };
      },
    });
    client.start();
    try {
      await client.waitAttached(3000);
      fake.sendLine({ id: "srv-1", method: "tool/approval/request", params: { tool: "bash" } });
      await until(() => fake.uplinkEnvelopes.some((e) => e.id === "srv-1"));
      const reply = fake.uplinkEnvelopes.find((e) => e.id === "srv-1")!;
      expect(reply.result).toEqual({ decision: "approved", reason: "test policy" });
      expect(reply.error).toBeUndefined();
    } finally {
      await client.stop();
    }
  });

  it("resumes after a transport drop with exact replay and no duplicates", async () => {
    const fake = new FakeHost();
    let detaches = 0;
    const { client, notifications, attaches } = makeClient(fake, { onDetach: () => detaches++ });
    client.start();
    try {
      await client.waitAttached(3000);
      const proto = client.rpc();
      fake.sendLine({ method: "n", params: { i: 1 } });
      fake.sendLine({ method: "n", params: { i: 2 } });
      fake.sendLine({ method: "n", params: { i: 3 } });
      await until(() => notifications.length === 3);
      // Let an ack land so the host trims, then drop the transport.
      await until(() => fake.lastAck === 3);
      fake.drop();
      // Lines queued while the phone is away are spooled, not lost.
      fake.sendLine({ method: "n", params: { i: 4 } });
      fake.sendLine({ method: "n", params: { i: 5 } });
      await until(() => notifications.length === 5);
      expect(notifications.map((n) => (n.params as { i: number }).i)).toEqual([1, 2, 3, 4, 5]);
      expect(attaches).toEqual([
        { session: "sess-2", resumed: false },
        { session: "sess-2", resumed: true },
      ]);
      // Same app-server connection: the protocol client survived the resume.
      expect(client.rpc()).toBe(proto);
      expect(proto!.isClosed()).toBe(false);
      // Exactly one detach fired for the transport drop.
      expect(detaches).toBe(1);
    } finally {
      await client.stop();
    }
  });

  it("falls back to a fresh connection when the spool is lost", async () => {
    const fake = new FakeHost();
    const { client, notifications, attaches } = makeClient(fake);
    client.start();
    try {
      await client.waitAttached(3000);
      const oldProto = client.rpc()!;
      fake.sendLine({ method: "n", params: { i: 1 } });
      await until(() => notifications.length === 1);
      fake.spoolLost = true; // simulates host restart / spool overflow
      fake.drop();
      await until(() => attaches.length === 2);
      expect(attaches[1].resumed).toBe(false);
      expect(attaches[1].session).not.toBe(attaches[0].session);
      // Fresh connection: the old protocol client is dead, a new one works.
      expect(oldProto.isClosed()).toBe(true);
      expect(client.rpc()).not.toBe(oldProto);
      const result = await client.call<{ echo: string }>("thread/list");
      expect(result.echo).toBe("thread/list");
      // lastRecv was reset: the fresh connection's seq=1 line is not dropped.
      fake.sendLine({ method: "n", params: { i: 100 } });
      await until(() => notifications.length >= 2);
      expect((notifications.at(-1)!.params as { i: number }).i).toBe(100);
    } finally {
      await client.stop();
    }
  });

  it("acks cumulatively so the host can trim its spool", async () => {
    const fake = new FakeHost();
    const { client, notifications } = makeClient(fake);
    client.start();
    try {
      await client.waitAttached(3000);
      for (let i = 1; i <= 5; i++) fake.sendLine({ method: "n", params: { i } });
      await until(() => notifications.length === 5);
      await until(() => fake.lastAck === 5);
    } finally {
      await client.stop();
    }
  });

  it("uplink is at-most-once: calls fail while detached", async () => {
    const fake = new FakeHost();
    const { client } = makeClient(fake, { reconnectMinMs: 10_000 });
    client.start();
    try {
      await client.waitAttached(3000);
      const proto = client.rpc()!;
      fake.drop();
      await until(() => !client.isAttached());
      await expect(proto.call("turn/start", { prompt: "x" })).rejects.toThrow(/not attached/);
    } finally {
      await client.stop();
    }
  });
});


describe("RemoteClient foreground recovery", () => {
  it("suspends native transport without resetting RPC identity or replay position", async () => {
    vi.useFakeTimers();
    const fake = new FakeHost();
    const { client, notifications } = makeClient(fake, { reconnectMinMs: 100, pingIntervalMs: 60000 });
    try {
      client.start(); await vi.advanceTimersByTimeAsync(0);
      const protocol = client.rpc();
      const sockets = fake.sockets.length;
      client.suspend(); await vi.advanceTimersByTimeAsync(0);
      expect(client.isAttached()).toBe(false);
      fake.sendLine({ method: 'background/completed', params: { done: true } });
      await vi.advanceTimersByTimeAsync(60000);
      expect(fake.sockets).toHaveLength(sockets);
      expect(protocol?.isClosed()).toBe(false);
      client.wake(); await vi.advanceTimersByTimeAsync(0);
      expect(client.isAttached()).toBe(true);
      expect(client.rpc()).toBe(protocol);
      expect(notifications.filter(event => event.method === 'background/completed')).toHaveLength(1);
      client.suspend(); await vi.advanceTimersByTimeAsync(0);
      await client.stop(); client.wake(); await vi.advanceTimersByTimeAsync(0);
      expect(fake.sockets).toHaveLength(sockets + 1);
    } finally { await client.stop(); vi.useRealTimers(); }
  });

  it("wake interrupts reconnect backoff and reattaches immediately", async () => {
    vi.useFakeTimers();
    const fake = new FakeHost();
    const { client } = makeClient(fake, {
      reconnectMinMs: 10_000,
      reconnectMaxMs: 10_000,
      pingIntervalMs: 60_000,
    });
    try {
      client.start();
      await vi.advanceTimersByTimeAsync(0);
      await client.waitAttached(3000);
      const sockets = fake.sockets.length;
      fake.drop();
      await vi.advanceTimersByTimeAsync(0);
      expect(fake.sockets.length).toBe(sockets);

      client.wake();
      await vi.advanceTimersByTimeAsync(0);
      await client.waitAttached(3000);
      expect(fake.sockets.length).toBe(sockets + 1);
      expect(client.isAttached()).toBe(true);
    } finally {
      await client.stop();
      vi.useRealTimers();
    }
  });

  it("wake sends a bounded fresh probe instead of wall-clock staleness", async () => {
    vi.useFakeTimers();
    const fake = new FakeHost();
    const { client } = makeClient(fake, {
      pingIntervalMs: 60_000,
      pongTimeoutMs: 250,
      reconnectMinMs: 10_000,
    });
    try {
      client.start();
      await vi.advanceTimersByTimeAsync(0);
      await client.waitAttached(3000);
      expect(fake.pingCount).toBe(0);

      client.wake();
      await vi.advanceTimersByTimeAsync(0);
      expect(fake.pingCount).toBe(1);

      // The pong answers the fresh probe and clears the deadline; the healthy
      // socket must not be closed even after the deadline would have elapsed.
      await vi.advanceTimersByTimeAsync(500);
      expect(fake.current().closed).toBe(false);
      expect(client.isAttached()).toBe(true);
    } finally {
      await client.stop();
      vi.useRealTimers();
    }
  });

  it("closes a half-open socket after the bounded pong deadline", async () => {
    vi.useFakeTimers();
    const fake = new FakeHost();
    const { client } = makeClient(fake, {
      pingIntervalMs: 100,
      pongTimeoutMs: 250,
      reconnectMinMs: 10_000,
    });
    try {
      client.start();
      await vi.advanceTimersByTimeAsync(0);
      await client.waitAttached(3000);

      fake.suppressPong = true;
      await vi.advanceTimersByTimeAsync(100); // ping fires and arms the deadline
      await vi.advanceTimersByTimeAsync(250); // deadline expires
      expect(fake.current().closed).toBe(true);
    } finally {
      await client.stop();
      vi.useRealTimers();
    }
  });

  it("reconnects immediately when a foreground probe expires during an active socket", async () => {
    vi.useFakeTimers();
    const fake = new FakeHost();
    const { client } = makeClient(fake, {
      pingIntervalMs: 60_000,
      pongTimeoutMs: 250,
      reconnectMinMs: 10_000,
    });
    try {
      client.start();
      await vi.advanceTimersByTimeAsync(0);
      const oldSocket = fake.current();
      fake.suppressPong = true;
      client.wake();
      client.wake();
      await vi.advanceTimersByTimeAsync(249);
      expect(fake.pingCount).toBe(1);
      expect(oldSocket.closed).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect(oldSocket.closed).toBe(true);
      expect(fake.sockets.length).toBe(2);
      expect(client.isAttached()).toBe(true);
    } finally {
      await client.stop();
      vi.useRealTimers();
    }
  });
});


it("reconnects after the local execution transport ends instead of staying detached", async () => {
  const fake = new FakeHost();
  const { client, attaches } = makeClient(fake);
  client.start();
  try {
    await client.waitAttached(3000);
    fake.endAppConnection();
    await until(() => attaches.length === 2);
    expect(client.isAttached()).toBe(true);
    expect(fake.sockets.length).toBe(2);
  } finally { await client.stop(); }
});
