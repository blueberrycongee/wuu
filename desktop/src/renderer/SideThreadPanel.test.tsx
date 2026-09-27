import { act, createElement, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  SideThreadPanel,
  type SideThreadPanelHandle,
} from "./SideThreadPanel";
import type { SideThreadEntryState } from "./SideThreadState";

let mountedRoots: Root[] = [];
let mountedContainers: HTMLElement[] = [];

function mount(element: React.ReactElement): HTMLElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(element));
  mountedRoots.push(root);
  mountedContainers.push(container);
  return container;
}

afterEach(() => {
  for (const root of mountedRoots) {
    act(() => root.unmount());
  }
  for (const container of mountedContainers) {
    container.remove();
  }
  mountedRoots = [];
  mountedContainers = [];
  vi.restoreAllMocks();
});

function makeEntry(
  overrides: Partial<SideThreadEntryState> = {},
): SideThreadEntryState {
  return {
    open: true,
    summary: null,
    messages: [],
    draft: "",
    streaming: false,
    ...overrides,
  };
}

function renderPanel(
  entry: SideThreadEntryState,
  callbacks: {
    onClose?: () => void;
    onResizeStart?: (event: unknown) => void;
    onChangeDraft?: (draft: string) => void;
  } = {},
): HTMLElement {
  return mount(
    createElement(SideThreadPanel, {
      entry,
      mainThreadId: "main-1",
      width: 400,
      composer: createElement("textarea", { "aria-label": "side composer" }),
      onClose: callbacks.onClose ?? (() => {}),
      onResizeStart: callbacks.onResizeStart ?? (() => {}),
      onChangeDraft: callbacks.onChangeDraft ?? (() => {}),
    }),
  );
}

describe("SideThreadPanel", () => {
  it("reserves the measured floating footer height in the scroll flow", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      function getBoundingClientRect(this: HTMLElement) {
        const height = this.classList.contains("side-thread-panel__footer") ? 144 : 0;
        return {
          x: 0,
          y: 0,
          top: 0,
          right: 0,
          bottom: height,
          left: 0,
          width: 0,
          height,
          toJSON: () => ({}),
        };
      },
    );

    const container = renderPanel(makeEntry());
    expect(
      container
        .querySelector<HTMLElement>(".side-thread-panel")
        ?.style.getPropertyValue("--side-thread-footer-height"),
    ).toBe("144px");
  });

  it("exposes safe focus control for the embedded composer", () => {
    const ref = createRef<SideThreadPanelHandle>();
    const container = mount(
      createElement(SideThreadPanel, {
        ref,
        entry: makeEntry(),
        mainThreadId: "main-1",
        width: 400,
        composer: createElement("textarea", { "aria-label": "side composer" }),
        onClose: () => {},
        onResizeStart: () => {},
        onChangeDraft: () => {},
      }),
    );
    act(() => ref.current?.focusComposer());
    expect(document.activeElement).toBe(container.querySelector("textarea"));
  });

  it("keeps shell actions, errors, and resize semantics", () => {
    const onClose = vi.fn();
    const container = renderPanel(makeEntry({ lastError: "rate limited" }), {
      onClose,
    });
    act(() => {
      container.querySelector<HTMLButtonElement>(
        ".side-thread-panel__close",
      )?.click();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(container.querySelector('[role="alert"]')?.textContent).toBe(
      "rate limited",
    );
    expect(
      container
        .querySelector(".side-thread-panel__resizer")
        ?.getAttribute("aria-valuenow"),
    ).toBe("400");
  });
});
