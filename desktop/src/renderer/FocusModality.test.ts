import { afterEach, describe, expect, it } from "vitest";
import {
  FOCUS_MODALITY_ATTRIBUTE,
  readFocusModality,
  startFocusModality,
} from "./FocusModality";

function modality(): string | null {
  return document.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE);
}

function keyDown(key: string, init: KeyboardEventInit = {}): void {
  document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, ...init }));
}

function pointerDown(): void {
  document.dispatchEvent(new Event("pointerdown", { bubbles: true }));
}

describe("FocusModality", () => {
  const stops: Array<() => void> = [];

  afterEach(() => {
    while (stops.length > 0) {
      stops.pop()?.();
    }
    document.documentElement.removeAttribute(FOCUS_MODALITY_ATTRIBUTE);
  });

  it("stamps pointer modality immediately so click-focus has no ring before the first event", () => {
    stops.push(startFocusModality());
    expect(modality()).toBe("pointer");
    expect(readFocusModality()).toBe("pointer");
  });

  it("switches to keyboard on Tab and back to pointer on a click", () => {
    stops.push(startFocusModality());
    keyDown("Tab");
    expect(readFocusModality()).toBe("keyboard");
    pointerDown();
    expect(readFocusModality()).toBe("pointer");
  });

  it("treats arrow keys as keyboard focus movement except inside text fields", () => {
    stops.push(startFocusModality());
    keyDown("ArrowDown");
    expect(readFocusModality()).toBe("keyboard");
    pointerDown();
    const field = document.createElement("textarea");
    document.body.appendChild(field);
    field.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    expect(readFocusModality()).toBe("pointer");
    field.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    expect(readFocusModality()).toBe("keyboard");
    pointerDown();
    keyDown("a");
    expect(readFocusModality()).toBe("pointer");
    keyDown("Enter");
    expect(readFocusModality()).toBe("pointer");
  });

  it("keeps a single listener while nested starts are active", () => {
    const first = startFocusModality();
    const second = startFocusModality();
    stops.push(first, second);
    keyDown("Tab");
    expect(readFocusModality()).toBe("keyboard");
    first();
    pointerDown();
    expect(readFocusModality()).toBe("pointer");
    second();
    keyDown("Tab");
    expect(readFocusModality()).toBe("pointer");
  });
});
