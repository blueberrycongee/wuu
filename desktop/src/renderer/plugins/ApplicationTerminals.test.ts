import { describe, expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../../shared/protocol";
import { ApplicationTerminals } from "./ApplicationTerminals";
import { scopeApplicationTerminals, type ApplicationTerminalsV1, type TerminalEventV1 } from "../../../../packages/plugin-sdk/src/ui-terminals";

function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
const started = { id: "pty-one", cwd: "/worktree", shell: "/bin/zsh", started_at: "2026-01-01" };
function fixture() {
  let listener: (event: TerminalEventV1) => void = () => {};
  const unsubscribe = vi.fn();
  const process = { id: "agent-process", owner_kind: "main_agent", owner_id: "thread", lifecycle: "managed", status: "running" as const, pid: 1, command: "dev", cwd: "/worktree", started_at: "now", updated_at: "now" };
  const api = {
    startTerminalSession: vi.fn().mockResolvedValue(started), stopTerminalSession: vi.fn().mockResolvedValue({ok:true}),
    writeTerminalSession: vi.fn().mockResolvedValue({ok:true}), resizeTerminalSession: vi.fn().mockResolvedValue({ok:true}),
    onTerminalEvent: vi.fn(handler => { listener = handler; return unsubscribe; }),
    listManagedProcesses: vi.fn().mockResolvedValue({processes:[process]}),
    readManagedProcess: vi.fn().mockResolvedValue({process,output:"abc",start_offset:3,end_offset:6,total_bytes:6,truncated:false,timed_out:false}),
    writeManagedProcess: vi.fn().mockResolvedValue({process,bytes_written:3}), resizeManagedProcess: vi.fn().mockResolvedValue({process}), stopManagedProcess: vi.fn().mockResolvedValue({process}),
  };
  const controls = new ApplicationTerminals(api as unknown as WuuDesktopApi);
  return {api,controls,unsubscribe,emit:(event:TerminalEventV1) => listener(event)};
}

describe("application terminal resource ownership", () => {
  it("cleans a native start resolving after disposal once and rejects new operations", async () => {
    const {api,controls} = fixture(); const pending = deferred<typeof started>(); api.startTerminalSession.mockReturnValue(pending.promise);
    const result = controls.start({cwd:"/worktree",cols:80,rows:24});
    const rejected = expect(result).rejects.toThrow(/cancel/i);
    const disposed = controls.dispose(); pending.resolve(started);
    await rejected; await disposed; await controls.stop(started.id);
    expect(api.stopTerminalSession).toHaveBeenCalledExactlyOnceWith(started.id);
    await expect(controls.start({cwd:"/new"})).rejects.toThrow(/cancel/i);
    await expect(controls.write(started.id,"x")).rejects.toThrow(/cancel/i);
    expect(api.startTerminalSession).toHaveBeenCalledExactlyOnceWith({cwd:"/worktree",cols:80,rows:24});
  });
  it("cleans request-aborted startup, live terminals and subscriptions without killing borrowed processes", async () => {
    const {api,controls,unsubscribe,emit} = fixture(); const pending = deferred<typeof started>(); api.startTerminalSession.mockReturnValue(pending.promise);
    const signal = new AbortController(); const result = controls.start({cwd:"/worktree"},{signal:signal.signal}); const rejected = expect(result).rejects.toThrow(/cancel/i);
    const listener = vi.fn(); controls.onEvent(listener); signal.abort(); pending.resolve(started); await rejected;
    await controls.listManaged("thread"); await controls.readManaged({thread_id:"thread",process_id:"agent-process",offset_bytes:3,wait_ms:10000});
    await controls.writeManaged("thread","agent-process","xyz"); await controls.resizeManaged("thread","agent-process",90,30);
    await controls.dispose(); emit({type:"data",id:started.id,text:"late"});
    expect(api.stopTerminalSession).toHaveBeenCalledExactlyOnceWith(started.id); expect(unsubscribe).toHaveBeenCalledTimes(1); expect(listener).not.toHaveBeenCalled();
    expect(api.stopManagedProcess).not.toHaveBeenCalled();
    expect(api.readManagedProcess).toHaveBeenCalledExactlyOnceWith({thread_id:"thread",process_id:"agent-process",offset_bytes:3,wait_ms:10000});
    expect(api.writeManagedProcess).toHaveBeenCalledExactlyOnceWith("thread","agent-process","xyz");
    expect(api.resizeManagedProcess).toHaveBeenCalledExactlyOnceWith("thread","agent-process",90,30);
  });
  it("detaches managed snapshots and retains explicit stop ownership", async () => {
    const {api,controls} = fixture(); const inventory = await controls.listManaged("thread");
    expect(Object.isFrozen(inventory.processes[0])).toBe(true); expect(inventory.processes[0]).not.toBe((await api.listManagedProcesses()).processes[0]);
    await controls.start({cwd:"/worktree"}); await controls.stop(started.id); await controls.dispose(); expect(api.stopTerminalSession).toHaveBeenCalledTimes(1);
    expect(api.stopManagedProcess).not.toHaveBeenCalled();
  });
  it("does not let a UI scope write or resize another scope's PTY", async () => {
    const {controls,api} = fixture(); const lifetime = new AbortController();
    const scoped = scopeApplicationTerminals(() => controls,lifetime.signal,() => {});
    await expect(scoped.write("foreign","input")).rejects.toThrow(/owned/);
    await expect(scoped.resize("foreign",80,24)).rejects.toThrow(/owned/);
    expect(api.writeTerminalSession).not.toHaveBeenCalled(); expect(api.resizeTerminalSession).not.toHaveBeenCalled();
  });
  it("cleans a late resource from its original public provider after generation replacement", async () => {
    const old = fixture(); const next = fixture(); const pending = deferred<typeof started>();
    old.controls.start = vi.fn(() => pending.promise);
    old.controls.stop = vi.fn().mockResolvedValue(undefined);
    let source:ApplicationTerminalsV1 = old.controls;
    const lifetime = new AbortController(); const scoped = scopeApplicationTerminals(() => source,lifetime.signal,() => {if(lifetime.signal.aborted) throw new Error("expired");});
    const result = scoped.start({cwd:"/worktree"}); const rejected = expect(result).rejects.toThrow();
    lifetime.abort(); source = next.controls; await rejected; pending.resolve(started); await vi.waitFor(() => expect(old.controls.stop).toHaveBeenCalledExactlyOnceWith(started.id));
    expect(next.api.stopTerminalSession).not.toHaveBeenCalled(); await expect(scoped.start({cwd:"/new"})).rejects.toThrow();
  });
  it("stops owned PTYs exactly once on generation expiry while preserving managed processes", async () => {
    const {controls,api,unsubscribe,emit} = fixture(); const lifetime = new AbortController();
    const scoped = scopeApplicationTerminals(() => controls,lifetime.signal,() => {if(lifetime.signal.aborted) throw new Error("expired");});
    const listener = vi.fn(); scoped.onEvent(listener); await scoped.start({cwd:"/worktree"}); await scoped.listManaged("thread"); lifetime.abort();
    await scoped.stop(started.id); emit({type:"data",id:started.id,text:"late"});
    expect(api.stopTerminalSession).toHaveBeenCalledTimes(1); expect(api.stopManagedProcess).not.toHaveBeenCalled(); expect(unsubscribe).toHaveBeenCalledTimes(1); expect(listener).not.toHaveBeenCalled();
    await expect(scoped.writeManaged("thread","agent-process","late")).rejects.toThrow(); expect(api.writeManagedProcess).not.toHaveBeenCalled();
  });
});
