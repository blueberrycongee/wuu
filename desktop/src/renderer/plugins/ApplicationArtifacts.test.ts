import { afterEach, describe, expect, expectTypeOf, it, vi } from "vitest";
import { ApplicationArtifacts } from "./ApplicationArtifacts";
import type { ArtifactResourceV1 } from "../../../../packages/plugin-sdk/src/ui-artifacts";
import type { SessionLocatorV1 as SDKSessionLocator } from "../../../../packages/plugin-sdk/src/session-transport";
import type { SessionLocatorV1 as WireSessionLocator } from "../../shared/protocol";
// Both independently consumable public packages describe the same wire value.
expectTypeOf<SDKSessionLocator>().toEqualTypeOf<WireSessionLocator>();
const resource:ArtifactResourceV1={id:"part",name:"report.pdf",mimeType:"application/pdf",text:"original"};
const preview={resolveResourceURL:vi.fn(({source}:{source:string})=>`resolved:${source}`),openFile:vi.fn().mockResolvedValue(undefined)};
afterEach(()=>vi.unstubAllGlobals());
// Boundary failures: late remote result, lease lifetime, native menu admission,
// exact identity, and bounded streaming cleanup.
describe("application artifact resources",()=>{
  it("revokes acquired URLs on abort and explicit release exactly once",async()=>{
    const create=vi.fn().mockReturnValue("blob:owned"),revoke=vi.fn();vi.stubGlobal("URL",class extends URL { static createObjectURL = create; static revokeObjectURL = revoke; });
    const service=new ApplicationArtifacts({},preview);const abort=new AbortController();
    const held=await service.acquireSource(resource,{signal:abort.signal});expect(held.url).toBe("blob:owned");
    abort.abort();service.releaseSource(held.leaseId!);expect(revoke).toHaveBeenCalledExactlyOnceWith("blob:owned");
  });
  it("rejects delayed remote bytes after generation cancellation",async()=>{
    let finish!:(bytes:string)=>void;const read=vi.fn(()=>new Promise<string>(resolve=>{finish=resolve;}));
    const service=new ApplicationArtifacts({readRemoteAttachment:read},preview);const abort=new AbortController();
    const reading=service.readAttachment({remoteRef:"thread:original"},{signal:abort.signal});abort.abort();finish("b3JpZ2luYWw=");
    await expect(reading).rejects.toMatchObject({name:"AbortError"});expect(read).toHaveBeenCalledWith("thread:original");
  });
  it("deduplicates native menus and does not save from a disposed generation's late choice",async()=>{
    let finish!:(result:{action:"save"})=>void;const menu=vi.fn(()=>new Promise<{action:"save"}>(resolve=>{finish=resolve;}));const save=vi.fn();
    const service=new ApplicationArtifacts({showArtifactItemMenu:menu,saveArtifactFile:save},preview);const abort=new AbortController();
    const sha="a".repeat(64);const managed={...resource,text:undefined,threadId:"thread",sha256:sha,uri:`wuu-artifact://workspace/thread/artifact/report.pdf?sha256=${sha}`};
    const opening=service.showItemMenu(managed,{signal:abort.signal});await service.showItemMenu(managed);
    expect(menu).toHaveBeenCalledTimes(1);abort.abort();finish({action:"save"});await expect(opening).rejects.toMatchObject({name:"AbortError"});expect(save).not.toHaveBeenCalled();
  });
  it("rejects a mismatched managed thread/hash before native invocation",async()=>{
    const menu=vi.fn();const service=new ApplicationArtifacts({showArtifactItemMenu:menu},preview);
    await expect(service.showItemMenu({...resource,text:undefined,threadId:"other",sha256:"hash",uri:"wuu-artifact://workspace/thread/artifact/report.pdf?sha256=hash"})).rejects.toThrow();expect(menu).not.toHaveBeenCalled();
  });
  it("saves inline documents from original bytes using the native supported protocol",async()=>{
    const save=vi.fn().mockResolvedValue(undefined);const service=new ApplicationArtifacts({saveArtifactFile:save},preview);
    await service.save({...resource,text:undefined,data:"b3JpZ2luYWw="});expect(save).toHaveBeenCalledWith("report.pdf","data:application/pdf;base64,b3JpZ2luYWw=");
  });
  it("cancels an oversized text stream",async()=>{
    const cancel=vi.fn();vi.stubGlobal("fetch",vi.fn().mockResolvedValue({ok:true,body:{getReader:()=>({read:vi.fn().mockResolvedValue({done:false,value:new Uint8Array(2*1024*1024+1)}),cancel})}}));
    const service=new ApplicationArtifacts({},preview);await expect(service.readText({...resource,text:undefined,uri:"https://example.test/report.txt"})).rejects.toThrow("Preview too large");expect(cancel).toHaveBeenCalledTimes(1);
  });
});

it("keeps remote item identity and rejects a result after cancellation", async () => {
  let finish!: (item: {id: string; type: "tool_call"; result: string}) => void;
  const read = vi.fn(() => new Promise<{id: string; type: "tool_call"; result: string}>(resolve => {finish = resolve;}));
  const service = new ApplicationArtifacts({readRemoteItem: read}, preview);
  const abort = new AbortController();
  const pending = service.readItem({remoteRef: "thread:original/item"}, {signal: abort.signal});
  abort.abort(); finish({id: "original", type: "tool_call", result: "late"});
  await expect(pending).rejects.toMatchObject({name: "AbortError"});
  expect(read).toHaveBeenCalledWith("thread:original/item");
});

it("saves the original managed document instead of its inline preview text", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const acquire = vi.fn(async ({requestId}:{requestId?:string}) => ({url:"wuu-artifact://lease/owned",leaseId:requestId!}));
  const release = vi.fn().mockResolvedValue(undefined);
  const locator: SDKSessionLocator = {version:1,execution_host:"source-host",session_id:"thread",storage_host:"objects"};
  const service = new ApplicationArtifacts({saveArtifactFile: save, acquireArtifactSource: acquire, releaseArtifactSource: release}, preview, () => locator);
  const uri = "cloud:opaque/artifact"; const sha256="a".repeat(64);
  await service.save({...resource, uri, sha256, threadId: "thread", text: "truncated preview"});
  expect(acquire).toHaveBeenCalledWith(expect.objectContaining({uri,threadId:"thread",sha256,locator,requestId:expect.any(String)}));
  expect(save).toHaveBeenCalledWith("report.pdf", "wuu-artifact://lease/owned");
  expect(release).toHaveBeenCalledWith(acquire.mock.calls[0][0].requestId);
});
