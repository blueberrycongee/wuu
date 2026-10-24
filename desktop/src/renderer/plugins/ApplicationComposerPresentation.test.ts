import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import { turnTelemetryStore } from "../TurnTelemetryStore";
import { PluginHost } from "./PluginHost";
import { ApplicationComposerPresentation } from "./ApplicationComposerPresentation";

const original = window.wuu;
beforeEach(() => { window.wuu = {} as typeof window.wuu; });
afterEach(() => { window.wuu = original; turnTelemetryStore.reset(); });

function fixture() {
  let state: AppState = { ...initialState, activeSessionTabID: "draft", activeContext: { kind: "no_project", cwd: "/one" },
    sessionTabs: [{ id: "draft", kind: "draft", title: "Draft", context: { kind: "no_project", cwd: "/one" }, prompt: "", images: [], files: [], createdAt: 1 }],
    initialized: { protocol_version: "test", workspace_root: "/one", provider: "api", model: "model", providers: [] } };
  const listeners = new Set<() => void>(); const host = new PluginHost({ react: React });
  const helper = new ApplicationComposerPresentation({ getState: () => state, subscribeState: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, getPluginHost: () => host });
  const life = new AbortController(); const service = helper.createScope(life.signal);
  return { host, helper, life, service, listeners, get: () => state, set: (value: AppState) => { state = value; for (const listener of listeners) listener(); } };
}

describe("composer presentation host boundary", () => {
  it("publishes an unavailable view when a mounted draft closes", () => {
    const f = fixture(); f.service.getSnapshot("draft");
    f.set({ ...f.get(), sessionTabs: [] });
    expect(f.service.getSnapshot("draft")).toMatchObject({ ownerTabId: "draft", available: false });
    expect(f.service.getSnapshot("draft")).toBe(f.service.getSnapshot("draft"));
    f.helper.dispose();
  });
  it("publishes immutable telemetry without mutating the canonical store", () => {
    const f = fixture();
    turnTelemetryStore.ingest({ kind: "notification", workdir: "/one", message: { method: "turn/usage", params: { turn_id: "one", output_tokens: 4 } } });
    const snapshot = f.service.getTelemetry("one");
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(snapshot).not.toBe(turnTelemetryStore.getSnapshot("one"));
    expect(f.service.getTelemetry("one")).toBe(snapshot);
    f.helper.dispose();
  });
  it("keeps immutable views stable across unrelated state updates", () => {
    const f = fixture(); const first = f.service.getSnapshot("draft");
    expect(first.available).toBe(true); expect(Object.isFrozen(first.configuration?.providers)).toBe(true);
    f.set({ ...f.get(), status: "ready" }); const second = f.service.getSnapshot("draft");
    expect(f.service.getSnapshot("draft")).toBe(second);
    f.set({ ...f.get(), threads: [...f.get().threads] }); expect(f.service.getSnapshot("draft")).toBe(second);
    f.helper.dispose();
  });
  it("captures command source and rejects removed providers without exposing executions", async () => {
    const f = fixture(); const execute = vi.fn(async () => {});
    await f.host.activateGeneration({ pluginId: "sample", generation: "one", register: api => { api.registerCommand({ id: "run", title: "Run", execute }); } });
    expect(f.service.getCommands()).toEqual([{ pluginId: "sample", id: "run" }]);
    await f.service.executeCommand({ ownerTabId: "draft", pluginId: "sample", id: "run", args: "hello" });
    expect(execute).toHaveBeenCalledWith({ source: "slash-command", commandId: "run", args: "hello", threadId: undefined, context: { kind: "no_project", cwd: "/one" } });
    f.host.unload("sample");
    await expect(f.service.executeCommand({ ownerTabId: "draft", pluginId: "sample", id: "run", args: "" })).rejects.toThrow();
    f.helper.dispose();
  });
  it("removes scope listeners and rejects stale reads and commands", async () => {
    const f = fixture(); const callback = vi.fn(); f.service.subscribe(callback); expect(f.listeners.size).toBe(1);
    f.life.abort(); expect(f.listeners.size).toBe(0);
    expect(() => f.service.getSnapshot("draft")).toThrow();
    await expect(f.service.executeCommand({ ownerTabId: "draft", pluginId: "missing", id: "run", args: "" })).rejects.toThrow();
    expect(callback).not.toHaveBeenCalled(); f.helper.dispose();
  });
  it("adapts inline providers with stable identity and no host ownership fields", async () => {
    const f = fixture();
    await f.host.activateGeneration({ pluginId: "sample", generation: "one", register: api => { api.registerComposerInlineCompletionProvider({ id: "complete", provideCompletion: async () => ({ text: "suffix" }) }); } });
    const providers = f.service.getComposerInlineCompletionProviders();
    expect(f.service.getComposerInlineCompletionProviders()).toBe(providers);
    expect(Object.keys(providers[0]).sort()).toEqual(["onDidInteract", "provideCompletion", "subscribeRefresh"]);
    const context = { contractVersion: 1 as const, kind: "main" as const, composerId: "one", revision: 1, draftText: "hello", selectionStart: 5, selectionEnd: 5, running: false };
    await expect(providers[0].provideCompletion(context, new AbortController().signal)).resolves.toEqual({ text: "suffix" });
    f.life.abort(); await expect(providers[0].provideCompletion(context, new AbortController().signal)).rejects.toThrow();
    f.helper.dispose();
  });
  it("publishes only presentation adapters for composer slots", async () => {
    const f = fixture();
    await f.host.activateGeneration({ pluginId: "sample", generation: "one", register: api => { api.registerSlot("composer.toolbar", { id: "button", render: () => null }); } });
    const contributions = f.service.getContributions("composer.toolbar");
    expect(contributions).toHaveLength(1); expect(Object.keys(contributions[0]).sort()).toEqual(["View", "key", "title"]);
    expect(f.service.getContributions("composer.toolbar")).toBe(contributions);
    f.host.unload("sample"); expect(f.service.getContributions("composer.toolbar")).toHaveLength(0); f.helper.dispose();
  });
});
