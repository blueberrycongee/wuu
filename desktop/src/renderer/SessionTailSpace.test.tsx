import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { submittedMessageScrollTop, useConversationScrollState } from "./ConversationScrollState";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { PluginHost } from "./plugins/PluginHost";
import { SESSION_TAIL_SPACE_MAX_PX, SESSION_TAIL_SPACE_MIN_PX, SESSION_TAIL_SPACE_RATIO } from "./SessionTailSpace";

let api: ReturnType<typeof useConversationScrollState>;
let root: Root;
let host: HTMLDivElement;
let naturalHeight: number;
let viewportHeight: number;
let top: number;
let pending: Map<number, FrameRequestCallback>;
let nextFrame: number;

function Probe({ id = "a", running = true, status = true, split = false, submittedMessage = false, pluginHost, onOpenSession = () => undefined }: { id?: string; running?: boolean; status?: boolean; split?: boolean; submittedMessage?: boolean; pluginHost?: PluginHost; onOpenSession?: (id: string) => void }) {
  api = useConversationScrollState({ activeThreadID: id, activePane: "primary", splitConversation: split, emptyConversation: false, initialized: true, running, nativeScrollBounce: false });
  return <main ref={api.conversationPaneRef}>
    <div ref={node => {
      api.conversationScrollRef.current = node;
      if (!node) return;
      Object.defineProperties(node, {
        clientHeight: { configurable: true, get: () => viewportHeight },
        scrollHeight: { configurable: true, get: () => naturalHeight + tailSpace() },
        scrollTop: { configurable: true, get: () => Math.min(top, node.scrollHeight - viewportHeight), set: value => { top = Math.max(0, Math.min(value, node.scrollHeight - viewportHeight)); } },
      });
    }} onScroll={() => api.handleConversationScroll()}>
      <div ref={api.scrollContentRef}>{submittedMessage ? <div data-user-message-id="submitted" /> : null}</div>
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
  viewportHeight = 600;
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

it("sizes ordinary-session run space from the viewport with tunable bounds", () => {
  render();
  expect(tailSpace()).toBe(Math.round(viewportHeight * SESSION_TAIL_SPACE_RATIO));

  viewportHeight = 300;
  render({ running: false, status: false });
  render({ running: true });
  expect(tailSpace()).toBe(SESSION_TAIL_SPACE_MIN_PX);

  viewportHeight = 1600;
  render({ running: false, status: false });
  render({ running: true });
  expect(tailSpace()).toBe(SESSION_TAIL_SPACE_MAX_PX);
});

it("does not add ordinary-session tail space to collaboration panes", () => {
  render({ split: true });
  expect(tailSpace()).toBe(0);
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

it("anchors the submitted message block from live viewport and rect measurements", () => {
  const viewport = document.createElement("div");
  const message = document.createElement("div");
  Object.defineProperties(viewport, {
    clientHeight: { configurable: true, value: 600 },
    scrollHeight: { configurable: true, value: 2000 },
    scrollTop: { configurable: true, writable: true, value: 900 },
  });
  vi.spyOn(viewport, "getBoundingClientRect").mockReturnValue({ top: 100 } as DOMRect);
  vi.spyOn(message, "getBoundingClientRect").mockReturnValue({ top: 500, bottom: 580, height: 80 } as DOMRect);

  expect(submittedMessageScrollTop(viewport, message)).toBe(1180);
});

it("anchors a long submitted message by its bottom edge", () => {
  const viewport = document.createElement("div");
  const message = document.createElement("div");
  Object.defineProperties(viewport, {
    clientHeight: { configurable: true, value: 600 },
    scrollHeight: { configurable: true, value: 3000 },
    scrollTop: { configurable: true, writable: true, value: 900 },
  });
  vi.spyOn(viewport, "getBoundingClientRect").mockReturnValue({ top: 100 } as DOMRect);
  vi.spyOn(message, "getBoundingClientRect").mockReturnValue({ top: 500, bottom: 900, height: 400 } as DOMRect);

  // The long block is still anchored in the upper band; its top is not used
  // as the target, which would push the reading context below the message.
  expect(submittedMessageScrollTop(viewport, message)).toBe(1490);
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

it("anchors a submitted message and preserves that reading position after completion", () => {
  render({ running: false, status: false, submittedMessage: true });
  const viewport = api.conversationScrollRef.current!;
  const message = viewport.querySelector<HTMLElement>("[data-user-message-id]")!;
  vi.spyOn(viewport, "getBoundingClientRect").mockReturnValue({ top: 100 } as DOMRect);
  vi.spyOn(message, "getBoundingClientRect").mockReturnValue({ top: 500, bottom: 580, height: 80 } as DOMRect);
  viewport.scrollTop = 0;

  act(() => api.requestSubmittedQueryScroll());
  expect(top).toBe(0);
  tick(0);
  expect(top).toBe(0);
  tick(110);
  expect(top).toBeGreaterThan(0);
  expect(top).toBeLessThan(280);
  tick(220);
  const anchored = top;
  expect(anchored).toBe(280);

  naturalHeight += 300;
  render({ running: true, status: false, submittedMessage: true });
  render({ running: false, status: false, submittedMessage: true });
  tick(0); tick(220);
  expect(top).toBe(anchored);
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
