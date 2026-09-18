import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useConversationScrollState } from "./ConversationScrollState";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { PluginHost } from "./plugins/PluginHost";

let api: ReturnType<typeof useConversationScrollState>;
let root: Root;
let host: HTMLDivElement;
let naturalHeight: number;
let top: number;
let pending: Map<number, FrameRequestCallback>;
let nextFrame: number;

function Probe({ id = "a", running = true, status = true, pluginHost, onOpenSession = () => undefined }: { id?: string; running?: boolean; status?: boolean; pluginHost?: PluginHost; onOpenSession?: (id: string) => void }) {
  api = useConversationScrollState({ activeThreadID: id, activePane: "primary", splitConversation: false, emptyConversation: false, initialized: true, running, nativeScrollBounce: false });
  return <main ref={api.conversationPaneRef}>
    <div ref={node => {
      api.conversationScrollRef.current = node;
      if (!node) return;
      Object.defineProperties(node, {
        clientHeight: { configurable: true, get: () => 600 },
        scrollHeight: { configurable: true, get: () => naturalHeight + tailSpace() },
        scrollTop: { configurable: true, get: () => Math.min(top, node.scrollHeight - 600), set: value => { top = Math.max(0, Math.min(value, node.scrollHeight - 600)); } },
      });
    }} onScroll={() => api.handleConversationScroll()}>
      <div ref={api.scrollContentRef} />
    </div>
    {pluginHost ? <ConversationStatusCluster host={pluginHost} visible threadId={id}
      clusterRef={api.statusClusterRef} onOpenSession={onOpenSession}
      todoUpdate={{ todos: [{ content: "Keep history controls accessible", status: "in_progress" }] }}
    /> : status ? <div ref={api.statusClusterRef} /> : null}
  </main>;
}

// jsdom has no layout engine; model only the measured tail's contribution to
// scroll range. Assertions below concern viewport ownership and lifecycle.
function tailSpace() {
  return Number.parseFloat(host.querySelector("main")?.style.getPropertyValue("--session-tail-space") || "0");
}
function render(props: Parameters<typeof Probe>[0] = {}) { act(() => root.render(<Probe {...props} />)); }
function tick(now: number) {
  act(() => {
    const callbacks = [...pending.values()];
    pending.clear();
    callbacks.forEach(callback => callback(now));
  });
}
function scrollUp(amount: number) {
  const node = api.conversationScrollRef.current!;
  act(() => {
    node.dispatchEvent(new WheelEvent("wheel", { deltaY: -amount }));
    node.scrollTop -= amount;
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
}

beforeEach(() => {
  naturalHeight = 2000;
  top = 0;
  nextFrame = 0;
  pending = new Map();
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => { pending.set(++nextFrame, callback); return nextFrame; });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { pending.delete(id); });
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ height: 34 } as DOMRect);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("consumes run space while browsing without moving the history viewport or re-adding it on stream ticks", () => {
  render();
  const initial = tailSpace();
  const before = top;
  scrollUp(150);
  expect(top).toBe(before - 150);
  expect(tailSpace()).toBeLessThan(initial);
  const consumed = tailSpace();
  naturalHeight += 300;
  act(() => api.scheduleStreamScroll());
  tick(1000);
  expect(top).toBe(before - 150);
  expect(tailSpace()).toBe(consumed);
});

it("does not pull a history reader to the bottom on submit or a new run", () => {
  render({ running: false });
  scrollUp(250);
  const away = top;
  act(() => api.requestSubmittedQueryScroll());
  render({ running: true });
  tick(0); tick(300);
  expect(top).toBe(away);
});

it("keeps TODO focus and plugin actions available while browsing without reclaiming the viewport", async () => {
  const pluginHost = new PluginHost({ react: React });
  const onOpenSession = vi.fn();
  await pluginHost.activateGeneration({
    pluginId: "history-status", generation: "one",
    register(registry) {
      registry.registerComposerStatusSource({
        id: "history-action",
        getSnapshot: () => [{ id: "action", label: "Inspect task", state: "running", action: { kind: "open-session", sessionId: "worker" } }],
        subscribe: () => () => undefined,
      });
    },
  });
  render({ pluginHost, onOpenSession });
  scrollUp(300);
  const away = top;
  const todo = host.querySelector<HTMLElement>(".conversation-status-todo-trigger")!;
  act(() => todo.focus());
  expect(document.activeElement).toBe(todo);
  expect(todo.closest("[inert]")).toBeNull();
  const action = host.querySelector<HTMLButtonElement>("button")!;
  act(() => action.click());
  expect(onOpenSession).toHaveBeenCalledWith("worker");
  naturalHeight += 200;
  act(() => api.scheduleStreamScroll());
  tick(1000);
  expect(top).toBe(away);
});

it("retires temporary space when the run ends without a one-frame jump", () => {
  render();
  const position = () => api.conversationScrollRef.current!.scrollTop;
  const before = position();
  render({ running: false, status: false });
  expect(position()).toBe(before);
  tick(0); tick(110);
  expect(position()).toBeLessThan(before);
  expect(position()).toBeGreaterThan(naturalHeight - 600);
  tick(220);
  expect(position()).toBe(naturalHeight - 600);
  expect(tailSpace()).toBe(0);
});

it("does not overwrite an incoming thread's history snapshot with outgoing tail cleanup", () => {
  render();
  scrollUp(300);
  const away = top;
  render({ id: "b", running: false, status: false });
  expect(tailSpace()).toBe(0);
  render({ id: "a" });
  expect(top).toBe(away);
  tick(1000);
  expect(top).toBe(away);
});

it("keeps history fixed when the run and its status capsule end together", () => {
  render();
  scrollUp(300);
  const away = top;
  render({ running: false, status: false });
  tick(0); tick(220);
  expect(top).toBe(away);
  expect(tailSpace()).toBe(0);
});

it("reserves independently visible status chrome after a run but not the larger run gap", () => {
  render();
  const runGap = tailSpace();
  render({ running: false });
  tick(0); tick(220);
  expect(tailSpace()).toBeGreaterThan(34);
  expect(tailSpace()).toBeLessThan(runGap);
});

it("finishes retiring space if a small upward gesture interrupts completion", () => {
  render();
  render({ running: false, status: false });
  tick(0);
  scrollUp(8);
  tick(50); tick(300);
  expect(tailSpace()).toBe(0);
  naturalHeight += 200;
  const away = api.conversationScrollRef.current!.scrollTop;
  act(() => api.scheduleStreamScroll());
  tick(400);
  expect(api.conversationScrollRef.current!.scrollTop).toBe(away);
});
