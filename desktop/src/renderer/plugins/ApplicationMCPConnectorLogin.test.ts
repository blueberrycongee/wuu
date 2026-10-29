import { afterEach, expect, it, vi } from "vitest";
import { ApplicationMCPConnectorLogin } from "./ApplicationMCPConnectorLogin";
import type { WuuDesktopApi } from "../../shared/protocol";

afterEach(() => vi.restoreAllMocks());

it.each(["cancel", "navigation-ABA", "source-change"])("does not dispatch enablement after metadata admission is invalidated (%s)", async outcome => {
  let presentation = "catalog", fingerprint = "original";
  let resolve!: (value: unknown) => void;
  const definition = new Promise(yes => { resolve = yes; });
  window.wuu = {
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ name: "account", plugin_id: "owner", plugin_active: true,
      configurable: true, connector: { title: "Account" }, resource_url: "https://example.test/mcp" }] }),
    readMCPDefinition: vi.fn(() => definition), configureMCPServers: vi.fn(),
  } as unknown as WuuDesktopApi;
  const owner = new ApplicationMCPConnectorLogin({ context: () => "context", presentation: () => presentation,
    inventory: () => [{ id: "owner", kind: "plugin", enabled: true, fingerprint, graph_generation: "graph",
      provenance: { plugin_id: "owner" } }] as never,
    workbench: () => ({ getSnapshot: () => ({ views: [], activeViewByRegion: {} }) }) as never,
  });
  const request = owner.request("account").catch(error => error);
  await vi.waitFor(() => expect(window.wuu.readMCPDefinition).toHaveBeenCalledOnce());
  if (outcome === "cancel") owner.cancel();
  if (outcome === "source-change") fingerprint = "replacement";
  if (outcome === "navigation-ABA") {
    presentation = "conversation"; owner.reconcile();
    presentation = "catalog"; owner.reconcile();
  }
  resolve({ config: { enabled: false }, revision: "original" });
  expect(await request).toBeInstanceOf(Error);
  expect(window.wuu.configureMCPServers).not.toHaveBeenCalled();
  expect(owner.getPending()).toBeUndefined();
});
