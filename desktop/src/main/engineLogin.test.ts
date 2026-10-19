import { expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { EngineLogin } from "./engineLogin";

const grok = { engine_id: "grok", method_id: "local-cli" };
const windowOwner = (id: number) => Object.assign(new EventEmitter(), { id });

it("pins cancellation to the original process and refuses duplicate or foreign-window control", async () => {
  const login = new EngineLogin();
  let finish!: (value: unknown) => void;
  const request = vi.fn((method: string) => method === "engine/authenticate"
    ? new Promise(resolve => { finish = resolve; })
    : Promise.resolve({ ok: true }));
  const second = vi.fn();
  const pending = login.start(windowOwner(1), () => request, grok);
  expect(request).toHaveBeenCalledExactlyOnceWith("engine/authenticate", { engine_id: "grok", method_id: "local-cli" });
  await expect(login.start(windowOwner(2), second, grok)).rejects.toThrow("already running");
  expect(second).not.toHaveBeenCalled();
  await expect(login.cancel(2)).resolves.toEqual({ ok: false });
  expect(request).toHaveBeenCalledTimes(1);
  await expect(login.cancel(1)).resolves.toEqual({ ok: true });
  expect(request).toHaveBeenLastCalledWith("engine/auth/cancel", { engine_id: "grok" });
  await login.cancel(1);
  expect(request).toHaveBeenCalledTimes(2);
  finish({ methods: [], authenticated: false });
  await pending;
  expect(login.active).toBe(false);
});

it("releases failed attempts and binds a retry to its new process", async () => {
  const login = new EngineLogin();
  const first = vi.fn().mockRejectedValue(new Error("fixture sign-in failure"));
  await expect(login.start(windowOwner(1), () => first, grok)).rejects.toThrow("fixture sign-in failure");
  expect(login.active).toBe(false);
  const next = vi.fn().mockResolvedValue({ methods: [], authenticated: true });
  await expect(login.start(windowOwner(2), () => next, grok)).resolves.toEqual({ methods: [], authenticated: true });
  expect(next).toHaveBeenCalledExactlyOnceWith("engine/authenticate", { engine_id: "grok", method_id: "local-cli" });
  await expect(login.cancel(2)).resolves.toEqual({ ok: false });
  expect(next).toHaveBeenCalledTimes(1);
});

it("does not retain ownership when the original execution service cannot be bound", async () => {
  const login = new EngineLogin();
  await expect(login.start(windowOwner(1), () => { throw new Error("service unavailable"); }, grok)).rejects.toThrow("service unavailable");
  expect(login.active).toBe(false);
});

it("cancels on window close and releases its listener when the original attempt finishes", async () => {
  const owner = windowOwner(1);
  const login = new EngineLogin();
  let finish!: (value: unknown) => void;
  const original = vi.fn((method: string) => method === "engine/authenticate"
    ? new Promise(resolve => { finish = resolve; })
    : Promise.resolve({ ok: true }));
  let current = original;
  const pending = login.start(owner, () => current, grok);
  current = vi.fn().mockResolvedValue({ ok: true });
  owner.emit("destroyed");
  expect(original).toHaveBeenLastCalledWith("engine/auth/cancel", { engine_id: "grok" });
  expect(current).not.toHaveBeenCalled();
  finish({ methods: [], authenticated: false });
  await pending;
  expect(owner.listenerCount("destroyed")).toBe(0);
});

it.each(["success", "cancel", "destroy", "failure"])("returns only for an owned successful CLI/ACP attempt: %s", async (action) => {
  const done = vi.fn();
  const login = new EngineLogin(done);
  const owner = windowOwner(1);
  let finish!: (value: unknown) => void;
  const request = vi.fn((method: string) => method === "engine/authenticate"
    ? new Promise(resolve => { finish = resolve; })
    : Promise.resolve({ ok: true }));
  const pending = login.start(owner, () => request, { engine_id: "claude", method_id: "oauth" });
  if (action === "cancel") await login.cancel(1);
  if (action === "destroy") owner.emit("destroyed");
  finish({ methods: [], authenticated: action !== "failure" });
  await pending;
  expect(done.mock.calls).toEqual(action === "success" ? [[1]] : []);
  expect(request).toHaveBeenCalledWith("engine/authenticate", { engine_id: "claude", method_id: "oauth" });
});
