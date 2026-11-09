import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it } from "vitest";
import { useConversationBubbleGesture } from "./ConversationBubbles";

const cleanups: Array<() => void> = [];
afterEach(() => cleanups.splice(0).forEach(cleanup => cleanup()));

function BubbleScroller(): JSX.Element {
  const scrollRef = React.useRef<HTMLDivElement>(null);
  const gesture = useConversationBubbleGesture(true, "conversation", scrollRef);
  return <div ref={scrollRef} data-revealed={gesture.revealed}><p>Message</p></div>;
}

it("leaves vertical and ambiguous diagonal trackpad wheel events to native scrolling", async () => {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<BubbleScroller />));

  const scroller = container.querySelector("div");
  const vertical = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX: 6, deltaY: 8 });
  const diagonal = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX: 8, deltaY: 6 });
  scroller?.dispatchEvent(vertical);
  scroller?.dispatchEvent(diagonal);

  expect(vertical.defaultPrevented).toBe(false);
  expect(diagonal.defaultPrevented).toBe(false);
  expect(scroller?.getAttribute("data-revealed")).toBe("false");
});

it("reveals timestamps for horizontal-dominant trackpad gestures with vertical noise", async () => {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<BubbleScroller />));

  const scroller = container.querySelector("div");
  const wheel = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX: 28, deltaY: 3 });
  await act(async () => scroller?.dispatchEvent(wheel));

  expect(wheel.defaultPrevented).toBe(true);
  expect(scroller?.getAttribute("data-revealed")).toBe("true");
});
