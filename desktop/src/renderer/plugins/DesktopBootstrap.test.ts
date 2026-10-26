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
