import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChromeOverlayApi, ChromeOverlayCommand, ChromeOverlayEvent, ChromeOverlayRequest } from "../shared/chromeOverlay";
import { NavigationRibbon } from "./NavigationRibbon";

// A website selection can replace the shell's pointer target with native chrome.
// Navigating back to an app page must not end that hover session. Only an actual
// leave or explicit dismissal may release it; keyboard retention must survive too.
let root: Root;
let container: HTMLDivElement;
let emit: (event: ChromeOverlayEvent) => void;
let request: ChromeOverlayRequest;
const original = window.wuu;
const originalScroll = Object.getOwnPropertyDescriptor(Element.prototype, "scrollIntoView");
const originalIterator = Object.getOwnPropertyDescriptor(CSSStyleDeclaration.prototype, Symbol.iterator);
const hide = vi.fn(async () => {});

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  hide.mockClear();
  Object.defineProperty(Element.prototype, "scrollIntoView", { configurable: true, value: () => {} });
  Object.defineProperty(CSSStyleDeclaration.prototype, Symbol.iterator, {
    configurable: true,
    value: function* (this: CSSStyleDeclaration) { for (let i = 0; i < this.length; i++) yield this.item(i); },
  });
  const api: ChromeOverlayApi = {
    show: vi.fn(async value => { request = value; }),
    hide,
    onEvent: callback => { emit = callback; return () => {}; },
  };
  window.wuu = { chromeOverlay: api } as unknown as typeof window.wuu;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  if (originalScroll) Object.defineProperty(Element.prototype, "scrollIntoView", originalScroll);
  else Reflect.deleteProperty(Element.prototype, "scrollIntoView");
  if (originalIterator) Object.defineProperty(CSSStyleDeclaration.prototype, Symbol.iterator, originalIterator);
  else Reflect.deleteProperty(CSSStyleDeclaration.prototype, Symbol.iterator);
  window.wuu = original;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

function Journey() {
  const [active, setActive] = useState("alpha");
  return <NavigationRibbon
    activeWebsiteId={active === "conversations" ? null : active}
    items={[{ id: "conversations", title: "Conversations", icon: null, nativeIcon: { kind: "conversations" },
      active: active === "conversations", onSelect: () => setActive("conversations") }]}
    websites={["alpha", "beta"].map(id => ({ id, title: id, url: `https://${id}.example/` }))}
    onSelectWebsite={website => setActive(website.id)}
  />;
}
const rail = () => container.querySelector("nav")!;
function command(command: ChromeOverlayCommand) {
  act(() => emit({ token: request.token, generation: request.generation, command }));
}
function open() {
  act(() => root.render(<Journey />));
  act(() => rail().dispatchEvent(new MouseEvent("pointerover", { bubbles: true })));
  act(() => vi.advanceTimersByTime(120));
  // The OS now delivers input to the native ribbon instead of the shell.
  act(() => rail().dispatchEvent(new MouseEvent("pointerout", { bubbles: true, relatedTarget: null })));
  expect(rail().dataset.expanded).toBe("true");
}

describe("NavigationRibbon hover navigation", () => {
  it("keeps the same open ribbon through website and app selections until pointer leave", () => {
    open();
    const opening = request.token;
    for (const id of ["beta", "conversations", "alpha", "conversations"]) {
      command({ type: "select", id });
      expect(container.querySelector(`[data-ribbon-id="${id}"]`)?.getAttribute("aria-current")).toBe("page");
      expect(rail().dataset.expanded).toBe("true");
      expect(request.token).toBe(opening);
      expect(hide).not.toHaveBeenCalled();
    }
    command({ type: "dismiss", reason: "leave" });
    expect(rail().dataset.expanded).toBeUndefined();
    expect(hide).toHaveBeenCalled();
  });

  it("allows Escape after switching to an app page without reopening under a stationary pointer", () => {
    open();
    command({ type: "select", id: "conversations" });
    command({ type: "dismiss", reason: "escape" });
    act(() => rail().dispatchEvent(new MouseEvent("pointerover", { bubbles: true })));
    act(() => vi.advanceTimersByTime(200));
    expect(rail().dataset.expanded).toBeUndefined();
    act(() => rail().dispatchEvent(new MouseEvent("pointerout", { bubbles: true, relatedTarget: null })));
    act(() => rail().dispatchEvent(new MouseEvent("pointerover", { bubbles: true })));
    act(() => vi.advanceTimersByTime(120));
    expect(rail().dataset.expanded).toBe("true");
  });
});
