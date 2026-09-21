import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

afterEach(() => {
  if (root) {
    act(() => root?.unmount());
  }
  container?.remove();
  document.body.querySelectorAll(".thread-row-context-menu").forEach((menu) => menu.remove());
  root = undefined;
  container = undefined;
  vi.restoreAllMocks();
});

describe("ThreadContextMenu", () => {
  it("reuses viewport placement so a session menu stays visible near the window edge", () => {
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(180);
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(200);
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(1000);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(800);

    container = document.createElement("div");
    document.body.appendChild(container);
    act(() => {
      root = createRoot(container!);
      root.render(
        <WuuUIRoot>
          <ThreadContextMenu
            x={950}
            y={700}
            items={[{ label: "归档", onSelect: () => {} }]}
            onClose={() => {}}
          />
        </WuuUIRoot>,
      );
    });

    const menu = document.body.querySelector<HTMLElement>(".thread-row-context-menu");
    expect(menu?.style.left).toBe("770px");
    expect(menu?.style.top).toBe("500px");
    expect(menu?.dataset.origin).toBe("bottom-right");
    expect(menu?.dataset.placed).toBe("true");
    expect(menu?.style.visibility).toBe("");
    expect(menu?.dataset.wuuComponent).toBe("menu");
    expect(menu?.dataset.wuuLayer).toBe("menu");
    expect(menu?.dataset.wuuState).toBe("open");
    expect(menu?.closest('[data-wuu-layer-host="true"]')).not.toBeNull();
  });

  it("does not dismiss on the opening right-click or a later right-click", async () => {
    const onClose = vi.fn();
    container = document.createElement("div");
    document.body.appendChild(container);
    act(() => {
      root = createRoot(container!);
      root.render(
        <WuuUIRoot>
          <ThreadContextMenu
            x={40}
            y={80}
            items={[{ label: "归档", onSelect: () => {} }]}
            onClose={onClose}
          />
        </WuuUIRoot>,
      );
    });

    act(() => {
      document.body.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 2 }),
      );
    });
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      await new Promise<void>((resolve) => {
        setTimeout(resolve, 0);
      });
    });

    act(() => {
      document.body.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 2 }),
      );
    });
    expect(onClose).not.toHaveBeenCalled();

    act(() => {
      document.body.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 0 }),
      );
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("dismisses an already-open menu before another instance can paint", () => {
    function DualMenus(): JSX.Element {
      const [firstOpen, setFirstOpen] = useState(true);
      return (
        <WuuUIRoot>
          {firstOpen ? (
            <ThreadContextMenu
              x={10}
              y={20}
              items={[{ label: "第一个", onSelect: () => {} }]}
              onClose={() => setFirstOpen(false)}
            />
          ) : null}
          <ThreadContextMenu
            x={30}
            y={40}
            items={[{ label: "第二个", onSelect: () => {} }]}
            onClose={() => {}}
          />
        </WuuUIRoot>
      );
    }

    container = document.createElement("div");
    document.body.appendChild(container);
    act(() => {
      root = createRoot(container!);
      root.render(<DualMenus />);
    });

    const menus = document.body.querySelectorAll(".thread-row-context-menu");
    expect(menus).toHaveLength(1);
    expect(menus[0]?.textContent).toBe("第二个");
  });
});
