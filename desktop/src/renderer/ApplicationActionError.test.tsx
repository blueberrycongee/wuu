import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import { showApplicationActionError } from "./ApplicationActionError";
import { clearToasts, ToastViewport } from "./Toast";

it("reports a repeated explicit action failure after dismissal without reporting retired work", () => {
  vi.useFakeTimers();
  clearToasts();
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    act(() => root.render(<ToastViewport />));
    act(() => showApplicationActionError(new Error("Action failed")));
    expect(document.body.querySelector('[role="alert"]')).not.toBeNull();
    act(() => document.body.querySelector<HTMLButtonElement>('[role="alert"] button')!.click());
    act(() => vi.advanceTimersByTime(1000));
    expect(document.body.querySelector('[role="alert"]')).toBeNull();
    act(() => showApplicationActionError(new Error("Action failed")));
    expect(document.body.querySelector('[role="alert"]')).not.toBeNull();
    act(() => clearToasts());
    act(() => showApplicationActionError(new PluginLifecycleCancellationError()));
    expect(document.body.querySelector('[role="alert"]')).toBeNull();
  } finally {
    act(() => root.unmount());
    clearToasts();
    container.remove();
    vi.useRealTimers();
  }
});
