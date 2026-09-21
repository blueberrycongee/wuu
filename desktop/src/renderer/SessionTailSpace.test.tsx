import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { submittedMessageScrollTop, useConversationScrollState } from "./ConversationScrollState";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { PluginHost } from "./plugins/PluginHost";
import type { ThreadItem, Turn } from "../shared/protocol";
import { TurnView } from "./TurnView";
import { ImagePreviewProvider } from "./ImagePreview";
import { ProcessSurface } from "./ProcessSurface";

let api: ReturnType<typeof useConversationScrollState>;
let root: Root;
let host: HTMLDivElement;
let naturalHeight: number;
let disclosureHeight: number;
let viewportHeight: number;
let statusHeight: number;
let composerHeight: number;
let messageBottom: number;
let messageHeight: number;
let top: number;
let pending: Map<number, FrameRequestCallback>;
let nextFrame: number;
let resizeCallbacks: Set<ResizeObserverCallback>;
let animations: { element: HTMLElement; currentTime: number; playState: string; cancel: ReturnType<typeof vi.fn> }[];

type Props = {
  id?: string | null;
  running?: boolean;
  split?: boolean;
  messageID?: string;
  pluginHost?: PluginHost;
  onOpenSession?: (id: string) => void;
  signalLayout?: boolean;
  item?: Partial<ThreadItem>;
  mountKey?: string;
  processItems?: ThreadItem[];
  todoComplete?: boolean;
  composer?: boolean;
  processRow?: boolean;
};
function LayoutSignal() {
  React.useLayoutEffect(() => { api.scheduleStreamScroll(); });
  return null;
}
function Probe({ id = "a", running = false, split = false, messageID, pluginHost, onOpenSession = () => undefined, signalLayout = false, item, mountKey, processItems, todoComplete, composer, processRow }: Props) {
  const [statusClusterNode, setStatusClusterNode] = React.useState<HTMLDivElement | null>(null);
  const primaryTurns: Turn[] = messageID ? [{ id: "turn", items_view: "full", status: running ? "in_progress" : "completed", items: [{ ...item, id: messageID, type: "user_message" }] }] : [];
  api = useConversationScrollState({ activeThreadID: id ?? undefined, activePane: "primary", splitConversation: split, primaryTurns, emptyConversation: !messageID, initialized: true, running, nativeScrollBounce: false, statusClusterNode });
  return <main ref={api.conversationPaneRef}>
    <div data-viewport ref={node => {
      api.conversationScrollRef.current = node;
      if (!node) return;
      Object.defineProperties(node, {
        clientHeight: { configurable: true, get: () => viewportHeight - Number.parseFloat(host.querySelector("main")?.style.getPropertyValue("--conversation-input-inset") || "0") },
        scrollHeight: { configurable: true, get: () => Math.max(node.clientHeight, naturalHeight + statusSpace() + tailSpace()) },
        scrollTop: { configurable: true, get: () => { top = Math.max(0, Math.min(top, node.scrollHeight - node.clientHeight)); return top; }, set: value => { top = Math.max(0, Math.min(value, node.scrollHeight - node.clientHeight)); } },
      });
    }} onScroll={() => api.handleConversationScroll()}>
      <div ref={api.scrollContentRef} data-content>
        {messageID ? item ? <ImagePreviewProvider><TurnView key={mountKey ?? messageID} turn={primaryTurns[0]} onStreamFrame={api.scheduleStreamScroll} /></ImagePreviewProvider>
          : <>
            <div key={mountKey ?? messageID} data-user-message-id={messageID}><div data-message-arrival /></div>
            {processRow ? <div className="assistant-turn-shell" data-process-row /> : null}
          </> : null}
        <div aria-hidden="true"><div data-user-message-id="hidden" /></div>
        {processItems ? <ProcessSurface processItems={processItems} streaming={running} /> : null}
        {signalLayout ? <LayoutSignal /> : null}
      </div>
    </div>
    {composer ? <div data-dock ref={api.dockComposerRef} /> : null}
    {pluginHost ? <ConversationStatusCluster host={pluginHost} visible threadId={id ?? undefined}
      clusterRef={setStatusClusterNode}
      onOpenSession={onOpenSession}
      todoUpdate={{ todos: [{ content: "Keep history controls accessible", status: todoComplete ? "completed" : "in_progress" }] }}
    /> : null}
  </main>;
}

// Model content height separately from scrollHeight's viewport floor. Reading
// scrollTop models Chromium's clamp, including after a layout-only shrink.
function tailSpace() {
  return Number.parseFloat(host.querySelector("main")?.style.getPropertyValue("--session-tail-space") || "0");
}
function leadSpace() {
  return Number.parseFloat(host.querySelector<HTMLElement>("[data-content]")?.style.paddingTop || "0");
}
function statusSpace() {
  return Number.parseFloat(host.querySelector("main")?.style.getPropertyValue("--conversation-status-space") || "0");
}
function scrollTop() { return api.conversationScrollRef.current!.scrollTop; }
function motionY(node = host.querySelector<HTMLElement>("[data-message-arrival]")) {
  const match = node?.style.transform.match(/translateY\(([-\d.]+)px\)/);
  return match ? Number(match[1]) : 0;
}
function render(props: Props = {}) { act(() => root.render(<Probe {...props} />)); }
function tick(now: number) {
  act(() => {
    const callbacks = [...pending.values()];
    pending.clear();
    callbacks.forEach(callback => callback(now));
  });
}
/** Paint frames until the placement stops scheduling them, then stop. */
function settle(from: number) {
  for (let time = from; pending.size > 0 && time <= from + 4000; time += 20) tick(time);
}
function scrollUp(amount: number) {
  const node = api.conversationScrollRef.current!;
  act(() => {
    node.dispatchEvent(new WheelEvent("wheel", { deltaY: -amount }));
    node.scrollTop -= amount;
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
}
function submit(props: Props = {}) {
  act(() => api.requestSubmittedQueryScroll(props.messageID ?? "submitted"));
  render({ running: true, messageID: "submitted", ...props });
  tick(0); settle(360);
}
function grow(amount: number) {
  naturalHeight += amount;
  act(() => api.scheduleStreamScroll());
  tick(1000);
}

const processItems: ThreadItem[] = [
  { id: "read-1", type: "tool_call", name: "read_file", status: "completed", arguments: '{"path":"one.ts"}' },
  { id: "read-2", type: "tool_call", name: "read_file", status: "completed", arguments: '{"path":"two.ts"}' },
];

function toggleTools(input = "pointer") {
  const fold = host.querySelector<HTMLDetailsElement>(".process-surface-fold")!;
  const summary = fold.querySelector<HTMLElement>("summary")!;
  const wasOpen = fold.open;
  act(() => {
    if (input === "pointer") summary.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    if (input === "keyboard") summary.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    if (input === "touch") summary.dispatchEvent(new TouchEvent("touchstart", { touches: [], bubbles: true }));
    summary.click();
    fold.dispatchEvent(new Event("toggle", { bubbles: true }));
  });
  expect(fold.open).toBe(!wasOpen);
}

function resizeDisclosure(height: number, nativeScroll = 0, resizeFirst = false) {
  naturalHeight += height - disclosureHeight;
  disclosureHeight = height;
  act(() => {
    // A layout/anchoring scroll can precede ResizeObserver and differ from
    // the last programmatic write during the native details transition.
    api.conversationScrollRef.current!.scrollTop += nativeScroll;
    if (resizeFirst) for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver);
    api.handleConversationScroll();
    if (!resizeFirst) for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver);
  });
}

beforeEach(() => {
  naturalHeight = 2000;
  disclosureHeight = 0;
  viewportHeight = 600;
  statusHeight = 34;
  composerHeight = 100;
  messageBottom = 1850;
  messageHeight = 80;
  top = 0;
  nextFrame = 0;
  pending = new Map();
  resizeCallbacks = new Set();
  animations = [];
  Object.defineProperty(HTMLElement.prototype, "animate", { configurable: true, value: vi.fn(function (this: HTMLElement) {
    const animation = { element: this, currentTime: 0, playState: "running", cancel: vi.fn(() => { animation.playState = "idle"; }), addEventListener: vi.fn() };
    animations.push(animation);
    return animation;
  }) });
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => { pending.set(++nextFrame, callback); return nextFrame; });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { pending.delete(id); });
  vi.stubGlobal("ResizeObserver", class {
    constructor(private callback: ResizeObserverCallback) { resizeCallbacks.add(callback); }
    observe() {}
    disconnect() { resizeCallbacks.delete(this.callback); }
    unobserve() {}
  });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    if (this.matches("details")) return { height: 34 + disclosureHeight } as DOMRect;
    if (this.hasAttribute("data-content")) return { height: naturalHeight + statusSpace() + tailSpace() + leadSpace() } as DOMRect;
    if (this.hasAttribute("data-viewport")) return { top: 100, height: this.clientHeight } as DOMRect;
    if (this.classList.contains("conversation-status-cluster")) return { height: statusHeight } as DOMRect;
    if (this.hasAttribute("data-dock")) return { height: composerHeight } as DOMRect;
    if (this.hasAttribute("data-user-message-id")) return { top: 100 + messageBottom - messageHeight - top, bottom: 100 + messageBottom - top, height: messageHeight } as DOMRect;
    if (this.hasAttribute("data-process-row")) {
      const processTop = 100 + messageBottom - top;
      return { top: processTop, bottom: processTop + 26, height: 26 } as DOMRect;
    }
    return { height: 34 } as DOMRect;
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  delete (HTMLElement.prototype as Partial<HTMLElement>).animate;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("restores history against the incoming composer's viewport before clamping", () => {
  naturalHeight = 2000;
  composerHeight = 300;
  render({ id: "a", messageID: "a-message", composer: true });
  scrollUp(100);
  const savedTop = scrollTop();
  expect(savedTop).toBe(1600);

  composerHeight = 100;
  render({ id: "b", messageID: "b-message", composer: true });
  composerHeight = 300;
  render({ id: "a", messageID: "a-message", composer: true });
  expect(scrollTop()).toBe(savedTop);
  settle(0);
  expect(scrollTop()).toBe(savedTop);
});

it("does not invent submission space when opening a running thread", () => {
  render({ messageID: "old", running: true });
  expect(tailSpace()).toBe(0);
  render({ messageID: "old", running: false });
  expect(tailSpace()).toBe(0);
});

it.each([false, true])("preserves reading ownership when toggling grouped tools (away: %s)", away => {
  render({ messageID: "old", processItems });
  if (away) scrollUp(200);
  const readingTop = scrollTop();
  for (const open of [true, false]) {
    toggleTools();
    resizeDisclosure(open ? 300 : 0, away ? 0 : -2);
    expect(scrollTop()).toBe(away ? readingTop : naturalHeight - viewportHeight);
  }
  grow(100);
  expect(scrollTop()).toBe(away ? readingTop : naturalHeight - viewportHeight);
});

it.each(["pointer", "keyboard", "touch"])("restores submission clearance after inspecting tools with %s while output grows", input => {
  render({ messageID: "old", processItems });
  submit({ processItems });
  const anchored = scrollTop();
  const initial = tailSpace();
  expect(initial).toBeGreaterThan(80);
  for (let cycle = 0; cycle < 2; cycle++) {
    toggleTools(input);
    resizeDisclosure(initial + 100);
    expect(tailSpace()).toBe(0);
    expect(scrollTop()).toBe(anchored);
    expect(api.captureConversationScrollPosition()?.submissionPhase).toBe("holding");
    grow(40);
    toggleTools(input);
    resizeDisclosure(80);
    resizeDisclosure(0);
    expect(tailSpace()).toBeCloseTo(initial - (cycle + 1) * 40);
    expect(scrollTop()).toBe(anchored);
  }
  grow(initial);
  expect(tailSpace()).toBe(0);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(true);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it.each([false, true])("does not let layout scrolling consume a paused reservation or resume following (resize first: %s)", resizeFirst => {
  render({ messageID: "old", processItems });
  submit({ processItems });
  scrollUp(80);
  const remaining = tailSpace();
  const readingTop = scrollTop();
  toggleTools();
  resizeDisclosure(300, 0, resizeFirst);
  toggleTools();
  resizeDisclosure(0, 0, resizeFirst);
  expect(scrollTop()).toBe(readingTop);
  expect(tailSpace()).toBe(remaining);
  grow(1000);
  expect(scrollTop()).toBe(readingTop);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
});

it("yields following to an outer scroll during the disclosure transition", () => {
  render({ messageID: "old", processItems, running: true });
  toggleTools();
  resizeDisclosure(150);
  scrollUp(100);
  const readingTop = scrollTop();
  resizeDisclosure(300);
  grow(100);
  expect(scrollTop()).toBe(readingTop);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
});

it.each(["wheel", "keyboard", "touch"].flatMap(input =>
  [false, true].map(resizing => ({ input, resizing })),
))("resumes following after a disclosure resize without scrolling ($input, resizing: $resizing)", ({ input, resizing }) => {
  render({ messageID: "old", processItems, running: true });
  scrollUp(200);
  toggleTools();
  naturalHeight += 300;
  disclosureHeight = 300;
  // Expanding below the browser's anchor need not move the outer viewport.
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  const node = api.conversationScrollRef.current!;
  act(() => {
    if (input === "wheel") node.dispatchEvent(new WheelEvent("wheel", { deltaY: 500, bubbles: true }));
    if (input === "keyboard") node.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
    if (input === "touch") {
      node.dispatchEvent(new TouchEvent("touchstart", { touches: [{ clientY: 500 } as Touch], bubbles: true }));
      node.dispatchEvent(new TouchEvent("touchmove", { touches: [{ clientY: 0 } as Touch], bubbles: true }));
    }
    // A transition can advance between input and the browser's scroll event.
    if (resizing) {
      naturalHeight += 40;
      disclosureHeight += 40;
    }
    node.scrollTop = node.scrollHeight - node.clientHeight;
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(true);
  grow(100);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it.each([false, true])("keeps a layout-only disclosure scroll paused at the bottom (resize first: %s)", resizeFirst => {
  render({ messageID: "old", processItems, running: true });
  act(() => api.disableConversationAutoFollow());
  toggleTools();
  resizeDisclosure(300, 300, resizeFirst);
  const readingTop = scrollTop();
  expect(readingTop).toBe(naturalHeight - viewportHeight);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  grow(100);
  expect(scrollTop()).toBe(readingTop);
});

it("keeps a temporarily occupied reservation across thread switches", () => {
  render({ messageID: "old", processItems });
  submit({ processItems });
  const initial = tailSpace();
  toggleTools();
  resizeDisclosure(initial + 100);
  render({ id: "b", messageID: "other", processItems });
  render({ id: "a", messageID: "submitted", processItems });
  grow(40);
  toggleTools();
  resizeDisclosure(0);
  expect(tailSpace()).toBeCloseTo(initial - 40);
  expect(api.captureConversationScrollPosition()?.submissionPhase).toBe("holding");
});

it("does not replenish clearance after real output fills it while tools are open", () => {
  render({ messageID: "old", processItems });
  submit({ processItems });
  const initial = tailSpace();
  toggleTools();
  resizeDisclosure(initial + 100);
  grow(initial + 50);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(true);
  toggleTools();
  resizeDisclosure(0);
  expect(tailSpace()).toBe(0);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("yields a held submission when a tap on the summary becomes a swipe", () => {
  render({ messageID: "old", processItems });
  submit({ processItems });
  const summary = host.querySelector<HTMLElement>(".process-surface-fold > summary")!;
  act(() => {
    summary.dispatchEvent(new TouchEvent("touchstart", { touches: [{ clientY: 100 } as Touch], bubbles: true }));
    summary.dispatchEvent(new TouchEvent("touchmove", { touches: [{ clientY: 130 } as Touch], bubbles: true }));
    api.conversationScrollRef.current!.scrollTop -= 30;
    api.handleConversationScroll();
  });
  const readingTop = scrollTop();
  grow(1000);
  expect(scrollTop()).toBe(readingTop);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
});

it.each(["following", "holding", "paused"])("reserves trailing status space without shortening the reading viewport while %s", mode => {
  const pluginHost = new PluginHost({ react: React });
  render({ messageID: "old" });
  if (mode === "holding") submit();
  if (mode === "paused") scrollUp(200);
  const messageID = mode === "holding" ? "submitted" : "old";
  const readingTop = scrollTop();
  const viewport = api.conversationScrollRef.current!;
  render({ messageID, pluginHost });
  expect(viewport.clientHeight).toBe(viewportHeight);
  expect(scrollTop()).toBe(mode === "following" ? naturalHeight + statusHeight - viewport.clientHeight : readingTop);
  statusHeight = 48;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  tick(800);
  expect(viewport.clientHeight).toBe(viewportHeight);
  expect(scrollTop()).toBe(mode === "following" ? naturalHeight + statusHeight - viewport.clientHeight : readingTop);
  render({ messageID, pluginHost, todoComplete: true });
  expect(viewport.clientHeight).toBe(viewportHeight);
  expect(scrollTop()).toBe(mode === "following" ? naturalHeight - viewport.clientHeight : readingTop);
  grow(1000);
  expect(scrollTop()).toBe(mode === "paused" ? readingTop : naturalHeight - viewportHeight);
});

it.each([false, true])("keeps materialized pending inputs in the current reading flow (paused: %s)", paused => {
  render({ messageID: "old", running: true });
  if (paused) scrollUp(200);
  const readingTop = scrollTop();
  grow(100);
  messageBottom += 100;
  render({ messageID: "accepted", item: { text: "Follow up", source_id: "local-input" } });
  tick(0); settle(360);
  expect(tailSpace()).toBe(0);
  expect(leadSpace()).toBe(0);
  expect(animations).toHaveLength(0);
  expect(scrollTop()).toBe(paused ? readingTop : naturalHeight - viewportHeight);
  grow(100);
  expect(scrollTop()).toBe(paused ? readingTop : naturalHeight - viewportHeight);
});

it.each(["scroll", "switch", "failure"])("does not reposition a delayed input after %s", reason => {
  render({ messageID: "old", running: true });
  if (reason === "scroll") scrollUp(200);
  if (reason === "switch") { render({ id: "b", messageID: "other" }); render({ messageID: "old" }); }
  if (reason === "failure") act(() => api.discardSubmittedMessage("local-input"));
  const before = scrollTop();
  render({ messageID: "accepted", item: { text: "Follow up", source_id: "local-input" } });
  tick(0); settle(360);
  expect(scrollTop()).toBe(before);
  expect(tailSpace()).toBe(0);
});

it("starts placement and entrance together for a delayed child-only mount", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  const message = document.createElement("div");
  message.dataset.userMessageId = "submitted";
  const bubble = document.createElement("div");
  bubble.dataset.messageArrival = "";
  message.append(bubble);
  host.querySelector("[data-content]")!.append(message);
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  expect(animations).toHaveLength(1);
  expect(animations[0].element).toBe(bubble);
  tick(0); settle(360);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
  expect(tailSpace()).toBeGreaterThan(0);
  animations[0].playState = "finished";
  render({ messageID: "old", running: true });
  expect(animations).toHaveLength(1);
});

it.each(["following", "placing", "holding", "paused"] as const)("settles queue drawer removal in the same frame while %s", mode => {
  composerHeight = 200;
  render({ messageID: "old", composer: true });
  if (mode === "holding") submit({ composer: true });
  if (mode === "placing") {
    act(() => api.requestSubmittedQueryScroll("submitted"));
    render({ messageID: "submitted", running: true, composer: true });
    tick(0); tick(180);
  }
  if (mode === "paused") scrollUp(200);
  const before = scrollTop();
  composerHeight = 100;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  tick(mode === "placing" ? 180 : 400);
  const node = api.conversationScrollRef.current!;
  expect(scrollTop()).toBe(mode === "following" ? node.scrollHeight - node.clientHeight : before);
  // The next observer delivery must not produce a second correction.
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  if (mode === "placing") {
    tick(196); settle(360);
    const message = host.querySelector<HTMLElement>('[data-user-message-id="submitted"]')!;
    expect(scrollTop()).toBeCloseTo(submittedMessageScrollTop(node, message, false));
    return;
  }
  tick(416);
  expect(scrollTop()).toBe(mode === "following" ? node.scrollHeight - node.clientHeight : before);
});

it("discards failed submission ownership and reserve without affecting a later submission", () => {
  render({ messageID: "old" });
  submit();
  act(() => api.discardSubmittedMessage("submitted"));
  expect(tailSpace()).toBe(0);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  submit({ messageID: "next" });
  const anchored = scrollTop();
  const remaining = tailSpace();
  act(() => api.discardSubmittedMessage("submitted"));
  expect(tailSpace()).toBe(remaining);
  grow(100);
  expect(scrollTop()).toBe(anchored);
});

it("keeps acknowledgement identity and the output baseline while its thread is inactive", () => {
  render({ messageID: "old" });
  submit();
  const anchored = scrollTop();
  naturalHeight += 800;
  messageHeight += 800;
  messageBottom += 800;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  expect(tailSpace()).toBe(0);
  render({ id: "b", messageID: "other" });
  const otherTop = scrollTop();
  act(() => api.acknowledgeSubmittedMessage("submitted", "accepted"));
  expect(scrollTop()).toBe(otherTop);
  render({ id: "a", messageID: "accepted" });
  expect(scrollTop()).toBe(anchored);
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  grow(100);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("waits for the exact submitted bubble, not the old or hidden cached message", () => {
  render({ messageID: "old" });
  const before = scrollTop();
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "old", running: true });
  tick(0); tick(220); tick(440); tick(660);
  expect(scrollTop()).toBe(before);
  expect(tailSpace()).toBe(0);
  render({ messageID: "submitted", running: true });
  tick(0); settle(360);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
  expect(tailSpace()).toBeGreaterThan(0);
});

it.each([600, 1600])("reserves enough measured range in a %ipx viewport", height => {
  viewportHeight = height;
  render({ messageID: "old" });
  submit();
  const message = host.querySelector<HTMLElement>('[data-user-message-id="submitted"]')!;
  const relativeBottom = message.getBoundingClientRect().bottom - 100;
  expect(relativeBottom / viewportHeight).toBeGreaterThanOrEqual(0.25);
  expect(relativeBottom / viewportHeight).toBeLessThanOrEqual(0.35);
});

it("holds the submitted bubble until output fills the gap, then follows streaming", () => {
  render({ messageID: "old" });
  submit();
  const anchored = scrollTop();
  const initial = tailSpace();
  grow(150);
  expect(tailSpace()).toBeCloseTo(initial - 150);
  expect(scrollTop()).toBe(anchored);
  grow(initial);
  expect(tailSpace()).toBe(0);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
  const following = scrollTop();
  render({ messageID: "submitted", running: false });
  expect(scrollTop()).toBe(following);
  grow(300);
  expect(tailSpace()).toBe(0);
  expect(scrollTop()).toBe(following + 300);
});

it("does not let a native scroll at the old bottom hand pending placement to following", () => {
  naturalHeight = 400;
  messageBottom = 250;
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  act(() => api.handleConversationScroll());
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  naturalHeight = 2000;
  messageBottom = 1850;
  render({ messageID: "submitted", running: true });
  tick(0); tick(180);
  // Chromium may deliver a coalesced scroll after layout, rather than the
  // exact last rAF write. Reaching the padded bottom is not user intent.
  act(() => {
    api.conversationScrollRef.current!.scrollTop = naturalHeight + tailSpace() - viewportHeight;
    api.handleConversationScroll();
    for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver);
  });
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  settle(360);
  const anchored = scrollTop();
  grow(100);
  expect(scrollTop()).toBe(anchored);
});

it("does not resume following from layout scroll events while holding an attachment", () => {
  render({ messageID: "old" });
  submit();
  act(() => api.handleConversationScroll());
  act(() => api.handleConversationScroll());
  expect(api.captureConversationScrollPosition()?.autoFollow).toBe(false);
  const anchored = scrollTop();
  naturalHeight += 800;
  messageHeight += 800;
  messageBottom += 800;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  expect(scrollTop()).toBe(anchored);
});

it("consumes layout growth even without a stream callback", () => {
  render({ messageID: "old" });
  submit();
  const initial = tailSpace();
  const anchored = scrollTop();
  naturalHeight += 100;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  tick(0); tick(220);
  expect(tailSpace()).toBeCloseTo(initial - 100);
  expect(scrollTop()).toBe(anchored);
});

it("preserves short-response clearance on completion until the user scrolls", () => {
  render({ messageID: "old" });
  submit();
  grow(30);
  const anchored = scrollTop();
  const remaining = tailSpace();
  render({ messageID: "submitted", running: false });
  tick(0); tick(220);
  expect(tailSpace()).toBe(remaining);
  expect(scrollTop()).toBe(anchored);
  scrollUp(120);
  expect(tailSpace()).toBeLessThan(remaining);
  expect(scrollTop()).toBe(anchored - 120);
});

it("consumes offscreen clearance while browsing and does not replenish it on a run transition", () => {
  render({ messageID: "old" });
  submit();
  const anchored = scrollTop();
  const initial = tailSpace();
  scrollUp(150);
  const consumed = tailSpace();
  expect(consumed).toBeLessThan(initial);
  expect(scrollTop()).toBe(anchored - 150);
  render({ messageID: "submitted", running: false });
  render({ messageID: "submitted", running: true });
  expect(tailSpace()).toBe(consumed);
  grow(100);
  expect(tailSpace()).toBeCloseTo(consumed - 100);
  expect(scrollTop()).toBe(anchored - 150);
});

it("restores a thread's remaining space before restoring its reading position", () => {
  render({ messageID: "old" });
  submit();
  const anchored = scrollTop();
  const remaining = tailSpace();
  render({ id: "b", messageID: "other" });
  expect(tailSpace()).toBe(0);
  render({ id: "a", messageID: "submitted", signalLayout: true });
  expect(tailSpace()).toBe(remaining);
  expect(scrollTop()).toBe(anchored);
});

it("follows leftover submission space to the content bottom, not the empty reservation", () => {
  render({ messageID: "old" });
  submit();
  const remaining = tailSpace();
  expect(remaining).toBeGreaterThan(80);
  act(() => api.enableConversationAutoFollow());
  render({ messageID: "submitted", running: true });
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
  expect(scrollTop()).toBeLessThan(naturalHeight + remaining - viewportHeight);
  render({ id: "b", messageID: "other" });
  render({ id: "a", messageID: "submitted" });
  expect(tailSpace()).toBe(remaining);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("keeps a short draft bubble in place when it is adopted by the new thread", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  submit({ id: null });
  const remaining = tailSpace();
  expect(remaining).toBeGreaterThan(0);
  expect(scrollTop()).toBe(0);
  render({ id: "created", messageID: "submitted", running: true });
  expect(tailSpace()).toBe(remaining);
  expect(scrollTop()).toBe(0);
  grow(100);
  expect(tailSpace()).toBeCloseTo(remaining - 100);
  expect(scrollTop()).toBe(0);
});

it("lifts a short first bubble from the composer when the viewport cannot scroll", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  expect(leadSpace()).toBeGreaterThan(100);
  expect(motionY()).toBe(0);
  expect(scrollTop()).toBe(0);
  tick(0);
  const start = leadSpace();
  tick(60);
  const first = start - leadSpace();
  tick(120);
  expect(first).toBeGreaterThan(0);
  expect(start - leadSpace() - first).toBeLessThan(first);
  settle(360);
  expect(leadSpace()).toBe(0);
  expect(scrollTop()).toBe(0);
});

it("does not feed fractional layout noise back into the submission reservation", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  const reserved = tailSpace();
  const content = api.scrollContentRef.current!;
  // The browser quantizes box geometry, not the animation's fractional input.
  content.getBoundingClientRect = () => ({
    height: Math.round((naturalHeight + tailSpace() + leadSpace()) * 64) / 64,
  }) as DOMRect;
  for (let time = 0; time < 700; time += 16) {
    tick(time);
    act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
    expect(tailSpace()).toBe(reserved);
  }
  grow(50);
  expect(tailSpace()).toBeCloseTo(reserved - 50, 1);
  grow(1000);
  expect(tailSpace()).toBe(0);
});

it("keeps a short first-bubble lift through thread adoption", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  tick(0); tick(80);
  const before = leadSpace();
  expect(before).toBeGreaterThan(0);
  render({ id: "created", messageID: "submitted", running: true });
  expect(leadSpace()).toBeCloseTo(before);
  expect(scrollTop()).toBe(0);
  settle(360);
  expect(leadSpace()).toBe(0);
});

it("places a short first bubble without a composer lift with reduced motion", () => {
  vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList);
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  expect(leadSpace()).toBe(0);
  expect(scrollTop()).toBe(0);
  expect(animations).toHaveLength(0);
});

it("clears an in-flight first-bubble lift when the user scrolls", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  tick(0);
  expect(leadSpace()).toBeGreaterThan(0);
  scrollUp(100);
  expect(leadSpace()).toBe(0);
});

it("clears an in-flight first-bubble lift when switching sessions", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true });
  tick(0);
  expect(leadSpace()).toBeGreaterThan(0);
  render({ id: "b", messageID: "other" });
  expect(leadSpace()).toBe(0);
});

it("lifts the in-progress timer with a short first bubble instead of a second transform", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted", running: true, processRow: true });
  const process = host.querySelector<HTMLElement>("[data-process-row]");
  expect(leadSpace()).toBeGreaterThan(100);
  expect(motionY()).toBe(0);
  expect(motionY(process)).toBe(0);
  tick(0); tick(80);
  expect(leadSpace()).toBeGreaterThan(0);
  expect(motionY()).toBe(0);
  expect(motionY(process)).toBe(0);
  settle(360);
  expect(leadSpace()).toBe(0);
});

it("does not lift a follow-up bubble that can scroll into place", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", running: true });
  expect(leadSpace()).toBe(0);
  expect(motionY()).toBe(0);
  tick(0);
  expect(leadSpace()).toBe(0);
  expect(motionY()).toBe(0);
  settle(360);
  expect(leadSpace()).toBe(0);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
});

it("creates a fresh consumable reserve for the next submission", () => {
  render({ messageID: "old" });
  submit();
  grow(1000);
  expect(tailSpace()).toBe(0);
  messageBottom = naturalHeight - 150;
  submit({ messageID: "next" });
  const anchored = scrollTop();
  const remaining = tailSpace();
  expect(remaining).toBeGreaterThan(0);
  grow(100);
  expect(tailSpace()).toBeCloseTo(remaining - 100);
  expect(scrollTop()).toBe(anchored);
});

it("does not let a delayed mount steal the viewport after a user scroll", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  scrollUp(100);
  const away = scrollTop();
  render({ messageID: "submitted", running: true });
  tick(0); tick(220);
  expect(scrollTop()).toBe(away);
  expect(tailSpace()).toBe(0);
});

it("cancels an in-flight bubble animation when the user scrolls", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", running: true });
  tick(0); tick(80);
  scrollUp(100);
  const away = scrollTop();
  tick(220);
  grow(100);
  expect(scrollTop()).toBe(away);
});

it("keeps the bubble on its screen trajectory when earlier content collapses during submission", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", running: true });
  tick(0); tick(80);
  const screenTop = messageBottom - messageHeight - scrollTop();
  naturalHeight -= 120;
  messageBottom -= 120;
  render({ messageID: "submitted", running: true });
  expect(messageBottom - messageHeight - scrollTop()).toBeCloseTo(screenTop);
  tick(80);
  expect(messageBottom - messageHeight - scrollTop()).toBeCloseTo(screenTop);
  settle(360);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
});

it.each([-240, 240])("keeps the same visible trajectory while preceding layout moves by %ipx", displacement => {
  const run = (id: string, moving: boolean) => {
    naturalHeight = 2000;
    messageBottom = 1850;
    render({ id, messageID: "old" });
    act(() => api.requestSubmittedQueryScroll("submitted"));
    render({ id, messageID: "submitted", running: true });
    const positions: number[] = [];
    for (let now = 0; now <= 360; now += 20) {
      if (moving && now >= 80 && now < 240) {
        const before = messageBottom - messageHeight - scrollTop();
        messageBottom += displacement / 8;
        naturalHeight += displacement / 8;
        render({ id, messageID: "submitted", running: true });
        // A React commit must compensate before paint, not wait for another
        // animation frame after showing the bubble at its reflowed position.
        expect(messageBottom - messageHeight - scrollTop()).toBeCloseTo(before);
      }
      tick(now);
      positions.push(messageBottom - messageHeight - scrollTop());
    }
    return positions;
  };
  const fixed = run("fixed", false);
  const moving = run("moving", true);
  moving.forEach((position, index) => expect(position).toBeCloseTo(fixed[index]));
});

it("places the bubble without animation with reduced motion", () => {
  vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList);
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", running: true });
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
  const anchored = scrollTop();
  grow(100);
  expect(scrollTop()).toBe(anchored);
  expect(animations).toHaveLength(0);
});

it("does not add ordinary-session space to collaboration panes", () => {
  render({ split: true, messageID: "old" });
  submit({ split: true });
  expect(tailSpace()).toBe(0);
});

it("keeps independent status controls accessible while browsing", async () => {
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
  render({ messageID: "old", pluginHost, onOpenSession });
  expect(tailSpace()).toBe(0);
  scrollUp(300);
  const away = scrollTop();
  const todo = host.querySelector<HTMLElement>(".conversation-status-todo-trigger")!;
  act(() => todo.focus());
  expect(document.activeElement).toBe(todo);
  expect(todo.closest("[inert]")).toBeNull();
  act(() => host.querySelector<HTMLButtonElement>("button")!.click());
  expect(onOpenSession).toHaveBeenCalledWith("worker");
  grow(200);
  expect(scrollTop()).toBe(away);
  expect(tailSpace()).toBe(0);
});

it.each([400, 900])("keeps the beginning of a %ipx message visible instead of clipping it", height => {
  messageHeight = height;
  render({ messageID: "old" });
  submit();
  const viewport = api.conversationScrollRef.current!;
  const message = host.querySelector<HTMLElement>('[data-user-message-id="submitted"]')!;
  expect(submittedMessageScrollTop(viewport, message)).toBe(scrollTop());
  expect(message.getBoundingClientRect().top - 100).toBeGreaterThan(0);
  expect(scrollTop()).toBeLessThan(messageBottom - messageHeight);
});

it("moves immediately and decelerates into the reading position", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted" });
  tick(0);
  const start = scrollTop();
  tick(60);
  const first = scrollTop() - start;
  tick(120);
  expect(first).toBeGreaterThan(0);
  expect(scrollTop() - start - first).toBeLessThan(first);
  settle(360);
  expect(scrollTop()).toBeCloseTo(submittedMessageScrollTop(api.conversationScrollRef.current!, host.querySelector('[data-user-message-id="submitted"]')!));
});

it("hands off optimistic motion and positioning without restarting at acknowledgement", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted" });
  tick(0); tick(80);
  animations[0].currentTime = 80;
  const before = scrollTop();
  act(() => api.acknowledgeSubmittedMessage("submitted", "accepted"));
  messageBottom -= 60;
  render({ messageID: "accepted" });
  expect(scrollTop()).toBeCloseTo(before - 60);
  expect(animations).toHaveLength(2);
  expect(animations[1].currentTime).toBe(80);
  expect(animations[0].cancel).toHaveBeenCalled();
  tick(96); settle(360);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
  render({ messageID: "accepted" });
  expect(animations).toHaveLength(2);
});

it("handles acknowledgements before the optimistic bubble mounts", () => {
  render({ messageID: "old" });
  act(() => {
    api.requestSubmittedQueryScroll("submitted");
    api.acknowledgeSubmittedMessage("submitted", "accepted");
  });
  render({ messageID: "accepted" });
  tick(0); settle(360);
  expect(animations).toHaveLength(1);
  expect(scrollTop()).toBeCloseTo(messageBottom - 200);
});

it("does not replay a completed entrance on acknowledgement or draft promotion", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  submit({ id: null });
  expect(leadSpace()).toBe(0);
  expect(animations).toHaveLength(0);
  render({ id: "created", messageID: "submitted", mountKey: "promoted" });
  expect(animations).toHaveLength(0);
  act(() => api.acknowledgeSubmittedMessage("submitted", "accepted"));
  render({ id: "created", messageID: "accepted" });
  expect(animations).toHaveLength(0);
  expect(scrollTop()).toBe(0);
});

it("restores waiting-to-follow after switching threads, but preserves deliberate pauses", () => {
  render({ messageID: "old" });
  submit();
  render({ id: "b", messageID: "other" });
  render({ messageID: "submitted" });
  grow(1000);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
  scrollUp(200);
  const away = scrollTop();
  grow(300);
  expect(scrollTop()).toBe(away);
  const viewport = api.conversationScrollRef.current!;
  act(() => {
    viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: 1000 }));
    viewport.scrollTop = viewport.scrollHeight;
    viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  grow(100);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("does not resume following when a paused submission's entire reserve is consumed", () => {
  render({ messageID: "old" });
  submit();
  scrollUp(100);
  const away = scrollTop();
  grow(2000);
  expect(tailSpace()).toBe(0);
  expect(scrollTop()).toBe(away);
});

const longText = Array.from({ length: 30 }, (_, index) => `Paragraph ${index}: ${"detailed message ".repeat(8)}`).join("\n");
const images = Array.from({ length: 6 }, (_, index) => ({ media_type: "image/png", data: "aGVsbG8=", filename: `${index}.png` }));
const files = Array.from({ length: 5 }, (_, index) => ({ media_type: "text/plain", data: "aGVsbG8=", filename: `${index}.txt` }));
const pasted = { type: "pasted_text" as const, text: longText, title: "Source material" };

it.each([
  ["long text", { text: longText }],
  ["images only", { text: "", images }],
  ["files only", { text: "", files }],
  ["pasted text", { text: "", content_parts: [pasted] }],
  ["mixed content", { text: longText, images, files, content_parts: [pasted, { type: "text" as const, text: longText }] }],
] satisfies [string, Partial<ThreadItem>][]) ("places actual %s and yields to its expansion", (_name, item) => {
  messageHeight = 400;
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", item });
  tick(0); tick(80);
  const anchor = host.querySelector<HTMLElement>('[data-user-message-id="submitted"]')!;
  expect(animations).toHaveLength(1);
  expect(animations[0].element).not.toBe(anchor);
  expect(anchor.contains(animations[0].element)).toBe(true);
  const before = scrollTop();
  const expand = anchor.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!;
  expect(expand).not.toBeNull();
  act(() => expand.click());
  expect(anchor.querySelector('[aria-expanded="true"]')).not.toBeNull();
  expect(animations[0].cancel).toHaveBeenCalled();
  messageHeight += 300;
  messageBottom += 300;
  grow(1000);
  settle(360);
  expect(scrollTop()).toBe(before);
  expect(animations).toHaveLength(1);
  act(() => anchor.querySelector<HTMLButtonElement>('button[aria-expanded="true"]')!.click());
  expect(animations).toHaveLength(1);
});

it("adapts to asynchronous attachment sizing during placement without replaying it", () => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted", item: { text: "", images } });
  tick(0); tick(80);
  const messageTop = messageBottom - messageHeight;
  messageHeight += 240;
  messageBottom += 240;
  naturalHeight += 240;
  act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
  tick(96); settle(360);
  expect(scrollTop()).toBeLessThan(messageTop);
  expect(animations).toHaveLength(1);
  const anchored = scrollTop();
  messageHeight += 20;
  messageBottom += 20;
  grow(20);
  expect(scrollTop()).toBe(anchored);
  expect(animations).toHaveLength(1);
});

it("does not confuse intrinsic attachment growth with response output", () => {
  render({ messageID: "old" });
  submit({ item: { images } });
  const anchored = scrollTop();
  messageHeight += 1000;
  messageBottom += 1000;
  grow(1000);
  expect(tailSpace()).toBe(0);
  expect(scrollTop()).toBe(anchored);
  grow(100);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("continues an in-flight draft placement through a pane remount and viewport resize", () => {
  render({ id: null });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ id: null, messageID: "submitted" });
  tick(0); tick(80);
  animations[0].currentTime = 80;
  viewportHeight = 900;
  render({ id: "created", messageID: "submitted", mountKey: "promoted" });
  tick(96); settle(360);
  const viewport = api.conversationScrollRef.current!;
  const message = host.querySelector<HTMLElement>('[data-user-message-id="submitted"]')!;
  expect(scrollTop()).toBeCloseTo(submittedMessageScrollTop(viewport, message, false));
  expect(animations).toHaveLength(2);
  expect(animations[1].currentTime).toBe(80);
  grow(1200);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it("follows after a short first turn fills its viewport", () => {
  naturalHeight = 220;
  messageBottom = 100;
  render({ id: null });
  submit({ id: null });
  render({ id: "created", messageID: "submitted" });
  grow(200);
  expect(scrollTop()).toBe(0);
  grow(200);
  expect(scrollTop()).toBe(naturalHeight - viewportHeight);
});

it.each(["wheel", "pointer", "touch", "keyboard"])("yields an in-flight placement to %s input", input => {
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted" });
  tick(0); tick(80);
  const viewport = api.conversationScrollRef.current!;
  const before = scrollTop();
  act(() => {
    if (input === "wheel") viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: 30 }));
    if (input === "pointer") viewport.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    if (input === "touch") viewport.dispatchEvent(new TouchEvent("touchstart", { touches: [] }));
    if (input === "keyboard") viewport.dispatchEvent(new KeyboardEvent("keydown", { key: "PageDown" }));
  });
  settle(360);
  grow(1200);
  expect(scrollTop()).toBe(before);
});

it.each(["hidden", "reduced motion"])("stops live motion when the document changes to %s", state => {
  const media = new EventTarget() as MediaQueryList;
  Object.defineProperty(media, "matches", { configurable: true, value: false });
  vi.spyOn(window, "matchMedia").mockReturnValue(media);
  render({ messageID: "old" });
  act(() => api.requestSubmittedQueryScroll("submitted"));
  render({ messageID: "submitted" });
  tick(0); tick(80);
  const before = scrollTop();
  act(() => {
    if (state === "hidden") {
      vi.spyOn(document, "hidden", "get").mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
    } else {
      Object.defineProperty(media, "matches", { value: true });
      media.dispatchEvent(new Event("change"));
    }
  });
  settle(360);
  expect(scrollTop()).toBe(before);
  expect(animations[0].cancel).toHaveBeenCalled();
});
