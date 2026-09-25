import { afterEach, describe, expect, it, vi } from "vitest";
import { startRendererVisibilitySync } from "./RendererVisibility";

afterEach(() => {
  document.documentElement.removeAttribute("data-renderer-hidden");
  vi.restoreAllMocks();
});

describe("renderer visibility", () => {
  it("tracks document visibility changes for the renderer lifetime", () => {
    const visibilityState = vi
      .spyOn(document, "visibilityState", "get")
      .mockReturnValue("hidden");
    const stop = startRendererVisibilitySync();
    expect(document.documentElement.hasAttribute("data-renderer-hidden")).toBe(true);

    visibilityState.mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(document.documentElement.hasAttribute("data-renderer-hidden")).toBe(false);

    stop();
  });
});
