import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SidebarBrand } from "./SidebarBrand";
import { I18nProvider, setActiveLocale } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  setActiveLocale("zh-CN");
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
  vi.restoreAllMocks();
});

describe("SidebarBrand bell", () => {
  it("clears unread after a hold without also toggling the attention view", () => {
    const onClearUnread = vi.fn();
    const onToggleUnreadView = vi.fn();
    act(() => {
      root = createRoot(container);
      root.render(
        <WuuUIRoot>
          <I18nProvider>
            <SidebarBrand
              attentionCount={1}
              hasUnread
              onClearUnread={onClearUnread}
              onToggleUnreadView={onToggleUnreadView}
            />
          </I18nProvider>
        </WuuUIRoot>,
      );
    });
    const bell = container.querySelector<HTMLButtonElement>(".sidebar-notifications-button")!;

    act(() => {
      bell.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0 }));
      vi.advanceTimersByTime(600);
      bell.dispatchEvent(new MouseEvent("pointerup", { bubbles: true, button: 0 }));
      bell.click();
    });
    expect(onClearUnread).toHaveBeenCalledTimes(1);
    expect(onToggleUnreadView).not.toHaveBeenCalled();

    act(() => {
      bell.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0 }));
      bell.dispatchEvent(new MouseEvent("pointerup", { bubbles: true, button: 0 }));
      bell.click();
    });
    expect(onToggleUnreadView).toHaveBeenCalledTimes(1);
    expect(onClearUnread).toHaveBeenCalledTimes(1);
  });
});
