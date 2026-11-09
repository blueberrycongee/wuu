import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TurnSource } from "./ToolActivityHelpers";
import { TurnSourcesRow, TurnSourceChips } from "./TurnSourcesRow";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";
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
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container); roots.push(root); act(() => root.render(element));
  return { container, rerender: (next: JSX.Element) => act(() => root.render(next)) };
}
const sources: TurnSource[] = Array.from({ length: 20 }, (_, i) => ({ host: `site${i}.example.com`, url: `https://site${i}.example.com/article`, title: `Article ${i}`, origin: "web_search" }));
function links(container: ParentNode) { return [...container.querySelectorAll<HTMLButtonElement>(".web-source-link")]; }
describe("TurnSourcesRow", () => {
  it("stays absent without web sources", () => { expect(mount(<TurnSourcesRow sources={[]} />).container.firstChild).toBeNull(); });
  it("keeps direct link routing and modifiers", () => {
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sources} onOpen={onOpen} />);
    act(() => links(container)[0].click());
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(sources[0].url);
    act(() => links(container)[1].dispatchEvent(new MouseEvent("click", { bubbles: true, metaKey: true })));
    expect(onOpen.mock.calls[1]).toEqual([sources[1].url, expect.objectContaining({ metaKey: true })]);
  });
  it("announces the total source count for the compact group", () => {
    const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 8)} />);
    expect(links(container)).toHaveLength(6);
    expect(container.querySelector("[role=group]")?.getAttribute("aria-label")).toContain("8");
  });
  it("reduces inline icons without losing the total count on narrow headers", () => {
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) { return this.classList.contains("process-surface-inline-controls") ? 160 : 0; });
    const view = (items: TurnSource[]) => <div className="process-surface-inline-controls"><TurnSourcesRow sources={items} inline /></div>;
    const { container, rerender } = mount(view([])); rerender(view(sources));
    expect(links(container)).toHaveLength(3); expect(container.querySelector("[role=group]")?.getAttribute("aria-label")).toContain("20");
  });
  it("keeps full titles and URLs in hints and accessible names", async () => {
    vi.useFakeTimers(); const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 1)} />);
    expect(links(container)[0].getAttribute("aria-label")).toContain(sources[0].title);
    expect(await hoverTooltipText(links(container)[0])).toContain(sources[0].url);
  });
  it("retains a fallback when favicons fail", () => {
    const { container } = mount(<TurnSourcesRow sources={sources.slice(0, 1)} />);
    act(() => container.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(container.querySelector("img")).toBeNull(); expect(links(container)[0].textContent).toBe("S");
  });
});
describe("TurnSourceChips", () => {
  it("includes the first six sources and reveals all remaining links with an exact count", () => {
    const { container } = mount(<TurnSourceChips sources={sources} />);
    expect(links(container)).toHaveLength(8);
    expect(links(container)[0].getAttribute("aria-label")).toContain(sources[0].url);
    const reveal = container.querySelector<HTMLButtonElement>(".web-research-reveal")!;
    expect(reveal.textContent).toContain("12");
    act(() => reveal.click()); expect(links(container)).toHaveLength(20);
    act(() => reveal.click()); expect(links(container)).toHaveLength(8);
  });
  it("keeps every same-site page separately reachable and never replays arrival motion", () => {
    const same = sources.slice(0, 8).map((source, i) => ({ ...source, host: "site.example.com", url: `https://site.example.com/${i}` }));
    const onOpen = vi.fn(); const { container } = mount(<TurnSourceChips sources={same} onOpen={onOpen} />);
    act(() => links(container).forEach(link => link.click()));
    expect(onOpen.mock.calls.map(call => call[0])).toEqual(same.map(source => source.url));
    expect(container.querySelector(".is-arriving")).toBeNull();
  });
  it("supports keyboard navigation while retaining ordinary tab targets", () => {
    const { container } = mount(<TurnSourceChips sources={sources.slice(0, 8)} />); const chips = links(container);
    act(() => chips[0].focus());
    act(() => chips[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true })));
    expect(document.activeElement).toBe(chips[1]); expect(chips[1].tabIndex).toBe(0);
    act(() => chips[1].dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(document.activeElement).toBe(chips.at(-1));
  });
});
