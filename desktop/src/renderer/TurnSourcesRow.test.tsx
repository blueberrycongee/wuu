import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TurnSource } from "./ToolActivityHelpers";
import { TurnSourcesRow } from "./TurnSourcesRow";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";

// Protect source navigation, tooltip access, overflow and focus recovery.
// Motion and responsive geometry are exercised in dev/web-research/capture.cjs.
const roots: Root[] = [];
afterEach(() => {
  unhoverTooltip();
  vi.useRealTimers();
  for (const root of roots.splice(0)) act(() => root.unmount());
  document.body.replaceChildren();
  vi.restoreAllMocks();
  delete (window as { wuu?: unknown }).wuu;
});

function mount(element: JSX.Element) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  roots.push(root);
  act(() => root.render(element));
  return { container, rerender: (next: JSX.Element) => act(() => root.render(next)) };
}
const sources: TurnSource[] = Array.from({ length: 8 }, (_, i) => ({
  host: `site${i}.example.com`, url: `https://site${i}.example.com/article`,
  title: `Article ${i}`, origin: "web_search",
}));

function sourceButtons(container: ParentNode = document.body) {
  return [...container.querySelectorAll<HTMLButtonElement>(".web-source-link")];
}

describe("TurnSourcesRow", () => {
  it("keeps same-site pages separately reachable by their original URLs", () => {
    const sameSite = Array.from({ length: 8 }, (_, i) => ({ ...sources[0], url: `https://site0.example.com/page-${i}`, title: `Page ${i}` }));
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sameSite} onOpen={onOpen} />);
    act(() => container.querySelector<HTMLButtonElement>(".web-research-more")!.click());
    const links = sourceButtons();
    expect(links).toHaveLength(8);
    act(() => links.forEach(link => link.click()));
    expect(onOpen.mock.calls.map(call => call[0])).toEqual(sameSite.map(source => source.url));
  });

  it("stays absent for turns without web activity", () => {
    expect(mount(<TurnSourcesRow sources={[]} />).container.firstChild).toBeNull();
  });

  it("opens a full source URL exactly once and preserves modifier clicks", () => {
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 2)} onOpen={onOpen} />);
    const buttons = sourceButtons(container);
    expect(buttons[0].getAttribute("aria-label")).toContain(sources[0].url);
    act(() => buttons[0].click());
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(sources[0].url);
    act(() => buttons[1].dispatchEvent(new MouseEvent("click", { bubbles: true, metaKey: true })));
    expect(onOpen.mock.calls[1][0]).toBe(sources[1].url);
    expect(onOpen.mock.calls[1][1]).toMatchObject({ metaKey: true });
  });

  it("opens a single source through the external-browser fallback", () => {
    const openExternal = vi.fn().mockResolvedValue(undefined);
    (window as unknown as { wuu: { openExternal: typeof openExternal } }).wuu = { openExternal };
    const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 1)} />);
    act(() => sourceButtons(container)[0].click());
    expect(openExternal).toHaveBeenCalledExactlyOnceWith(sources[0].url);
  });

  it("opens overflow outside the conversation flow and restores focus with Escape", async () => {
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sources} onOpen={onOpen} />);
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    expect(sourceButtons(container).length).toBeLessThan(sources.length);
    act(() => more.click());
    expect(more.getAttribute("aria-expanded")).toBe("true");
    await act(async () => vi.advanceTimersToNextFrame());
    const menu = document.getElementById(more.getAttribute("aria-controls")!)!;
    expect(menu.getAttribute("role")).toBe("menu");
    expect(container.contains(menu)).toBe(false);
    expect(sourceButtons(container)).toHaveLength(6);
    const buttons = sourceButtons();
    expect(buttons).toHaveLength(sources.length);
    expect(document.activeElement).toBe(sourceButtons(menu)[0]);
    // Overflow keeps readable destinations in its compact floating surface.
    expect(buttons.at(-1)!.getAttribute("aria-label")).toContain(sources.at(-1)!.title);
    expect(buttons.at(-1)!.textContent).toContain(sources.at(-1)!.host);
    act(() => buttons.at(-1)!.click());
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(sources.at(-1)!.url);
    act(() => {
      buttons.at(-1)!.focus();
      buttons.at(-1)!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(more);
    expect(sourceButtons(container).length).toBeLessThan(sources.length);
  });

  it("closes overflow with one Escape even when the focused source tooltip is visible", async () => {
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sources} onOpen={onOpen} />);
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => more.click());
    const last = sourceButtons().at(-1)!;
    act(() => last.focus());
    await act(async () => vi.advanceTimersByTime(500));
    expect(document.querySelector("[role=tooltip]")).not.toBeNull();
    act(() => last.click());
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(sources.at(-1)!.url);
    act(() => last.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(more);
    expect(document.querySelector("[role=tooltip]")).toBeNull();
    expect(sourceButtons(container)).toHaveLength(6);
  });

  it("supports keyboard entry, arrow navigation and leaving the portaled menu with Tab", async () => {
    vi.useFakeTimers();
    const { container } = mount(<TurnSourcesRow sources={sources} />);
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => {
      more.focus();
      more.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    });
    await act(async () => vi.advanceTimersToNextFrame());
    const menu = document.getElementById(more.getAttribute("aria-controls")!)!;
    const items = sourceButtons(menu);
    expect(document.activeElement).toBe(items.at(-1));
    act(() => items.at(-1)!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    expect(document.activeElement).toBe(items[0]);
    act(() => items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(document.activeElement).toBe(items.at(-1));
    act(() => items.at(-1)!.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true })));
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(more);
    expect(document.body.contains(menu)).toBe(false);
  });

  it("reveals bounded batches and keeps keyboard targets visible without scrolling the conversation", async () => {
    vi.useFakeTimers();
    const manySources = Array.from({ length: 20 }, (_, index) => ({
      ...sources[0], host: `source${index}.example.com`, url: `https://source${index}.example.com`,
    }));
    const { container } = mount(<TurnSourcesRow sources={manySources} />);
    container.scrollTop = 96;
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => {
      more.focus();
      more.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    });
    const menu = document.getElementById(more.getAttribute("aria-controls")!)!;
    await act(async () => vi.advanceTimersToNextFrame());
    const reveal = menu.querySelector<HTMLButtonElement>(".web-research-reveal")!;
    expect(document.activeElement).toBe(reveal);
    expect(sourceButtons(menu)).toHaveLength(8);
    expect(reveal.textContent).toContain("6");
    act(() => reveal.click());
    const items = sourceButtons(menu);
    expect(items).toHaveLength(14);
    // jsdom has no layout; represent a 280px scrollport with fourteen rows.
    vi.spyOn(menu, "getBoundingClientRect").mockReturnValue(new DOMRect(100, 100, 360, 280));
    Object.defineProperties(menu, { clientTop: { value: 1 }, clientHeight: { value: 278 } });
    items.forEach((item, index) => {
      vi.spyOn(item, "getBoundingClientRect").mockImplementation(() => new DOMRect(105, 105 + index * 38 - menu.scrollTop, 350, 36));
    });
    act(() => reveal.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true })));
    expect(document.activeElement).toBe(items.at(-1));
    expect(menu.scrollTop).toBeGreaterThan(0);
    expect(items.at(-1)!.getBoundingClientRect().bottom).toBeLessThanOrEqual(379);
    expect(container.scrollTop).toBe(96);
    act(() => items.at(-1)!.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    expect(document.activeElement).toBe(items[0]);
    expect(items[0].getBoundingClientRect().top).toBeGreaterThanOrEqual(101);
    expect(container.scrollTop).toBe(96);
  });

  it("moves between wrapping source chips in source order", () => {
    const many = Array.from({ length: 10 }, (_, i) => ({ ...sources[0], host: `chip-${i}.example.com`, url: `https://chip-${i}.example.com/article` }));
    const { container } = mount(<TurnSourcesRow sources={many} />);
    act(() => container.querySelector<HTMLButtonElement>(".web-research-more")!.click());
    const cards = sourceButtons(document.querySelector(".web-research-popover")!);
    cards.forEach((card, i) => vi.spyOn(card, "getBoundingClientRect").mockReturnValue(new DOMRect((i % 2) * 170, Math.floor(i / 2) * 100, 164, 94)));
    act(() => cards[0].focus());
    act(() => cards[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true })));
    expect(document.activeElement).toBe(cards[1]);
    act(() => cards[1].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    expect(document.activeElement).toBe(cards[2]);
    act(() => cards[2].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true })));
    expect(document.activeElement).toBe(cards[1]);
  });

  it("reduces inline source icons instead of adding another row on narrow headers", () => {
    const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) {
      return this.classList.contains("process-surface-inline-controls") ? 320 : 0;
    });
    const view = (items: TurnSource[]) => <div className="process-surface-inline-controls"><TurnSourcesRow sources={items} inline /></div>;
    const { container, rerender } = mount(view([]));
    rerender(view(sources));
    expect(sourceButtons(container)).toHaveLength(2);
    expect(container.querySelector(".web-research-more")?.textContent).toContain("+6");
    act(() => container.querySelector<HTMLButtonElement>(".web-research-more")!.click());
    expect(sourceButtons()).toHaveLength(sources.length);
    width.mockRestore();
  });

  it("dismisses outside presses and focus without swallowing the outside action", () => {
    const { container } = mount(<TurnSourcesRow sources={sources} />);
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    const outside = document.createElement("button");
    const click = vi.fn();
    outside.addEventListener("click", click);
    document.body.append(outside);
    act(() => more.click());
    act(() => outside.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(more.getAttribute("aria-expanded")).toBe("false");
    act(() => outside.click());
    expect(click).toHaveBeenCalledOnce();
    act(() => more.click());
    act(() => outside.focus());
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(outside);
  });

  it("removes its floating sources when a cached conversation becomes inactive", () => {
    const view = (active: boolean) => <ConversationRenderActivityProvider active={active}>
      <TurnSourcesRow sources={sources} />
    </ConversationRenderActivityProvider>;
    const { container, rerender } = mount(view(true));
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => more.click());
    expect(sourceButtons()).toHaveLength(sources.length);
    rerender(view(false));
    expect(sourceButtons()).toHaveLength(6);
    rerender(view(true));
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(sourceButtons()).toHaveLength(6);
  });

  it("keeps full URLs available in tooltips and accessible names with or without titles", async () => {
    vi.useFakeTimers();
    const { container } = mount(<TurnSourcesRow sources={[sources[0], { ...sources[1], title: undefined }]} />);
    const buttons = sourceButtons(container);
    for (const [index, button] of buttons.entries()) {
      expect(button.getAttribute("aria-label")).toContain(sources[index].url);
      expect(await hoverTooltipText(button)).toContain(sources[index].url);
    }
    expect(container.textContent).not.toMatch(/web_search|web_fetch/);
  });

  it("retains a letter fallback and accessible URL when a favicon cannot load", () => {
    const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 1)} />);
    const image = container.querySelector("img")!;
    act(() => image.dispatchEvent(new Event("error")));
    expect(container.querySelector("img")).toBeNull();
    expect(sourceButtons(container)[0].textContent).toBe("S");
    expect(sourceButtons(container)[0].getAttribute("aria-label")).toContain(sources[0].url);
  });
});
