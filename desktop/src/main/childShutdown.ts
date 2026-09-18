import type { ChildProcess } from "node:child_process";

// Wait for exit, not pipe closure: descendants may still hold inherited pipes.
export function shutdownChild(child: ChildProcess, requestStop: () => void, graceMs = 20_000): Promise<void> {
  if (child.exitCode != null || child.signalCode != null) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timers: ReturnType<typeof setTimeout>[] = [];
    const finish = (error?: Error) => {
      for (const timer of timers) clearTimeout(timer);
      child.removeListener("exit", exited);
      child.removeListener("error", failed);
      if (error) reject(error); else resolve();
    };
    const exited = () => finish();
    const failed = (error: Error) => finish(error);
    child.once("exit", exited);
    child.once("error", failed);
    for (const [delay, signal] of [[graceMs, "SIGTERM"], [graceMs + 3_000, "SIGKILL"]] as const) {
      const timer = setTimeout(() => { child.kill(signal); }, delay);
      timer.unref();
      timers.push(timer);
    }
    const timeout = setTimeout(() => finish(new Error("Child process did not exit after shutdown")), graceMs + 5_000);
    timeout.unref();
    timers.push(timeout);
    try { requestStop(); } catch { child.kill("SIGTERM"); }
  });
}
