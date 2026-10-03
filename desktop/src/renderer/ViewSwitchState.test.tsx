import { act, createElement, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useConversationSwitchReady } from "./ConversationSwitchReady";
import { ViewSwitchLoading } from "./LoadingViews";
import {
  useViewSwitchState,
  type ViewSwitchStateController,
} from "./ViewSwitchState";

let mountedRoots: Root[] = [];

afterEach(() => {
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  document.body.innerHTML = "";
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function renderViewSwitchState(): Promise<{
  get: () => ViewSwitchStateController;
}> {
  let latest: ViewSwitchStateController | undefined;

  function Probe(): JSX.Element | null {
    latest = useViewSwitchState({ loadingDelayMs: 50 });
    return latest.pendingViewSwitch?.visible ? createElement(ViewSwitchLoading) : null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  await act(async () => {
    root.render(createElement(Probe));
  });

  return {
    get: () => {
      if (!latest) {
        throw new Error("view switch state was not rendered");
      }
      return latest;
    },
  };
}

describe("useViewSwitchState", () => {
  it("covers threads and blocks sends through snapshot and layout restoration", async () => {
    const hook = await renderViewSwitchState();
    let id = 0;
    act(() => { id = hook.get().beginViewSwitch("thread", "target"); });
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    expect(hook.get().submissionTargetPending).toBe(true);
    act(() => { hook.get().prepareThreadReveal(id); });
    expect(hook.get().pendingViewSwitch?.restoreRequestID).toBe(id);
    expect(hook.get().submissionTargetPending).toBe(true);
    act(() => { hook.get().finishViewSwitch(id); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().submissionTargetPending).toBe(false);
  });

  it.each(["replacement", "cancel"])("ignores stale resume and reveal after %s", async action => {
    const hook = await renderViewSwitchState();
    let id = 0;
    act(() => {
      id = hook.get().beginViewSwitch("thread", "old");
      hook.get().prepareThreadReveal(id);
      if (action === "cancel") hook.get().cancelViewSwitch();
      else hook.get().beginViewSwitch("thread", "new");
    });
    expect(hook.get().isCurrentViewSwitchRequest(id)).toBe(false);
    expect(hook.get().prepareThreadReveal(id)).toBe(false);
    expect(hook.get().finishViewSwitch(id)).toBe(false);
    expect(hook.get().pendingViewSwitch?.targetID).toBe(action === "cancel" ? undefined : "new");
  });

  it("preserves workspace loading delay and cancels its timer", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();
    act(() => { hook.get().beginViewSwitch("workspace", "project"); });
    expect(hook.get().visiblePendingWorkspaceID).toBe("project");
    expect(document.querySelector('[role="status"]')).toBeNull();
    act(() => { vi.advanceTimersByTime(50); });
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    act(() => { hook.get().cancelViewSwitch(); vi.advanceTimersByTime(100); });
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it("waits for resumed layout and scroll to settle before revealing", async () => {
    const frames = new Map<number, FrameRequestCallback>();
    let nextFrame = 0;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
    const paint = () => act(() => {
      const callbacks = [...frames.values()];
      frames.clear();
      callbacks.forEach(callback => callback(0));
    });
    let controller!: ViewSwitchStateController;
    const settleLayout = vi.fn();
    function Probe() {
      controller = useViewSwitchState();
      const viewportRef = useRef<HTMLDivElement>(null);
      useConversationSwitchReady({ pendingViewSwitch: controller.pendingViewSwitch,
        activeThreadID: "target", viewportRef, settleLayout, finishViewSwitch: controller.finishViewSwitch });
      return createElement("div", { ref: viewportRef });
    }
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    mountedRoots.push(root);
    await act(async () => { root.render(createElement(Probe)); });
    let id = 0;
    act(() => { id = controller.beginViewSwitch("thread", "target"); });
    paint(); paint();
    expect(settleLayout).not.toHaveBeenCalled();
    act(() => { controller.prepareThreadReveal(id); });
    const viewport = container.firstElementChild as HTMLElement;
    let height = 400;
    Object.defineProperty(viewport, "scrollHeight", { get: () => height });
    paint();
    height = 600;
    paint();
    expect(controller.submissionTargetPending).toBe(true);
    paint();
    expect(controller.submissionTargetPending).toBe(true);
    paint();
    expect(controller.submissionTargetPending).toBe(false);
    expect(settleLayout).toHaveBeenCalled();
  });
});
