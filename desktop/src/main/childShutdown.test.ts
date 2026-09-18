import { EventEmitter } from "node:events";
import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it, vi } from "vitest";
import { shutdownChild } from "./childShutdown";

afterEach(() => vi.useRealTimers());
function child() {
  return Object.assign(new EventEmitter(), { exitCode: null, signalCode: null, kill: vi.fn() }) as unknown as ChildProcess;
}
describe("shutdownChild", () => {
  it("waits for process exit and cancels escalation after a graceful shutdown", async () => {
    vi.useFakeTimers();
    const process = child();
    const stop = vi.fn();
    const done = shutdownChild(process, stop);
    expect(stop).toHaveBeenCalledOnce();
    process.emit("exit", 0);
    await done;
    await vi.runAllTimersAsync();
    expect(process.kill).not.toHaveBeenCalled();
  });
  it("terminates and reaps a core that ignores the shutdown request", async () => {
    vi.useFakeTimers();
    const process = child();
    const done = shutdownChild(process, () => {});
    await vi.advanceTimersByTimeAsync(20_000);
    expect(process.kill).toHaveBeenCalledWith("SIGTERM");
    await vi.advanceTimersByTimeAsync(3_000);
    expect(process.kill).toHaveBeenLastCalledWith("SIGKILL");
    process.emit("exit", null, "SIGKILL");
    await done;
    expect(vi.getTimerCount()).toBe(0);
  });
});
