import { afterEach, describe, expect, it, vi } from "vitest";
import { RecoveryRetry, recoveryDiagnostics, recoveryLaunchArguments } from "./desktopRecoveryState";

afterEach(() => vi.useRealTimers());

describe("recovery boundary", () => {
  it("exports only bootstrap metadata, never supplied errors, paths, config or content", () => {
    const input = { version: "2026.10.19", platform: "linux", mode: "diagnostic" as const,
      error: "token=secret /home/person/project draft text", config: { api_key: "secret" }, path: "/home/person" };
    const result = JSON.stringify(recoveryDiagnostics(input));
    expect(result).toContain("2026.10.19");
    for (const secret of ["token", "secret", "/home", "draft text", "api_key"]) expect(result).not.toContain(secret);
    expect(recoveryDiagnostics({ ...input, version: "/home/person secret", platform: "private-path" })).toMatchObject({ version: "unknown", platform: "unknown" });
  });

  it("applies launch mode without retaining old safety flags or changing other arguments", () => {
    expect(recoveryLaunchArguments(["app", "--safe-mode", "--inspect=9229"], "normal")).toEqual(["app", "--inspect=9229"]);
    expect(recoveryLaunchArguments(["app", "--safe-mode"], "diagnostic")).toEqual(["app", "--safe-mode"]);
  });

  it("coalesces repeated retry requests and reports load completion, not graph health", async () => {
    let finish!: () => void;
    const run = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const retry = new RecoveryRetry(run, vi.fn());
    retry.start(); retry.start();
    expect(run).toHaveBeenCalledOnce();
    expect(retry.snapshot().phase).toBe("loading");
    finish(); await Promise.resolve();
    expect(retry.snapshot()).toMatchObject({ phase: "loaded", pending: false, attempts: 1 });
    retry.dispose();
  });

  it("cancels waiting without claiming cancellation of the live reload or starting another", async () => {
    let finish!: () => void;
    const run = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const retry = new RecoveryRetry(run, vi.fn());
    retry.start(); retry.cancel(); retry.start();
    expect(run).toHaveBeenCalledOnce();
    expect(retry.snapshot()).toMatchObject({ phase: "cancelled", pending: true });
    finish(); await Promise.resolve();
    expect(retry.snapshot()).toMatchObject({ phase: "cancelled", pending: false });
    retry.dispose();
  });

  it("times out waiting and ignores late success without releasing a still-running attempt", async () => {
    vi.useFakeTimers();
    let finish!: () => void;
    const run = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const retry = new RecoveryRetry(run, vi.fn());
    retry.start();
    await vi.advanceTimersByTimeAsync(30_000);
    retry.start();
    expect(run).toHaveBeenCalledOnce();
    expect(retry.snapshot()).toMatchObject({ phase: "timed-out", pending: true });
    finish(); await Promise.resolve();
    expect(retry.snapshot()).toMatchObject({ phase: "timed-out", pending: false });
    retry.dispose();
  });

  it("does not publish raw rejection details and bounds explicit retry attempts", async () => {
    const run = vi.fn(async () => { throw new Error("/secret/path token=private"); });
    const retry = new RecoveryRetry(run, vi.fn());
    for (let i = 0; i < 5; i++) { retry.start(); await Promise.resolve(); }
    expect(run).toHaveBeenCalledTimes(3);
    expect(retry.snapshot()).toMatchObject({ phase: "failed", attempts: 3, pending: false });
    expect(JSON.stringify(retry.snapshot())).not.toContain("private");
    retry.dispose();
  });
});
