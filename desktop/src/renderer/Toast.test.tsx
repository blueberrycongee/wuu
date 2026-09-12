import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  clearToasts,
  showErrorToast,
  showToast,
  ToastViewport,
  toastErrorMessage,
} from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  clearToasts();
  container = document.createElement("div");
  document.body.appendChild(container);
  act(() => {
    root = createRoot(container);
    root.render(
      <WuuUIRoot>
        <ToastViewport />
      </WuuUIRoot>,
    );
  });
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
  clearToasts();
  vi.useRealTimers();
});

describe("ToastViewport", () => {
  it("removes Electron IPC wrappers from operation errors", () => {
    expect(toastErrorMessage(new Error(
      "Error invoking remote method 'wuu:channel-agent-update': Error: named agent cannot be edited while it is running",
    ))).toBe("named agent cannot be edited while it is running");
    expect(toastErrorMessage(
      "Error: Error invoking remote method 'wuu:channel-agent-update': Error: save failed",
    )).toBe("save failed");
  });

  it("renders operation errors as global alert toasts", () => {
    act(() => {
      showErrorToast(new Error("save failed"));
    });

    const notice = container.querySelector('[role="alert"]');
    expect(notice?.textContent).toContain("save failed");
    expect((notice as HTMLElement | null)?.dataset.wuuComponent).toBe("notice");
    expect((notice as HTMLElement | null)?.dataset.wuuLayer).toBe("notice");
    expect((notice as HTMLElement | null)?.dataset.wuuState).toBe("open");
    expect(notice?.closest('[data-wuu-layer-host="true"]')).not.toBeNull();
  });

  it("queues distinct notices and suppresses repeated failures", () => {
    act(() => {
      showErrorToast("first failure");
      showErrorToast("first failure");
      showToast({ message: "second notice" });
    });
    expect(container.querySelector(".archive-tip-message")?.textContent).toBe("first failure");

    act(() => {
      vi.advanceTimersByTime(6_000);
    });
    expect(container.querySelector(".archive-tip-message")?.textContent).toBe("second notice");

    act(() => {
      vi.advanceTimersByTime(6_000);
    });
    expect(container.querySelector(".archive-tip")).toBeNull();
  });
});
