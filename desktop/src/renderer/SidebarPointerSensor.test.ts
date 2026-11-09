import { describe, expect, it } from "vitest";
import { shouldStartSidebarReorder } from "./SidebarPointerSensor";

function activationEvent(
  pointerType: string,
  overrides: Partial<{
    isPrimary: boolean;
    button: number;
  }> = {},
) {
  return {
    nativeEvent: {
      isPrimary: true,
      button: 0,
      pointerType,
      ...overrides,
    },
  };
}

describe("shouldStartSidebarReorder", () => {
  it("starts a reorder for primary left-button mouse pointers", () => {
    expect(shouldStartSidebarReorder(activationEvent("mouse"))).toBe(true);
  });

  it("does not start a reorder for touch so swipes keep native scrolling", () => {
    expect(shouldStartSidebarReorder(activationEvent("touch"))).toBe(false);
  });

  it("does not start a reorder for pen", () => {
    expect(shouldStartSidebarReorder(activationEvent("pen"))).toBe(false);
  });

  it("does not start a reorder for non-primary or non-left-button pointers", () => {
    expect(shouldStartSidebarReorder(activationEvent("mouse", { isPrimary: false }))).toBe(false);
    expect(shouldStartSidebarReorder(activationEvent("mouse", { button: 2 }))).toBe(false);
  });
});
