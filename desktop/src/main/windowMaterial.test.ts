import { describe, expect, it } from "vitest";
import {
  MAC_CLEAR_WINDOW_BACKGROUND,
  MAC_WINDOW_VIBRANCY,
  macWindowMaterial,
} from "./windowMaterial";

describe("macWindowMaterial", () => {
  it("uses an opaque fill while the frame is dragging", () => {
    expect(macWindowMaterial(true, "#f6f6f4")).toEqual({
      backgroundColor: "#f6f6f4",
      vibrancy: null,
      backgroundBeforeVibrancy: true,
    });
  });

  it("restores the under-window material when the drag ends", () => {
    expect(macWindowMaterial(false, "#1d2024")).toEqual({
      backgroundColor: MAC_CLEAR_WINDOW_BACKGROUND,
      vibrancy: MAC_WINDOW_VIBRANCY,
      backgroundBeforeVibrancy: false,
    });
  });
});
