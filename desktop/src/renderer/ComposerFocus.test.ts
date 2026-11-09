import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { focusComposerTextarea, isTouchWebShell } from "./ComposerFocus";

function stubMatchMedia(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({ matches }),
  );
}

describe("ComposerFocus", () => {
  beforeEach(() => {
    delete document.documentElement.dataset.hostKind;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    delete document.documentElement.dataset.hostKind;
  });

  describe("isTouchWebShell", () => {
    it("is false without the web host stamp", () => {
      stubMatchMedia(true);
      expect(isTouchWebShell()).toBe(false);
    });

    it("is false for a fine pointer in the web shell", () => {
      document.documentElement.dataset.hostKind = "web";
      stubMatchMedia(false);
      expect(isTouchWebShell()).toBe(false);
    });

    it("is true for a coarse pointer in the web shell", () => {
      document.documentElement.dataset.hostKind = "web";
      stubMatchMedia(true);
      expect(isTouchWebShell()).toBe(true);
    });
  });

  describe("focusComposerTextarea", () => {
    it("keeps desktop focus deferred to the next frame", () => {
      const raf: FrameRequestCallback[] = [];
      vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
        raf.push(cb);
        return raf.length;
      });
      const textarea = document.createElement("textarea");
      const focus = vi.spyOn(textarea, "focus");

      focusComposerTextarea(textarea);

      expect(focus).not.toHaveBeenCalled();
      raf.forEach((cb) => cb(0));
      expect(focus).toHaveBeenCalledOnce();
    });

    it("focuses synchronously on the touch web shell and defers selection", () => {
      document.documentElement.dataset.hostKind = "web";
      stubMatchMedia(true);
      const raf: FrameRequestCallback[] = [];
      vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
        raf.push(cb);
        return raf.length;
      });
      const textarea = document.createElement("textarea");
      textarea.value = "caret at end";
      const focus = vi.spyOn(textarea, "focus");
      const setSelectionRange = vi.spyOn(textarea, "setSelectionRange");

      focusComposerTextarea(textarea, "end");

      expect(focus).toHaveBeenCalledWith({ preventScroll: true });
      expect(setSelectionRange).not.toHaveBeenCalled();
      raf.forEach((cb) => cb(0));
      expect(setSelectionRange).toHaveBeenCalledWith(12, 12);
    });
  });
});
