import { expect, it, vi } from "vitest";
import { authWindowReturn } from "./authWindowReturn";

it("restores and focuses only the captured existing window once", () => {
  const window = { isDestroyed: () => false, isMinimized: () => true, restore: vi.fn(), show: vi.fn(), focus: vi.fn() };
  const complete = authWindowReturn(window);
  complete();
  complete();
  expect(window.restore).toHaveBeenCalledTimes(1);
  expect(window.show).toHaveBeenCalledTimes(1);
  expect(window.focus).toHaveBeenCalledTimes(1);
});

it("never revives a closed or absent window", () => {
  const window = { isDestroyed: () => true, isMinimized: vi.fn(), restore: vi.fn(), show: vi.fn(), focus: vi.fn() };
  authWindowReturn(window)();
  authWindowReturn(null)();
  expect(window.show).not.toHaveBeenCalled();
  expect(window.focus).not.toHaveBeenCalled();
});
