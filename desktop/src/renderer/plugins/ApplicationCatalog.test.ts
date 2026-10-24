import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApplicationCatalog } from "./ApplicationCatalog";
import type { ExtensionInventoryRecord } from "../../shared/protocol";

describe("ApplicationCatalog", () => {
 let contextKey: string;
 let active: boolean;
 let inventory: ExtensionInventoryRecord[];
 let host: ApplicationCatalog;
 const install = vi.fn();
 const list = vi.fn();
 beforeEach(() => {
  contextKey = "project:first"; active = true; inventory = [];
  install.mockReset(); list.mockReset();
  window.wuu = { listSkills: list, installPluginPackage: install,
   refreshExtensionCatalog: vi.fn(), readSkillContent: vi.fn(), updateExtensionPackage: vi.fn(),
   removePluginPackage: vi.fn(), loadPluginIcon: vi.fn(),
  } as unknown as typeof window.wuu;
  host = new ApplicationCatalog({ getSource: () => ({ contextKey, inventory }), assertActive: () => { if (!active) throw new Error("generation disposed"); } });
 });
 it("preserves picker cancellation and never replays a mutation", async () => {
  install.mockResolvedValue(undefined);
  expect(await host.install()).toBeUndefined();
  expect(install).toHaveBeenCalledTimes(1);
  install.mockRejectedValue(new Error("outcome unknown"));
  await expect(host.install()).rejects.toThrow("outcome unknown");
  expect(install).toHaveBeenCalledTimes(2);
 });
 it("drops mutation results after workspace changes", async () => {
  let finish!: (result: unknown) => void;
  install.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const pending = host.install(); contextKey = "project:second";
  finish({ skills: [], extension_inventory: [], package: { id: "installed" } });
  expect(await pending).toBeUndefined();
  expect(install).toHaveBeenCalledTimes(1);
 });
 it("rejects stale reads and disposed generations without calling a successor", async () => {
  let finish!: (result: unknown) => void;
  list.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const pending = host.listSkills(); contextKey = "project:second"; finish({skills:[]});
  await expect(pending).rejects.toThrow("workspace changed");
  active = false;
  await expect(host.install()).rejects.toThrow("generation disposed");
  expect(install).not.toHaveBeenCalled();
 });
 it("rejects cancellation after an admitted effect without retrying it", async () => {
  const abort = new AbortController();
  install.mockImplementation(async () => { abort.abort(); return {skills:[],extension_inventory:[]}; });
  await expect(host.install({signal:abort.signal})).rejects.toThrow();
  expect(install).toHaveBeenCalledTimes(1);
 });
 it("publishes an immutable catalog projection and validates icon identity", async () => {
  inventory = [{id:"plugin",name:"Name",kind:"plugin",state:"active",provenance:{kind:"plugin",scope:"user",source:"package"}}];
  const snapshot = host.getSnapshot();
  expect(host.getSnapshot()).toBe(snapshot);
  expect(Object.isFrozen(snapshot.inventory[0].provenance)).toBe(true);
  inventory[0].name = "Changed outside snapshot";
  expect(snapshot.inventory[0].name).toBe("Name");
  vi.mocked(window.wuu.loadPluginIcon).mockResolvedValue({id:"other",fingerprint:"fp",path:"icon.svg",digest:"digest",url:"data:image/svg+xml,test"});
  await expect(host.loadIcon("plugin","fp","icon.svg")).rejects.toThrow("identity mismatch");
 });
});
