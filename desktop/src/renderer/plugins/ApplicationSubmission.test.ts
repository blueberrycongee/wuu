import * as composerMessages from "../ComposerMessages";
import { readComposerDraftRecovery } from "../ComposerDraftRecovery";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApplicationSubmission } from "./ApplicationSubmission";
import { initialState, cloneSessionTabDraft, emptyComposerDraft, type AppState } from "../AppState";
import { ApplicationDraftResources } from "./ApplicationDrafts";
import { createComposerPendingState } from "../ComposerPendingState";
import { createSidebarThreadCache } from "../SidebarWorkspaceState";
import type { Thread, ThreadStartParams, WuuDesktopApi } from "../../shared/protocol";

afterEach(() => vi.restoreAllMocks());

function deferred<T>() { let resolve!: (value:T) => void; let reject!: (error:Error) => void;
  const promise = new Promise<T>((yes,no) => { resolve=yes; reject=no; }); return {promise,resolve,reject}; }
const context = {kind:"no_project" as const,cwd:"/fixture"};
const thread = (id="created"):Thread => ({id,cwd:context.cwd,preview:"",status:"idle",model_provider:"fixture",model:"fixture",created_at:"",updated_at:"",turns:[]});
function fixture(existing?:Thread, reconcileOnUpdate = false, engine = "fixture", runtimeParams: Partial<ThreadStartParams> = {}) {
  let state:AppState={...initialState,activeContext:context,initialized:{provider:"fixture",model:"fixture"} as AppState["initialized"],thread:existing,threads:existing?[existing]:[],activeSessionTabID:existing?`thread:${existing.id}`:"draft:a",
    sessionTabs:existing?[{id:`thread:${existing.id}`,kind:"thread",threadID:existing.id,context,title:"Fixture",prompt:"hello",images:[],files:[]}]:[{id:"draft:a",kind:"draft",context,title:"",createdAt:1,prompt:"hello",images:[],files:[]}]};
  const getDraft=(id:string)=>{const tab=state.sessionTabs.find(tab=>tab.id===id);return tab?cloneSessionTabDraft(tab):emptyComposerDraft();};
  const setDraft=(id:string,draft:ReturnType<typeof emptyComposerDraft>)=>{state={...state,sessionTabs:state.sessionTabs.map(tab=>tab.id===id&&tab.kind!=="skills"?{...tab,...draft}:tab)};};
  const pending=createComposerPendingState({getAppState:()=>state,getComposerDraftForThread:getDraft,restoreComposerDraftForThread:setDraft,preserveFailedComposerMessage:vi.fn(),setStatus:vi.fn()});
  const changed=vi.fn();
  const resources=new ApplicationDraftResources(()=>{});
  const submission=new ApplicationSubmission({getState:()=>state,updateState:update=>{state=update(state);if(reconcileOnUpdate) submission.reconcile();},changed,isNavigationPending:()=>false,pending,resources,getDraft,setDraft,
    getRuntime:()=>({pending:false,params:{engine,model:"",effort:"",permission_mode:"unconfined",...runtimeParams}}),sidebarCache:createSidebarThreadCache(()=>[],()=>{})});
  return {submission,pending,changed,resources,getState:()=>state,setState:(next:AppState)=>{state=next;},getDraft};
}

// Escaped contracts: one creation owner, cancellation before/after admission,
// source-bound adoption, ordered queue handoff, and uncertain delivery recovery.
describe("application submission ownership",()=>{
  it.each([false, true])("preserves creation permission policy for project=%s",async(project)=>{
    const created={...thread(),permission_mode:"standard"};
    window.wuu={startThread:vi.fn().mockResolvedValue({thread:created}),startTurn:vi.fn().mockResolvedValue({turn:{id:"accepted",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture(undefined,false,"fixture",{provider:"chosen",model:"chosen-model",effort:"high",speed:"",permission_mode:"read_only",approve_for_me:true});
    const workspace={kind:"project" as const,project_id:"workspace",cwd:"/workspace"};
    const state=f.getState();
    f.setState({...state,activeContext:workspace,initialized:{...state.initialized!,features:{project_agent:true}},sessionTabs:state.sessionTabs.map(tab=>tab.kind==="draft"?{...tab,context:workspace,project:project ? true as const : undefined}:tab)});
    expect(f.submission.submit({ownerTabId:"draft:a"}).accepted).toBe(true);
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations[0]?.phase).toBe("completed"));
    const params=vi.mocked(window.wuu.startThread).mock.calls[0][0]!;
    expect(params).toMatchObject({provider:"chosen",model:"chosen-model",effort:"high"});
    if(project){
      expect(params).toMatchObject({engine:"wuu",workspace_id:"workspace",cwd:"/workspace",project:{name:"hello"}});
      expect(params).not.toHaveProperty("permission_mode");
      expect(params).not.toHaveProperty("approve_for_me");
    }else expect(params).toMatchObject({permission_mode:"read_only",approve_for_me:true});
  });
  it.each([false, true])("checkpoints retained inputs before disposal and fences late settlement (failure=%s)",async(failed)=>{
    const starting=deferred<{turn:Thread["turns"][number]}>();
    window.wuu={startTurn:vi.fn(()=>starting.promise)} as unknown as WuuDesktopApi;
    const f=fixture(thread());
    const release=vi.fn();vi.spyOn(f.resources,"retain").mockReturnValue(release);
    f.submission.submit({ownerTabId:"thread:created"});
    await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalledTimes(1));
    f.setState({...f.getState(),sessionTabs:f.getState().sessionTabs.map(tab=>({...tab,prompt:"newer draft"}))});
    f.submission.dispose();
    const checkpoint=f.getState();
    expect(checkpoint.sessionTabs.flatMap(tab=>tab.kind === "skills" ? [] : [tab.prompt])).toEqual(expect.arrayContaining(["hello","newer draft"]));
    expect(checkpoint.thread?.turns).toEqual([]);
    const persistence=vi.spyOn(Storage.prototype,"setItem");
    f.changed.mockClear();
    if(failed) starting.reject(new Error("admission rejected"));
    else starting.resolve({turn:{id:"accepted",status:"completed",items_view:"full",items:[]}});
    await vi.waitFor(()=>expect(release).toHaveBeenCalledTimes(1));
    expect(f.getState()).toBe(checkpoint);
    expect(persistence).not.toHaveBeenCalled();persistence.mockRestore();
    expect(f.changed).not.toHaveBeenCalled();
    expect(f.submission.getSnapshot().operations).toEqual([]);
    expect(f.submission.submit({ownerTabId:"thread:created",text:"late"}).accepted).toBe(false);
  });
  it.each([false,true])("fences a queue acknowledgement after retirement without resubmitting (failure=%s)",async(failed)=>{
    const queued=deferred<{queued:{id:string}}>();
    window.wuu={queueTurn:vi.fn(()=>queued.promise),startTurn:vi.fn()} as unknown as WuuDesktopApi;
    const f=fixture({...thread(),status:"in_progress",turns:[{id:"running",status:"in_progress",items_view:"full",items:[]}]});
    const release=vi.fn();vi.spyOn(f.resources,"retain").mockReturnValue(release);
    const submitted=f.submission.submit({ownerTabId:"thread:created",mode:"queue"});
    await vi.waitFor(()=>expect(window.wuu.queueTurn).toHaveBeenCalledOnce());
    const messageID=vi.mocked(window.wuu.queueTurn).mock.calls[0][3];
    expect(messageID).toBe(submitted.operationID);
    f.submission.dispose();
    const checkpoint=f.getState();const pending=JSON.stringify(f.pending.pendingComposerMessagesByThread);
    if(failed) queued.reject(new Error("late queue rejection"));else queued.resolve({queued:{id:messageID!}});
    await vi.waitFor(()=>expect(release).toHaveBeenCalledOnce());
    expect(f.getState()).toBe(checkpoint);
    expect(JSON.stringify(f.pending.pendingComposerMessagesByThread)).toBe(pending);
    expect(window.wuu.queueTurn).toHaveBeenCalledOnce();expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(f.submission.submit({ownerTabId:"thread:created",mode:"queue"}).accepted).toBe(false);
  });
  it("leaves an acknowledged queued message with the canonical queue instead of persisting a duplicate draft",async()=>{
    window.wuu={queueTurn:vi.fn().mockImplementation(async(_thread,_text,_images,id)=>({queued:{id}}))} as unknown as WuuDesktopApi;
    const f=fixture({...thread(),status:"in_progress",turns:[{id:"running",status:"in_progress",items_view:"full",items:[]}]});
    f.submission.submit({ownerTabId:"thread:created",mode:"queue"});
    await vi.waitFor(()=>{ const queued=f.pending.pendingComposerMessagesForThread("created").queued;expect(queued).toHaveLength(1);expect(queued[0].operationState).toBeUndefined(); });
    await vi.waitFor(()=>expect(window.wuu.queueTurn).toHaveBeenCalledOnce());
    await Promise.resolve();
    f.submission.dispose();
    expect(f.getDraft("thread:created").prompt).toBe("");
    expect(f.pending.pendingComposerMessagesForThread("created").queued).toHaveLength(1);
    expect(readComposerDraftRecovery().tabs).toEqual([]);
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations).toEqual([]));
    expect(window.wuu.queueTurn).toHaveBeenCalledOnce();
  });
  it("releases retired queue preparation without waiting for an attachment encoder",async()=>{
    const encoding=deferred<[]>();vi.spyOn(composerMessages,"awaitComposerImages").mockImplementationOnce(()=>encoding.promise);
    window.wuu={queueTurn:vi.fn()} as unknown as WuuDesktopApi;
    const f=fixture({...thread(),status:"in_progress",turns:[{id:"running",status:"in_progress",items_view:"full",items:[]}]});
    const release=vi.fn();vi.spyOn(f.resources,"retain").mockReturnValue(release);
    f.submission.submit({ownerTabId:"thread:created",mode:"queue"});
    await vi.waitFor(()=>expect(composerMessages.awaitComposerImages).toHaveBeenCalledOnce());
    f.submission.dispose();const checkpoint=f.getState();
    await vi.waitFor(()=>expect(release).toHaveBeenCalledOnce());
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();expect(f.getState()).toBe(checkpoint);
    encoding.resolve([]);await Promise.resolve();
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
  });
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
  it("retains the creation draft and rejects duplicate sends until admission",async()=>{
    const creating=deferred<{thread:Thread}>();const starting=deferred<{turn:Thread["turns"][number]}>();
    window.wuu={startThread:vi.fn(()=>creating.promise),startTurn:vi.fn(()=>starting.promise),queueTurn:vi.fn()} as unknown as WuuDesktopApi;
    // Native completion acceptance can submit live text before the parent draft catches up.
    const f=fixture();f.submission.submit({ownerTabId:"draft:a",text:"hello accepted suffix"});
    expect(f.submission.submit({ownerTabId:"draft:a"}).accepted).toBe(false);
    expect(f.getDraft("draft:a").prompt).toBe("hello accepted suffix");
    creating.resolve({thread:{...thread(),cwd:"/worktree"}});await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalledTimes(1));
    expect(vi.mocked(window.wuu.startTurn).mock.calls[0][1]).toBe("hello accepted suffix");
    expect(f.getDraft("thread:created").prompt).toBe("hello accepted suffix");
    expect(f.getState().thread?.turns).toEqual([]);
    expect(f.submission.submit({ownerTabId:"thread:created"}).accepted).toBe(false);
    starting.resolve({turn:{id:"first",status:"in_progress",items_view:"full",items:[]}});
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations[0].phase).toBe("completed"));
    expect(f.getDraft("thread:created").prompt).toBe("");
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
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
    preparing.resolve(thread());expect(await sending).toBe(false);
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(f.getState().threads[0].turns.at(-1)?.status).toBe("interrupted");
  });

  it("retires terminal operation receipts on the next submission and when the owner closes",async()=>{
    window.wuu={startTurn:vi.fn().mockResolvedValue({turn:{id:"accepted",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture(thread());const first=f.submission.submit({ownerTabId:"thread:created"});
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations[0].phase).toBe("completed"));
    const second=f.submission.submit({ownerTabId:"thread:created",text:"next"});
    expect(f.submission.getSnapshot().operations.map(operation=>operation.operationID)).not.toContain(first.operationID);
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations.find(operation=>operation.operationID===second.operationID)?.phase).toBe("completed"));
    f.setState({...f.getState(),thread:undefined,threads:[],sessionTabs:[],activeSessionTabID:""});f.submission.reconcile();
    expect(f.submission.getSnapshot().operations).toEqual([]);
  });



  it("keeps the canonical unconfirmed-delivery barrier after a queued RPC times out",async()=>{
    const queued=deferred<Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>>();
    const target={...thread(),status:"in_progress" as const,turns:[{id:"running",status:"in_progress" as const,items_view:"full" as const,items:[]}]};
    window.wuu={queueTurn:vi.fn(()=>queued.promise)} as unknown as WuuDesktopApi;
    const f=fixture(target);f.submission.submit({ownerTabId:"thread:created"});
    await vi.waitFor(()=>expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1));
    f.pending.updateThreadPendingComposerMessages("created",previous=>({...previous,queued:previous.queued.map(message=>({...message,deliveryUnconfirmed:true,operationState:undefined}))}));
    queued.reject(new Error("rpc timeout: turn/queue"));
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations[0].phase).toBe("completed"));
    expect(f.pending.pendingComposerMessagesByThread.created.queued[0].deliveryUnconfirmed).toBe(true);
    expect(f.getDraft("thread:created").prompt).toBe("");expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
  });
  it("submits an independent inline edit without consuming the main draft",async()=>{
    window.wuu={startThread:vi.fn().mockResolvedValue({thread:thread()}),startTurn:vi.fn().mockResolvedValue({turn:{id:"edit",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture();
    expect(await f.submission.submitIndependent("draft:a",{id:"inline",text:"edit selection",images:[],files:[]})).toBe(true);
    expect(f.getDraft("thread:created").prompt).toBe("hello");
    expect(vi.mocked(window.wuu.startTurn).mock.calls[0][1]).toBe("edit selection");
  });
  it("queues background Context Chat behind an admitted turn without touching drafts",async()=>{
    const starting=deferred<{turn:Thread["turns"][number]}>();
    window.wuu={startTurn:vi.fn(()=>starting.promise),queueTurn:vi.fn().mockResolvedValue({queued:{id:"background"}})} as unknown as WuuDesktopApi;
    const f=fixture(thread());const first=f.submission.submitToThread({id:"first",text:"first",images:[],files:[]},thread());
    const second=f.submission.submitBackground({id:"background",text:"context",images:[],files:[]},thread(),context);
    await vi.waitFor(()=>expect(window.wuu.startTurn).toHaveBeenCalled());expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    starting.resolve({turn:{id:"first",status:"in_progress",items_view:"full",items:[]}});
    expect(await first).toBe(true);expect(await second).toBe(true);expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    expect(f.getDraft("thread:created").prompt).toBe("hello");
  });

  it("retains the completion receipt when controller reconciliation runs during draft-to-thread binding",async()=>{
    window.wuu={startThread:vi.fn().mockResolvedValue({thread:thread()}),startTurn:vi.fn().mockResolvedValue({turn:{id:"accepted",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture(undefined,true);const result=f.submission.submit({ownerTabId:"draft:a"});
    await vi.waitFor(()=>expect(f.submission.getSnapshot().operations.find(operation=>operation.operationID===result.operationID)).toMatchObject({phase:"completed",threadId:"created"}));
    expect(f.getState().activeSessionTabID).toBe("thread:created");
    expect(f.submission.getSnapshot().pendingCreations).toHaveLength(0);
    expect(window.wuu.startTurn).toHaveBeenCalledTimes(1);
  });

  it("finishes an admitted background conversation after navigation removes it from loaded state",async()=>{
    window.wuu={startTurn:vi.fn().mockResolvedValue({turn:{id:"background",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture();const current=f.getState();const elsewhere={kind:"no_project" as const,cwd:"/elsewhere"};
    f.setState({...current,activeContext:elsewhere,sessionTabs:current.sessionTabs.map(tab=>({...tab,context:elsewhere}))});
    expect(await f.submission.submitBackground({id:"captured",text:"page context",images:[],files:[]},thread(),context)).toBe(true);
    expect(vi.mocked(window.wuu.startTurn).mock.calls[0][7]).toEqual(context);
    expect(f.getState().activeContext).toEqual(elsewhere);expect(f.getState().activeSessionTabID).toBe("draft:a");
    expect(f.getState().threads).toEqual([]);expect(f.getDraft("draft:a").prompt).toBe("hello");
  });
  it("keeps the explicitly captured background project context when queuing after navigation",async()=>{
    const target={...thread(),status:"in_progress" as const,turns:[{id:"running",status:"in_progress" as const,items_view:"full" as const,items:[]}]};
    window.wuu={queueTurn:vi.fn().mockResolvedValue({queued:{id:"queued"}})} as unknown as WuuDesktopApi;
    const f=fixture(target);const captured={kind:"project" as const,project_id:"source-project",cwd:context.cwd};
    expect(await f.submission.submitBackground({id:"queued",text:"captured",images:[],files:[]},target,captured)).toBe(true);
    expect(vi.mocked(window.wuu.queueTurn).mock.calls[0][8]).toEqual(captured);
  });

  it("rejects an independent native draft edit before creating a thread when no provider is ready",async()=>{
    window.wuu={startThread:vi.fn().mockResolvedValue({thread:thread()}),startTurn:vi.fn().mockResolvedValue({turn:{id:"unexpected",status:"completed",items_view:"full",items:[]}})} as unknown as WuuDesktopApi;
    const f=fixture(undefined,false,"wuu");
    expect(await f.submission.submitIndependent("draft:a",{id:"edit",text:"edit",images:[],files:[]})).toBe(false);
    expect(window.wuu.startThread).not.toHaveBeenCalled();expect(f.getDraft("draft:a").prompt).toBe("hello");
  });

});

it("projects a complete immutable optimistic turn while thread creation is pending", async () => {
  const creating = deferred<{thread:Thread}>();
  window.wuu = {startThread:vi.fn(() => creating.promise), deleteThread:vi.fn().mockResolvedValue({})} as unknown as WuuDesktopApi;
  const f = fixture();
  const state = f.getState();
  const image = {id:"image",media_type:"image/png",data:"b3JpZ2luYWw="};
  f.setState({...state, sessionTabs:state.sessionTabs.map(tab => ({...tab, images:[image]}))});
  const parts = [{type:"pasted_text" as const, text:"original source", title:"Source"}];
  const result = f.submission.submit({ownerTabId:"draft:a", contentParts:parts});
  const pending = f.submission.getSnapshot().pendingCreations[0];
  expect(pending.turn).toMatchObject({status:"in_progress", items_view:"full", started_at:new Date(pending.startedAtMs).toISOString(),
    items:[{type:"user_message",content_parts:parts,images:[{media_type:"image/png",data:"b3JpZ2luYWw="}]}]});
  expect(Object.isFrozen(pending.turn.items[0])).toBe(true);
  expect(Object.isFrozen(pending.turn.items[0].images![0])).toBe(true);
  expect(pending.turn.items[0].images![0]).not.toBe(image);
  f.submission.cancel(result.operationID!); creating.resolve({thread:thread()});
  await vi.waitFor(() => expect(f.submission.getSnapshot().pendingCreations).toHaveLength(0));
});
