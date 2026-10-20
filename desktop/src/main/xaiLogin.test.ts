import { EventEmitter } from "node:events";
import { afterEach, expect, it, vi } from "vitest";
import { XAILoginCoordinator } from "./xaiLogin";

const started = { login_id: "device", expires_in: 900, interval_ms: 1000, user_code: "code", verification_uri: "https://accounts.x.ai" };
class Owner extends EventEmitter {
  destroyed = false;
  constructor(readonly id: number) { super(); }
  isDestroyed() { return this.destroyed; }
  destroy() { this.destroyed = true; this.emit("destroyed"); }
}
afterEach(() => vi.useRealTimers());

it("pins device polling and cancellation to the initiating service and window", async () => {
  vi.useFakeTimers();
  const done = vi.fn();
  const flow = new XAILoginCoordinator(done);
  const request = vi.fn().mockResolvedValueOnce(started).mockResolvedValue({ status: "pending" });
  await flow.start(new Owner(1), () => request);
  await expect(flow.poll(2, "device")).rejects.toThrow("not found");
  await expect(flow.cancel(2, "device")).resolves.toEqual({ ok: false });
  await flow.poll(1, "device");
  expect(request).toHaveBeenLastCalledWith("auth/xai/login/poll", { login_id: "device" });
  expect(done).not.toHaveBeenCalled();
  request.mockResolvedValueOnce({ ok: true });
  await flow.cancel(1, "device");
  expect(request).toHaveBeenLastCalledWith("auth/xai/login/cancel", { login_id: "device" });
});

it.each(["success", "cancel", "destroy", "expiry", "denied"])("returns only for a current owned device success: %s", async action => {
  vi.useFakeTimers();
  const done = vi.fn();
  const flow = new XAILoginCoordinator(done);
  let finish!: (value: unknown) => void;
  const request = vi.fn().mockResolvedValueOnce(started).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockResolvedValue({ ok: true });
  const owner = new Owner(1);
  await flow.start(owner, () => request);
  const poll = flow.poll(1, "device");
  if (action === "cancel") await flow.cancel(1, "device");
  if (action === "destroy") owner.destroy();
  if (action === "expiry") await vi.advanceTimersByTimeAsync(905_000);
  finish({ status: action === "denied" ? "denied" : "success" });
  await poll;
  expect(done.mock.calls).toEqual(action === "success" ? [[1]] : []);
  expect(owner.listenerCount("destroyed")).toBe(0);
});

it("cancels a device start that arrives after its window closes", async () => {
  vi.useFakeTimers();
  let finish!: (value: unknown) => void;
  const request = vi.fn().mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockResolvedValue({ ok: true });
  const flow = new XAILoginCoordinator(vi.fn());
  const owner = new Owner(1);
  const start = flow.start(owner, () => request);
  owner.destroy();
  finish(started);
  await start;
  expect(request).toHaveBeenLastCalledWith("auth/xai/login/cancel", { login_id: "device" });
});

it("does not focus twice when concurrent device polls succeed", async () => {
  vi.useFakeTimers();
  const done = vi.fn();
  const flow = new XAILoginCoordinator(done);
  const results: Array<(value: unknown) => void> = [];
  const request = vi.fn().mockResolvedValueOnce(started).mockImplementation(() => new Promise(resolve => results.push(resolve)));
  await flow.start(new Owner(1), () => request);
  const first = flow.poll(1, "device");
  const second = flow.poll(1, "device");
  results[0]({ status: "success" });
  await first;
  results[1]({ status: "success" });
  await second;
  expect(done).toHaveBeenCalledExactlyOnceWith(1);
});

it("suppresses late device success even after cancellation transport fails", async () => {
  vi.useFakeTimers();
  const done = vi.fn();
  const flow = new XAILoginCoordinator(done);
  let finish!: (value: unknown) => void;
  const request = vi.fn().mockResolvedValueOnce(started).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockRejectedValue(new Error("offline"));
  await flow.start(new Owner(1), () => request);
  const poll = flow.poll(1, "device");
  await expect(flow.cancel(1, "device")).rejects.toThrow("offline");
  finish({ status: "success" });
  await poll;
  expect(done).not.toHaveBeenCalled();
});
