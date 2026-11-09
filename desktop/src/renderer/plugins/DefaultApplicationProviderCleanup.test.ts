import "../test-support/AbortSignal";
import * as React from "react";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { activate } from "./DefaultApplicationProvider";
import { flushTerminalCleanup } from "../RendererTerminalCleanup";
import type { WorkbenchController } from "./Workbench";
import * as RuntimeLoader from "../RuntimeLoadState";
import type { WuuDesktopApi } from "../../shared/protocol";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";

function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
afterEach(async () => { await flushTerminalCleanup(); vi.restoreAllMocks(); });

async function fixture(held: "terminal-start" | "terminal-stop" | "web-initialize") {
  window.localStorage.clear(); window.sessionStorage.clear();
  const source = { kind: "project" as const, project_id: "a", cwd: "/synthetic/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/synthetic/b" };
  const projects = [source, target].map(c => ({ id: c.project_id, name: c.project_id, path: c.cwd, created_at: "", updated_at: "" }));
  let selected = source;
  const oldTerminal = { id: "old-owned-pty", cwd: source.cwd, shell: "/synthetic/shell", started_at: "" };
  const newTerminal = { ...oldTerminal, id: "new-owned-pty", cwd: target.cwd };
  const webResult = { id: "borrowed-collection", tabs: [], activeTabId: null };
  const startGate = deferred<typeof oldTerminal>(), stopGate = deferred<{ ok: boolean }>(), webGate = deferred<typeof webResult>();
  const native = {
    onState: () => () => {}, initializeTabs: vi.fn().mockResolvedValue(webResult), close: vi.fn().mockResolvedValue(undefined),
    dismissOptions: vi.fn().mockResolvedValue(undefined), reportBounds: vi.fn().mockResolvedValue(undefined), setShortcutTarget: vi.fn().mockResolvedValue(undefined),
  };
  if (held === "web-initialize") native.initializeTabs.mockImplementationOnce(() => webGate.promise);
  const startTerminal = vi.fn().mockResolvedValue(newTerminal);
  startTerminal.mockImplementationOnce(() => held === "terminal-start" ? startGate.promise : Promise.resolve(oldTerminal));
  const stopTerminal = vi.fn((id: string) => id === oldTerminal.id && held === "terminal-stop" ? stopGate.promise : Promise.resolve({ ok: true }));
  window.wuu = { listProjects: vi.fn(async () => ({ projects, active_context: selected })),
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    startTerminalSession: startTerminal, stopTerminalSession: stopTerminal, webWorkspace: native,
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementation(async () => ({ activeContext: selected, activeProjectId: selected.project_id,
    projects, threads: [], initialized: { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] } as never, status: "ready" }));
  const host = new PluginHost({ react: React });
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "same-code", graphGeneration: "source-graph", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "same-code", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  const sourceApp = app;
  const sourceWorkbench = (app.workbench.renderLayers({}) as React.ReactElement<{ controller: WorkbenchController }>).props.controller;
  const pendingResource = held === "web-initialize" ? webGate.promise : app.terminals.start({ cwd: source.cwd, cols: 80, rows: 24 }).catch(error => error);
  await vi.waitFor(() => expect(held === "web-initialize" ? native.initializeTabs : startTerminal).toHaveBeenCalledOnce());
  if (held === "terminal-stop") await pendingResource;
  let handoff: Promise<unknown> | undefined;
  return { host, sourceApp, sourceWorkbench, startGate, stopGate, stopTerminal, oldTerminal, newTerminal, native, pendingResource,
    beginHandoff() { selected = target; return handoff = host.activateGeneration({ pluginId: "business", generation: "same-code", graphGeneration: "target-graph", register }); },
    async targetApp() {
      await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
      expect(app.getSnapshot().workspaces.activeRoot).toBe(target.cwd); return app;
    },
    release() { startGate.resolve(oldTerminal); stopGate.resolve({ ok: true }); webGate.resolve(webResult); },
    async cleanup() { this.release(); await pendingResource; await handoff; await host.unloadAndWait("shell"); await host.unloadAndWait("business"); },
  };
}

it.each(["terminal-start", "terminal-stop"] as const)("activates the new writer before retired %s joins, without reusing its PTY ownership", async held => {
  const f = await fixture(held);
  try {
    const switched = f.beginHandoff();
    if (held === "terminal-stop") await vi.waitFor(() => expect(f.stopTerminal).toHaveBeenCalledWith("old-owned-pty"));
    await vi.waitFor(() => expect(f.host.applicationProviders.current?.graphGeneration).toBe("target-graph"), { timeout: 250 });
    await switched;
    expect(() => f.sourceApp.getSnapshot()).toThrow();
    expect(() => f.sourceWorkbench.retainWebPage("retired-callback")).toThrow();
    const next = await f.targetApp();
    await next.invoke("composer.set-draft", { text: "editable before old IPC finishes" });
    const created = await next.terminals.start({ cwd: "/synthetic/b", cols: 80, rows: 24 });
    expect(created.id).toBe("new-owned-pty");
    f.release(); await f.pendingResource;
    await vi.waitFor(() => expect(f.stopTerminal).toHaveBeenCalledExactlyOnceWith("old-owned-pty"));
    expect(next.getSnapshot().composer.draftText).toBe("editable before old IPC finishes");
    expect(f.stopTerminal).not.toHaveBeenCalledWith("new-owned-pty");
  } finally { await f.cleanup(); }
});

it("revokes captured workbench callbacks synchronously while web cleanup still blocks the handoff", async () => {
  const f = await fixture("web-initialize");
  try {
    let finished = false;
    const switched = f.beginHandoff().then(() => { finished = true; });
    await vi.waitFor(() => expect(f.host.applicationProviders.current).toBeUndefined());
    expect(() => f.sourceApp.getSnapshot()).toThrow();
    expect(() => f.sourceWorkbench.retainWebPage("retired-callback")).toThrow();
    expect(finished).toBe(false);
    expect(window.wuu.listProjects).toHaveBeenCalledTimes(1);
    f.release(); await switched;
    const next = await f.targetApp();
    await next.invoke("composer.set-draft", { text: "after web ownership settles" });
    expect(next.getSnapshot().composer.draftText).toBe("after web ownership settles");
    expect(f.native.close).not.toHaveBeenCalled();
  } finally { await f.cleanup(); }
});


it("keeps the document shutdown drain responsible after the retired provider has been removed", async () => {
  const f = await fixture("terminal-start");
  try {
    const switched = f.beginHandoff();
    await vi.waitFor(() => expect(f.host.applicationProviders.current?.graphGeneration).toBe("target-graph"));
    await switched;
    await f.host.unloadAndWait("shell"); await f.host.unloadAndWait("business");
    let drained = false;
    const shutdown = flushTerminalCleanup().then(() => { drained = true; });
    await Promise.resolve(); await Promise.resolve();
    expect(drained).toBe(false);
    f.release(); await shutdown;
    expect(f.stopTerminal).toHaveBeenCalledExactlyOnceWith("old-owned-pty");
  } finally { await f.cleanup(); }
});

it("reports a late retired terminal failure without failing or rewriting the successor workspace", async () => {
  const errors = vi.spyOn(console, "error").mockImplementation(() => {});
  const f = await fixture("terminal-stop");
  try {
    const switched = f.beginHandoff();
    await vi.waitFor(() => expect(f.host.applicationProviders.current?.graphGeneration).toBe("target-graph"));
    await switched;
    const next = await f.targetApp();
    await next.invoke("composer.set-draft", { text: "new owner wins" });
    const failed = new Error("old PTY stop failed");
    f.stopGate.reject(failed);
    await expect(flushTerminalCleanup()).rejects.toMatchObject({ errors: [failed] });
    await vi.waitFor(() => expect(errors).toHaveBeenCalledWith("Application resource cleanup failed", expect.any(AggregateError)));
    expect(f.host.applicationProviders.failure).toBeUndefined();
    expect(next.getSnapshot().workspaces.activeRoot).toBe("/synthetic/b");
    expect(next.getSnapshot().composer.draftText).toBe("new owner wins");
    expect(() => f.sourceWorkbench.retainWebPage("late-old-callback")).toThrow();
  } finally { await f.cleanup(); }
});
