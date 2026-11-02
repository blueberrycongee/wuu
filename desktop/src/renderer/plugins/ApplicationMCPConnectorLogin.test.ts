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

it.each(["navigation", "context", "source", "removed", "disabled"])("revokes an attached native continuation on real owner change (%s)", async change => {
  let context = "context", presentation = "catalog", fingerprint = "original", enabled = true, present = true;
  const server = { name: "account", plugin_id: "owner", plugin_active: true, configuration_enabled: true,
    configurable: true, connector: { title: "Account" }, resource_url: "https://example.test/mcp" };
  window.wuu = {
    listMCPServers: vi.fn().mockResolvedValue({ servers: [server] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: { enabled: true }, revision: "original" }),
    startMCPLogin: vi.fn().mockResolvedValue({ login_id: "native-attempt" }),
    pollMCPLogin: vi.fn().mockResolvedValue({ status: "pending" }),
    cancelMCPLogin: vi.fn().mockResolvedValue({ ok: true }),
  } as unknown as WuuDesktopApi;
  const owner = new ApplicationMCPConnectorLogin({ context: () => context, presentation: () => presentation,
    inventory: () => present ? [{ id: "owner", kind: "plugin", enabled, fingerprint, graph_generation: "graph",
      provenance: { plugin_id: "owner" } }] as never : [],
    workbench: () => ({ getSnapshot: () => ({ views: [], activeViewByRegion: {} }) }) as never,
  });
  try {
    await owner.request("account"); await owner.take(); await owner.start();
    expect(owner.getPending()).toBe("account");
    expect(window.wuu.pollMCPLogin).not.toHaveBeenCalled();
    if (change === "navigation") presentation = "conversation";
    if (change === "context") context = "another context";
    if (change === "source") fingerprint = "replacement";
    if (change === "removed") present = false;
    if (change === "disabled") enabled = false;
    owner.reconcile();
    expect(owner.getPending()).toBeUndefined();
    expect(window.wuu.cancelMCPLogin).toHaveBeenCalledExactlyOnceWith("native-attempt");
    expect(owner.checkpoint()).toBeUndefined();
  } finally { owner.cancel(); }
});

it("requires explicit retry after a polling transport error and never repeats a terminal poll", async () => {
  const server = { name: "account", plugin_id: "owner", plugin_active: true, configuration_enabled: true,
    configurable: true, connector: { title: "Account" }, resource_url: "https://example.test/mcp" };
  window.wuu = {
    listMCPServers: vi.fn().mockResolvedValue({ servers: [server] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: { enabled: true }, revision: "original" }),
    startMCPLogin: vi.fn().mockResolvedValue({ login_id: "native-attempt" }),
    pollMCPLogin: vi.fn().mockRejectedValueOnce(new Error("transport unavailable")).mockResolvedValue({ status: "success" }),
    cancelMCPLogin: vi.fn().mockResolvedValue({ ok: true }),
  } as unknown as WuuDesktopApi;
  const owner = new ApplicationMCPConnectorLogin({ context: () => "context", presentation: () => "catalog",
    inventory: () => [{ id: "owner", kind: "plugin", enabled: true, fingerprint: "original", graph_generation: "graph",
      provenance: { plugin_id: "owner" } }] as never,
    workbench: () => ({ getSnapshot: () => ({ views: [], activeViewByRegion: {} }) }) as never,
  });
  try {
    await owner.request("account"); await owner.take(); await owner.start();
    expect(await owner.poll()).toEqual({ status: "error", error: "transport unavailable" });
    expect(await owner.poll()).toEqual({ status: "error", error: "transport unavailable" });
    expect(window.wuu.pollMCPLogin).toHaveBeenCalledOnce();
    await owner.retry();
    expect(window.wuu.startMCPLogin).toHaveBeenCalledTimes(2);
    expect(await owner.poll()).toEqual({ status: "success" });
    expect(await owner.poll()).toEqual({ status: "success" });
    expect(window.wuu.pollMCPLogin).toHaveBeenCalledTimes(2);
  } finally { owner.cancel(); }
});

it.each(["manual", "unsupported-native", "invalid-redirect"])("does not start or checkpoint automatic auth for %s", async mode => {
  const server = { name: "account", plugin_id: "owner", plugin_active: true, configuration_enabled: true,
    configurable: true, connector: { title: "Account" }, resource_url: "https://example.test/mcp",
    ...(mode === "manual" ? { oauth_redirect_uri: "https://example.test/callback" } : mode === "invalid-redirect" ? { oauth_redirect_uri: "not a URL" } : {}) };
  window.wuu = {
    ...(mode === "unsupported-native" ? { unsupportedMethods: ["startMCPLogin"] } : {}),
    listMCPServers: vi.fn().mockResolvedValue({ servers: [server] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: { enabled: true }, revision: "original" }),
    startMCPLogin: vi.fn().mockRejectedValue(new Error("native stub unsupported")),
  } as unknown as WuuDesktopApi;
  const owner = new ApplicationMCPConnectorLogin({ context: () => "context", presentation: () => "catalog",
    inventory: () => [{ id: "owner", kind: "plugin", enabled: true, fingerprint: "original", graph_generation: "graph",
      provenance: { plugin_id: "owner" } }] as never,
    workbench: () => ({ getSnapshot: () => ({ views: [], activeViewByRegion: {} }) }) as never,
  });
  try {
    await owner.request("account");
    expect(await owner.take()).toEqual(server);
    expect(window.wuu.startMCPLogin).not.toHaveBeenCalled();
    expect(owner.checkpoint()).toBeUndefined();
  } finally { owner.cancel(); }
});

it("retains a synchronous native-start failure for explicit retry instead of losing the continuation", async () => {
  const server = { name: "account", plugin_id: "owner", plugin_active: true, configuration_enabled: true,
    configurable: true, connector: { title: "Account" }, resource_url: "https://example.test/mcp" };
  window.wuu = {
    listMCPServers: vi.fn().mockResolvedValue({ servers: [server] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: { enabled: true }, revision: "original" }),
    startMCPLogin: vi.fn().mockImplementationOnce(() => { throw new Error("IPC unavailable"); }).mockResolvedValue({ login_id: "native-attempt" }),
    cancelMCPLogin: vi.fn().mockResolvedValue({ ok: true }),
  } as unknown as WuuDesktopApi;
  const owner = new ApplicationMCPConnectorLogin({ context: () => "context", presentation: () => "catalog",
    inventory: () => [{ id: "owner", kind: "plugin", enabled: true, fingerprint: "original", graph_generation: "graph",
      provenance: { plugin_id: "owner" } }] as never,
    workbench: () => ({ getSnapshot: () => ({ views: [], activeViewByRegion: {} }) }) as never,
  });
  try {
    await owner.request("account");
    expect(await owner.take()).toEqual(server);
    await expect(owner.start()).rejects.toThrow("IPC unavailable");
    expect(owner.getPending()).toBe("account");
    await owner.retry();
    expect(await owner.start()).toEqual({ login_id: "native-attempt" });
    expect(window.wuu.startMCPLogin).toHaveBeenCalledTimes(2);
  } finally { owner.cancel(); }
});
