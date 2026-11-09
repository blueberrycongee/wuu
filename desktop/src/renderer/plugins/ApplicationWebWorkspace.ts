import type {
  ApplicationWebWorkspaceV1, WebWorkspaceBoundsV1, WebWorkspaceDestinationV1, WebWorkspaceLegacyDestinationV1,
  WebWorkspaceCommandV1, WebWorkspaceFindOptionsV1, WebWorkspaceMenuRequestV1, WebWorkspacePopoverBoundsV1,
  WebWorkspaceTooltipV1, WebWorkspaceDownloadCommandV1, WebWorkspaceStateV1,
} from "../../../../packages/plugin-sdk/src/ui-web-workspace";
import { WEB_WORKSPACE_TAB_LIMIT, WEB_WORKSPACE_ZOOM_MIN, WEB_WORKSPACE_ZOOM_MAX, type WebWorkspaceApi } from "../../shared/webWorkspace";

type Resource = { owner: ApplicationWebWorkspace; borrowed: boolean; closing?: Promise<void>; closed: boolean };
type Shared = {
  resources: Map<string, Resource>;
  tails: Map<string, Promise<unknown>>;
  presentation: Map<string, {bounds?: ApplicationWebWorkspace; shortcuts?: ApplicationWebWorkspace; options?: ApplicationWebWorkspace}>;
};
function immutable<T>(value:T): T {
  const copy=structuredClone(value);
  const freeze=(item:unknown):void=>{if(item && typeof item==="object") {Object.values(item).forEach(freeze);Object.freeze(item);}};
  freeze(copy); return copy;
}

/** Native pages are owned; restored user collections, history and downloads are borrowed. */
export class ApplicationWebWorkspace implements ApplicationWebWorkspaceV1 {
  private disposed=false;
  private disposal?:Promise<void>;
  private readonly children=new Set<ApplicationWebWorkspace>();
  private readonly resources=new Map<string,Resource>();
  private readonly subscriptions=new Set<()=>void>();
  private readonly popovers=new Map<string,string>();
  private readonly hints=new Map<string,string>();
  private readonly initializing=new Set<Promise<unknown>>();
  readonly limits=Object.freeze({tabs:WEB_WORKSPACE_TAB_LIMIT,zoomMin:WEB_WORKSPACE_ZOOM_MIN,zoomMax:WEB_WORKSPACE_ZOOM_MAX});
  readonly capabilities:ApplicationWebWorkspaceV1["capabilities"];
  readonly library:ApplicationWebWorkspaceV1["library"];
  constructor(private readonly api:WebWorkspaceApi|undefined,readonly platform:string,private readonly external:(url:string)=>Promise<unknown>,private readonly shared:Shared={resources:new Map(),tails:new Map(),presentation:new Map()},private readonly parent?:ApplicationWebWorkspace) {
    this.capabilities=Object.freeze({available:!!api,windowInsets:api?.supportsWindowInsets===true,nativeOverlays:!!api});
    this.library=Object.freeze({
      getState:()=>this.run(()=>this.native().library.getState()),
      setBookmark:(url:string,title:string,bookmarked:boolean)=>this.run(()=>this.native().library.setBookmark(url,title,bookmarked)),
      removeHistory:(id:string)=>this.run(()=>this.native().library.removeHistory(id)),
      clearHistory:()=>this.run(()=>this.native().library.clearHistory()),
      onState:(listener:Parameters<ApplicationWebWorkspaceV1["library"]["onState"]>[0])=>this.subscribe(next=>this.native().library.onState(next),listener),
    });
  }
  private check():void {if(this.disposed) throw new DOMException("Web workspace owner disposed","AbortError");}
  private native():WebWorkspaceApi {if(!this.api) throw new Error("Web workspace unavailable"); return this.api;}
  private async run<T>(operation:()=>Promise<T>):Promise<T> {this.check();const result=await operation();this.check();return immutable(result);}
  private queue<T>(key:string,operation:()=>Promise<T>):Promise<T> {
    const before=this.shared.tails.get(key)??Promise.resolve();
    const result=before.then(operation,operation); this.shared.tails.set(key,result);
    void result.finally(()=>{if(this.shared.tails.get(key)===result)this.shared.tails.delete(key);}).catch(()=>{});
    return result;
  }
  private resource(id:string):Resource {const value=this.resources.get(id);if(!value || value.closed) throw new Error("Web workspace destination is not owned by this owner");return value;}
  private observes(id:string):boolean {const value=this.resources.get(id);return !!value && !value.closed;}
  private async mutate<T>(id:string,operation:()=>Promise<T>):Promise<T> {
    this.check();this.resource(id);
    return this.queue(`page:${id}`,async()=>{this.check();this.resource(id);const result=await operation();this.check();return immutable(result);});
  }
  private subscribe<T>(start:(listener:(value:T)=>void)=>()=>void,listener:(value:T)=>void):()=>void {
    this.check();let stopped=false;const off=start(value=>{if(!this.disposed&&!stopped)listener(immutable(value));});
    const stop=()=>{if(stopped)return;stopped=true;this.subscriptions.delete(stop);off();}; this.subscriptions.add(stop);return stop;
  }
  fork():ApplicationWebWorkspace {this.check();const child=new ApplicationWebWorkspace(this.api,this.platform,this.external,this.shared,this);this.children.add(child);return child;}
  async open(destination:WebWorkspaceDestinationV1) {
    this.check();let resource=this.resources.get(destination.id);
    if(!resource || resource.closed) {
      let existing=this.shared.resources.get(destination.id);
      while(existing && !existing.closed) {
        if(!existing.owner.disposed) throw new Error("Web workspace destination has another live owner");
        await existing.owner.close(destination.id);
        this.check();existing=this.shared.resources.get(destination.id);
      }
      resource={owner:this,borrowed:false,closed:false};this.resources.set(destination.id,resource);this.shared.resources.set(destination.id,resource);
    }
    return this.mutate(destination.id,()=>this.native().open({...destination}));
  }
  async initializeTabs(destinations:readonly WebWorkspaceLegacyDestinationV1[]) {
    this.check();
    const pending=this.queue("initialize",async()=>{
      this.check();const state=await this.native().initializeTabs(destinations.map(value=>({...value})));
      this.resources.set(state.id,{owner:this,borrowed:true,closed:false});
      if(this.disposed) {await this.close(state.id);this.check();}
      return immutable(state);
    });
    this.initializing.add(pending);try{return await pending;}finally{this.initializing.delete(pending);}
  }
  async close(id:string):Promise<void> {
    const resource=this.resources.get(id);if(!resource)throw new Error("Web workspace destination is not owned by this owner");
    if(resource.closed)return;if(resource.closing)return resource.closing;
    const pending=this.queue(`page:${id}`,async()=>{
      const presentation=this.shared.presentation.get(id);
      if(!resource.borrowed || presentation?.options===this) await this.native().dismissOptions(id);
      if(!resource.borrowed || presentation?.bounds===this) await this.native().reportBounds(id,null);
      if(!resource.borrowed || presentation?.shortcuts===this) await this.native().setShortcutTarget(id,false);
      if(!resource.borrowed) await this.native().close(id);
      if(presentation){
        for(const key of ["options","bounds","shortcuts"] as const)if(presentation[key]===this)delete presentation[key];
        if(Object.keys(presentation).length===0)this.shared.presentation.delete(id);
      }
      resource.closed=true;if(this.shared.resources.get(id)===resource)this.shared.resources.delete(id);
    });
    resource.closing=pending;
    try {await pending;}catch(error){resource.closing=undefined;throw error;}
  }
  dispose():Promise<void> {
    this.disposed=true; for(const stop of this.subscriptions)stop();
    return this.disposal??=this.cleanup().catch(error=>{this.disposal=undefined;throw error;});
  }
  private async cleanup():Promise<void> {
    // Queue release immediately, before a replacement owner can reuse the ID.
    const pages=[...this.resources.keys()].map(id=>this.close(id));
    const overlays=[...this.popovers.keys()].map(token=>this.reportPopover(token,null));
    const hints=[...this.hints.keys()].map(token=>this.showTooltip(token,null));
    await Promise.all([...pages,...overlays,...hints,...[...this.children].map(child=>child.dispose())]);
    await Promise.allSettled([...this.initializing]);
    await Promise.all([...this.resources.keys()].map(id=>this.close(id)));
    this.parent?.children.delete(this);
  }
  async reportBounds(id:string,bounds:WebWorkspaceBoundsV1|null):Promise<void> {
    if(bounds===null){const resource=this.resources.get(id);if(!resource||resource.closed)return;return this.queue(`page:${id}`,async()=>{
      const current=this.shared.presentation.get(id);if(current?.bounds!==this)return;
      await this.native().reportBounds(id,null);delete current.bounds;
    });}
    return this.mutate(id,()=>{const value=this.shared.presentation.get(id)??{};value.bounds=this;this.shared.presentation.set(id,value);return this.native().reportBounds(id,{...bounds});});
  }
  async setShortcutTarget(id:string,active:boolean):Promise<void> {
    if(!active){const resource=this.resources.get(id);if(!resource||resource.closed)return;return this.queue(`page:${id}`,async()=>{
      const current=this.shared.presentation.get(id);if(current?.shortcuts!==this)return;await this.native().setShortcutTarget(id,false);delete current.shortcuts;
    });}
    return this.mutate(id,()=>{const value=this.shared.presentation.get(id)??{};value.shortcuts=this;this.shared.presentation.set(id,value);return this.native().setShortcutTarget(id,true);});
  }
  async dismissOptions(id:string):Promise<void> {
    const resource=this.resources.get(id);if(!resource||resource.closed)return;
    const current=this.shared.presentation.get(id);if(current?.options!==this)return;
    await this.native().dismissOptions(id);delete current.options;
  }
  async showOptions(id:string,request:WebWorkspaceMenuRequestV1) {
    this.check();this.resource(id);const value=this.shared.presentation.get(id)??{};value.options=this;this.shared.presentation.set(id,value);
    // A native menu waits for dismissal, so it must not hold the mutation queue.
    return this.run(()=>this.native().showOptions(id,{...request,labels:{...request.labels}}));
  }
  private async overlay<T>(tokens:Map<string,string>,token:string,value:T|null,send:(nativeToken:string,value:T|null)=>Promise<void>):Promise<void> {
    if(value!==null)this.check();let nativeToken=tokens.get(token);
    if(!nativeToken){if(value===null)return;nativeToken=crypto.randomUUID();tokens.set(token,nativeToken);}
    const key=nativeToken;
    return this.queue(`overlay:${key}`,async()=>{if(value!==null)this.check();await send(key,value);if(value!==null)this.check();});
  }
  reportPopover(token:string,bounds:WebWorkspacePopoverBoundsV1|null) {return this.overlay(this.popovers,token,bounds,(key,value)=>this.native().reportPopover(key,value));}
  showTooltip(token:string,hint:WebWorkspaceTooltipV1|null) {return this.overlay(this.hints,token,hint,(key,value)=>this.native().showTooltip(key,value));}
  onPopoverDismiss(listener:(token:string)=>void) {return this.subscribe<string>(next=>this.native().onPopoverDismiss(next),token=>{for(const [local,native]of this.popovers)if(native===token)listener(local);});}
  onState(listener:(state:WebWorkspaceStateV1)=>void) {return this.subscribe<WebWorkspaceStateV1>(next=>this.native().onState(next),state=>{if(this.observes(state.id))listener(state);});}
  onReveal(listener:(id:string)=>void) {return this.subscribe(next=>this.native().onReveal(next),listener);}
  onFocusFind(listener:(id:string)=>void) {return this.subscribe<string>(next=>this.native().onFocusFind(next),id=>{if(this.observes(id))listener(id);});}
  onFocusAddress(listener:(id:string,tabId?:string)=>void) {return this.subscribe<[string,string?]>(next=>this.native().onFocusAddress((id,tab)=>next([id,tab])),([id,tab])=>{if(this.observes(id))listener(id,tab);});}
  capture(id:string) {this.resource(id);return this.run(()=>this.native().capture(id));}
  command(id:string,command:WebWorkspaceCommandV1,url?:string) {return this.mutate(id,()=>this.native().command(id,command,url));}
  createTab(id:string,url?:string) {return this.mutate(id,()=>this.native().createTab(id,url));}
  selectTab(id:string,tab:string) {return this.mutate(id,()=>this.native().selectTab(id,tab));}
  closeTab(id:string,tab:string) {return this.mutate(id,()=>this.native().closeTab(id,tab));}
  reopenTab(id:string) {return this.mutate(id,()=>this.native().reopenTab(id));}
  setTabPinned(id:string,tab:string,pinned:boolean) {return this.mutate(id,()=>this.native().setTabPinned(id,tab,pinned));}
  setTabMuted(id:string,tab:string,muted:boolean) {return this.mutate(id,()=>this.native().setTabMuted(id,tab,muted));}
  reorderTabs(id:string,tabs:readonly string[]) {return this.mutate(id,()=>this.native().reorderTabs(id,[...tabs]));}
  find(id:string,text:string,options?:WebWorkspaceFindOptionsV1) {return this.mutate(id,()=>this.native().find(id,text,options));}
  stopFind(id:string) {return this.mutate(id,()=>this.native().stopFind(id));}
  setZoom(id:string,factor:number) {return this.mutate(id,()=>this.native().setZoom(id,factor));}
  listDownloads() {return this.run(()=>this.native().listDownloads());}
  downloadCommand(id:string,command:WebWorkspaceDownloadCommandV1) {return this.run(()=>this.native().downloadCommand(id,command));}
  onDownloads(listener:Parameters<ApplicationWebWorkspaceV1["onDownloads"]>[0]) {return this.subscribe(next=>this.native().onDownloads(next),listener);}
  async openExternal(url:string):Promise<void> {await this.run(()=>this.external(url));}
}
