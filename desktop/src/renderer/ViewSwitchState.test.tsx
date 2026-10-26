import { act, createElement, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useConversationSwitchReady } from "./ConversationSwitchReady";
import { ViewSwitchLoading } from "./LoadingViews";
import { useApplicationNavigationState } from "./modules/ApplicationNavigationState";
import { UICompositionPendingContext } from "./plugins/ApplicationShell";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
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


it.each([true, false])("releases navigation when the conversation viewport is temporarily absent (mounts=%s)", async mounts => {
  const frames = new Map<number, FrameRequestCallback>();
  let nextFrame = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
    frames.set(++nextFrame, callback); return nextFrame;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
  const paint = () => act(() => {
    const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(0));
  });
  const viewportRef = { current: null as HTMLDivElement | null };
  const finishViewSwitch = vi.fn(() => true);
  const settleLayout = vi.fn();
  function Probe() {
    useConversationSwitchReady({ pendingViewSwitch: { kind: "thread", targetID: "target", visible: true, restoreRequestID: 7 },
      activeThreadID: "target", viewportRef, settleLayout, finishViewSwitch });
    return null;
  }
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  await act(async () => { root.render(createElement(Probe)); });
  paint();
  expect(finishViewSwitch).not.toHaveBeenCalled();
  if (mounts) viewportRef.current = document.createElement("div");
  for (let index = 0; index < 8; index++) paint();
  expect(finishViewSwitch).toHaveBeenCalledExactlyOnceWith(7);
  expect(settleLayout.mock.calls.length > 0).toBe(mounts);
});


it.each(["workspace", "runtime", "tab"] as const)("reveals a verified %s destination with or without a thread and fences an old completion", async kind => {
  const frames = new Map<number, FrameRequestCallback>();
  let nextFrame = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
    frames.set(++nextFrame, callback); return nextFrame;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
  const paint = () => act(() => {
    const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(0));
  });
  let tabs = { navigationRevision: 1, pending: { kind, targetId: "destination-address", requestId: 1, contentReady: false } } as ApplicationSnapshotV1["tabs"];
  const listeners = new Set<() => void>();
  const publish = () => { for (const listener of listeners) listener(); };
  const invoke = vi.fn(async (_action: string, input: { requestId: number }) => {
    if (input.requestId === tabs.pending?.requestId) { tabs = { ...tabs, pending: undefined }; publish(); }
  });
  const application = { getSnapshot: () => ({ tabs }), subscribe: (listener: () => void) => {
    listeners.add(listener); return () => listeners.delete(listener);
  }, invoke } as unknown as ApplicationServiceV1;
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  let surfacesPending = false;
  function Probe() {
    const navigation = useApplicationNavigationState(application);
    const viewportRef = useRef<HTMLDivElement>(null);
    useConversationSwitchReady({ pendingViewSwitch: navigation.pendingViewSwitch, activeThreadID: tabs.primaryThreadId,
      viewportRef, settleLayout: () => {}, finishViewSwitch: navigation.finishViewSwitch });
    return createElement("div", { ref: viewportRef }, createElement("textarea", { disabled: navigation.submissionTargetPending }));
  }
  const render = () => act(async () => { root.render(createElement(UICompositionPendingContext.Provider,
    { value: surfacesPending }, createElement(Probe))); });
  await render();
  for (let index = 0; index < 8; index++) paint();
  expect(invoke).not.toHaveBeenCalled();
  expect(container.querySelector("textarea")!.disabled).toBe(true);
  // Destination state is authoritative, but its selected surfaces still need to mount.
  surfacesPending = true;
  tabs = { ...tabs, pending: { ...tabs.pending!, contentReady: true } };
  await render();
  for (let index = 0; index < 8; index++) paint();
  expect(invoke).not.toHaveBeenCalled();
  surfacesPending = false; await render();
  paint(); paint(); paint();
  expect(invoke).toHaveBeenLastCalledWith("navigation.reveal-complete", { requestId: 1 });
  expect(container.querySelector("textarea")!.disabled).toBe(false);
  act(() => { tabs = { ...tabs, navigationRevision: 2,
    pending: { kind, targetId: "destination-address", requestId: 2, contentReady: true } }; publish(); });
  paint(); paint();
  const staleFrame = [...frames.values()][0];
  expect(staleFrame).toBeDefined();
  // A new intent can use the same destination address. Its request owns reveal.
  act(() => { tabs = { ...tabs, navigationRevision: 3, pending: { ...tabs.pending!, requestId: 3, contentReady: false } }; publish(); });
  act(() => { staleFrame(0); });
  for (let index = 0; index < 8; index++) paint();
  expect(invoke).toHaveBeenCalledTimes(1);
  expect(container.querySelector("textarea")!.disabled).toBe(true);
  act(() => { tabs = { ...tabs, pending: { ...tabs.pending!, contentReady: true } }; publish(); });
  paint(); paint(); paint();
  expect(invoke).toHaveBeenLastCalledWith("navigation.reveal-complete", { requestId: 3 });
  expect(container.querySelector("textarea")!.disabled).toBe(false);
  // Workspace/tab addresses also differ from the thread shown at that destination.
  act(() => { tabs = { ...tabs, primaryThreadId: "loaded-thread", navigationRevision: 4,
    pending: { kind, targetId: "destination-address", requestId: 4, contentReady: true } }; publish(); });
  paint(); paint(); paint();
  expect(invoke).toHaveBeenLastCalledWith("navigation.reveal-complete", { requestId: 4 });
  expect(invoke).toHaveBeenCalledTimes(3);
  expect(container.querySelector("textarea")!.disabled).toBe(false);
});

it("does not reveal a ready thread intent while a different thread is active", async () => {
  const frames = new Map<number, FrameRequestCallback>(); let nextFrame = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => { frames.set(++nextFrame, callback); return nextFrame; });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
  const finishViewSwitch = vi.fn(() => true), viewportRef = { current: document.createElement("div") };
  function Probe({ activeThreadID }: { activeThreadID: string }) {
    useConversationSwitchReady({ pendingViewSwitch: { kind: "thread", targetID: "expected-thread", visible: true, restoreRequestID: 4 },
      activeThreadID, viewportRef, settleLayout: () => {}, finishViewSwitch });
    return null;
  }
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  await act(async () => { root.render(createElement(Probe, { activeThreadID: "different-thread" })); });
  expect(frames.size).toBe(0); expect(finishViewSwitch).not.toHaveBeenCalled();
  await act(async () => { root.render(createElement(Probe, { activeThreadID: "expected-thread" })); });
  for (let index = 0; index < 3; index++) act(() => {
    const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(0));
  });
  expect(finishViewSwitch).toHaveBeenCalledExactlyOnceWith(4);
});
