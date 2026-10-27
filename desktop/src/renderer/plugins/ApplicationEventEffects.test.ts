import { afterEach, describe, expect, it, vi } from "vitest";
import type { AppState } from "../AppState";
import { initialState } from "../AppState";
import type { ServerEvent, Thread } from "../../shared/protocol";
import { turnTelemetryStore } from "../TurnTelemetryStore";
import { externalAgentActivityStore } from "../ExternalAgentActivityStore";
import { showErrorToast } from "../Toast";
import { ApplicationEventEffects } from "./ApplicationEventEffects";
vi.mock("../Toast",()=>({showErrorToast:vi.fn()}));
function thread(id:string, extras:Partial<Thread>={}):Thread {
  return {id,preview:"",cwd:"/active",model:"model",model_provider:"provider",status:"idle",created_at:"",updated_at:"",turns:[],...extras};
}
function notification(method:string,params:Record<string,unknown>={},workdir="/active"):ServerEvent {return {kind:"notification",workdir,message:{method,params}};}
function fixture() {
  let state:AppState={...initialState,initialized:{} as AppState["initialized"],activeContext:{kind:"no_project",cwd:"/active"},threads:[thread("main"),thread("child",{parent_id:"main"}),thread("temporary",{ephemeral:true})]};
  const telemetry=vi.spyOn(turnTelemetryStore,"ingest").mockImplementation(()=>{});
  const activity=vi.spyOn(externalAgentActivityStore,"ingest").mockImplementation(()=>{});
  const notify=vi.fn().mockResolvedValue(undefined), reportError=vi.fn();
  const scheduleStatusRefresh=vi.fn();
  const effects=new ApplicationEventEffects({getState:()=>state,getEnvironment:()=>({scheduleStatusRefresh}),notify,reportError});
  return {effects,telemetry,activity,notify,reportError,scheduleStatusRefresh,set:(value:Partial<AppState>)=>{state={...state,...value};}};
}
afterEach(()=>{vi.restoreAllMocks();vi.mocked(showErrorToast).mockClear();});

describe("host server event effects",()=>{
  it("ingests background telemetry exactly once and notifies known main completions before runtime filtering",()=>{
    const h=fixture(); const event=notification("turn/completed",{thread_id:"main"},"/other");h.effects.receive(event);
    expect(h.telemetry).toHaveBeenCalledExactlyOnceWith(event);expect(h.activity).toHaveBeenCalledExactlyOnceWith(event);
    expect(h.notify).toHaveBeenCalledTimes(1);expect(h.scheduleStatusRefresh).toHaveBeenCalledWith(600,{signal:expect.any(AbortSignal)});
    h.effects.dispose();
  });
  it("keeps child and ephemeral completions/questions quiet while preserving independent question delivery",()=>{
    const h=fixture();
    for(const id of ["child","temporary","unknown"]){h.effects.receive(notification("turn/completed",{thread_id:id}));}
    for(const id of ["child","temporary"]){h.effects.receive(notification("user-question/requested",{request:{thread_id:id,request_id:"q"}}));}
    h.effects.receive(notification("user-question/requested",{}));expect(h.notify).not.toHaveBeenCalled();
    h.effects.receive(notification("user-question/requested",{request:{thread_id:"background",request_id:"q"}},"/other"));
    expect(h.notify).toHaveBeenCalledTimes(1);h.effects.dispose();
  });
  it("keeps startup and foreign-runtime errors quiet and preserves active toast dedupe keys",()=>{
    const h=fixture();h.set({initialized:undefined});h.effects.receive({kind:"server-error",workdir:"/active",message:"startup"});
    h.set({initialized:{} as AppState["initialized"]});h.effects.receive({kind:"server-error",workdir:"/other",message:"foreign"});
    expect(showErrorToast).not.toHaveBeenCalled();
    h.effects.receive({kind:"server-error",workdir:"/active",message:"error"});
    h.effects.receive({kind:"server-exit",workdir:"/active",code:1,message:"exit"});
    h.effects.receive(notification("config/error",{message:"config"}));h.effects.receive(notification("config/error",{message:42}));
    expect(showErrorToast).toHaveBeenCalledTimes(3);
    expect(showErrorToast).toHaveBeenCalledWith("error",expect.any(String));
    expect(showErrorToast).toHaveBeenCalledWith("exit",expect.any(String),"app-server:exit");
    expect(showErrorToast).toHaveBeenCalledWith("config","","app-server:config-error");h.effects.dispose();
  });
  it("refreshes only eligible terminal events and cancels their scheduled work on disposal",()=>{
    const h=fixture();
    for(const method of ["item/agentMessage/delta","turn/usage","user-question/requested"]){h.effects.receive(notification(method));}
    h.effects.receive(notification("turn/error",{thread_id:"unknown"},"/other"));expect(h.scheduleStatusRefresh).not.toHaveBeenCalled();
    h.effects.receive(notification("turn/error",{thread_id:"main"}));const options=h.scheduleStatusRefresh.mock.calls[0][1];
    expect(options.signal.aborted).toBe(false);h.effects.dispose();expect(options.signal.aborted).toBe(true);
    const calls=[h.telemetry.mock.calls.length,h.activity.mock.calls.length,h.notify.mock.calls.length,h.scheduleStatusRefresh.mock.calls.length];
    h.effects.receive(notification("turn/completed",{thread_id:"main"}));h.effects.receive({kind:"server-error",workdir:"/active",message:"late"});
    expect([h.telemetry.mock.calls.length,h.activity.mock.calls.length,h.notify.mock.calls.length,h.scheduleStatusRefresh.mock.calls.length]).toEqual(calls);
    expect(showErrorToast).not.toHaveBeenCalled();
  });
  it("contains native notification failures without dispatching late diagnostics after disposal",async()=>{
    const h=fixture();h.notify.mockRejectedValueOnce(new Error("native failure"));
    expect(()=>h.effects.receive(notification("turn/completed",{thread_id:"main"}))).not.toThrow();await Promise.resolve();
    expect(h.reportError).toHaveBeenCalledWith(expect.objectContaining({message:"native failure"}));
    let reject!:(error:unknown)=>void;h.notify.mockImplementationOnce(()=>new Promise((_resolve,fail)=>{reject=fail;}));
    h.effects.receive(notification("turn/completed",{thread_id:"main"}));h.effects.dispose();reject(new Error("late"));await Promise.resolve();
    expect(h.reportError).toHaveBeenCalledTimes(1);
  });
});
