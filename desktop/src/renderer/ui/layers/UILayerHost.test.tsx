import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { UILayerPortal, WuuUIRoot } from "./UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
});

describe("WuuUIRoot", () => {
  it("keeps host-owned floating UI under the protected layer host", () => {
    act(() => {
      root = createRoot(container);
      root.render(
        <WuuUIRoot>
          <main data-testid="content">content</main>
          <UILayerPortal layer="menu">
            <div data-testid="menu">menu</div>
          </UILayerPortal>
        </WuuUIRoot>,
      );
    });

    const host = container.querySelector<HTMLElement>(
      '[data-wuu-layer-host="true"]',
    );
    const menu = container.querySelector<HTMLElement>('[data-testid="menu"]');
    const content = container.querySelector<HTMLElement>('[data-testid="content"]');

    expect(host).not.toBeNull();
    expect(host?.dataset.wuuComponent).toBe("layer-host");
    expect(host?.contains(menu ?? null)).toBe(true);
    expect(content?.contains(menu ?? null)).toBe(false);
  });
});
