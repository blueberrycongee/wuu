import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Credentials, RemoteClientOptions } from "@wuu/remote-core";

const remote = vi.hoisted(() => ({ call: vi.fn(), wake: vi.fn(), options: {} as RemoteClientOptions, attached: true }));
vi.mock("@wuu/remote-core", () => ({
  RemoteClient: class {
    call = remote.call;
    wake = remote.wake;
    constructor(_credentials: Credentials, options: RemoteClientOptions) { remote.options = options; }
    isAttached = () => remote.attached;
    start = () => remote.options.onAttach?.({ session: "first", resumed: false });
    waitAttached = async () => {};
    latestState = () => null;
    stop = async () => { remote.attached = false; remote.options.onDetach?.(); };
  },
  pair: vi.fn(),
}));

import { RemoteDesktopBridge, UnavailableHostOperationError } from "../src/lib/desktopBridge";

beforeEach(() => {
  remote.attached = true;
  remote.wake.mockReset();
  remote.call.mockReset().mockResolvedValue({ current: "/paired/workspace" });
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  });
  vi.stubGlobal("navigator", { userAgent: "iPhone Mac OS", language: "en-US" });
  vi.stubGlobal("window", { open: vi.fn() });
});

async function api() {
  const bridge = await connectBridge();
  remote.call.mockClear();
  return bridge.api;
}

describe("browser host contract", () => {
  it("leaves optional Electron integrations absent and rejects unsupported actions", async () => {
    const host = await api();
    expect(host.reportBrowserBounds).toBeUndefined();
    expect(host.openSideThread).toBeUndefined();
    expect(host.onBrowserInvalidate).toBeUndefined();
    expect(host.unsupportedMethods).toContain("installPluginPackage");
    await expect(host.installPluginPackage()).rejects.toBeInstanceOf(UnavailableHostOperationError);
    await expect(host.installPluginPackage()).rejects.toMatchObject({ code: "host_operation_unavailable" });
    await expect(host.updateVoiceInputSettings({ polish_enabled: true, language: "en-US" }))
      .rejects.toBeInstanceOf(UnavailableHostOperationError);
    await expect(host.selectProject("another-computer")).rejects.toThrow("Unknown remote workspace");
    expect(remote.call).not.toHaveBeenCalled();
  });

  it("preserves explicit resets and thread-scoped model selection over RPC", async () => {
    await (await api()).updateRuntimeSettings(undefined, undefined, "", undefined, "", "read_only", "thread-1");
    expect(remote.call).toHaveBeenCalledWith("config/model/update", {
      thread_id: "thread-1", effort: "", variant: "", permission_mode: "read_only",
    }, 30_000, expect.any(String));
  });

  it("returns host engine inventory and routes process input to its owning thread", async () => {
    const host = await api();
    const inventory = { engines: [{ id: "codex", installed: true }] };
    remote.call.mockResolvedValueOnce(inventory);
    expect(await host.listEngines()).toBe(inventory);
    await host.writeManagedProcess("thread-1", "process-2", "\u0003");
    expect(remote.call).toHaveBeenLastCalledWith("process/write", {
      thread_id: "thread-1", process_id: "process-2", input: "\u0003",
    }, 30_000, expect.any(String));
  });

  it("forwards question holds and preserves mixed message parts", async () => {
    const host = await api();
    await host.holdUserQuestion("question-1");
    expect(remote.call).toHaveBeenLastCalledWith("user-question/hold", { request_id: "question-1" }, 30_000, expect.any(String));
    const parts = [{ type: "text" as const, text: "Review this file" }];
    await host.startTurn("thread-1", "Review this file", [], [], "read_only", { path: "src/main.go" }, parts);
    expect(remote.call).toHaveBeenLastCalledWith("turn/start", expect.objectContaining({
      thread_id: "thread-1", active_document: { path: "src/main.go" }, content_parts: parts,
    }), 30_000, expect.any(String));
  });

  it("reads and resolves files in the selected conversation worktree on the host", async () => {
    const bridge = await connectBridge();
    await bridge.api.listWorkspaceDirectory("src", "/paired/worktree");
    expect(remote.call).toHaveBeenLastCalledWith("workspace/directory/list", { path: "src", root: "/paired/worktree" }, 30_000, expect.any(String));
    await bridge.api.readWorkspaceFile("src/main.go", "/paired/worktree");
    expect(remote.call).toHaveBeenLastCalledWith("workspace/file/read", { path: "src/main.go", root: "/paired/worktree" }, 30_000, expect.any(String));
    await bridge.api.resolveWorkspaceFileReference("main.go:12");
    expect(remote.call).toHaveBeenLastCalledWith("workspace/file/resolve", { reference: "main.go:12", root: "/paired/workspace" }, 30_000, expect.any(String));
  });

  it("reads repository changes from the selected host worktree", async () => {
    const bridge = await connectBridge();
    await bridge.api.gitStatus();
    expect(remote.call).toHaveBeenLastCalledWith("workspace/git/status", { root: "/paired/workspace" }, 30_000, expect.any(String));
    await bridge.api.listGitChanges("/paired/worktree");
    expect(remote.call).toHaveBeenLastCalledWith("workspace/git/changes", { root: "/paired/worktree" }, 30_000, expect.any(String));
    await bridge.api.readGitFileDiff("src/main.go", "/paired/worktree");
    expect(remote.call).toHaveBeenLastCalledWith("workspace/git/diff", { path: "src/main.go", root: "/paired/worktree" }, 30_000, expect.any(String));
  });

  it("preserves manageable plugins while excluding desktop modules and asset icons", async () => {
    const bridge = await connectBridge();
    const record = { id: "desktop-extension", kind: "plugin", name: "Example", fingerprint: "generation-1", enabled: true };
    const namedIconPlugin = {
      id: "ask-user",
      kind: "plugin",
      name: "Ask User",
      fingerprint: "generation-2",
      enabled: true,
      icon: { name: "sparkles" },
    };
    const inventory = [
      { ...record, desktop: { entry: "wuu-plugin://module" }, icon: { path: "assets/icon.svg" } },
      namedIconPlugin,
    ];
    const payload = { extension_inventory: inventory, skills: [{ name: "host-skill" }], epoch: 2 };
    const expected = { ...payload, extension_inventory: [record, namedIconPlugin] };
    remote.call.mockResolvedValueOnce(payload);
    expect(await bridge.api.initialize()).toMatchObject(expected);
    const received = vi.fn();
    bridge.api.onServerEvent(received);
    remote.options.onNotification?.("plugin/inventory/changed", payload);
    expect(received).toHaveBeenCalledWith(expect.objectContaining({
      message: { method: "plugin/inventory/changed", params: expected },
    }));
    remote.call.mockResolvedValueOnce(payload);
    expect(await bridge.api.refreshExtensionCatalog()).toEqual(expected);
    for (const action of ["disable", "enable"] as const) {
      const enabled = action === "enable";
      remote.call.mockResolvedValueOnce({ extension_inventory: [{ ...inventory[0], enabled }] });
      const update = { id: record.id, fingerprint: record.fingerprint, action };
      expect(await bridge.api.updateExtensionPackage(update)).toEqual({
        extension_inventory: [{ ...record, enabled }],
      });
      expect(remote.call).toHaveBeenLastCalledWith("extension/package/update", update, 30_000, expect.any(String));
    }
    expect(payload.extension_inventory).toBe(inventory);
    expect(inventory[0]).toHaveProperty("desktop");
    expect(inventory[0]).toHaveProperty("icon");
  });

  it("does not open executable URL schemes", async () => {
    const host = await api();
    await expect(host.openExternal("javascript:alert(1)")).rejects.toThrow("HTTP");
    expect(window.open).not.toHaveBeenCalled();
    await host.openExternal("https://example.com/docs");
    expect(window.open).toHaveBeenCalledWith("https://example.com/docs", "_blank", "noopener,noreferrer");
  });
});


async function connectBridge() {
  const bridge = new RemoteDesktopBridge({ host_pub: "paired-host" } as Credentials);
  await bridge.connect();
  return bridge;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("connection recovery", () => {
  it("wakes only the owned live client", async () => {
    const bridge = await connectBridge();
    bridge.wake();
    expect(remote.wake).toHaveBeenCalledOnce();
    await bridge.disconnect();
    bridge.wake();
    expect(remote.wake).toHaveBeenCalledOnce();
  });

  it.each(["respond", "reject"])("does not consume a server request when %s is attempted offline", async (action) => {
    const bridge = await connectBridge();
    const settled = vi.fn();
    const response = remote.options.onServerRequest!({ id: "approval", method: "tool/approve", params: {} });
    void Promise.resolve(response).then(settled);
    remote.attached = false;
    remote.options.onDetach?.();
    const reply = () => action === "respond"
      ? bridge.api.respondToServerRequest("approval", { approved: true })
      : bridge.api.rejectServerRequest("approval", "No");
    await expect(reply()).rejects.toThrow("disconnected");
    expect(settled).not.toHaveBeenCalled();
    remote.attached = true;
    remote.options.onAttach?.({ session: "first", resumed: true });
    await vi.waitFor(() => expect(bridge.getConnectionSnapshot().phase).toBe("connected"));
    expect(settled).not.toHaveBeenCalled();
    await reply();
    expect(await response).toEqual(action === "respond"
      ? { result: { approved: true } }
      : { error: { code: "rejected", message: "No" } });
  });

  it("waits for a real host workspace before presenting the workbench", async () => {
    const bridge = await connectBridge();
    expect(bridge.getConnectionSnapshot().phase).toBe("connected");
    expect((await bridge.api.listProjects()).active_context?.cwd).toBe("/paired/workspace");
  });

  it("restores a fresh app-server connection in place and waits for subscribers", async () => {
    const bridge = await connectBridge();
    const restore = deferred<void>();
    const handler = vi.fn(() => restore.promise);
    bridge.api.onRuntimeRestore!(handler);
    remote.options.onDetach?.();
    expect(bridge.getConnectionSnapshot().phase).toBe("connected");
    remote.options.onAttach?.({ session: "next", resumed: false });
    await vi.waitFor(() => expect(handler).toHaveBeenCalledOnce());
    expect(bridge.getConnectionSnapshot().phase).toBe("restoring");
    restore.resolve();
    await vi.waitFor(() => expect(bridge.getConnectionSnapshot().phase).toBe("connected"));
  });

  it("keeps a resumed connection in place without restoring the workbench", async () => {
    const bridge = await connectBridge();
    const handler = vi.fn(async () => {});
    bridge.api.onRuntimeRestore!(handler);
    remote.call.mockClear();
    remote.options.onDetach?.();
    expect(bridge.getConnectionSnapshot().phase).toBe("connected");
    remote.options.onAttach?.({ session: "next", resumed: true });
    await vi.waitFor(() => expect(bridge.getConnectionSnapshot().phase).toBe("connected"));
    expect(handler).not.toHaveBeenCalled();
    expect(remote.call).not.toHaveBeenCalled();
  });

  it("hides a brief disconnect and only surfaces reconnecting after the grace window", async () => {
    vi.useFakeTimers();
    try {
      const bridge = await connectBridge();
      const handler = vi.fn(async () => {});
      bridge.api.onRuntimeRestore!(handler);
      remote.options.onDetach?.();
      expect(bridge.getConnectionSnapshot().phase).toBe("connected");
      await vi.advanceTimersByTimeAsync(599);
      expect(bridge.getConnectionSnapshot().phase).toBe("connected");
      remote.options.onAttach?.({ session: "next", resumed: true });
      await vi.advanceTimersByTimeAsync(1_000);
      expect(bridge.getConnectionSnapshot().phase).toBe("connected");
      expect(handler).not.toHaveBeenCalled();
      remote.options.onDetach?.();
      await vi.advanceTimersByTimeAsync(600);
      expect(bridge.getConnectionSnapshot().phase).toBe("reconnecting");
    } finally {
      vi.useRealTimers();
    }
  });

  it("ignores stale restoration completion after another disconnect", async () => {
    const bridge = await connectBridge();
    const restore = deferred<void>();
    bridge.api.onRuntimeRestore!(() => restore.promise);
    remote.options.onAttach?.({ session: "next", resumed: false });
    await Promise.resolve();
    remote.options.onDetach?.();
    restore.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(bridge.getConnectionSnapshot().phase).toBe("reconnecting");
  });

  it("surfaces restore failure and retries without pairing or replacing the bridge", async () => {
    const bridge = await connectBridge();
    const restore = vi.fn().mockRejectedValueOnce(new Error("snapshot failed")).mockResolvedValue(undefined);
    bridge.api.onRuntimeRestore!(restore);
    remote.options.onAttach?.({ session: "next", resumed: false });
    await vi.waitFor(() => expect(bridge.getConnectionSnapshot().phase).toBe("error"));
    expect(bridge.getConnectionSnapshot().error).toBe("snapshot failed");
    remote.call.mockClear();
    await expect(bridge.api.startTurn("thread", "do work")).rejects.toThrow("restoring");
    expect(remote.call).not.toHaveBeenCalled();
    await bridge.retryRestore();
    expect(bridge.getConnectionSnapshot().phase).toBe("connected");
    expect(restore).toHaveBeenCalledTimes(2);
  });

  it("never queues offline sends and rejects responses from replaced connections", async () => {
    const bridge = await connectBridge();
    remote.call.mockClear();
    remote.attached = false;
    remote.options.onDetach?.();
    await expect(bridge.api.startTurn("thread", "do work")).rejects.toThrow("disconnected");
    expect(remote.call).not.toHaveBeenCalled();
    remote.attached = true;
    const pending = deferred<unknown>();
    remote.call.mockReturnValueOnce(pending.promise);
    const request = bridge.api.listThreads();
    remote.options.onDetach?.();
    pending.resolve({ threads: [] });
    await expect(request).rejects.toThrow("connection changed");
  });

  it("blocks writes until restoration finishes", async () => {
    const bridge = await connectBridge();
    const restore = deferred<void>();
    bridge.api.onRuntimeRestore!(() => restore.promise);
    remote.options.onAttach?.({ session: "next", resumed: false });
    await expect(bridge.api.startTurn("thread", "do work")).rejects.toThrow("restoring");
    restore.resolve();
  });
});


describe("workspace routing", () => {
  const workspaces = {
    current: "/computer/alpha", current_id: "alpha",
    workspaces: [{ id: "alpha", name: "Alpha", path: "/computer/alpha" },
      { id: "beta", name: "Beta", path: "/computer/beta" }],
  };

  it("selects registered workspaces and starts work with the computer's workspace ID", async () => {
    remote.call.mockResolvedValue(workspaces);
    const bridge = await connectBridge();
    expect((await bridge.api.listProjects()).projects.map((project) => project.id)).toEqual(["alpha", "beta"]);
    const selected = await bridge.api.selectProject("beta");
    expect(selected.active_context).toEqual({ kind: "project", project_id: "beta", cwd: "/computer/beta" });
    await bridge.api.listThreads();
    expect(remote.call).toHaveBeenLastCalledWith("thread/list", { cwd: "/computer/beta", summary_only: true }, 30_000, "/computer/beta");
    await bridge.api.startThread({ model: "chosen-model" });
    expect(remote.call).toHaveBeenLastCalledWith("thread/start", {
      model: "chosen-model", cwd: "/computer/beta", workspace_id: "beta",
    }, 30_000, expect.any(String));
    await bridge.api.listWorkspaceDirectory();
    expect(remote.call).toHaveBeenLastCalledWith("workspace/directory/list", { path: undefined, root: "/computer/beta" }, 30_000, expect.any(String));
  });

  it("routes background and worktree events to their owning project after switching", async () => {
    remote.call.mockResolvedValue(workspaces);
    const bridge = await connectBridge();
    const events: Array<{ workdir: string }> = [];
    bridge.api.onServerEvent((event) => events.push(event));
    remote.options.onNotification?.("thread/started", { thread: { id: "a", cwd: "/computer/alpha-tree", workspace_id: "alpha", worktree: { base_repo: "/computer/alpha" } } });
    await bridge.api.selectProject("beta");
    remote.options.onNotification?.("item/agentMessage/delta", { thread_id: "a", delta: "still running" });
    expect(events.at(-1)?.workdir).toBe("/computer/alpha");
    remote.options.onNotification?.("thread/started", { thread: { id: "b", cwd: "/computer/beta", workspace_id: "beta" } });
    expect(events.at(-1)?.workdir).toBe("/computer/beta");
    remote.options.onState?.({ ver: 1, host: { workdir: "/computer/alpha" }, running: [{ thread_id: "a" }, { thread_id: "b" }] });
    expect(await bridge.api.getRunningThreadsSnapshot()).toEqual([
      { thread_id: "a", workdir: "/computer/alpha" }, { thread_id: "b", workdir: "/computer/beta" },
    ]);
  });

  it("reopens a known unregistered conversation without creating a native scratch workspace", async () => {
    remote.call.mockResolvedValue(workspaces);
    const bridge = await connectBridge();
    remote.call.mockResolvedValueOnce({ threads: [{ id: "scratch", cwd: "/computer/scratch" }] });
    await bridge.api.listAllThreads();
    expect((await bridge.api.selectNoProject(false, "/computer/scratch")).active_context)
      .toEqual({ kind: "no_project", cwd: "/computer/scratch" });
    await bridge.api.selectNoProject(true);
    expect(remote.call).toHaveBeenLastCalledWith("desktop/projects/no-project", { fresh: true, cwd: undefined }, 30_000, expect.any(String));
    await expect(bridge.api.selectNoProject(false, "/unknown")).rejects.toBeInstanceOf(UnavailableHostOperationError);
  });

  it("preserves selection on reconnect and never substitutes a different directory after removal", async () => {
    remote.call.mockResolvedValue(workspaces);
    const bridge = await connectBridge();
    await bridge.api.selectProject("beta");
    remote.options.onAttach?.({ session: "next", resumed: false });
    await vi.waitFor(() => expect(bridge.getConnectionSnapshot().phase).toBe("connected"));
    expect((await bridge.api.listProjects()).active_context?.cwd).toBe("/computer/beta");
    remote.call.mockResolvedValue({ ...workspaces, workspaces: [workspaces.workspaces[0]] });
    const refreshed = await bridge.api.listProjects();
    expect(refreshed.active_context?.cwd).toBe("/computer/beta");
    expect(refreshed.projects.find((project) => project.id === "beta")?.missing).toBe(true);
  });
});


it("routes background thread actions and questions to their owner after a workspace switch", async () => {
  remote.call.mockResolvedValue({ current: "/alpha", current_id: "alpha", workspaces: [{ id: "alpha", name: "A", path: "/alpha" }, { id: "beta", name: "B", path: "/beta" }] });
  const bridge = await connectBridge();
  remote.options.onNotification?.("thread/started", { thread: { id: "t", cwd: "/alpha", workspace_id: "alpha" } }, "/alpha");
  remote.options.onNotification?.("user-question/requested", { request: { request_id: "q", thread_id: "t" } }, "/alpha");
  await bridge.api.selectProject("beta");
  await bridge.api.interruptTurn("t");
  expect(remote.call).toHaveBeenLastCalledWith("turn/interrupt", { thread_id: "t" }, 30_000, "/alpha");
  await bridge.api.holdUserQuestion("q");
  expect(remote.call).toHaveBeenLastCalledWith("user-question/hold", { request_id: "q" }, 30_000, "/alpha");
});

it("synchronizes a resumed snapshot once from the response without requesting a broadcast", async () => {
  const bridge = await connectBridge();
  const listener = vi.fn();
  bridge.api.onServerEvent(listener);
  const result = { thread: { id: "t", cwd: "/paired/workspace", turns: [] }, held_user_messages: [{id:"held"}] };
  remote.call.mockResolvedValueOnce(result);
  expect(await bridge.api.resumeThread("t")).toEqual(result);
  expect(remote.call).toHaveBeenLastCalledWith("thread/resume", {session_id:"t", response_only:true}, 30_000, "/paired/workspace");
  expect(listener).toHaveBeenCalledTimes(1);
  expect(listener.mock.calls[0][0].message).toEqual({method:"thread/resumed", params:result});
});

it("assembles explicitly requested image chunks and shares repeated reads", async () => {
  const host = await api();
  const data = "a".repeat(300_000);
  remote.call.mockImplementation(async (_method, {offset}) => ({offset, total:data.length, data:data.slice(offset, offset+128*1024)}));
  expect(await host.readRemoteAttachment!("ref")).toBe(data);
  const calls = remote.call.mock.calls.length;
  expect(await host.readRemoteAttachment!("ref")).toBe(data);
  expect(remote.call).toHaveBeenCalledTimes(calls);
});

it("bounds thumbnail concurrency and routes cached previews to their source workspace", async () => {
  const host = await api();
  remote.call.mockClear();
  const releases: Array<(value: unknown) => void> = [];
  remote.call.mockImplementation(() => new Promise(resolve => releases.push(resolve)));
  const refs = [0,1,2].map(index => "thread:" + btoa(JSON.stringify(["t","turn","item",index,"a".repeat(64)])));
  const reads = refs.map(ref => host.readRemoteAttachmentPreview!(ref));
  expect(remote.call).toHaveBeenCalledTimes(2);
  const response = {data:"dGh1bWI=",total:8,content_type:"image/jpeg"};
  releases[0](response);
  expect(await reads[0]).toBe("data:image/jpeg;base64,dGh1bWI=");
  expect(remote.call).toHaveBeenCalledTimes(3);
  releases[1](response); releases[2](response);
  await Promise.all(reads);
  await host.readRemoteAttachmentPreview!(refs[0]);
  expect(remote.call).toHaveBeenCalledTimes(3);
});

it("marks a terminal as ended when a new remote attachment replaces its owner", async () => {
 const bridge=await connectBridge();
 const events:unknown[]=[];bridge.api.onTerminalEvent(event=>events.push(event));
 remote.call.mockResolvedValueOnce({id:'term-1',cwd:'/paired/workspace',shell:'/bin/sh',started_at:'now'});
 await bridge.api.startTerminalSession();
 remote.options.onNotification?.('desktop/terminal/event',{type:'data',id:'term-1',text:'output'},'/paired/workspace');
 expect(events).toContainEqual({type:'data',id:'term-1',text:'output'});
 remote.options.onDetach?.();remote.options.onAttach?.({session:'replacement',resumed:false});
 expect(events).toContainEqual(expect.objectContaining({type:'error',id:'term-1'}));
 await bridge.disconnect();
});
