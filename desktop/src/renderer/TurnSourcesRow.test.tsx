import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TurnSource } from "./ToolActivityHelpers";
import { TurnSourcesRow } from "./TurnSourcesRow";
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

function sourceButtons(container: HTMLElement) {
  return [...container.querySelectorAll<HTMLButtonElement>("button[aria-label^='打开 ']")].filter(button => !button.closest("[inert]"));
}

describe("TurnSourcesRow", () => {
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

  it("exposes overflow sources and returns keyboard focus when Escape collapses them", () => {
    const onOpen = vi.fn();
    const { container } = mount(<TurnSourcesRow sources={sources} onOpen={onOpen} />);
    const more = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    expect(sourceButtons(container).length).toBeLessThan(sources.length);
    act(() => more.click());
    expect(more.getAttribute("aria-expanded")).toBe("true");
    const buttons = sourceButtons(container);
    expect(buttons).toHaveLength(sources.length);
    // Opening the disclosure turns unknown icons into readable destinations.
    expect(buttons.at(-1)!.textContent).toContain(sources.at(-1)!.title);
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
