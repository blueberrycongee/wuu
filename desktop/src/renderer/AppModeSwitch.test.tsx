import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { WuuDesktopApi } from "../shared/protocol";
import {
  AppModeSwitch,
  CLEAR_UNREAD_HINT_SEEN_KEY,
} from "./AppModeSwitch";
import { I18nProvider, setActiveLocale } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

type ObserverCallback = IntersectionObserverCallback;

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];

  readonly callback: ObserverCallback;
  readonly observed = new Set<Element>();

  constructor(callback: ObserverCallback) {
    this.callback = callback;
    FakeIntersectionObserver.instances.push(this);
  }

  observe(target: Element): void {
    this.observed.add(target);
  }

  unobserve(target: Element): void {
    this.observed.delete(target);
  }

  disconnect(): void {
    this.observed.clear();
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }

  emit(isIntersecting: boolean): void {
    const entries = [...this.observed].map((target) => ({
      isIntersecting,
      intersectionRatio: isIntersecting ? 1 : 0,
      target,
    })) as IntersectionObserverEntry[];
    this.callback(entries, this as unknown as IntersectionObserver);
  }
}

let container: HTMLDivElement;
let root: Root | null = null;
let originalIntersectionObserver: typeof IntersectionObserver | undefined;

beforeEach(() => {
  vi.useFakeTimers();
  setActiveLocale("zh-CN");
  window.wuu = {
    initialLanguagePreference: "zh-CN",
    initialSystemLocale: "zh-CN",
  } as unknown as WuuDesktopApi;
  window.localStorage.clear();
  FakeIntersectionObserver.instances = [];
  originalIntersectionObserver = window.IntersectionObserver;
  window.IntersectionObserver =
    FakeIntersectionObserver as unknown as typeof IntersectionObserver;
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  document
    .querySelectorAll('[data-wuu-component="sidebar-clear-unread-hint"]')
    .forEach((node) => node.remove());
  if (originalIntersectionObserver) {
    window.IntersectionObserver = originalIntersectionObserver;
  } else {
    Reflect.deleteProperty(window, "IntersectionObserver");
  }
  delete (window as unknown as { wuu?: unknown }).wuu;
  setActiveLocale("zh-CN");
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function renderSwitch(
  props: Partial<React.ComponentProps<typeof AppModeSwitch>> = {},
): void {
  act(() => {
    root ??= createRoot(container);
    root.render(
      <WuuUIRoot>
        <I18nProvider>
          <AppModeSwitch
            mode="harness"
            collaborationEnabled={false}
            unreadCount={props.unreadCount ?? 0}
            unreadViewOpen={props.unreadViewOpen}
            onClearUnread={props.onClearUnread}
            onToggleUnreadView={props.onToggleUnreadView}
            {...props}
          />
        </I18nProvider>
      </WuuUIRoot>,
    );
  });

  const button = container.querySelector(".sidebar-notifications-button");
  if (button instanceof HTMLElement) {
    vi.spyOn(button, "getBoundingClientRect").mockReturnValue({
      x: 240,
      y: 48,
      left: 240,
      top: 48,
      right: 272,
      bottom: 80,
      width: 32,
      height: 32,
      toJSON: () => ({}),
    } as DOMRect);
  }
}

function hintLayer(): HTMLElement | null {
  return document.querySelector('[data-wuu-component="sidebar-clear-unread-hint"]');
}

function latestObserver(): FakeIntersectionObserver | undefined {
  return FakeIntersectionObserver.instances.at(-1);
}

function showBell(visible = true): void {
  const observer = latestObserver();
  if (!observer) {
    throw new Error("expected an IntersectionObserver instance");
  }
  act(() => {
    observer.emit(visible);
  });
}

describe("AppModeSwitch clear-unread hint", () => {
  it("shows a one-time tip when unread first appears and the bell is visible", () => {
    renderSwitch({ unreadCount: 0 });
    expect(hintLayer()).toBeNull();

    renderSwitch({ unreadCount: 2 });
    expect(hintLayer()).toBeNull();

    showBell(true);
    expect(hintLayer()?.textContent).toContain("长按铃铛，可一次清除全部未读");
  });

  it("hides the tip while the sidebar bell is off-canvas", () => {
    renderSwitch({ unreadCount: 1 });
    showBell(true);
    expect(hintLayer()).not.toBeNull();

    showBell(false);
    expect(hintLayer()).toBeNull();

    showBell(true);
    expect(hintLayer()).not.toBeNull();
  });

  it("keeps the pointer centered on the bell when the viewport clamps the tip", () => {
    renderSwitch({ unreadCount: 1 });
    showBell(true);
    const tip = hintLayer()!;
    vi.spyOn(tip, "getBoundingClientRect").mockReturnValue({ width: 194, height: 32 } as DOMRect);
    act(() => window.dispatchEvent(new Event("resize")));
    const bellCenter = 256;
    expect(Number.parseFloat(tip.style.left) + Number.parseFloat(tip.style.getPropertyValue("--hint-anchor-x")))
      .toBe(bellCenter);
    expect(Number.parseFloat(tip.style.top)).toBeGreaterThan(80);

    vi.spyOn(tip, "getBoundingClientRect").mockReturnValue({ width: 280, height: 32 } as DOMRect);
    act(() => window.dispatchEvent(new Event("resize")));
    expect(Number.parseFloat(tip.style.left)).toBeGreaterThanOrEqual(0);
    expect(Number.parseFloat(tip.style.left) + Number.parseFloat(tip.style.getPropertyValue("--hint-anchor-x")))
      .toBe(bellCenter);
  });

  it("dismisses permanently when the tip close button is pressed", () => {
    renderSwitch({ unreadCount: 1 });
    showBell(true);
    const dismiss = hintLayer()?.querySelector(
      ".sidebar-clear-unread-hint-dismiss",
    );
    expect(dismiss).not.toBeNull();

    act(() => {
      dismiss?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(hintLayer()).toBeNull();
    expect(window.localStorage.getItem(CLEAR_UNREAD_HINT_SEEN_KEY)).toBe("true");

    renderSwitch({ unreadCount: 3 });
    showBell(true);
    expect(hintLayer()).toBeNull();
  });

  it("marks the tip seen after a successful long-press clear", () => {
    const onClearUnread = vi.fn();
    renderSwitch({ unreadCount: 1, onClearUnread });
    showBell(true);
    expect(hintLayer()).not.toBeNull();

    const button = container.querySelector(".sidebar-notifications-button");
    expect(button).not.toBeNull();

    act(() => {
      button?.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      vi.advanceTimersByTime(600);
    });

    expect(onClearUnread).toHaveBeenCalledTimes(1);
    expect(window.localStorage.getItem(CLEAR_UNREAD_HINT_SEEN_KEY)).toBe("true");
    expect(hintLayer()).toBeNull();
  });

  it("stays hidden once the tip has already been seen", () => {
    window.localStorage.setItem(CLEAR_UNREAD_HINT_SEEN_KEY, "true");
    renderSwitch({ unreadCount: 4 });
    expect(latestObserver()).toBeUndefined();
    expect(hintLayer()).toBeNull();
  });
});
