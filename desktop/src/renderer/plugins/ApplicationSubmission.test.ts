import { describe, expect, it, vi } from "vitest";
import { ApplicationSubmission } from "./ApplicationSubmission";
import { initialState, cloneSessionTabDraft, emptyComposerDraft, type AppState } from "../AppState";
import { ApplicationDraftResources } from "./ApplicationDrafts";
import { createComposerPendingState } from "../ComposerPendingState";
import { createSidebarThreadCache } from "../SidebarWorkspaceState";
import type { Thread, WuuDesktopApi } from "../../shared/protocol";

function deferred<T>() { let resolve!: (value:T) => void; let reject!: (error:Error) => void;
  const promise = new Promise<T>((yes,no) => { resolve=yes; reject=no; }); return {promise,resolve,reject}; }
const context = {kind:"no_project" as const,cwd:"/fixture"};
const thread = (id="created"):Thread => ({id,cwd:context.cwd,preview:"",status:"idle",model_provider:"fixture",model:"fixture",created_at:"",updated_at:"",turns:[]});
function fixture(existing?:Thread) {
  let state:AppState={...initialState,activeContext:context,initialized:{provider:"fixture",model:"fixture"} as AppState["initialized"],thread:existing,threads:existing?[existing]:[],activeSessionTabID:existing?`thread:${existing.id}`:"draft:a",
    sessionTabs:existing?[{id:`thread:${existing.id}`,kind:"thread",threadID:existing.id,context,title:"Fixture",prompt:"hello",images:[],files:[]}]:[{id:"draft:a",kind:"draft",context,title:"",createdAt:1,prompt:"hello",images:[],files:[]}]};
  const getDraft=(id:string)=>{const tab=state.sessionTabs.find(tab=>tab.id===id);return tab?cloneSessionTabDraft(tab):emptyComposerDraft();};
  const setDraft=(id:string,draft:ReturnType<typeof emptyComposerDraft>)=>{state={...state,sessionTabs:state.sessionTabs.map(tab=>tab.id===id&&tab.kind!=="skills"?{...tab,...draft}:tab)};};
  const pending=createComposerPendingState({getAppState:()=>state,getComposerDraftForThread:getDraft,restoreComposerDraftForThread:setDraft,preserveFailedComposerMessage:vi.fn(),setStatus:vi.fn()});
  const submission=new ApplicationSubmission({getState:()=>state,updateState:update=>{state=update(state);},changed:vi.fn(),isNavigationPending:()=>false,pending,resources:new ApplicationDraftResources(()=>{}),getDraft,setDraft,
    getRuntime:()=>({pending:false,params:{engine:"fixture",model:"",effort:"",permission_mode:"unconfined"}}),sidebarCache:createSidebarThreadCache(()=>[],()=>{})});
  return {submission,pending,getState:()=>state,setState:(next:AppState)=>{state=next;},getDraft};
}

// Escaped contracts: one creation owner, cancellation before/after admission,
// source-bound adoption, ordered queue handoff, and uncertain delivery recovery.
describe("application submission ownership",()=>{
  it("publishes one optimistic creation at click time and deletes a late empty thread after cancellation",async()=>{
    const creating=deferred<{thread:Thread}>();const remove=vi.fn().mockResolvedValue({});
    window.wuu={startThread:vi.fn(()=>creating.promise),deleteThread:remove,startTurn:vi.fn()} as unknown as WuuDesktopApi;
    const f=fixture();const before=Date.now();const result=f.submission.submit({ownerTabId:"draft:a"});
    expect(result.accepted).toBe(true);expect(f.submission.getPendingDraftTabIds()).toEqual(["draft:a"]);
    expect(f.submission.getSnapshot().pendingCreations).toHaveLength(1);
    expect(f.submission.getSnapshot().pendingCreations[0].startedAtMs).toBeGreaterThanOrEqual(before);
    expect(f.submission.cancel(result.operationID!)).toBe(true);
    await vi.waitFor(()=>expect(f.submission.getSnapshot().pendingCreations).toHaveLength(0));
    creating.resolve({thread:thread()});
    await vi.waitFor(()=>expect(remove).toHaveBeenCalledWith("created"));
    expect(window.wuu.startTurn).not.toHaveBeenCalled();expect(f.getState().threads).toHaveLength(0);
    expect(f.getDraft("draft:a").prompt).toBe("hello");
  });
  it("binds a completed creation to the source without replacing the newly selected draft",async()=>{
    const creating=deferred<{thread:Thread}>();
    window.wuu={startThread:vi.fn(()=>creating.promise),startTurn:vi.fn().mockResolvedValue({turn:{id:"accepted",status:"completed",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture();f.submission.submit({ownerTabId:"draft:a"});
    const state=f.getState();f.setState({...state,activeSessionTabID:"draft:b",sessionTabs:[...state.sessionTabs,{id:"draft:b",kind:"draft",context,title:"",createdAt:2,prompt:"new work",images:[],files:[]}]});
    creating.resolve({thread:thread()});await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalledTimes(1));
    expect(f.getState().activeSessionTabID).toBe("draft:b");expect(f.getDraft("draft:b").prompt).toBe("new work");
    expect(f.getState().sessionTabs.some(tab=>tab.id==="thread:created")).toBe(true);
  });
  it("migrates queued sends behind a first creation through the same pending owner",async()=>{
    const creating=deferred<{thread:Thread}>();const starting=deferred<{turn:Thread["turns"][number]}>();
    window.wuu={startThread:vi.fn(()=>creating.promise),startTurn:vi.fn(()=>starting.promise),queueTurn:vi.fn().mockResolvedValue({queued:{id:"queued"}})} as unknown as WuuDesktopApi;
    const f=fixture();f.submission.submit({ownerTabId:"draft:a"});f.submission.submit({ownerTabId:"draft:a",text:"second"});
    expect(f.pending.pendingComposerMessagesByThread["draft:a"].queued).toHaveLength(1);
    creating.resolve({thread:{...thread(),cwd:"/worktree"}});await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalled());
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();starting.resolve({turn:{id:"first",status:"in_progress",items_view:"full",items:[]}});
    await vi.waitFor(()=>expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1));
    expect(f.pending.pendingComposerMessagesByThread.created.queued[0].id).toBe("queued");
    expect(vi.mocked(window.wuu.queueTurn).mock.calls[0][8]).toMatchObject({cwd:"/worktree"});
  });
  it("does not replay an uncertain turn admission as a queue send and restores input",async()=>{
    window.wuu={startTurn:vi.fn().mockRejectedValue(new Error("request timed out")),queueTurn:vi.fn()} as unknown as WuuDesktopApi;
    const f=fixture(thread());f.submission.submit({ownerTabId:"thread:created"});
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations[0].phase).toBe("failed"));
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();expect(f.getDraft("thread:created").prompt).toBe("hello");
  });
  it("stops an accepted turn when cancellation arrives while startTurn is pending",async()=>{
    const starting=deferred<{turn:Thread["turns"][number]}>();
    window.wuu={startTurn:vi.fn(()=>starting.promise),interruptTurn:vi.fn().mockResolvedValue({ok:true})} as unknown as WuuDesktopApi;
    const f=fixture(thread());const result=f.submission.submit({ownerTabId:"thread:created"});
    await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalled());
    f.submission.cancel(result.operationID!);expect(window.wuu.interruptTurn).not.toHaveBeenCalled();
    starting.resolve({turn:{id:"accepted",status:"in_progress",items_view:"full",items:[]}});
    await vi.waitFor(()=>expect(window.wuu.interruptTurn).toHaveBeenCalledWith("created"));
  });
  it("routes question steer and interrupt through the same canonical pending owner",async()=>{
    const target={...thread(),status:"in_progress" as const,turns:[{id:"running",status:"in_progress" as const,items_view:"full" as const,items:[]}]};
    window.wuu={steerTurn:vi.fn().mockResolvedValue({}),interruptTurn:vi.fn().mockResolvedValue({ok:true})} as unknown as WuuDesktopApi;
    const f=fixture(target);
    expect(await f.submission.steerToThread({id:"answer",text:"answer",images:[],files:[]},target)).toBe(true);
    expect(f.pending.pendingComposerMessagesByThread.created.guides[0].id).toBe("answer");
    await f.submission.interruptThread("created");expect(window.wuu.interruptTurn).toHaveBeenCalledWith("created");
  });
  it("keeps an authoritative history edit result when Stop cancels its preparation",async()=>{
    const preparing=deferred<Thread>();
    window.wuu={startTurn:vi.fn(),interruptTurn:vi.fn()} as unknown as WuuDesktopApi;
    const f=fixture(thread());
    const sending=f.submission.submitToThread({id:"edited",text:"edited",images:[],files:[]},thread(),()=>preparing.promise);
    await f.submission.stop("thread:created");
    preparing.resolve(thread());expect(await sending).toBe(true);
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(f.getState().threads[0].turns.at(-1)?.status).toBe("interrupted");
  });

});
