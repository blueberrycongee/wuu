import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTabExitRetention } from "./TabMotion";

type Item = { id: string; label: string };

function Strip({ items }: { items: Item[] }): JSX.Element {
  const entries = useTabExitRetention(items, (item) => item.id);
  return (
    <div>
      {entries.map((entry) => (
        <span
          key={`${entry.closing ? "closing-" : ""}${entry.tab.id}`}
          data-testid={entry.closing ? `closing-${entry.tab.id}` : entry.tab.id}
        >
          {entry.tab.label}
        </span>
      ))}
    </div>
  );
}

let container: HTMLDivElement;
let root: Root | null = null;

function renderStrip(items: Item[]): void {
  act(() => {
    if (!root) {
      root = createRoot(container);
    }
    root.render(<Strip items={items} />);
  });
}

function byTestID(id: string): HTMLElement | null {
  return container.querySelector(`[data-testid="${id}"]`);
}

describe("useTabExitRetention", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    container = document.createElement("div");
    document.body.appendChild(container);
  });
  afterEach(() => {
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    vi.useRealTimers();
  });

  it("keeps a removed tab mounted as a closing ghost for one motion beat", () => {
    const a = { id: "a", label: "A" };
    const b = { id: "b", label: "B" };
    const c = { id: "c", label: "C" };
    renderStrip([a, b, c]);
    renderStrip([a, c]);

    // Ghost stays at its old position with its last-known data.
    const ghost = byTestID("closing-b");
    expect(ghost?.textContent).toBe("B");
    expect(ghost?.nextElementSibling?.getAttribute("data-testid")).toBe("c");

    // jsdom has no computed styles, so the hook falls back to 120ms.
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(byTestID("closing-b")).toBeNull();
    expect(byTestID("a")).toBeTruthy();
    expect(byTestID("c")).toBeTruthy();
  });

  it("keeps a closing tab in place across unrelated renders until its exit ends", () => {
    const a = { id: "a", label: "A" };
    const b = { id: "b", label: "B" };
    const c = { id: "c", label: "C" };
    renderStrip([a, b, c]);
    renderStrip([a, c]);
    act(() => { vi.advanceTimersByTime(60); });
    renderStrip([a, { ...c, label: "C updated" }]);

    expect(byTestID("closing-b")?.textContent).toBe("B");
    expect(byTestID("closing-b")?.nextElementSibling?.textContent).toBe("C updated");
    act(() => { vi.advanceTimersByTime(60); });
    expect(byTestID("closing-b")).toBeNull();
  });

  it("retains independently closing tabs in visual order through staggered exits", () => {
    const items = ["a", "b", "c", "d"].map((id) => ({ id, label: id }));
    const [a, , , d] = items;
    renderStrip(items);
    renderStrip([a, items[2], d]);
    act(() => { vi.advanceTimersByTime(60); });
    renderStrip([a, d]);
    expect([...container.querySelectorAll("span")].map((node) => node.textContent)).toEqual(["a", "b", "c", "d"]);

    act(() => { vi.advanceTimersByTime(60); });
    expect(byTestID("closing-b")).toBeNull();
    expect(byTestID("closing-c")?.textContent).toBe("c");
    expect(byTestID("closing-c")?.nextElementSibling?.textContent).toBe("d");
    act(() => { vi.advanceTimersByTime(60); });
    expect(byTestID("closing-c")).toBeNull();
  });

  it("revives a tab that reappears while its ghost is still collapsing", () => {
    const a = { id: "a", label: "A" };
    const b = { id: "b", label: "B" };
    renderStrip([a, b]);
    renderStrip([a]);
    expect(byTestID("closing-b")).toBeTruthy();

    renderStrip([a, { id: "b", label: "B2" }]);
    expect(byTestID("closing-b")).toBeNull();
    expect(byTestID("b")?.textContent).toBe("B2");

    // The stale timer must not remove the revived tab later.
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(byTestID("b")).toBeTruthy();
  });
});
