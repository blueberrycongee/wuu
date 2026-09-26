import { randomUUID } from "node:crypto";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { EventEmitter } from "node:events";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({
  app: {
    getAppPath: () => process.cwd(),
  },
}));

import {
  configurePackagedCUA,
  activityServerRequestRejection,
  AppServerClient,
  type AppServerClientEvent,
  appServerExitMessage,
  AppServerClientPool,
  type AppServerSpawn,
  appServerHelperEnvironment,
  updateStoppedActivityIDs,
} from "./appServerClients";

class FakeAppServerChild extends EventEmitter {
  readonly stdin = new PassThrough();
  readonly stdout = new PassThrough();
  readonly stderr = new PassThrough();
  killed = false;

  kill(): boolean {
    this.killed = true;
    return true;
  }

  asChildProcess(): ChildProcessWithoutNullStreams {
    return this as unknown as ChildProcessWithoutNullStreams;
  }
}

function makeClient(spawnAppServer: AppServerSpawn): {
  client: AppServerClient;
  events: AppServerClientEvent[];
  stateChanges: () => number;
} {
  const events: AppServerClientEvent[] = [];
  let stateChangeCount = 0;
  const client = new AppServerClient(
    tmpdir(),
    "",
    (_source, event) => events.push(event),
    () => {
      stateChangeCount += 1;
    },
    spawnAppServer,
    (_env, workdir) => ({ command: "test-wuu-core", args: [], cwd: workdir }),
  );
  return {
    client,
    events,
    stateChanges: () => stateChangeCount,
  };
}

function serverExitEvents(events: AppServerClientEvent[]): Extract<
  AppServerClientEvent,
  { kind: "server-exit" }
>[] {
  return events.filter(
    (event): event is Extract<AppServerClientEvent, { kind: "server-exit" }> =>
      event.kind === "server-exit",
  );
}

describe("appServerExitMessage", () => {
  it("preserves stderr and the exit code", () => {
    expect(appServerExitMessage(1, "parse config: unknown field")).toBe(
      "wuu core exited (code 1): parse config: unknown field",
    );
  });

  it("still reports an exit without stderr", () => {
    expect(appServerExitMessage(null, "")).toBe("wuu core exited");
  });

  it("rejects future plugin bridge requests after their Activity stops", () => {
    const stopped = new Set<string>();
    updateStoppedActivityIDs(stopped, {
      method: "activity/stopped",
      params: { id: "activity-1", thread_id: "thread-1" },
    });
    expect(
      activityServerRequestRejection(
        {
          id: "bridge-1",
          method: "official-plugin/browser-command",
          params: { activity_id: "activity-1", action: "click" },
        },
        stopped,
      ),
    ).toBe("activity activity-1 is stopped");
    expect(
      activityServerRequestRejection(
        {
          id: "bridge-2",
          method: "official-plugin/browser-command",
          params: { activity_id: "activity-2", action: "click" },
        },
        stopped,
      ),
    ).toBeUndefined();
  });
});

describe("appServerHelperEnvironment", () => {
  it("injects packaged first-party plugin helpers and the signed macOS helper", () => {
    const resourcesPath = "/Applications/wuu.app/Contents/Resources";
    const packagedBin = join(resourcesPath, "bin");
    const available = new Set([
      join(packagedBin, "wuu-subagent-plugin"),
      join(packagedBin, "wuu-automation-plugin"),
      join(packagedBin, "wuu-memory-plugin"),
      join(packagedBin, "wuu-dream-plugin"),
      join(packagedBin, "wuu-todo-plugin"),
      join(packagedBin, "wuu-goal-plugin"),
      join(packagedBin, "wuu-cua-mac"),
    ]);
    const result = appServerHelperEnvironment(
      { HOME: "/Users/test" },
      "/source",
      resourcesPath,
      "darwin",
      (path) => available.has(path),
    );
    expect(result.WUU_SUBAGENT_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-subagent-plugin"));
    expect(result.WUU_AUTOMATION_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-automation-plugin"));
    expect(result.WUU_MEMORY_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-memory-plugin"));
    expect(result.WUU_DREAM_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-dream-plugin"));
    expect(result.WUU_GOAL_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-goal-plugin"));
    expect(result.WUU_TODO_PLUGIN_HELPER).toBe(join(packagedBin, "wuu-todo-plugin"));
    expect(result.WUU_CUA_MAC_HELPER).toBe(join(packagedBin, "wuu-cua-mac"));
  });

  it("uses development plugin helpers without replacing explicit overrides", () => {
    const sourceBin = join("/source", "desktop", "build", "bin");
    const available = new Set([
      join(sourceBin, "wuu-subagent-plugin"),
      join(sourceBin, "wuu-automation-plugin"),
      join(sourceBin, "wuu-memory-plugin"),
      join(sourceBin, "wuu-dream-plugin"),
      join(sourceBin, "wuu-todo-plugin"),
      join(sourceBin, "wuu-goal-plugin"),
      join(sourceBin, "wuu-cua-mac"),
    ]);
    const discovered = appServerHelperEnvironment(
      {},
      "/source",
      undefined,
      "darwin",
      (path) => available.has(path),
    );
    expect(discovered.WUU_GOAL_PLUGIN_HELPER).toBe(join(sourceBin, "wuu-goal-plugin"));
    expect(discovered.WUU_TODO_PLUGIN_HELPER).toBe(join(sourceBin, "wuu-todo-plugin"));
    expect(discovered.WUU_CUA_MAC_HELPER).toBe(join(sourceBin, "wuu-cua-mac"));
    const overridden = appServerHelperEnvironment(
      {
        WUU_SUBAGENT_PLUGIN_HELPER: "/custom/subagent",
        WUU_CUA_MAC_HELPER: "/custom/cua",
      },
      "/source",
      undefined,
      "darwin",
      () => true,
    );
    expect(overridden.WUU_SUBAGENT_PLUGIN_HELPER).toBe("/custom/subagent");
    expect(overridden.WUU_CUA_MAC_HELPER).toBe("/custom/cua");
  });

  it("uses .exe plugin helpers on Windows without injecting the macOS helper", () => {
    const result = appServerHelperEnvironment(
      {},
      "C:\\source",
      undefined,
      "win32",
      (path) => path.endsWith("wuu-subagent-plugin.exe"),
    );
    expect(result.WUU_SUBAGENT_PLUGIN_HELPER).toMatch(/wuu-subagent-plugin\.exe$/);
    expect(result.WUU_CUA_MAC_HELPER).toBeUndefined();
  });
});

describe("AppServerClientPool Activity routing", () => {
  it("does not create a new workspace client for an unknown Activity workdir", async () => {
    const pool = new AppServerClientPool(
      () => ({ kind: "no_project", cwd: "/active" }),
      () => "/active",
      () => undefined,
    );
    await expect(
      pool.requestForWorkdir("/missing", "activity/stop", {
        thread_id: "thread-1",
        activity_id: "activity-1",
      }),
    ).rejects.toThrow("activity workspace is no longer connected");
  });
});

describe("AppServerClientPool admission", () => {
  afterEach(() => vi.unstubAllEnvs());

  it.each(["read", "session read", "turn", "error"])(
    "admits a remote %s while four other workspaces run turns and reclaims idle clients",
    async (operation) => {
      vi.stubEnv("WUU_DESKTOP_CORE", "test-wuu-core");
      const contexts = Array.from({ length: 6 }, (_, index) => ({
        kind: "project" as const,
        project_id: `project-${index}`,
        cwd: join(tmpdir(), `admission-${index}`),
      }));
      const children = new Map<string, FakeAppServerChild>();
      const requests = new Map<string, Array<{ id: string; method: string }>>();
      const disposed: string[] = [];
      const pool = new AppServerClientPool(
        () => contexts[0],
        () => contexts[0].cwd,
        () => {},
        (_command, _args, options) => {
          const child = new FakeAppServerChild();
          children.set(options.cwd, child);
          requests.set(options.cwd, []);
          child.stdin.on("data", data => {
            const request = JSON.parse(String(data));
            requests.get(options.cwd)!.push(request);
            if (request.method === "shutdown") {
              queueMicrotask(() => child.emit("exit", 0, null));
            } else if (request.method === "initialize") {
              queueMicrotask(() => child.stdout.write(`${JSON.stringify({ id: request.id, result: {} })}\n`));
            }
          });
          return child.asChildProcess();
        },
        () => ({}),
      );
      pool.setClientTorndownHandler(cwd => disposed.push(cwd));
      const reply = (index: number, response: object) => {
        const request = requests.get(contexts[index].cwd)!.at(-1)!;
        children.get(contexts[index].cwd)!.stdout.write(`${JSON.stringify({ id: request.id, ...response })}\n`);
      };
      try {
        for (let index = 0; index < 4; index++) {
          const started = pool.requestInContext(contexts[index], "turn/start", { thread_id: `thread-${index}` });
          reply(index, { result: { turn: { status: "in_progress" } } });
          await started;
        }
        const running = pool.runningThreadsSnapshot();
        expect(running).toHaveLength(4);
        const fifth = operation === "session read"
          ? pool.requestInContext(contexts[4], "thread/resume", { session_id: "remote-thread" })
          : pool.requestInContext(contexts[4], operation === "turn" ? "turn/start" : "thread/list",
            operation === "turn" ? { thread_id: "remote-thread" } : undefined);
        // Another admission and startup responses must not evict the pending fifth request.
        const sixth = pool.requestInContext(contexts[5], "thread/list");
        await Promise.resolve();
        expect(disposed).toEqual([]);
        reply(5, { result: { threads: [] } });
        await expect(sixth).resolves.toEqual({ threads: [] });
        expect(disposed).toEqual([contexts[5].cwd]);
        if (operation === "error") {
          const rejected = expect(fifth).rejects.toThrow("fixture request failed");
          reply(4, { error: { code: "error", message: "fixture request failed" } });
          await rejected;
        } else {
          const result = operation === "turn"
            ? { turn: { status: "in_progress" } }
            : operation === "session read"
              ? { thread: { id: "remote-thread", status: "idle" } }
              : { threads: [] };
          reply(4, { result });
          await expect(fifth).resolves.toEqual(result);
        }
        if (operation === "turn") {
          expect(disposed).toEqual([contexts[5].cwd]);
          expect(pool.runningThreadsSnapshot()).toHaveLength(5);
          children.get(contexts[4].cwd)!.stdout.write(`${JSON.stringify({
            method: "turn/completed", params: { thread_id: "remote-thread" },
          })}\n`);
        }
        expect(disposed).toEqual([contexts[5].cwd, contexts[4].cwd]);
        expect(pool.runningThreadsSnapshot()).toEqual(running);
      } finally {
        await pool.shutdown();
      }
    },
  );
});

describe("AppServerClientPool session routing", () => {
  afterEach(() => vi.unstubAllEnvs());
  it("negotiates initialize capabilities for prewarmed and restarted project processes", async () => {
    vi.stubEnv("WUU_DESKTOP_CORE", "test-wuu-core");
    const children: FakeAppServerChild[] = [];
    const methods: string[][] = [];
    const context = { kind: "project" as const, project_id: "project", cwd: "/project" };
    const initialize = { capabilities: { reverse_rpc: { methods: ["browser/cdp"] } } };
    const pool = new AppServerClientPool(() => context, () => context.cwd, () => {}, () => {
      const child = new FakeAppServerChild();
      const received: string[] = [];
      children.push(child);
      methods.push(received);
      child.stdin.on("data", data => {
        const request = JSON.parse(String(data));
        if (!request.method) return;
        received.push(request.method);
        if (request.method === "initialize") expect(request.params).toEqual(initialize);
        child.stdout.write(`${JSON.stringify({ id: request.id, result: {} })}\n`);
      });
      return child.asChildProcess();
    }, () => initialize);
    pool.prewarmContexts([context]);
    expect(methods).toEqual([["initialize"]]);
    children[0].emit("exit", 1, null);
    await pool.requestInContext(context, "thread/list");
    expect(methods).toEqual([["initialize"], ["initialize", "thread/list"]]);
    void pool.shutdown();
  });

  it("routes ordinary session reads and controls to the session executor", async () => {
    vi.stubEnv("WUU_DESKTOP_CORE", "test-wuu-core");
    const children = new Map<string, FakeAppServerChild>();
    const requests = new Map<string, Array<{ id: string; method: string }>>();
    const active = { kind: "no_project" as const, cwd: "/project" };
    const owner = { kind: "no_project" as const, cwd: "/executor" };
    const pool = new AppServerClientPool(() => active, () => active.cwd, () => {}, (_cmd, _args, options) => {
      const child = new FakeAppServerChild();
      children.set(options.cwd, child);
      requests.set(options.cwd, []);
      child.stdin.on("data", data => requests.get(options.cwd)!.push(JSON.parse(String(data))));
      return child.asChildProcess();
    });
    pool.prewarmContexts([active, owner]);
    children.get(owner.cwd)!.stdout.write(`${JSON.stringify({ method: "turn/started", params: { thread_id: "session" } })}\n`);
    // An explicit destination is sufficient before resume/history hydration.
    const followUp = pool.requestInContext(owner, "turn/start", { thread_id: "cached", prompt: "Next" });
    const read = pool.request("thread/resume", { session_id: "session" });
    const steer = pool.requestInContext(active, "turn/steer", { thread_id: "session", prompt: "Correction" });
    const pending = requests.get(owner.cwd)!;
    expect(pending.map(request => request.method)).toEqual(["turn/start", "thread/resume", "turn/steer"]);
    expect(requests.get(active.cwd)).toEqual([]);
    for (const request of pending) {
      children.get(owner.cwd)!.stdout.write(`${JSON.stringify({ id: request.id, result: { owner: true } })}\n`);
    }
    expect(await read).toEqual({ owner: true });
    expect(await followUp).toEqual({ owner: true });
    expect(await steer).toEqual({ owner: true });
    pool.shutdown();
  });

  it("reads the execution owner across workspaces, retains completion, and forgets exited owners", async () => {
    vi.stubEnv("WUU_DESKTOP_CORE", "test-wuu-core");
    const children = new Map<string, FakeAppServerChild>();
    const order: string[] = [];
    const active = { kind: "no_project" as const, cwd: "/active" };
    const owner = { kind: "no_project" as const, cwd: "/owner" };
    const pool = new AppServerClientPool(() => active, () => active.cwd, event => {
      if (event.kind === "notification") order.push(event.message.method);
    }, (_cmd, _args, options) => {
      const child = new FakeAppServerChild();
      children.set(options.cwd, child);
      return child.asChildProcess();
    });
    pool.prewarmContexts([active, owner]);
    const send = (cwd: string, message: unknown) => children.get(cwd)!.stdout.write(`${JSON.stringify(message)}\n`);
    send(owner.cwd, { method: "turn/started", params: { thread_id: "session" } });
    const read = pool.requestForSession(active, "session", "thread/resume", { session_id: "session" }, (_response, cwd) => order.push(`snapshot:${cwd}`));
    send(owner.cwd, { id: "client-1", result: { text: "Live" } });
    send(owner.cwd, { method: "item/agentMessage/delta", params: { thread_id: "session", delta: " later" } });
    expect(await read).toEqual({ text: "Live" });
    expect(order.slice(-2)).toEqual(["snapshot:/owner", "item/agentMessage/delta"]);
    send(owner.cwd, { method: "turn/completed", params: { thread_id: "session" } });
    const completed = pool.requestForSession(active, "session", "thread/resume");
    send(owner.cwd, { id: "client-2", result: { text: "Completed" } });
    expect(await completed).toEqual({ text: "Completed" });
    children.get(owner.cwd)!.emit("exit", 0, null);
    const restored = pool.requestForSession(active, "session", "thread/resume");
    send(active.cwd, { id: "client-1", result: { text: "Durable history" } });
    expect(await restored).toEqual({ text: "Durable history" });
    pool.shutdown();
  });
});

describe("AppServerClient child lifecycle", () => {
  it("can start a client before its first request and reuses that process", () => {
    const child = new FakeAppServerChild();
    const spawnAppServer = vi.fn(() => child.asChildProcess());
    const { client } = makeClient(spawnAppServer);

    client.start();
    client.start();

    expect(spawnAppServer).toHaveBeenCalledTimes(1);
  });

  it("tracks the cwd of running threads and clears it on completion", async () => {
    const child = new FakeAppServerChild();
    const { client } = makeClient(() => child.asChildProcess());
    const listed = client.request("thread/list");
    child.stdout.write(
      `${JSON.stringify({
        id: "client-1",
        result: {
          threads: [
            {
              id: "thread-1",
              cwd: "/repo/.wuu/worktrees/thread-1",
              status: "in_progress",
            },
          ],
        },
      })}\n`,
    );
    await listed;

    expect(client.runningThreadCwds()).toEqual([
      "/repo/.wuu/worktrees/thread-1",
    ]);

    child.stdout.write(
      `${JSON.stringify({
        method: "turn/completed",
        params: { thread_id: "thread-1" },
      })}\n`,
    );
    expect(client.runningThreadCwds()).toEqual([]);
  });

  it("retains an idle worktree cwd when its next turn starts", async () => {
    const child = new FakeAppServerChild();
    const { client } = makeClient(() => child.asChildProcess());
    const listed = client.request("thread/list");
    child.stdout.write(
      `${JSON.stringify({
        id: "client-1",
        result: {
          threads: [
            {
              id: "thread-1",
              cwd: "/repo/.wuu/worktrees/thread-1",
              status: "idle",
            },
          ],
        },
      })}\n`,
    );
    await listed;
    expect(client.runningThreadCwds()).toEqual([]);

    const started = client.request("turn/start", { thread_id: "thread-1" });
    expect(client.runningThreadCwds()).toEqual([
      "/repo/.wuu/worktrees/thread-1",
    ]);
    child.stdout.write(
      `${JSON.stringify({
        id: "client-2",
        result: { turn: { status: "in_progress" } },
      })}\n`,
    );
    await started;
    expect(client.runningThreadCwds()).toEqual([
      "/repo/.wuu/worktrees/thread-1",
    ]);
  });

  it("finalizes a real ENOENT spawn error and its later close exactly once", async () => {
    const missingBinary = join(tmpdir(), `wuu-missing-${randomUUID()}`);
    let closePromise: Promise<void> | undefined;
    const spawnMissing: AppServerSpawn = (_command, _args, options) => {
      const child = spawn(missingBinary, [], options);
      closePromise = new Promise((resolve) => {
        child.once("close", () => resolve());
      });
      return child;
    };
    const { client, events, stateChanges } = makeClient(spawnMissing);

    await expect(client.request("initialize")).rejects.toThrow(/ENOENT/);
    await closePromise;

    expect(serverExitEvents(events)).toHaveLength(1);
    expect(serverExitEvents(events)[0]?.message).toMatch(/ENOENT/);
    expect(stateChanges()).toBe(1);
    expect(client.isBusy()).toBe(false);
  });

  it("routes a synchronous stdin write failure through finalization", async () => {
    const child = new FakeAppServerChild();
    child.stdin.write = (() => {
      throw Object.assign(new Error("write EPIPE"), { code: "EPIPE" });
    }) as typeof child.stdin.write;
    const { client, events, stateChanges } = makeClient(
      () => child.asChildProcess(),
    );

    await expect(client.request("initialize")).rejects.toThrow(
      /stdin write failed: write EPIPE/,
    );
    child.emit("exit", 1, null);
    child.emit("close", 1, null);

    expect(serverExitEvents(events)).toHaveLength(1);
    expect(stateChanges()).toBe(1);
    expect(client.isBusy()).toBe(false);
    expect(child.killed).toBe(true);
  });

  it("keeps live stderr off the composer status and still reports it on exit", async () => {
    const child = new FakeAppServerChild();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { client, events } = makeClient(() => child.asChildProcess());
    try {
      const pending = client.request("initialize");
      child.stderr.write(
        "wuu: agent.tool_loading = \"native\", but grok-4.6 does not support provider-native deferred tool discovery.\n",
      );
      expect(events.filter((event) => event.kind === "server-error")).toEqual([]);
      expect(warn).toHaveBeenCalledWith(
        expect.stringMatching(/^\[app-server\] .*agent\.tool_loading/),
      );

      child.emit("exit", 1, null);
      child.emit("close", 1, null);
      await expect(pending).rejects.toThrow(/agent\.tool_loading/);
      expect(serverExitEvents(events)).toHaveLength(1);
      expect(serverExitEvents(events)[0]?.message).toContain("agent.tool_loading");
    } finally {
      warn.mockRestore();
      await client.dispose();
    }
  });

  it("still reports invalid app-server JSON as a protocol error", async () => {
    const child = new FakeAppServerChild();
    const { client, events } = makeClient(() => child.asChildProcess());
    try {
      client.start();
      child.stdout.write("not-json\n");
      expect(events).toEqual([
        { kind: "server-error", message: "Invalid app-server JSON: not-json" },
      ]);
    } finally {
      child.emit("exit", 0, null);
      child.emit("close", 0, null);
      await client.dispose();
    }
  });

  it("clears pending and running state on EPIPE without letting stale child events clear its replacement", async () => {
    const first = new FakeAppServerChild();
    const second = new FakeAppServerChild();
    const children = [first, second];
    let spawnIndex = 0;
    const { client, events, stateChanges } = makeClient(() => {
      const child = children[spawnIndex];
      spawnIndex += 1;
      if (!child) {
        throw new Error("unexpected extra app-server spawn");
      }
      return child.asChildProcess();
    });

    const started = client.request("turn/start", { thread_id: "thread-1" });
    expect(client.runningThreadCwds()).toEqual([tmpdir()]);
    first.stdout.write(
      `${JSON.stringify({
        id: "client-1",
        result: { turn: { status: "in_progress" } },
      })}\n`,
    );
    await expect(started).resolves.toEqual({
      turn: { status: "in_progress" },
    });
    expect(client.isBusy()).toBe(true);

    const pending = client.request("thread/list");
    const pendingRejection = expect(pending).rejects.toThrow(
      /stdin failed: write EPIPE/,
    );
    first.stdin.emit(
      "error",
      Object.assign(new Error("write EPIPE"), { code: "EPIPE" }),
    );
    await pendingRejection;
    expect(client.isBusy()).toBe(false);
    expect(serverExitEvents(events)).toHaveLength(1);
    expect(first.killed).toBe(true);

    const replacement = client.request("initialize");
    expect(spawnIndex).toBe(2);
    expect(client.isBusy()).toBe(true);
    const changesBeforeStaleEvents = stateChanges();

    first.stderr.write("late stderr from old child\n");
    first.stdout.write(
      `${JSON.stringify({ id: "client-3", result: { stale: true } })}\n`,
    );
    first.emit("exit", 1, null);
    first.emit("close", 1, null);

    expect(serverExitEvents(events)).toHaveLength(1);
    expect(stateChanges()).toBe(changesBeforeStaleEvents);
    expect(client.isBusy()).toBe(true);

    second.stdout.write(
      `${JSON.stringify({ id: "client-3", result: { ready: true } })}\n`,
    );
    await expect(replacement).resolves.toEqual({ ready: true });
    expect(client.isBusy()).toBe(false);
  });
});

it.each([false, true])("preserves snapshot content and event order across pipe chunks (fragmented=%s)", async fragmented => {
  const child = new FakeAppServerChild();
  const order: string[] = [];
  const snapshot = { thread: { id: "t", turns: [{ text: "History 中文 🦆\n".repeat(20000) }] } };
  const client = new AppServerClient(tmpdir(), "", (_source, event) => {
    if (event.kind === "notification") order.push("notification");
  }, () => {}, () => child.asChildProcess(), (_env, cwd) => ({ command: "test-core", args: [], cwd }));
  try {
    const pending = client.request("thread/resume", { session_id: "t" }, () => order.push("snapshot"));
    const wire = Buffer.from(JSON.stringify({ id: "client-1", result: snapshot }) + "\r\n\n" +
      JSON.stringify({ method: "item/agentMessage/delta", params: { thread_id: "t", delta: "new" } }) + "\n");
    const chunkSize = fragmented ? 1021 : wire.length;
    for (let offset = 0; offset < wire.length; offset += chunkSize) {
      child.stdout.write(wire.subarray(offset, offset + chunkSize));
    }
    await expect(pending).resolves.toEqual(snapshot);
    expect(order).toEqual(["snapshot", "notification"]);
  } finally { client.dispose(); }
});


describe("packaged CUA", () => {
  it("uses the installed bundle instead of stale development overrides", () => {
    const env: NodeJS.ProcessEnv = { WUU_CUA_MAC_HELPER: "/old/helper", WUU_CUA_MAC_PIP_HELPER: "/old/pip" };
    configurePackagedCUA(env, "/Applications/wuu.app/Contents/Resources", "darwin", () => true);
    expect(env.WUU_ENABLE_CUA_MAC).toBe("1");
    expect(env.WUU_CUA_MAC_HELPER).toBe("/Applications/wuu.app/Contents/Resources/bin/wuu-cua-mac");
    expect(env.WUU_CUA_MAC_PIP_HELPER).toBe("/Applications/wuu.app/Contents/Resources/bin/wuu-cua-mac-pip");
  });
  it("does not enable a broken installation or inherit external helpers", () => {
    const env: NodeJS.ProcessEnv = { WUU_ENABLE_CUA_MAC: "1", WUU_CUA_MAC_HELPER: "/old/helper" };
    configurePackagedCUA(env, "/app", "darwin", (path) => !path.endsWith("-pip"));
    expect(env.WUU_ENABLE_CUA_MAC).toBeUndefined();
    expect(env.WUU_CUA_MAC_HELPER).toBeUndefined();
  });
});


it("does not restart a disposed core while the desktop is waiting for exit", async () => {
  const child = new FakeAppServerChild();
  const spawn = vi.fn(() => child.asChildProcess());
  const { client } = makeClient(spawn);
  client.start();
  const stopped = client.dispose();
  expect(() => client.start()).toThrow("disposed");
  expect(spawn).toHaveBeenCalledTimes(1);
  child.emit("exit", 0, null);
  await stopped;
});
