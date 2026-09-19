import { describe, expect, it } from "vitest";
import {
  allowRendererReload,
  isDisposedWebFrameError,
  shouldReloadAfterRendererGone,
} from "./rendererProcessGone";

describe("shouldReloadAfterRendererGone", () => {
  it("reloads after a crash or out-of-memory death", () => {
    expect(shouldReloadAfterRendererGone("crashed")).toBe(true);
    expect(shouldReloadAfterRendererGone("oom")).toBe(true);
    expect(shouldReloadAfterRendererGone("abnormal-exit")).toBe(true);
    expect(shouldReloadAfterRendererGone("launch-failed")).toBe(true);
  });

  it("does not reload a clean or requested exit", () => {
    expect(shouldReloadAfterRendererGone("clean-exit")).toBe(false);
    expect(shouldReloadAfterRendererGone("killed")).toBe(false);
    expect(shouldReloadAfterRendererGone("integrity-failure")).toBe(false);
  });
});

describe("allowRendererReload", () => {
  it("allows the first reload and then respects the cooldown", () => {
    expect(allowRendererReload(1_000, undefined, 5_000)).toBe(true);
    expect(allowRendererReload(5_999, 1_000, 5_000)).toBe(false);
    expect(allowRendererReload(6_000, 1_000, 5_000)).toBe(true);
  });
});

describe("isDisposedWebFrameError", () => {
  it("matches Electron's disposed-frame send failure", () => {
    expect(
      isDisposedWebFrameError(
        new Error("Render frame was disposed before WebFrameMain could be accessed"),
      ),
    ).toBe(true);
    expect(isDisposedWebFrameError(new Error("boom"))).toBe(false);
    expect(isDisposedWebFrameError("Render frame was disposed")).toBe(false);
  });
});
