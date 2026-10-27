import { expect, it, vi } from "vitest";
import type { InitializeResult, ServerEvent, WuuDesktopApi } from "../../shared/protocol";
import { DesktopBootstrap } from "./DesktopBootstrap";

it("keeps initialization and inventory bound to the active transport workspace, including racing publications", async () => {
  let receive!: (event: ServerEvent) => void;
  let initialized!: Parameters<NonNullable<WuuDesktopApi["onRuntimeInitialization"]>>[0];
  let resolve!: (value: InitializeResult) => void;
  const first = new Promise<InitializeResult>(done => { resolve = done; });
  window.wuu = { initialize: () => first,
    onServerEvent: (handler: typeof receive) => { receive = handler; return () => {}; },
    onRuntimeInitialization: (handler: typeof initialized) => { initialized = handler; return () => {}; },
  } as unknown as WuuDesktopApi;
  const bootstrap = new DesktopBootstrap(); const stop = bootstrap.start();
  const publish = (workdir: string, generation: string) => receive({ kind: "notification", workdir, message: { method: "plugin/inventory/changed", params: { extension_inventory: [], application_surfaces: { "ui.application": { plugin_id: "business", generation, provider_id: "ui.application" } } } } } as ServerEvent);
  try {
    publish("/one", "newer-than-initialize");
    resolve({ workspace_root: "/one", extension_inventory: [] } as unknown as InitializeResult);
    await vi.waitFor(() => expect(bootstrap.getSnapshot().initialized?.application_surfaces?.["ui.application"].generation).toBe("newer-than-initialize"));
    publish("/other", "foreign");
    expect(bootstrap.getSnapshot().initialized?.workspace_root).toBe("/one");
    expect(bootstrap.getSnapshot().initialized?.application_surfaces?.["ui.application"].generation).toBe("newer-than-initialize");
    initialized({ phase: "started", requestId: 2 });
    initialized({ phase: "completed", requestId: 2, initialized: { workspace_root: "/other", extension_inventory: [], application_surfaces: { "ui.application": { plugin_id: "business", generation: "new-context", provider_id: "ui.application" } } } as unknown as InitializeResult });
    expect(bootstrap.getSnapshot().initialized?.workspace_root).toBe("/other");
    expect(bootstrap.getSnapshot().initialized?.application_surfaces?.["ui.application"].generation).toBe("new-context");
    const inventory = bootstrap.getSnapshot().initialized?.extension_inventory;
    initialized({ phase: "started", requestId: 3 });
    initialized({ phase: "completed", requestId: 3, initialized: Object.freeze({ ...bootstrap.getSnapshot().initialized, extension_inventory: [] }) as InitializeResult });
    expect(bootstrap.getSnapshot().initialized?.extension_inventory).toBe(inventory);
    initialized({ phase: "completed", requestId: 2, initialized: { workspace_root: "/stale", extension_inventory: [] } as unknown as InitializeResult });
    expect(bootstrap.getSnapshot().initialized?.workspace_root).toBe("/other");
  } finally { stop(); }
});

it("holds restoration across an asynchronous provider registration gap until the exact host generation settles", async () => {
  let restore!: () => Promise<void>;
  window.wuu = { initialize: async () => ({ workspace_root: "/a", extension_inventory: [] }),
    onRuntimeRestore: (handler: typeof restore) => { restore = handler; return () => {}; },
    onServerEvent: () => () => {},
  } as unknown as WuuDesktopApi;
  const bootstrap = new DesktopBootstrap(), stop = bootstrap.start();
  await vi.waitFor(() => expect(bootstrap.getSnapshot().initialized).toBeDefined());
  try {
    let done = false;
    const pending = restore().then(() => { done = true; });
    await vi.waitFor(() => expect(bootstrap.getSnapshot().connectionGeneration).toBe(1));
    await Promise.resolve(); await Promise.resolve();
    expect(done).toBe(false);
    bootstrap.settleRestoration(0);
    await Promise.resolve(); expect(done).toBe(false);
    bootstrap.settleRestoration(1);
    await pending; expect(done).toBe(true);
  } finally { stop(); }
});

it("rejects live graph restoration failure and cancels a stopped bootstrap barrier", async () => {
  let restore!: () => Promise<void>;
  window.wuu = { initialize: async () => ({ workspace_root: "/a", extension_inventory: [] }),
    onRuntimeRestore: (handler: typeof restore) => { restore = handler; return () => {}; },
    onServerEvent: () => () => {},
  } as unknown as WuuDesktopApi;
  const bootstrap = new DesktopBootstrap(), stop = bootstrap.start();
  await vi.waitFor(() => expect(bootstrap.getSnapshot().initialized).toBeDefined());
  const failed = restore(); const rejected = expect(failed).rejects.toThrow("selected application failed");
  await vi.waitFor(() => expect(bootstrap.getSnapshot().connectionGeneration).toBe(1));
  bootstrap.settleRestoration(1, "selected application failed"); await rejected;
  const stopped = restore(); const cancelled = expect(stopped).rejects.toThrow(/stopped/);
  await vi.waitFor(() => expect(bootstrap.getSnapshot().connectionGeneration).toBe(2));
  stop(); await cancelled;
});

it("cancels an older restoration and ignores its late acknowledgement", async () => {
  let restore!: () => Promise<void>;
  window.wuu = { initialize: async () => ({ workspace_root: "/a", extension_inventory: [] }),
    onRuntimeRestore: (handler: typeof restore) => { restore = handler; return () => {}; },
    onServerEvent: () => () => {},
  } as unknown as WuuDesktopApi;
  const bootstrap = new DesktopBootstrap(), stop = bootstrap.start();
  await vi.waitFor(() => expect(bootstrap.getSnapshot().initialized).toBeDefined());
  try {
    const first = restore(); const cancelled = expect(first).rejects.toThrow(/superseded/);
    await vi.waitFor(() => expect(bootstrap.getSnapshot().connectionGeneration).toBe(1));
    let done = false; const second = restore().then(() => { done = true; });
    await cancelled;
    await vi.waitFor(() => expect(bootstrap.getSnapshot().connectionGeneration).toBe(2));
    bootstrap.settleRestoration(1); await Promise.resolve(); expect(done).toBe(false);
    bootstrap.settleRestoration(2); await second; expect(done).toBe(true);
  } finally { stop(); }
});
