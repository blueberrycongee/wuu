import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
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
  it("blocks sends immediately but only marks an uncached thread busy after the loading delay", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();

    let requestID = 0;
    act(() => {
      requestID = hook.get().beginViewSwitch("thread", "thread-1");
    });

    expect(hook.get().pendingViewSwitch).toEqual({
      kind: "thread",
      targetID: "thread-1",
      visible: false,
    });
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    expect(hook.get().viewSwitchPending).toBe(true);
    expect(hook.get().viewContextSwitchPending).toBe(false);
    act(() => { vi.advanceTimersByTime(49); });
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    act(() => { vi.advanceTimersByTime(1); });
    expect(hook.get().visiblePendingThreadID).toBe("thread-1");
    act(() => { hook.get().finishViewSwitch(requestID); });
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    expect(hook.get().viewSwitchPending).toBe(false);
  });

  it("marks the selected project immediately but delays the animation", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();

    act(() => {
      hook.get().beginViewSwitch("project", "project-1");
    });
    expect(hook.get().pendingViewSwitch).toEqual({
      kind: "project",
      targetID: "project-1",
      visible: false,
    });
    expect(hook.get().visiblePendingProjectID).toBe("project-1");
    expect(hook.get().viewContextSwitchPending).toBe(true);
  });

  it("rejects stale finish requests and keeps the newest pending switch", async () => {
    const hook = await renderViewSwitchState();

    let staleRequest = 0;
    let currentRequest = 0;
    act(() => {
      staleRequest = hook.get().beginViewSwitch("thread", "thread-old");
      currentRequest = hook.get().beginViewSwitch("thread", "thread-new");
    });

    expect(hook.get().finishViewSwitch(staleRequest)).toBe(false);
    expect(hook.get().pendingViewSwitch?.targetID).toBe("thread-new");
    let finished = false;
    act(() => {
      finished = hook.get().finishViewSwitch(currentRequest);
    });
    expect(finished).toBe(true);
    expect(hook.get().pendingViewSwitch).toBeUndefined();
  });

  it("keeps cached thread switches send-blocked without marking the tab busy or covering content during a slow resume", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();

    let requestID = 0;
    act(() => {
      requestID = hook.get().beginInstantThreadSwitch("thread-cached");
    });

    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    expect(hook.get().viewSwitchPending).toBe(true);
    act(() => { vi.advanceTimersByTime(5_000); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().pendingViewSwitch).toEqual({
      kind: "thread",
      targetID: "thread-cached",
      visible: false,
    });
    expect(hook.get().viewSwitchPending).toBe(true);
    expect(hook.get().viewContextSwitchPending).toBe(false);
    expect(hook.get().visiblePendingThreadID).toBeUndefined();

    act(() => {
      expect(hook.get().finishViewSwitch(requestID)).toBe(true);
    });
    expect(hook.get().pendingViewSwitch).toBeUndefined();
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    expect(hook.get().viewSwitchPending).toBe(false);
  });

  it.each(["thread", "project", "runtime"] as const)(
    "shows the shared animation only while a slow %s switch is pending",
    async (kind) => {
      vi.useFakeTimers();
      const hook = await renderViewSwitchState();
      let requestID = 0;
      act(() => {
        requestID = hook.get().beginViewSwitch(kind, "target");
      });
      expect(document.querySelector('[role="status"]')).toBeNull();
      act(() => { vi.advanceTimersByTime(50); });
      expect(document.querySelector('[role="status"]')).not.toBeNull();
      act(() => { hook.get().finishViewSwitch(requestID); });
      expect(document.querySelector('[role="status"]')).toBeNull();
    },
  );

  it.each([25, 50])("clears a previous loading switch after %ims when selecting a cached tab", async (elapsedMs) => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();
    let oldRequest = 0;
    act(() => { oldRequest = hook.get().beginViewSwitch("thread", "uncached"); });
    act(() => { vi.advanceTimersByTime(elapsedMs); });
    expect(hook.get().pendingViewSwitch?.visible).toBe(elapsedMs >= 50);
    expect(hook.get().visiblePendingThreadID).toBe(elapsedMs >= 50 ? "uncached" : undefined);

    let cachedRequest = 0;
    act(() => { cachedRequest = hook.get().beginInstantThreadSwitch("cached"); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    act(() => { vi.advanceTimersByTime(5_000); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().finishViewSwitch(oldRequest)).toBe(false);
    expect(hook.get().isCurrentViewSwitchRequest(oldRequest)).toBe(false);
    expect(hook.get().isCurrentViewSwitchRequest(cachedRequest)).toBe(true);
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    expect(hook.get().pendingViewSwitch).toEqual({
      kind: "thread",
      targetID: "cached",
      visible: false,
    });
    expect(hook.get().viewSwitchPending).toBe(true);
    act(() => { hook.get().finishViewSwitch(cachedRequest); });
    expect(hook.get().pendingViewSwitch).toBeUndefined();
  });

  it("still shows loading for an uncached switch after a cached switch", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();
    let cachedRequest = 0;
    act(() => { cachedRequest = hook.get().beginInstantThreadSwitch("cached"); });
    act(() => { hook.get().beginViewSwitch("thread", "uncached"); });
    expect(hook.get().finishViewSwitch(cachedRequest)).toBe(false);
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    act(() => { vi.advanceTimersByTime(50); });
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    expect(hook.get().visiblePendingThreadID).toBe("uncached");
  });

  it("does not show a completed or cancelled switch after the delay", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();
    act(() => {
      const requestID = hook.get().beginInstantThreadSwitch("fast");
      hook.get().finishViewSwitch(requestID);
    });
    act(() => { vi.advanceTimersByTime(100); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
    act(() => {
      hook.get().beginViewSwitch("thread", "cancelled");
      hook.get().cancelViewSwitch();
    });
    act(() => { vi.advanceTimersByTime(100); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().visiblePendingThreadID).toBeUndefined();
  });

  it("gives a replacement switch its own delay and ignores late completion", async () => {
    vi.useFakeTimers();
    const hook = await renderViewSwitchState();
    let oldRequest = 0;
    act(() => { oldRequest = hook.get().beginViewSwitch("thread", "old"); });
    act(() => { vi.advanceTimersByTime(40); });
    act(() => { hook.get().beginViewSwitch("thread", "new"); });
    act(() => { vi.advanceTimersByTime(10); });
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(hook.get().finishViewSwitch(oldRequest)).toBe(false);
    act(() => { vi.advanceTimersByTime(40); });
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    expect(hook.get().pendingViewSwitch?.targetID).toBe("new");
  });

  it("cancel invalidates in-flight request IDs", async () => {
    const hook = await renderViewSwitchState();

    let requestID = 0;
    act(() => {
      requestID = hook.get().beginInstantThreadSwitch();
      hook.get().cancelViewSwitch();
    });

    expect(hook.get().isCurrentViewSwitchRequest(requestID)).toBe(false);
    expect(hook.get().pendingViewSwitch).toBeUndefined();
  });
});
