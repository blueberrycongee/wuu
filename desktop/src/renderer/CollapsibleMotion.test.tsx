import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CollapsibleDetails } from "./CollapsibleMotion";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.useRealTimers();
});

function renderDetails(expanded: boolean): void {
  act(() => {
    root.render(
      <CollapsibleDetails expanded={expanded}>
        <span data-testid="heavy-content">details</span>
      </CollapsibleDetails>,
    );
  });
}

describe("CollapsibleDetails", () => {
  it("does not mount the hidden body until a collapsed fold opens", () => {
    renderDetails(false);
    expect(container.querySelector("[data-testid='heavy-content']")).toBeNull();

    renderDetails(true);
    expect(container.querySelector("[data-testid='heavy-content']")?.textContent).toBe(
      "details",
    );
  });

  it("retains content for the close motion and then releases it", () => {
    renderDetails(true);
    renderDetails(false);
    expect(container.querySelector("[data-testid='heavy-content']")).not.toBeNull();

    act(() => vi.runAllTimers());

    expect(container.querySelector("[data-testid='heavy-content']")).toBeNull();
  });
});
