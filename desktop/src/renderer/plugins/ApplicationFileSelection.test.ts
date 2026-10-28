import { describe, expect, it, vi } from "vitest";
import { ApplicationFileSelection } from "./ApplicationFileSelection";
import { initialState, emptyComposerDraft, type AppState } from "../AppState";
import { fileSelectionRevision } from "../FileSelectionMapping";
import { readCollapsedPromptParts } from "../ComposerCollapsedPrompt";

const context={kind:"no_project" as const,cwd:"/workspace"};
const source={workspace:context.cwd,path:"note.md",revision:fileSelectionRevision("original"),quote:"original",start_line:1,start_column:1,end_line:1,end_column:9};
function fixture() {
  let state:AppState={...initialState,activeContext:context,initialized:{} as AppState["initialized"],activeSessionTabID:"draft:selection",sessionTabs:[{id:"draft:selection",kind:"draft",context,title:"",createdAt:1,...emptyComposerDraft(),prompt:"question"}]};
  let draft={...emptyComposerDraft(),prompt:"question"};
  const submitIndependent=vi.fn().mockResolvedValue(true); const preview=vi.fn().mockResolvedValue(undefined);
  const readFile=vi.fn().mockResolvedValue({root:context.cwd,path:source.path,absolute_path:"/workspace/note.md",size_bytes:8,mtime_ms:1,sha256:"digest",binary:false,truncated:false,text:"original"});
  const service=new ApplicationFileSelection({getState:()=>state,subscribeState:()=>()=>{},isNavigationPending:()=>false,getDraft:()=>draft,setDraft:(_id,value)=>{draft=value;},submission:{submitIndependent,getSnapshot:()=>({operations:[]})},preview:{invoke:preview},previewContent:{readFile},side:{available:()=>true,open:vi.fn().mockResolvedValue(true)}});
  return {service,target:{ownerTabId:"draft:selection"},getDraft:()=>draft,submitIndependent,preview,readFile,setState:(next:AppState)=>{state=next;},getState:()=>state};
}
// Contracts: canonical metadata, owner/source fencing across async reads,
// independent edits, and immutable PDF source identity.
describe("public file selection owner",()=>{
  it("keeps canonical comments and typed text intact through edits/removal",()=>{
    const f=fixture();f.service.comment({target:f.target,source,comment:"explain",id:"comment"});
    expect(f.service.getSnapshot(f.target).comments[0].comment).toBe("explain");
    expect(readCollapsedPromptParts(f.target.ownerTabId,f.getDraft().prompt)?.at(-1)).toEqual({type:"text",text:"question"});
    f.service.comment({target:f.target,source,comment:"updated",id:"comment"});
    expect(f.service.getSnapshot(f.target).comments).toHaveLength(1);
    f.service.removeComment({target:f.target,id:"comment"});expect(f.getDraft().prompt).toBe("question");
  });
  it("rejects stale text revisions and never consumes the main draft for inline edits",async()=>{
    const f=fixture();expect(await f.service.edit({target:f.target,source:{...source,revision:"old"},instruction:"rewrite"})).toBe(false);
    expect(f.submitIndependent).not.toHaveBeenCalled();
    expect(await f.service.edit({target:f.target,source,instruction:"rewrite"})).toBe(true);
    expect(f.submitIndependent.mock.calls[0][1]).toMatchObject({activeDocument:{path:source.path},contentParts:[{type:"file_selection",intent:"edit",source}]});
    expect(f.getDraft().prompt).toBe("question");
  });
  it("fences an edit whose owner changes while its source is read",async()=>{
    const f=fixture();let finish!: (value:unknown)=>void;f.readFile.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
    const editing=f.service.edit({target:f.target,source,instruction:"rewrite"});
    f.setState({...f.getState(),activeSessionTabID:"draft:other"});finish({text:"original"});
    expect(await editing).toBe(false);expect(f.submitIndependent).not.toHaveBeenCalled();
  });
  it("keeps exact PDF artifact ownership and digest when reopening",async()=>{
    const f=fixture();const sha="a".repeat(64);const uri=`wuu-artifact://workspace/thread/artifact/report.pdf?sha256=${sha}`;
    const thread={id:"thread",cwd:context.cwd,turns:[],status:"idle",model_provider:"",model:"",preview:"",created_at:"",updated_at:""} as const;
    f.setState({...f.getState(),thread:{...thread,turns:[]},threads:[{...thread,turns:[]}],activeSessionTabID:"thread:thread",sessionTabs:[{id:"thread:thread",kind:"thread",threadID:"thread",context,title:"",...emptyComposerDraft()}]});
    const target={ownerTabId:"thread:thread"};const pdf={...source,revision:`sha256:${sha}`,pdf:{start_page:2,end_page:3,artifact_uri:uri,artifact_sha256:sha,artifact_thread_id:"thread"}};
    expect(await f.service.openPdf({target,source:pdf})).toBe(true);
    expect(f.preview).toHaveBeenCalledWith("preview.open-artifact",{threadId:"thread",artifactId:uri,sha256:sha,page:2},undefined);
    expect(await f.service.openPdf({target,source:{...pdf,revision:"different"}})).toBe(false);
    expect(f.preview).toHaveBeenCalledTimes(1);
  });
});

it("keeps disabled snapshots stable for startup, unknown and removed owners", () => {
  const f = fixture();
  const startup = {ownerTabId:""}, unknown = {ownerTabId:"unknown"};
  const firstStartup = f.service.getSnapshot(startup);
  const firstUnknown = f.service.getSnapshot(unknown);
  expect(firstStartup.enabled).toBe(false);
  expect(f.service.getSnapshot(startup)).toBe(firstStartup);
  expect(f.service.getSnapshot(unknown)).toBe(firstUnknown);
  const enabled = f.service.getSnapshot(f.target);
  expect(enabled.enabled).toBe(true);
  f.setState({...f.getState(), sessionTabs:[]});
  const removed = f.service.getSnapshot(f.target);
  expect(removed.enabled).toBe(false);
  expect(removed).not.toBe(enabled);
  expect(f.service.getSnapshot(startup)).toBe(firstStartup);
  expect(f.service.getSnapshot(f.target)).toBe(removed);
});
