import { describe, expect, it, vi } from "vitest";

import {
  RemoteChild,
  RemoteChildStream,
  RemoteHostEvent,
  RemoteHostManager,
  RemoteSpawn,
} from "./remoteControl";

class FakeStream implements RemoteChildStream {
  private listeners: Array<(chunk: string) => void> = [];

  setEncoding(): void {}

  on(_event: "data", listener: (chunk: string) => void): void {
    this.listeners.push(listener);
  }

  push(chunk: string): void {
    for (const listener of this.listeners) {
      listener(chunk);
    }
  }
}

class FakeChild implements RemoteChild {
  stdin = { end: vi.fn() };
  stdout = new FakeStream();
  stderr = new FakeStream();
  signals: string[] = [];
  exitOnKill = true;
  private exitListeners: Array<(code: number | null) => void> = [];
  private closeListeners: Array<(code: number | null) => void> = [];
  private errorListeners: Array<(err: Error) => void> = [];
  private exited = false;

  constructor(
    readonly command: string,
    readonly args: string[],
  ) {}

  on(
    event: "exit" | "close" | "error",
    listener: ((code: number | null) => void) | ((err: Error) => void),
  ): void {
    if (event === "exit") {
      this.exitListeners.push(listener as (code: number | null) => void);
    } else if (event === "close") {
      this.closeListeners.push(listener as (code: number | null) => void);
    } else {
      this.errorListeners.push(listener as (err: Error) => void);
    }
  }

  kill(signal?: NodeJS.Signals): boolean {
    this.signals.push(signal ?? "SIGTERM");
    if (this.exitOnKill) {
      queueMicrotask(() => this.exit(0));
    }
    return true;
  }

  exit(code: number | null): void {
    if (this.exited) {
      return;
    }
    this.exited = true;
    for (const listener of this.exitListeners) {
      listener(code);
    }
  }

  fail(err: Error): void {
    for (const listener of this.errorListeners) {
      listener(err);
    }
  }

  close(code: number | null): void {
    for (const listener of this.closeListeners) {
      listener(code);
    }
  }
}

function makeManager(overrides: { onEvent?: (ev: RemoteHostEvent) => void } = {}) {
  const children: FakeChild[] = [];
  const spawn: RemoteSpawn = (command, args) => {
    const child = new FakeChild(command, args);
    children.push(child);
    return child;
  };
  const events: RemoteHostEvent[] = [];
  const manager = new RemoteHostManager({
    spawn,
    resolveCommand: (workdir) => ({ command: "wuu", args: [], cwd: workdir }),
    env: {},
    onEvent: (ev) => {
      events.push(ev);
      overrides.onEvent?.(ev);
    },
  });
  return { manager, children, events };
}

async function flush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("RemoteHostManager.status", () => {
  it("parses the status JSON and passes the right argv", async () => {
    const { manager, children } = makeManager();
    const statusPromise = manager.status("/work/dir");
    await flush();
    const child = children[0];
    expect(child.args).toEqual(["remote", "status", "--json"]);
    child.stdout.push(
      JSON.stringify({
        fingerprint: "abc123",
        relay_url: "ws://relay/v1/connect",
        store: "/home/.wuu/remote.json",
        devices: [{ pub: "PUB", fingerprint: "fff", name: "phone", added_at: "2026-07-07T00:00:00Z" }],
      }),
    );
    child.exit(0);
    const status = await statusPromise;
    expect(status.fingerprint).toBe("abc123");
    expect(status.devices).toHaveLength(1);
    expect(status.devices[0].name).toBe("phone");
  });

  it("rejects with stderr when the CLI fails", async () => {
    const { manager, children } = makeManager();
    const statusPromise = manager.status("/work/dir");
    await flush();
    children[0].stderr.push("error: no relay configured\n");
    children[0].exit(1);
    await expect(statusPromise).rejects.toThrow(/no relay configured/);
  });
});

describe("RemoteHostManager host lifecycle", () => {
  it("captures the pairing URI and the paired event", async () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/work/dir", { pair: true });
    const child = children[0];
    expect(child.args).toEqual(["remote", "host", "--workdir", "/work/dir", "--pair"]);
    expect(manager.isRunning()).toBe(true);
    expect(manager.currentPairUri()).toBeNull();

    child.stdout.push("pairing uri (scan or pass to `wuu remote phone pair --uri`):\n");
    child.stdout.push("wuu://pair?v=1&p=abc&k=KEY&h=HOST&r=ws%3A%2F%2Frelay\n");
    expect(manager.currentPairUri()).toBe("wuu://pair?v=1&p=abc&k=KEY&h=HOST&r=ws%3A%2F%2Frelay");
    expect(events.some((e) => e.kind === "pair-uri")).toBe(true);

    child.stdout.push("paired: my phone (2579e6ff1255)\n");
    expect(manager.currentPairUri()).toBeNull();
    const paired = events.find((e) => e.kind === "paired");
    expect(paired && paired.kind === "paired" ? paired.detail : "").toBe("my phone (2579e6ff1255)");
  });

  it("handles split lines across stdout chunks", () => {
    const { manager, children } = makeManager();
    manager.startHost("/w");
    const child = children[0];
    child.stdout.push("wuu://pair?v=1");
    expect(manager.currentPairUri()).toBeNull();
    child.stdout.push("&p=xyz\n");
    expect(manager.currentPairUri()).toBe("wuu://pair?v=1&p=xyz");
  });

  it("stopHost terminates the child and clears state", async () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/w", { pair: true });
    children[0].stdout.push("wuu://pair?v=1&p=abc\n");
    await manager.stopHost();
    expect(children[0].signals).toContain("SIGTERM");
    expect(manager.isRunning()).toBe(false);
    expect(manager.currentPairUri()).toBeNull();
    expect(events.some((e) => e.kind === "host-exit")).toBe(true);
  });

  it("reports unexpected exits and clears the pairing window", async () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/w", { pair: true });
    children[0].stdout.push("wuu://pair?v=1&p=abc\n");
    children[0].exit(1);
    await flush();
    expect(manager.isRunning()).toBe(false);
    expect(manager.currentPairUri()).toBeNull();
    const exit = events.find((e) => e.kind === "host-exit");
    expect(exit && exit.kind === "host-exit" ? exit.code : null).toBe(1);
  });

  it("clears host state once when spawn error is followed by close and exit", () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/w", { pair: true });
    const child = children[0];
    child.stdout.push("wuu://pair?v=1&p=abc\n");

    child.fail(
      Object.assign(new Error("spawn wuu ENOENT"), { code: "ENOENT" }),
    );
    expect(manager.isRunning()).toBe(false);
    expect(manager.runningWorkdir()).toBeNull();
    expect(manager.currentPairUri()).toBeNull();

    child.close(-2);
    child.exit(null);

    expect(events.filter((event) => event.kind === "host-exit")).toEqual([
      { kind: "host-exit", code: null },
    ]);
    expect(
      events.some(
        (event) =>
          event.kind === "host-log" && event.message.includes("ENOENT"),
      ),
    ).toBe(true);
  });

  it("lets stopHost complete from close when exit never arrives", async () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/w");
    const child = children[0];
    child.exitOnKill = false;

    const stopped = manager.stopHost();
    child.close(0);
    await stopped;

    expect(child.signals).toContain("SIGTERM");
    expect(manager.isRunning()).toBe(false);
    expect(events.filter((event) => event.kind === "host-exit")).toEqual([
      { kind: "host-exit", code: 0 },
    ]);
  });

  it("ignores late output and lifecycle events from a replaced child", () => {
    const { manager, children, events } = makeManager();
    manager.startHost("/old", { pair: true });
    const oldChild = children[0];
    oldChild.fail(new Error("spawn old host ENOENT"));

    manager.startHost("/new", { pair: true });
    const newChild = children[1];
    const eventCount = events.length;

    oldChild.stdout.push("wuu://pair?v=1&p=stale\n");
    oldChild.stderr.push("late stderr from old host\n");
    oldChild.exit(1);
    oldChild.close(1);

    expect(events).toHaveLength(eventCount);
    expect(manager.isRunning()).toBe(true);
    expect(manager.runningWorkdir()).toBe("/new");
    expect(manager.currentPairUri()).toBeNull();

    newChild.stdout.push("wuu://pair?v=1&p=fresh\n");
    expect(manager.currentPairUri()).toBe("wuu://pair?v=1&p=fresh");
  });

  it("startHost is idempotent while a host is running", () => {
    const { manager, children } = makeManager();
    manager.startHost("/w");
    manager.startHost("/w");
    expect(children).toHaveLength(1);
  });
});

describe("RemoteHostManager.removeDevice", () => {
  it("revokes and restarts a running host without reopening pairing", async () => {
    const { manager, children } = makeManager();
    manager.startHost("/work/dir", { pair: true });
    expect(children).toHaveLength(1);

    const removePromise = manager.removeDevice("/work/dir", "2579e6ff1255");
    await flush();
    const removeChild = children[1];
    expect(removeChild.args).toEqual(["remote", "devices", "remove", "2579e6ff1255"]);
    removeChild.exit(0);
    await removePromise;

    // Old host got SIGTERM'd; a fresh one is running without --pair.
    expect(children[0].signals).toContain("SIGTERM");
    expect(children).toHaveLength(3);
    expect(children[2].args).toEqual(["remote", "host", "--workdir", "/work/dir"]);
    expect(manager.isRunning()).toBe(true);
  });

  it("does not start a host when none was running", async () => {
    const { manager, children } = makeManager();
    const removePromise = manager.removeDevice("/w", "fp");
    await flush();
    children[0].exit(0);
    await removePromise;
    expect(children).toHaveLength(1);
    expect(manager.isRunning()).toBe(false);
  });
});


describe("phone access pairing lifecycle", () => {
  it("expires an unused pairing offer", () => {
    vi.useFakeTimers();
    try {
      const { manager, children } = makeManager();
      manager.startHost("/work", { pair: true });
      children[0].stdout.push("wuu://pair?v=1&p=offer\n");
      expect(manager.currentPairUri()).not.toBeNull();
      vi.advanceTimersByTime(10 * 60 * 1000);
      expect(manager.currentPairUri()).toBeNull();
    } finally { vi.useRealTimers(); }
  });

  it("retains the LAN relay when revoking a device", async () => {
    const { manager, children } = makeManager();
    const relay = "ws://192.168.1.2:8787/v1/connect";
    manager.startHost("/work", { pair: true, relay });
    const pending = manager.removeDevice("/work", "fingerprint");
    await flush();
    children[1].exit(0);
    await pending;
    expect(children[2].args).toContain(relay);
    expect(children[2].args).not.toContain("--pair");
  });
});


it("does not start an independent runtime when the configured desktop service is unavailable", () => {
  const spawn = vi.fn();
  const manager = new RemoteHostManager({ spawn, resolveCommand: cwd => ({ command: "wuu", args: [], cwd }), appServerEndpoint: () => undefined });
  expect(() => manager.startHost("/work")).toThrow("Shared desktop app-server is unavailable");
  expect(spawn).not.toHaveBeenCalled();
});

it('passes account secrets on stdin without exposing them in process arguments', async () => {
 const { manager, children } = makeManager();
 const pending = manager.account('/tmp','login',{server:'https://example.com',username:'alice',password:'private-password'});
 const child = children[0];
 expect(child.args).toEqual(['remote','account','login']);
 expect(child.stdin.end).toHaveBeenCalledWith(JSON.stringify({server:'https://example.com',username:'alice',password:'private-password'}));
 child.stdout.push('{"username":"alice"}'); child.exit(0);
 await expect(pending).resolves.toEqual({username:'alice'});
});
