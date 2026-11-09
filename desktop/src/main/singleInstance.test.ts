import { describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({
  app: {
    requestSingleInstanceLock: vi.fn(),
    quit: vi.fn(),
    on: vi.fn(),
  },
}));

import { app } from "electron";
import { installSingleInstanceGuard } from "./singleInstance";

const lock = vi.mocked(app.requestSingleInstanceLock);
const quit = vi.mocked(app.quit);
const on = vi.mocked(app.on);

describe("installSingleInstanceGuard", () => {
  it("quits without registering handlers when another instance holds the lock", () => {
    lock.mockReturnValueOnce(false);
    expect(installSingleInstanceGuard(vi.fn())).toBe(false);
    expect(quit).toHaveBeenCalledTimes(1);
    expect(on).not.toHaveBeenCalled();
  });

  it("reveals the existing window when a second instance launches", () => {
    lock.mockReturnValueOnce(true);
    const reveal = vi.fn();
    expect(installSingleInstanceGuard(reveal)).toBe(true);
    const calls = on.mock.calls as unknown as [string, () => void][];
    const listener = calls.find(([event]) => event === "second-instance")?.[1];
    expect(listener).toBeTypeOf("function");
    listener?.();
    expect(reveal).toHaveBeenCalledTimes(1);
  });
});
