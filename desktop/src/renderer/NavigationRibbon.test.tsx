import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { NavigationRibbon } from "./NavigationRibbon";
import { readNavigationRibbonPreferences, saveNavigationRibbonPreferences } from "./NavigationRibbonPreferences";

let desktop: ApplicationDesktopControlsV1;
let root: Root;
let container: HTMLDivElement;
const original = window.wuu;
const originalScroll = Object.getOwnPropertyDescriptor(Element.prototype, "scrollIntoView");
beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  Object.defineProperty(Element.prototype, "scrollIntoView", { configurable: true, value: () => {} });
  desktop = { hostKind: "desktop", popOut: { kind: null, threadID: null, context: null },
    capabilities: { account: false, pets: false, nativeControls: false, chromeOverlay: false, notifications: false },
    onVisibilityChange: () => () => {} };
  window.wuu = {} as typeof window.wuu;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  if (originalScroll) Object.defineProperty(Element.prototype, "scrollIntoView", originalScroll);
  else Reflect.deleteProperty(Element.prototype, "scrollIntoView");
  window.wuu = original;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("NavigationRibbon tab order", () => {
  it("settles restored tab order without feeding native state back into repeated reorder commands", () => {
    const reorder = vi.fn();
    const websites = [{ id: "alpha", url: "https://example.com/a", title: "Alpha" }, { id: "beta", url: "https://example.com/b", title: "Beta" }];
    const render = (tabs = websites) => renderWithDesktop(<NavigationRibbon items={[]} websites={tabs} onReorderTabs={ids => reorder(ids)} />);
    saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), order: ["alpha", "beta"] });
    act(() => render());
    expect(reorder).not.toHaveBeenCalled();
    act(() => saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), order: ["beta", "alpha"] }));
    expect(reorder).toHaveBeenCalledExactlyOnceWith(["beta", "alpha"]);
    // The native reply causes the application to render again with fresh
    // callbacks, and later browser title updates must not restart that cycle.
    act(() => render([websites[1], websites[0]]));
    act(() => render([{ ...websites[1], title: "New title" }, websites[0]]));
    expect(reorder).toHaveBeenCalledTimes(1);
  });

});

// Real keyboard and close contracts: a long rail is one Tab stop, directional
// navigation skips unavailable destinations, and background closing must not
// steal the focused editor. A pinned tab requires an explicit close gesture.
describe("NavigationRibbon keyboard and close behavior", () => {
  function BrowserJourney() {
    const [tabs, setTabs] = useState(["alpha", "beta", "pinned"]);
    const [active, setActive] = useState("alpha");
    return <><input aria-label="Page editor" /><NavigationRibbon
      items={[{ id: "unavailable", title: "Unavailable", icon: null, disabled: true, onSelect: () => {} }]}
      websites={tabs.map(id => ({ id, title: id, pinned: id === "pinned", url: `https://${id}.example/` }))}
      activeWebsiteId={active}
      onSelectWebsite={tab => setActive(tab.id)}
      onCloseTab={id => {
        setTabs(tabs.filter(tab => tab !== id));
        if (id === active) setActive(tabs[tabs.indexOf(id) + 1] ?? tabs[tabs.indexOf(id) - 1]);
      }}
    /></>;
  }
  function renderBrowser() {
    desktop = { ...desktop, chromeOverlay: undefined };
    act(() => renderWithDesktop(<BrowserJourney />));
  }
  function button(id: string) { return container.querySelector<HTMLButtonElement>(`[data-ribbon-id="${id}"]`)!; }
  function key(target: HTMLElement, key: string) {
    act(() => target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true })));
  }

  it("uses one Tab stop and moves focus through available rows without navigating pages", () => {
    renderBrowser();
    expect([...container.querySelectorAll<HTMLButtonElement>("[data-ribbon-id]")].filter(button => button.tabIndex === 0).map(button => button.dataset.ribbonId)).toEqual(["alpha"]);
    act(() => button("alpha").focus());
    key(button("alpha"), "ArrowDown");
    expect(document.activeElement).toBe(button("beta"));
    expect(button("alpha").getAttribute("aria-current")).toBe("page");
    key(button("beta"), "End");
    expect(document.activeElement).toBe(button("pinned"));
    key(button("pinned"), "Home");
    expect(document.activeElement).toBe(button("alpha"));
    key(button("alpha"), "ArrowUp");
    expect(document.activeElement).toBe(button("pinned"));
  });

  it("preserves page input focus when middle-click closes a background tab", () => {
    renderBrowser();
    const input = container.querySelector("input")!;
    act(() => input.focus());
    act(() => button("beta").dispatchEvent(new MouseEvent("auxclick", { button: 1, bubbles: true })));
    expect(button("beta")).toBeNull();
    expect(document.activeElement).toBe(input);
    expect(button("alpha").getAttribute("aria-current")).toBe("page");
  });

  it("keeps keyboard focus in the rail after closing the focused tab", () => {
    renderBrowser();
    act(() => button("alpha").focus());
    key(button("alpha"), "Delete");
    expect(button("alpha")).toBeNull();
    expect(document.activeElement).toBe(button("beta"));
  });

  it("allows an explicit keyboard close of a pinned tab", () => {
    renderBrowser();
    act(() => button("pinned").focus());
    key(button("pinned"), "Delete");
    expect(button("pinned")).toBeNull();
    expect(document.activeElement).toBe(button("beta"));
  });
});

// A reopened tab has a new native identity, but must reclaim its old mixed
// navigation slot before an effect can send an appended order back to the host.
describe("NavigationRibbon reopened positions", () => {
  it("restores the former slot on first render and preserves later user reordering", () => {
    localStorage.setItem("wuu.navigation-ribbon.v1", JSON.stringify({ websites: [], order: ["conversations", "alpha", "beta", "files", "gamma"] }));
    const reorder = vi.fn();
    const render = (restored?: { id: string; reopenedFrom: string }) => renderWithDesktop(<NavigationRibbon
      items={["conversations", "files"].map(id => ({ id, title: id, icon: null, onSelect: () => {} }))}
      websites={[{ id: "alpha", title: "Alpha", url: "https://alpha.example/" },
        ...(restored ? [{ ...restored, title: "Beta", url: "https://beta.example/", pinned: true }] : []),
        { id: "gamma", title: "Gamma", url: "https://gamma.example/" }]}
      onSelectWebsite={() => {}} onReorderTabs={reorder}
    />);
    act(() => render());
    reorder.mockClear();
    act(() => render({ id: "beta-restored", reopenedFrom: "beta" }));
    const order = () => [...container.querySelectorAll<HTMLElement>("[data-ribbon-id]")].map(element => element.dataset.ribbonId);
    expect(order()).toEqual(["conversations", "alpha", "beta-restored", "files", "gamma"]);
    expect(reorder).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem("wuu.navigation-ribbon.v1")!).order).toEqual(order());
    act(() => {
      localStorage.setItem("wuu.navigation-ribbon.v1", JSON.stringify({ websites: [], order: ["conversations", "beta-restored", "alpha", "files", "gamma"] }));
      window.dispatchEvent(new Event("wuu-navigation-ribbon-change"));
    });
    act(() => render({ id: "beta-restored", reopenedFrom: "beta" }));
    expect(order()).toEqual(["conversations", "beta-restored", "alpha", "files", "gamma"]);
    expect(reorder).toHaveBeenLastCalledWith(["beta-restored", "alpha", "gamma"]);
  });
});

function renderWithDesktop(node: ReactNode) { root.render(<DesktopControlsProvider controls={desktop}>{node}</DesktopControlsProvider>); }

// Visible navigation labels should not cover neighboring rows with duplicate
// hints. Compact controls still need their accessible names on hover.
it("shows destination hints only when the navigation label is hidden", () => {
  saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), width: 200 });
  act(() => renderWithDesktop(<NavigationRibbon items={[{ id: "extensions", title: "Extensions", icon: null, onSelect: () => {} }]} websites={[]} />));
  const hover = (element: Element) => {
    act(() => {
      element.dispatchEvent(new Event("pointerover", { bubbles: true }));
      vi.advanceTimersByTime(600);
    });
  };
  hover(container.querySelector('[data-ribbon-id="extensions"]')!);
  expect(document.querySelector(".tooltip-layer")).toBeNull();
  hover(container.querySelector('[data-ribbon-id="extensions"] .sidebar-ribbon-label')!);
  expect(document.querySelector(".tooltip-layer")).toBeNull();
  act(() => saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), width: 52 }));
  hover(container.querySelector('[data-ribbon-id="extensions"]')!);
  expect(document.querySelector(".tooltip-layer")?.textContent).toBe("Extensions");
});
