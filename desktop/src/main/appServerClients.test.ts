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
      join(packagedBin, "wuu-note-compaction-plugin"),
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
    expect(result.WUU_NOTE_COMPACTION_PLUGIN_HELPER).toBe(
      join(packagedBin, "wuu-note-compaction-plugin"),
    );
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
      join(sourceBin, "wuu-note-compaction-plugin"),
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
    expect(discovered.WUU_NOTE_COMPACTION_PLUGIN_HELPER).toBe(
      join(sourceBin, "wuu-note-compaction-plugin"),
    );
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

describe("AppServerClientPool session routing", () => {
  afterEach(() => vi.unstubAllEnvs());
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
    const read = pool.requestForSession(active, "session", "channel/session/read", { sessionRef: "session" }, (_response, cwd) => order.push(`snapshot:${cwd}`));
    send(owner.cwd, { id: "client-1", result: { text: "Live" } });
    send(owner.cwd, { method: "item/agentMessage/delta", params: { thread_id: "session", delta: " later" } });
    expect(await read).toEqual({ text: "Live" });
    expect(order.slice(-2)).toEqual(["snapshot:/owner", "item/agentMessage/delta"]);
    send(owner.cwd, { method: "turn/completed", params: { thread_id: "session" } });
    const completed = pool.requestForSession(active, "session", "channel/session/read");
    send(owner.cwd, { id: "client-2", result: { text: "Completed" } });
    expect(await completed).toEqual({ text: "Completed" });
    children.get(owner.cwd)!.emit("exit", 0, null);
    const restored = pool.requestForSession(active, "session", "channel/session/read");
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

it("forwards a snapshot response before the next notification in the same stdout chunk", async () => {
  const child = new FakeAppServerChild();
  const order: string[] = [];
  const client = new AppServerClient(tmpdir(), "", (_source, event) => {
    if (event.kind === "notification") order.push("notification");
  }, () => {}, () => child.asChildProcess(), (_env, cwd) => ({ command: "test-core", args: [], cwd }));
  try {
    const pending = client.request("thread/resume", { session_id: "t" }, () => order.push("snapshot"));
    child.stdout.write(JSON.stringify({ id: "client-1", result: { thread: { id: "t", turns: [] } } }) + "\n" +
      JSON.stringify({ method: "item/agentMessage/delta", params: { thread_id: "t", delta: "new" } }) + "\n");
    await pending;
    expect(order).toEqual(["snapshot", "notification"]);
  } finally { client.dispose(); }
});
