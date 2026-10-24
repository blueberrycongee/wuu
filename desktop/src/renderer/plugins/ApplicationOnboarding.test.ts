import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../../shared/protocol";
import { initialState, type AppState } from "../AppState";
import type { ApplicationOnboardingWorkflowV1 } from "../../../../packages/plugin-sdk/src/ui-onboarding";
import { ApplicationOnboarding } from "./ApplicationOnboarding";
const workflow: ApplicationOnboardingWorkflowV1 = { step: "ready", selectedPluginIDs: [], initializedPluginSelection: true,
  applyingPlugins: false, savingProvider: false, connected: true, savingRuntime: false, finishing: false,
  importingBrowser: "", browserImportError: "", browserImportResults: {}, browserImported: false,
  scanningWorkspaces: false, choosingWorkspace: false, selectedPaths: ["/one"], scanAttempt: 0, scannedRevision: 0, error: null, selectedEngine: "wuu" };
let state: AppState; let owner: ApplicationOnboarding; const select = vi.fn(); const original = window.wuu;
beforeEach(() => {
  state = { ...initialState, onboardingComplete: false, activeContext: { kind: "no_project", cwd: "/source" } };
  select.mockReset(); select.mockResolvedValue(undefined);
  window.wuu = { initialOnboardingComplete: false, discoverRecentWorkspaces: vi.fn(), chooseWorkspaceDirectory: vi.fn(),
    importWorkspaces: vi.fn().mockResolvedValue({ projects: [{ id: "one", path: "/one" }] }), completeOnboarding: vi.fn().mockResolvedValue(undefined) } as unknown as WuuDesktopApi;
  owner = new ApplicationOnboarding({ getState: () => state, updateState: update => { state = typeof update === "function" ? update(state) : update; }, selectImportedWorkspace: select });
});
afterEach(() => { owner.dispose(); window.wuu = original; });
describe("public onboarding owner", () => {
  it("preserves immutable setup choices and refuses stale checkpoints", () => {
    const first = owner.getSnapshot(); owner.checkpoint({ revision: first.revision, workflow });
    const next = owner.getSnapshot(); expect(next.workflow?.selectedPaths).toEqual(["/one"]);
    expect(Object.isFrozen(next.workflow?.selectedPaths)).toBe(true); expect(state.onboarding?.selectedPaths).toEqual(new Set(["/one"]));
    expect(() => owner.checkpoint({ revision: first.revision, workflow: { ...workflow, selectedPaths: [] } })).toThrow(/revision/);
    state = { ...state, status: "unrelated" }; owner.notifyChanged(); expect(owner.getSnapshot()).toBe(next);
  });
  it("imports exact paths, navigates once, then publishes durable completion", async () => {
    const calls: string[] = [];
    vi.mocked(window.wuu.importWorkspaces!).mockImplementation(async () => { calls.push("import"); return { projects: [{ id: "one", path: "/one" }] } as never; });
    select.mockImplementation(async () => { calls.push("select"); });
    vi.mocked(window.wuu.completeOnboarding!).mockImplementation(async () => { calls.push("complete"); return undefined as never; });
    await owner.complete({ paths: ["/one"] });
    expect(calls).toEqual(["import", "select", "complete"]); expect(window.wuu.importWorkspaces).toHaveBeenCalledExactlyOnceWith(["/one"]);
    expect(select).toHaveBeenCalledExactlyOnceWith("one", undefined); expect(state.onboardingComplete).toBe(true); expect(owner.getSnapshot().completed).toBe(true);
  });
  it("does not finish after failed import identity or navigation", async () => {
    await expect(owner.complete({ paths: ["/missing"] })).rejects.toThrow(/workspace/);
    expect(select).not.toHaveBeenCalled(); expect(window.wuu.completeOnboarding).not.toHaveBeenCalled();
    select.mockRejectedValue(new Error("navigation unavailable")); await expect(owner.complete({ paths: ["/one"] })).rejects.toThrow("navigation unavailable");
    expect(window.wuu.completeOnboarding).not.toHaveBeenCalled(); expect(state.onboardingComplete).toBe(false);
  });
  it("fences stale discovery and cancels preparation without repeating import", async () => {
    let resolve!: (value: unknown) => void;
    vi.mocked(window.wuu.discoverRecentWorkspaces!).mockImplementation(() => new Promise(done => { resolve = value => done(value as Awaited<ReturnType<NonNullable<WuuDesktopApi["discoverRecentWorkspaces"]>>>); }));
    const reading = owner.discoverWorkspaces(); state = { ...state, activeContext: { kind: "no_project", cwd: "/other" } };
    resolve({ candidates: [], incomplete: false }); await expect(reading).rejects.toThrow(/source/);
    const signal = new AbortController(); select.mockImplementation(() => new Promise(done => { resolve = done; }));
    const completing = owner.complete({ paths: ["/one"] }, { signal: signal.signal });
    await vi.waitFor(() => expect(select).toHaveBeenCalledOnce());
    await expect(owner.complete({ paths: [] })).rejects.toThrow(/pending/);
    signal.abort(); resolve(undefined); await expect(completing).rejects.toThrow();
    expect(window.wuu.importWorkspaces).toHaveBeenCalledOnce(); expect(window.wuu.completeOnboarding).not.toHaveBeenCalled();
    owner.dispose(); expect(() => owner.getSnapshot()).toThrow(/disposed/);
  });
  it("publishes confirmed durable completion even when its caller cancels while persistence settles", async () => {
    let finish!: () => void;
    vi.mocked(window.wuu.completeOnboarding!).mockImplementation(() => new Promise(done => { finish = () => done(undefined as never); }));
    const signal = new AbortController(); const completing = owner.complete({ paths: [] }, { signal: signal.signal });
    await vi.waitFor(() => expect(window.wuu.completeOnboarding).toHaveBeenCalledOnce());
    signal.abort(); finish(); await expect(completing).rejects.toThrow();
    expect(owner.getSnapshot().completed).toBe(true);
  });
});
