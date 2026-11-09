import * as React from "react";
import { expect, it, vi } from "vitest";
import { ScopedContentRuntime } from "./ScopedContentRuntime";
import type { ExtensionInventoryRecord } from "../../shared/protocol";

it("isolates renderer graphs and revokes a scoped renderer immediately on invalidation", async () => {
  const source = { id: "personal", fingerprint: "personal-v1", graph_generation: "coding-graph", user_space_id: "local", product_domain: "personal" as const, composition_profile: "personal" };
  const provider = { id: "test:ui", kind: "plugin", state: "active", enabled: true, approval_state: "official", fingerprint: "ui-v1", graph_generation: "personal-graph", provenance: { kind: "bundled", source: "builtin", scope: "user", plugin_id: "ui", official: true }, desktop: { entry: "ui.js", content_renderers: [{ category: "tool-result", match: "application/test+json" }] } } as unknown as ExtensionInventoryRecord;
  let invalidated!: (event: { id: string; fingerprint: string; graph_generation: string; scope: string; epoch: number }) => void;
  let inventory = [provider], unblock!: () => void;
  const bridge = { initializePluginScope: vi.fn(async () => inventory), onPluginScopeInvalidated: vi.fn((fn) => { invalidated = fn; return () => {}; }), loadPluginDesktopModule: vi.fn(async () => ({ id: provider.id, fingerprint: provider.fingerprint, url: "fixture" })), reportPluginDesktopActivation: vi.fn(async () => {}), requestPluginRuntime: vi.fn(async () => ({ ok: true as const, value: { result: { value: null } } })), cancelPluginRuntime: vi.fn(async () => {}) };
  const loaded = vi.fn(async () => ({ activateContent(api: any) { api.registerRenderer({ id: "content", category: "tool-result", match: "application/test+json", render: () => React.createElement("p", null, "scoped") }); } }));
  const runtime = new ScopedContentRuntime(source, bridge as never, loaded);
  try {
    await runtime.refresh();
    expect(runtime.controller.getRenderer("tool-result", "application/test+json")).toBeDefined();
    const pending = new Promise<void>(resolve => { unblock = resolve; });
    bridge.initializePluginScope.mockImplementationOnce(async () => { await pending; return []; });
    invalidated({ ...source, scope: JSON.stringify(["local", "personal", "personal"]), epoch: 1 });
    expect(runtime.controller.getRenderer("tool-result", "application/test+json")).toBeUndefined();
    unblock(); await vi.waitFor(() => expect(bridge.initializePluginScope).toHaveBeenCalledTimes(2));
    inventory = [{ ...provider, enabled: false }];
    await runtime.refresh();
    expect(runtime.controller.getRenderer("tool-result", "application/test+json")).toBeUndefined();
  } finally { await runtime.dispose(); }
});
