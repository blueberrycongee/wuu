import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationPresentationBoundary, ConversationPresentationCancellation, usePresentedNavigation, useConversationPresentationTransition } from "./ConversationPresentationTransition";

type Pending = { requestId: number; contentReady?: boolean };
const cleanups: Array<() => void> = [];
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
function raster() {
  const transitions: Array<{ callback: () => void; reject: (error: Error) => void; revoked: ReturnType<typeof vi.fn> }> = [];
  const start = vi.fn(() => new Promise<any>((resolve, reject) => {
    const index = transitions.length;
    transitions.push({ callback: () => resolve({ data: new Uint8Array(index + 1), width: innerWidth, height: innerHeight, scaleFactor: 1 }), reject, revoked: vi.fn() });
  }));
  vi.stubGlobal("wuu", { capturePresentationFrame: start });
  vi.stubGlobal("URL", class extends URL {
    static createObjectURL(blob: Blob) { return `blob:${blob.size}`; }
    static revokeObjectURL(url: string) { transitions[Number(url.slice(5)) - 1].revoked(); }
  });
  Object.defineProperty(HTMLImageElement.prototype, "decode", { configurable: true, value: vi.fn(async () => {}) });
  cleanups.push(() => { delete (HTMLImageElement.prototype as any).decode; });
  return { transitions, start };
}
async function mountBoundary() {
  const api = raster();
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  let present!: ReturnType<typeof usePresentedNavigation>;
  function Pane({ pending }: { pending?: Pending & { intentId?: string } }) {
    const ref = React.useRef<HTMLDivElement>(null);
    present = usePresentedNavigation();
    useConversationPresentationTransition({ scopeRef: ref, pending, enabled: true });
    return <div ref={ref}>Current conversation<textarea defaultValue="draft" /></div>;
  }
  type State = { binding?: number; retiring?: boolean; failed?: boolean; pending?: Pending & { intentId?: string } };
  const tree = ({ binding = 1, retiring = false, failed = false, pending }: State = {}) => <ConversationPresentationBoundary binding={binding} retiring={retiring} failed={failed}>
    {retiring ? <div role="status">Real loading</div> : <Pane key={binding} pending={pending} />}
  </ConversationPresentationBoundary>;
  const render = async (state: State = {}) => { await act(async () => root.render(tree(state))); };
  const navigate = async (state: State) => {
    let result!: Promise<void>;
    await act(async () => { result = present(async () => { root.render(tree(state)); }); });
    await act(async () => api.transitions.at(-1)!.callback());
    await result;
  };
  await render(); cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  return { ...api, container, render, navigate, action: <T,>(state: State, operation: () => Promise<T>) => present(() => { root.render(tree(state)); return operation(); }), present: <T,>(action: () => Promise<T>) => present(action) };
}
it("retains one root raster through verified retirement and matching successor reveal", async () => {
  const ui = await mountBoundary();
  await ui.navigate({ pending: { requestId: 5, intentId: "stable-intent" } });
  const scope = ui.container.firstElementChild as HTMLElement;
  await ui.render({ retiring: true });
  expect(ui.transitions[0].revoked).not.toHaveBeenCalled(); expect(scope.inert).toBe(true);
  expect(ui.container.textContent).not.toContain("Real loading");
  await ui.render({ binding: 2, pending: { requestId: 1, intentId: "stable-intent", contentReady: true } });
  expect(ui.start).toHaveBeenCalledOnce(); expect(ui.transitions[0].revoked).not.toHaveBeenCalled();
  await ui.render({ binding: 2 });
  expect(ui.transitions[0].revoked).toHaveBeenCalledOnce(); expect(scope.inert).toBe(false);
  expect(ui.container.textContent).toContain("Current conversation");
});
it("does not adopt unrelated successor counters, and exposes genuine handoff failure", async () => {
  const ui = await mountBoundary();
  await ui.navigate({ pending: { requestId: 1, intentId: "old" } });
  await ui.render({ retiring: true });
  await ui.render({ binding: 2, pending: { requestId: 1, intentId: "other", contentReady: true } });
  expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
  await ui.navigate({ binding: 2, pending: { requestId: 2, intentId: "next" } });
  await ui.render({ binding: 2, retiring: true, failed: true });
  expect(ui.transitions[1].revoked).toHaveBeenCalledOnce(); expect(ui.container.textContent).toContain("Real loading");
});
it("limits retained presentation without completing or cancelling business navigation", async () => {
  vi.useFakeTimers();
  try {
    const ui = await mountBoundary();
    await ui.navigate({ pending: { requestId: 1, intentId: "slow" } });
    await ui.render({ retiring: true });
    await act(async () => vi.advanceTimersByTime(4000));
    expect(ui.transitions[0].revoked).toHaveBeenCalledOnce(); expect(ui.container.textContent).toContain("Real loading");
  } finally { vi.useRealTimers(); }
});

it("starts only the latest A → B → A action after capture, never obsolete callbacks", async () => {
  const ui = await mountBoundary(), oldA = vi.fn(async () => "old-a"), oldB = vi.fn(async () => "old-b"), latestA = vi.fn(async () => "new-a");
  let first!: Promise<unknown>, second!: Promise<unknown>, latest!: Promise<unknown>;
  await act(async () => { first = ui.present(oldA).catch(error => error); second = ui.present(oldB).catch(error => error); latest = ui.present(latestA); });
  expect(oldA).not.toHaveBeenCalled(); expect(oldB).not.toHaveBeenCalled(); expect(latestA).not.toHaveBeenCalled();
  await act(async () => { for (const transition of ui.transitions) transition.callback(); });
  expect(await first).toBeInstanceOf(ConversationPresentationCancellation); expect(await second).toBeInstanceOf(ConversationPresentationCancellation);
  expect(await latest).toBe("new-a"); expect(oldA).not.toHaveBeenCalled(); expect(oldB).not.toHaveBeenCalled(); expect(latestA).toHaveBeenCalledOnce();
});
it("unsupported capture starts the existing action immediately and preserves genuine failures", async () => {
  const ui = await mountBoundary(); vi.stubGlobal("wuu", {});
  const failure = new Error("real action failed"), action = vi.fn(async () => { throw failure; });
  await expect(ui.present(action)).rejects.toBe(failure); expect(action).toHaveBeenCalledOnce();
  expect(ui.start).not.toHaveBeenCalled();
});
it("runs the action once if own-window capture rejects", async () => {
  const ui = await mountBoundary(); const action = vi.fn(async () => "done");
  let result!: Promise<string>; await act(async () => { result = ui.present(action); });
  await act(async () => ui.transitions[0].reject(new Error("unavailable")));
  expect(await result).toBe("done"); expect(action).toHaveBeenCalledOnce();
});

it("does not retain a frame across an unrelated unmount without provider retirement", async () => {
  const ui = await mountBoundary();
  await ui.navigate({ pending: { requestId: 7, intentId: "original" } });
  await ui.render({ binding: 2 });
  expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
  expect(ui.container.firstElementChild!.hasAttribute("data-wuu-conversation-transition")).toBe(false);
});

it.each([true, false])("preserves only proven source retirement during a verified handoff: %s", async expectedRetirement => {
  const ui = await mountBoundary();
  let reject!: (error: Error) => void;
  const operation = new Promise<void>((_resolve, no) => { reject = no; });
  let result!: Promise<unknown>;
  await act(async () => { result = ui.action({ pending: { requestId: 1, intentId: "handoff" } }, () => operation).catch(error => error); });
  await act(async () => ui.transitions[0].callback());
  await ui.render({ retiring: true });
  const error = expectedRetirement ? new PluginLifecycleCancellationError() : new Error("Actual action failed");
  await act(async () => reject(error)); expect(await result).toBe(error);
  expect(ui.transitions[0].revoked).toHaveBeenCalledTimes(expectedRetirement ? 0 : 1);
  if (expectedRetirement) {
    await ui.render({ binding: 2, pending: { requestId: 1, intentId: "handoff", contentReady: true } });
    await ui.render({ binding: 2 }); expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
  }
});

it("dispatches after the short capture budget and never repeats a late capture result", async () => {
  const ui = await mountBoundary(); vi.useFakeTimers();
  try {
    const action = vi.fn(async () => "done");
    let result!: Promise<string>; await act(async () => { result = ui.present(action); });
    await act(async () => vi.advanceTimersByTime(119)); expect(action).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTime(1)); expect(await result).toBe("done");
    expect(ui.transitions[0].revoked).not.toHaveBeenCalled();
    await act(async () => ui.transitions[0].callback()); expect(action).toHaveBeenCalledOnce();
  } finally { vi.useRealTimers(); }
});
it("never dispatches superseded requests when the capture budget expires", async () => {
  const ui = await mountBoundary(); vi.useFakeTimers();
  try {
    const old = vi.fn(async () => "old"), latest = vi.fn(async () => "latest");
    let obsolete!: Promise<unknown>, result!: Promise<string>;
    await act(async () => { obsolete = ui.present(old).catch(error => error); result = ui.present(latest); });
    expect(await obsolete).toBeInstanceOf(ConversationPresentationCancellation);
    await act(async () => vi.advanceTimersByTime(120)); expect(await result).toBe("latest");
    await act(async () => ui.transitions.forEach(item => item.callback()));
    expect(old).not.toHaveBeenCalled(); expect(latest).toHaveBeenCalledOnce();
  } finally { vi.useRealTimers(); }
});
it("starts hidden-document navigation immediately without capturing", async () => {
  const ui = await mountBoundary();
  const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  try {
    const action = vi.fn(async () => "done");
    expect(await ui.present(action)).toBe("done"); expect(action).toHaveBeenCalledOnce(); expect(ui.start).not.toHaveBeenCalled();
  } finally { visibility.mockRestore(); }
});
it("keeps the held image inert and releases its blob on resize", async () => {
  const ui = await mountBoundary();
  await ui.navigate({ pending: { requestId: 1, intentId: "resize" } });
  const image = document.querySelector<HTMLImageElement>("[data-wuu-presentation-frame]")!;
  expect(image.style.pointerEvents).toBe("none"); expect(image.inert).toBe(true); expect(image.getAttribute("aria-hidden")).toBe("true");
  expect((ui.container.firstElementChild as HTMLElement).inert).not.toBe(true);
  await act(async () => window.dispatchEvent(new Event("resize")));
  expect(document.querySelector("[data-wuu-presentation-frame]")).toBeNull(); expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
});
it("falls back if image decoding fails and releases the allocated blob", async () => {
  const ui = await mountBoundary(); vi.mocked(HTMLImageElement.prototype.decode).mockRejectedValueOnce(new Error("bad image"));
  const action = vi.fn(async () => "done"); let result!: Promise<string>;
  await act(async () => { result = ui.present(action); }); await act(async () => ui.transitions[0].callback());
  expect(await result).toBe("done"); expect(action).toHaveBeenCalledOnce(); expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
  expect(document.querySelector("[data-wuu-presentation-frame]")).toBeNull();
});
it("revokes a decoding frame at the capture deadline and ignores its late decode", async () => {
  const ui = await mountBoundary(); vi.useFakeTimers();
  let decoded!: () => void; vi.mocked(HTMLImageElement.prototype.decode).mockReturnValueOnce(new Promise<void>(resolve => { decoded = resolve; }));
  try {
    const action = vi.fn(async () => "done"); let result!: Promise<string>;
    await act(async () => { result = ui.present(action); }); await act(async () => ui.transitions[0].callback());
    await act(async () => vi.advanceTimersByTime(120)); expect(await result).toBe("done");
    await act(async () => decoded()); expect(action).toHaveBeenCalledOnce(); expect(ui.transitions[0].revoked).toHaveBeenCalledOnce();
    expect(document.querySelector("[data-wuu-presentation-frame]")).toBeNull();
  } finally { vi.useRealTimers(); }
});
it("treats a synchronous host capture exception as optional and dispatches once", async () => {
  const ui = await mountBoundary(); ui.start.mockImplementationOnce(() => { throw new Error("host unavailable"); });
  const action = vi.fn(async () => "done");
  let result!: Promise<string>; await act(async () => { result = ui.present(action); });
  expect(await result).toBe("done"); expect(action).toHaveBeenCalledOnce();
});
