import { describe, expect, it, vi } from "vitest";
import type { WebWorkspaceApi, WebWorkspaceState } from "../../shared/webWorkspace";
import { ApplicationWebWorkspace } from "./ApplicationWebWorkspace";
import { scopeApplicationWebWorkspace } from "../../../../packages/plugin-sdk/src/ui-web-workspace";

function deferred<T>() { let resolve!: (value:T)=>void; const promise = new Promise<T>(done=>{resolve=done;}); return {promise,resolve}; }
const state: WebWorkspaceState = {id:"site",url:"https://example.com",title:"Example",loading:false,canGoBack:false,canGoForward:false};
function fixture() {
  const off = vi.fn(); let event:(state:WebWorkspaceState)=>void = ()=>{};
  const api = {
    open:vi.fn(async (input:{id:string})=>({...state,id:input.id})), close:vi.fn(async()=>{}),
    initializeTabs:vi.fn(async()=>({...state,id:"saved-collection"})),
    reportBounds:vi.fn(async()=>{}),setShortcutTarget:vi.fn(async()=>{}),dismissOptions:vi.fn(async()=>{}),stopFind:vi.fn(async()=>state),
    reportPopover:vi.fn<WebWorkspaceApi["reportPopover"]>(async()=>{}),showTooltip:vi.fn(async()=>{}),
    command:vi.fn(async()=>state),onState:vi.fn(handler=>{event=handler;return off;}),
    library:{getState:vi.fn(async()=>({history:[],bookmarks:[]}))},
  };
  const service = new ApplicationWebWorkspace(api as unknown as WebWorkspaceApi,"darwin",vi.fn(async()=>{}));
  return {api,service,off,emit:(value:WebWorkspaceState)=>event(value)};
}

describe("WebWorkspace ownership",()=>{
  it("retains late creation cleanup before another owner reuses the exact destination ID",async()=>{
    const {api,service}=fixture(); const old=service.fork(), next=service.fork(); const pending=deferred<WebWorkspaceState>();
    api.open.mockImplementationOnce(()=>pending.promise);
    const opening=old.open({id:"site",url:state.url}); const rejected=expect(opening).rejects.toThrow(/dispos|cancel/i);
    await Promise.resolve(); const disposed=old.dispose(); const replacement=next.open({id:"site",url:state.url});
    pending.resolve(state); await rejected; await disposed; await replacement;
    expect(api.open).toHaveBeenCalledTimes(2); expect(api.close).toHaveBeenCalledExactlyOnceWith("site");
    expect(api.close.mock.invocationCallOrder[0]).toBeLessThan(api.open.mock.invocationCallOrder[1]);
    await next.dispose(); await service.dispose(); expect(api.close).toHaveBeenCalledTimes(2);
  });
  it("rejects foreign live ownership and preserves exact command identities",async()=>{
    const {api,service}=fixture(); const owner=service.fork(), other=service.fork();
    await owner.open({id:"site",url:state.url,tabs:true});
    await expect(other.open({id:"site",url:state.url})).rejects.toThrow(/owner/);
    await expect(other.command("site","reload")).rejects.toThrow(/own/);
    await expect(other.close("site")).rejects.toThrow(/own/);
    await owner.command("site","navigate","https://example.org");
    expect(api.command).toHaveBeenCalledExactlyOnceWith("site","navigate","https://example.org"); await service.dispose();
  });
  it("preserves the borrowed restored collection and does not hide its replacement presentation",async()=>{
    const {api,service}=fixture(); const old=service.fork(), next=service.fork();
    await old.initializeTabs([]); await next.initializeTabs([]);
    await old.reportBounds("saved-collection",{x:0,y:0,width:10,height:10});
    await next.reportBounds("saved-collection",{x:1,y:1,width:20,height:20});
    api.reportBounds.mockClear(); await old.dispose(); expect(api.reportBounds).not.toHaveBeenCalled();
    await next.dispose(); expect(api.reportBounds).toHaveBeenCalledExactlyOnceWith("saved-collection",null);
    expect(api.close).not.toHaveBeenCalled(); await service.dispose();
  });
  it("clears late geometry, popup and tooltip work and drops late events",async()=>{
    const {api,service,off,emit}=fixture(); const owner=service.fork(); await owner.open({id:"site",url:state.url});
    const callback=vi.fn(); owner.onState(callback); const late=deferred<void>(); api.reportPopover.mockImplementationOnce(()=>late.promise);
    const showing=owner.reportPopover("hint",{x:0,y:0,width:10,height:10,focus:true}); const rejected=expect(showing).rejects.toThrow();
    await owner.showTooltip("hint",{text:"x",x:0,y:0,width:1,height:1,style:{} as never});
    const disposed=owner.dispose(); emit(state); late.resolve(); await rejected; await disposed;
    expect(off).toHaveBeenCalledTimes(1); expect(callback).not.toHaveBeenCalled();
    const token=api.reportPopover.mock.calls[0][0]; expect(token).not.toBe("hint");
    expect(api.reportPopover).toHaveBeenLastCalledWith(token,null);
    expect(api.showTooltip).toHaveBeenLastCalledWith(expect.any(String),null); expect(api.dismissOptions).toHaveBeenCalledWith("site");
    await expect(owner.reportBounds("site",{x:0,y:0,width:1,height:1})).rejects.toThrow();
    await owner.close("site"); expect(api.close).toHaveBeenCalledTimes(1); await service.dispose();
  });
  it("captures the public source owner before generation abort and fences returned snapshots",async()=>{
    const first=fixture(), replacement=fixture(); let source=first.service; const lifetime=new AbortController();
    const scoped=scopeApplicationWebWorkspace(()=>source,lifetime.signal,()=>lifetime.signal.throwIfAborted());
    const result=await scoped.open({id:"site",url:state.url}); expect(Object.isFrozen(result)).toBe(true);
    lifetime.abort(); source=replacement.service; await scoped.dispose();
    expect(first.api.close).toHaveBeenCalledExactlyOnceWith("site"); expect(replacement.api.close).not.toHaveBeenCalled();
    await expect(scoped.command("site","reload")).rejects.toThrow(); await first.service.dispose(); await replacement.service.dispose();
  });
});
