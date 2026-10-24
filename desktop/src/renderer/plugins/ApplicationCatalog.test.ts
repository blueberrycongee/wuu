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
 it("keeps an onboarding preset as one atomic update without replay after failure", async () => {
  const updates = Object.freeze([{ id: "a", action: "enable" as const }, { id: "b", action: "disable" as const }]);
  vi.mocked(window.wuu.updateExtensionPackage).mockRejectedValue(new Error("outcome unknown"));
  await expect(host.update({ updates })).rejects.toThrow("outcome unknown");
  expect(window.wuu.updateExtensionPackage).toHaveBeenCalledExactlyOnceWith({ updates });
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
 it("queries the captured thread with an immutable source-tagged result and no shared cache", async () => {
  const skills = [{name:"scoped",source:"project",project:{root:"/repo",path:".skills/scoped"},allowed_tools:["read"],user_invocable:true,disable_model_invoke:false}];
  list.mockResolvedValue({skills});
  const input = {threadId:"thread-a",contextKey};
  const pending = host.listSkillsForThread(input); input.threadId="thread-b";
  const result = await pending;
  expect(list).toHaveBeenCalledExactlyOnceWith({thread_id:"thread-a"});
  expect(result).toMatchObject({contextKey:"project:first",threadId:"thread-a"});
  expect(Object.isFrozen(result.skills[0].project)).toBe(true);
  expect(Object.isFrozen(result.skills[0].allowed_tools)).toBe(true);
  skills[0].project.root="/changed"; skills[0].allowed_tools.push("write");
  expect(result.skills[0].project?.root).toBe("/repo");
  expect(result.skills[0].allowed_tools).toEqual(["read"]);
  await host.listSkillsForThread({threadId:"thread-b"});
  expect(list).toHaveBeenLastCalledWith({thread_id:"thread-b"});
  expect(list).toHaveBeenCalledTimes(2);
 });
 it.each([undefined, ""])("uses workspace skills for a draft without a thread (%s)", async threadId => {
  list.mockResolvedValue({skills:[]});
  expect(await host.listSkillsForThread({threadId})).toEqual({contextKey:"project:first",threadId:undefined,skills:[]});
  expect(list).toHaveBeenCalledExactlyOnceWith(undefined);
 });
 it("rejects a mismatched source or pre-aborted request before reaching the host RPC", async () => {
  await expect(host.listSkillsForThread({threadId:"thread-a",contextKey:"project:other"})).rejects.toThrow("workspace changed");
  const abort=new AbortController(); abort.abort();
  await expect(host.listSkillsForThread({threadId:"thread-a"},{signal:abort.signal})).rejects.toMatchObject({name:"AbortError"});
  expect(list).not.toHaveBeenCalled();
 });
 it.each(["workspace", "abort", "generation"])("rejects thread results after %s changes without replay", async change => {
  let finish!: (result: unknown) => void;
  list.mockReturnValue(new Promise(resolve => { finish=resolve; }));
  const abort=new AbortController();
  const pending=host.listSkillsForThread({threadId:"thread-a"},{signal:abort.signal});
  if(change==="workspace") contextKey="project:second";
  if(change==="abort") abort.abort();
  if(change==="generation") active=false;
  finish({skills:[]});
  await expect(pending).rejects.toThrow();
  expect(list).toHaveBeenCalledTimes(1);
 });

});
