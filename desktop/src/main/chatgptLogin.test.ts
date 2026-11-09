import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AuthChatGPTLoginResult, RuntimeContext } from "../shared/protocol";
import { ChatGPTLoginCoordinator } from "./chatgptLogin";
import { AppServerResponseError } from "./appServerErrors";

class Owner extends EventEmitter {
  destroyed = false;
  constructor(readonly id: number) { super(); }
  isDestroyed() { return this.destroyed; }
  destroy() { this.destroyed = true; this.emit("destroyed"); }
}
const context: RuntimeContext = { kind: "project", project_id: "one", cwd: "/fixture/one" };
const otherContext: RuntimeContext = { kind: "project", project_id: "two", cwd: "/fixture/two" };
const pending: AuthChatGPTLoginResult = { login_id: "fixture", status: "pending", expires_in: 900, interval_ms: 1000, authorization_url: "https://auth.openai.com/api/accounts/authorize?state=fixture" };
afterEach(() => { vi.useRealTimers(); });

describe("ChatGPT login ownership", () => {
  it("opens only an official authorization URL and never exposes the URL to renderer", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValue(pending);
    const open = vi.fn().mockResolvedValue(undefined);
    const flow = new ChatGPTLoginCoordinator(request, open);
    const result = await flow.start(new Owner(1), context, "registered");
    expect(request).toHaveBeenCalledWith(context, "auth/chatgpt/login/start", { account_id: "registered" });
    expect(open).toHaveBeenCalledWith(pending.authorization_url);
    expect(result).not.toHaveProperty("authorization_url");
  });
  it("cancels when opening browser fails or authorization origin is invalid", async () => {
    vi.useFakeTimers();
    for (const url of [pending.authorization_url, "https://attacker.invalid/authorize"]) {
      const request = vi.fn().mockResolvedValueOnce({ ...pending, authorization_url: url }).mockResolvedValue({ ok: true });
      const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockRejectedValue(new Error("browser unavailable")));
      await expect(flow.start(new Owner(1), context)).rejects.toThrow();
      expect(request).toHaveBeenLastCalledWith(context, "auth/chatgpt/login/cancel", { login_id: "fixture" });
    }
  });
  it("rejects another window and keeps polling/cancellation on the original context", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValue(pending);
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    await flow.start(new Owner(1), context);
    await expect(flow.start(new Owner(2), otherContext)).rejects.toThrow("already running");
    await expect(flow.poll(2, "fixture")).rejects.toThrow("not found");
    await flow.poll(1, "fixture");
    expect(request).toHaveBeenLastCalledWith(context, "auth/chatgpt/login/poll", { login_id: "fixture" });
    request.mockResolvedValueOnce({ ok: true });
    await flow.cancel(1, "fixture");
    expect(request).toHaveBeenLastCalledWith(context, "auth/chatgpt/login/cancel", { login_id: "fixture" });
    await flow.start(new Owner(2), otherContext);
  });
  it("cancels if its owner closes before startup returns", async () => {
    vi.useFakeTimers();
    let resolveStart!: (value: AuthChatGPTLoginResult) => void;
    const request = vi.fn().mockImplementationOnce(() => new Promise<AuthChatGPTLoginResult>(resolve => { resolveStart = resolve; })).mockResolvedValue({ ok: true });
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    const owner = new Owner(1);
    const started = flow.start(owner, context);
    owner.destroy();
    resolveStart(pending);
    await started;
    expect(request).toHaveBeenLastCalledWith(context, "auth/chatgpt/login/cancel", { login_id: "fixture" });
  });
  it("cancels a failed poll before admitting another login", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValueOnce(pending).mockRejectedValueOnce(new Error("poll transport failed")).mockResolvedValueOnce({ ok: true }).mockResolvedValueOnce(pending);
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    await flow.start(new Owner(1), context);
    await expect(flow.poll(1, "fixture")).rejects.toThrow("poll transport failed");
    expect(request).toHaveBeenLastCalledWith(context, "auth/chatgpt/login/cancel", { login_id: "fixture" });
    await flow.start(new Owner(2), otherContext);
  });
  it("preserves the duplicate guard after poll and cleanup transport failures until expiry", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValueOnce(pending).mockRejectedValue(new Error("transport failed"));
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    await flow.start(new Owner(1), context);
    await expect(flow.poll(1, "fixture")).rejects.toThrow("transport failed");
    await expect(flow.start(new Owner(2), otherContext)).rejects.toThrow("already running");
    await expect(flow.cancel(1, "fixture")).rejects.toThrow("transport failed");
    await expect(flow.start(new Owner(2), otherContext)).rejects.toThrow("already running");
    await vi.advanceTimersByTimeAsync(905_000);
    request.mockResolvedValueOnce(pending);
    await flow.start(new Owner(2), otherContext);
  });
  it("retains ownership after an uncertain startup response until the callback lifetime ends", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValueOnce(pending);
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    await expect(flow.start(new Owner(1), context)).rejects.toThrow("status is unknown");
    await expect(flow.start(new Owner(2), otherContext)).rejects.toThrow("already running");
    await vi.advanceTimersByTimeAsync(16 * 60 * 1000);
    await flow.start(new Owner(2), otherContext);
  });
  it("clears terminal ownership and startup failures without retaining listeners", async () => {
    vi.useFakeTimers();
    const owner = new Owner(1);
    const request = vi.fn().mockRejectedValueOnce(new AppServerResponseError("keyring unavailable")).mockResolvedValueOnce(pending).mockResolvedValueOnce({ ...pending, status: "success" });
    const flow = new ChatGPTLoginCoordinator(request, vi.fn().mockResolvedValue(undefined));
    await expect(flow.start(owner, context)).rejects.toThrow("keyring unavailable");
    expect(owner.listenerCount("destroyed")).toBe(0);
    await flow.start(owner, context);
    await flow.poll(1, "fixture");
    expect(owner.listenerCount("destroyed")).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});

it("returns to the owning window once and suppresses concurrent late successes", async () => {
  vi.useFakeTimers();
  const results: Array<(value: AuthChatGPTLoginResult) => void> = [];
  const request = vi.fn().mockResolvedValueOnce(pending).mockImplementation(() => new Promise<AuthChatGPTLoginResult>(resolve => results.push(resolve)));
  const done = vi.fn();
  const flow = new ChatGPTLoginCoordinator(request, vi.fn(), done);
  await flow.start(new Owner(1), context);
  const first = flow.poll(1, "fixture");
  const second = flow.poll(1, "fixture");
  results[0]({ ...pending, status: "success" });
  await first;
  results[1]({ ...pending, status: "success" });
  await second;
  expect(done).toHaveBeenCalledExactlyOnceWith(1);
});

it.each(["cancel", "destroy", "expiry"])("does not return after %s even when an in-flight poll succeeds", async (action) => {
  vi.useFakeTimers();
  let finish!: (value: AuthChatGPTLoginResult) => void;
  const request = vi.fn().mockResolvedValueOnce(pending).mockImplementationOnce(() => new Promise<AuthChatGPTLoginResult>(resolve => { finish = resolve; })).mockResolvedValue({ ok: true });
  const done = vi.fn();
  const flow = new ChatGPTLoginCoordinator(request, vi.fn(), done);
  const owner = new Owner(1);
  await flow.start(owner, context);
  const polling = flow.poll(1, "fixture");
  if (action === "cancel") await flow.cancel(1, "fixture");
  if (action === "destroy") owner.destroy();
  if (action === "expiry") await vi.advanceTimersByTimeAsync(905_000);
  finish({ ...pending, status: "success" });
  await polling;
  expect(done).not.toHaveBeenCalled();
});

it("revokes return immediately even if cancellation cannot reach the service", async () => {
  vi.useFakeTimers();
  let finish!: (value: AuthChatGPTLoginResult) => void;
  const request = vi.fn().mockResolvedValueOnce(pending).mockImplementationOnce(() => new Promise<AuthChatGPTLoginResult>(resolve => { finish = resolve; })).mockRejectedValue(new Error("offline"));
  const done = vi.fn();
  const flow = new ChatGPTLoginCoordinator(request, vi.fn(), done);
  await flow.start(new Owner(1), context);
  const poll = flow.poll(1, "fixture");
  await expect(flow.cancel(1, "fixture")).rejects.toThrow("offline");
  finish({ ...pending, status: "success" });
  await poll;
  expect(done).not.toHaveBeenCalled();
});
