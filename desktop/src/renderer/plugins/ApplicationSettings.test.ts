import { describe, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import { ApplicationController } from "./ApplicationController.test-support";
import { ApplicationSettings } from "./ApplicationSettings";

function fixture() {
  let state: AppState = { ...initialState,
    activeContext: { kind: "project", project_id: "one", cwd: "/one" },
    initialized: { protocol_version: "test", workspace_root: "/one", provider: "first", model: "old", effort: "high", variant: "fast",
      providers: [{ name: "first", type: "openai", model: "old", models: [{ id: "old" }] }] },
  };
  const settings = new ApplicationSettings({ getState: () => state,
    updateState: update => { state = typeof update === "function" ? update(state) : update; },
    changed: vi.fn(), isNavigationPending: () => false, deleteArchivedThread: vi.fn(), unarchiveThread: vi.fn() });
  return { settings, get: () => state, set: (next: AppState) => { state = next; } };
}

// Settings saves are workspace defaults, preserve context ownership, expose
// immutable views, and must never weaken archived-only destructive actions.
describe("application settings operations", () => {
  it("publishes independent deeply immutable settings data", () => {
    const h = fixture();
    const view = h.settings.getSnapshot();
    expect(Object.isFrozen(view.configuration?.providers?.[0].models)).toBe(true);
    expect(() => { (view.configuration!.providers![0].models as unknown as unknown[]).push({}); }).toThrow();
    expect(h.get().initialized?.providers?.[0].models).toHaveLength(1);
    expect(view.configuration).toMatchObject({ effort: "high", variant: "fast" });
  });

  it("retains the public settings view across unrelated stream updates", () => {
    const h = fixture();
    const first = h.settings.getSnapshot();
    h.set({ ...h.get(), status: "streaming" });
    expect(h.settings.getSnapshot()).toBe(first);
    h.set({ ...h.get(), initialized: { ...h.get().initialized!, model: "changed" } });
    const changed = h.settings.getSnapshot();
    expect(changed).not.toBe(first);
    expect(changed.configuration?.model).toBe("changed");
    expect(first.configuration?.model).toBe("old");
  });

  it("does not apply a late settings save to another workspace", async () => {
    const h = fixture();
    let resolve!: (value: unknown) => void;
    window.wuu = { updateGeneralSettings: vi.fn(() => new Promise(done => { resolve = done; })) } as unknown as typeof window.wuu;
    const pending = h.settings.invoke("settings.update-general", { git_attribution_enabled: false });
    h.set({ ...h.get(), activeContext: { kind: "project", project_id: "two", cwd: "/two" },
      initialized: { ...h.get().initialized!, workspace_root: "/two" } });
    resolve({ general_settings: { git_attribution_enabled: false } });
    await pending;
    expect(h.get().initialized?.general_settings).toBeUndefined();
  });

  it("saves a provider default without targeting or overwriting the open conversation", async () => {
    const h = fixture();
    h.set({ ...h.get(), thread: { id: "thread", preview: "", cwd: "/one", model_provider: "first", model: "pinned", status: "idle", created_at: "", updated_at: "", turns: [] } });
    const update = vi.fn(async (..._args: unknown[]) => ({ provider: "first", model: "new" }));
    window.wuu = { updateRuntimeSettings: update } as unknown as typeof window.wuu;
    await h.settings.invoke("settings.save-provider", { provider: "first", model: "new" });
    expect(update.mock.calls[0]?.[6]).toBeUndefined();
    expect(h.get().initialized?.model).toBe("new");
    expect(h.get().thread?.model).toBe("pinned");
  });

  it("passes the archived-only guard and preserves state when storage rejects deletion", async () => {
    const h = fixture();
    const remove = vi.fn().mockRejectedValue(new Error("restored elsewhere"));
    window.wuu = { deleteThread: remove } as unknown as typeof window.wuu;
    const before = h.get();
    await expect(new ApplicationController(h.get()).invoke("settings.delete-archived", { threadId: "thread" })).rejects.toThrow("restored elsewhere");
    expect(remove).toHaveBeenCalledWith("thread", { onlyIfArchived: true });
    expect(h.get()).toBe(before);
  });
});

it("invalidates shared engine metadata when the provider generation changes", async () => {
  const h = fixture();
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: false }] }) } as unknown as typeof window.wuu;
  await h.settings.invoke("settings.refresh-engines", {});
  expect(h.settings.getSnapshot().engineInventory?.engines[0].requires_host_model).toBe(false);
  h.set({ ...h.get(), initialized: { ...h.get().initialized!, extension_inventory: [] } });
  expect(h.settings.getSnapshot().engineInventory).toBeUndefined();
  vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: true }] });
  await h.settings.invoke("settings.refresh-engines", {});
  expect(h.settings.getSnapshot().engineInventory?.engines[0].requires_host_model).toBe(true);
});
