import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  LAYOUT_MOTION_CLASS,
  WINDOW_RESIZE_SETTLE_DELAY_MS,
  WINDOW_RESIZING_CLASS,
  createWindowResizeSettleScheduler,
  flushWindowResizeSettle,
  isWindowResizing,
  releaseWindowResizeClass,
} from "./WindowResizeState";

describe("WindowResizeState", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.documentElement.classList.remove(
      WINDOW_RESIZING_CLASS,
      LAYOUT_MOTION_CLASS,
    );
  });

  afterEach(() => {
    document.documentElement.classList.remove(
      WINDOW_RESIZING_CLASS,
      LAYOUT_MOTION_CLASS,
    );
    vi.useRealTimers();
  });

  it("keeps a scheduled callback pending while the window is resizing", () => {
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    const callback = vi.fn();
    const scheduler = createWindowResizeSettleScheduler(callback);
    scheduler.schedule();
    vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    expect(callback).not.toHaveBeenCalled();
    scheduler.cancel();
  });

  it("runs the callback after the settle delay once the freeze lifts", () => {
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    const callback = vi.fn();
    const scheduler = createWindowResizeSettleScheduler(callback);
    scheduler.schedule();
    document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
    vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    expect(callback).toHaveBeenCalledTimes(1);
  });

  it("applies pending layout before the freeze class is removed", () => {
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    const callback = vi.fn();
    const scheduler = createWindowResizeSettleScheduler(callback);
    scheduler.schedule();
    releaseWindowResizeClass(WINDOW_RESIZING_CLASS);
    expect(callback).toHaveBeenCalledTimes(1);
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(
      false,
    );
    vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    expect(callback).toHaveBeenCalledTimes(1);
  });

  it("does not flush while another freeze class is still holding layout", () => {
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    document.documentElement.classList.add(LAYOUT_MOTION_CLASS);
    const callback = vi.fn();
    const scheduler = createWindowResizeSettleScheduler(callback);
    scheduler.schedule();
    releaseWindowResizeClass(WINDOW_RESIZING_CLASS);
    expect(callback).not.toHaveBeenCalled();
    expect(isWindowResizing()).toBe(true);
    releaseWindowResizeClass(LAYOUT_MOTION_CLASS);
    expect(callback).toHaveBeenCalledTimes(1);
    expect(isWindowResizing()).toBe(false);
  });

  it("lets flushWindowResizeSettle run callbacks while the freeze class is still on", () => {
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    const callback = vi.fn();
    createWindowResizeSettleScheduler(callback).schedule();
    expect(isWindowResizing()).toBe(true);
    flushWindowResizeSettle();
    expect(callback).toHaveBeenCalledTimes(1);
    expect(isWindowResizing()).toBe(true);
    vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    expect(callback).toHaveBeenCalledTimes(1);
  });
});
