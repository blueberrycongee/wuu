import "../test-support/AbortSignal";
import * as React from "react";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { activate } from "./DefaultApplicationProvider";
import * as RuntimeLoader from "../RuntimeLoadState";
import type { WuuDesktopApi } from "../../shared/protocol";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";

const hosts: PluginHost[] = [];
afterEach(async () => { for (const host of hosts.splice(0)) { await host.unloadAndWait("shell"); await host.unloadAndWait("business"); } vi.restoreAllMocks(); });

// Public boundary failure cases: no host controller, dormant unselected package,
// stopped checkpoint carries unsent text, and disable retires captured handles.
it("loads the default business through registration, transfers unsent drafts, and disables without a fallback", async () => {
  const context = { kind: "no_project" as const, cwd: "/fixture" };
  const off = vi.fn();
  window.wuu = { listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
    onServerEvent: () => off, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: [], threads: [],
    initialized: { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] } as never, status: "ready" });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  expect(window.wuu.listProjects).not.toHaveBeenCalled();
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let application!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { application = api.application; } });
  await application.invoke("composer.set-draft", { text: "unsent draft" });
  await application.invoke("settings.open", { pageId: "appearance" });
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  expect(() => application.getSnapshot()).toThrow();
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { application = api.application; } });
  expect(application.getSnapshot().composer.draftText).toBe("unsent draft");
  expect(application.getSnapshot().settings).toMatchObject({ open: true, activePageId: "appearance" });
  await host.unloadAndWait("business");
  expect(() => application.getSnapshot()).toThrow();
  expect(off).toHaveBeenCalled();
});

it.each([false, true])("transfers pending input before replacing the writer and ignores its late delivery (failure=%s)", async failed => {
  const context = { kind: "no_project" as const, cwd: "/fixture" };
  const thread = { id: "pending-thread", cwd: context.cwd, title: "Pending", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [] };
  let resolve!: (value: unknown) => void; let reject!: (error: Error) => void;
  const delivery = new Promise((yes, no) => { resolve = yes; reject = no; });
  window.wuu = { listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    startThread: vi.fn().mockResolvedValue({ thread }), startTurn: vi.fn(() => delivery),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: [], threads: [],
    initialized: { provider: "fixture", model: "fixture", providers: [{ name: "fixture", type: "openai-compatible", model: "fixture", api_key_configured: true }], extension_inventory: [] } as never, status: "ready" });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "pending original" });
  const owner = app.getSnapshot().drafts.primary.ownerTabId!;
  expect(await app.invoke("composer.submit", { ownerTabId: owner })).toMatchObject({ accepted: true });
  await vi.waitFor(() => expect(window.wuu.startTurn).toHaveBeenCalledOnce());
  await app.invoke("composer.set-draft", { text: "newer input" });
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
  expect(app.getSnapshot().tabs.items.map(tab => tab.draftText)).toEqual(expect.arrayContaining(["pending original", "newer input"]));
  const before = app.getSnapshot().tabs;
  const persisted = { local: { ...window.localStorage }, session: { ...window.sessionStorage } };
  if (failed) reject(new Error("late rejected delivery"));
  else resolve({ turn: { id: "accepted-late", status: "completed", items_view: "full", items: [] } });
  await new Promise(done => setTimeout(done, 0));
  expect(app.getSnapshot().tabs).toEqual(before);
  expect({ local: { ...window.localStorage }, session: { ...window.sessionStorage } }).toEqual(persisted);
});
