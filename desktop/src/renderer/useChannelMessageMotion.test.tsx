import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelMessage } from "../shared/protocol";
import { useChannelMessageMotion } from "./useChannelMessageMotion";

let container: HTMLDivElement;
let root: Root;
let acknowledge: (pending: string, message: string) => void;
const animations: { currentTime: number; playState: string; cancel: ReturnType<typeof vi.fn> }[] = [];
const animate = vi.fn(() => {
  const animation = { currentTime: 0, playState: "running", cancel: vi.fn(), addEventListener: vi.fn() };
  animations.push(animation);
  return animation;
});
const message = (id: string, seq: number): ChannelMessage => ({ id, seq, room_id: "room", author_type: "agent", author_id: "agent", kind: "text", body: id, created_at: "2026-09-13T00:00:00Z" });

function Transcript({ room = "room", ready = true, messages, pending }: { room?: string; ready?: boolean; messages: ChannelMessage[]; pending?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  acknowledge = useChannelMessageMotion(ref, room, ready, messages, pending);
  return <div ref={ref}>{messages.map(m => <article key={m.id} data-message-id={m.id}>{m.body}</article>)}
    {pending ? <article key={pending} className="own" data-message-id={pending}>Sending</article> : null}</div>;
}

beforeEach(() => {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  animations.length = 0; animate.mockClear();
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  Object.defineProperty(HTMLElement.prototype, "animate", { configurable: true, value: animate });
});
afterEach(() => { act(() => root.unmount()); container.remove(); delete (HTMLElement.prototype as Partial<HTMLElement>).animate; vi.unstubAllGlobals(); });

it("animates new published messages once, while history, edits and room switches remain at rest", () => {
  act(() => root.render(<Transcript ready={false} messages={[]} />));
  act(() => root.render(<Transcript messages={[message("history", 4)]} />));
  expect(animate).not.toHaveBeenCalled();
  act(() => root.render(<Transcript messages={[message("history", 4), message("reply", 5)]} />));
  expect(animate).toHaveBeenCalledTimes(1);
  act(() => root.render(<Transcript messages={[message("older", 3), message("history", 4), { ...message("reply", 5), body: "Updated" }]} />));
  expect(animate).toHaveBeenCalledTimes(1);
  act(() => root.render(<Transcript room="other" messages={[message("other-history", 8)]} />));
  expect(animate).toHaveBeenCalledTimes(1);
  expect(animations[0].cancel).toHaveBeenCalled();
});

it("continues the optimistic entrance at the same frame on acknowledgement", () => {
  act(() => root.render(<Transcript messages={[]} />));
  act(() => root.render(<Transcript messages={[]} pending="pending" />));
  expect(animate).toHaveBeenCalledTimes(1);
  animations[0].currentTime = 110;
  act(() => { acknowledge("pending", "sent"); root.render(<Transcript messages={[message("sent", 1)]} />); });
  expect(animate).toHaveBeenCalledTimes(2);
  expect(animations[1].currentTime).toBe(110);
  expect(animations[0].cancel).toHaveBeenCalled();
});

it("does not replay a completed entrance but still animates a send acknowledged before its pending row painted", () => {
  act(() => root.render(<Transcript messages={[]} />));
  act(() => root.render(<Transcript messages={[]} pending="pending" />));
  animations[0].playState = "finished";
  act(() => { acknowledge("pending", "sent"); root.render(<Transcript messages={[message("sent", 1)]} />); });
  expect(animate).toHaveBeenCalledTimes(1);
  act(() => { acknowledge("unpainted", "fast"); root.render(<Transcript messages={[message("sent", 1), message("fast", 2)]} />); });
  expect(animate).toHaveBeenCalledTimes(2);
});

it("respects reduced motion without hiding new content", () => {
  vi.mocked(window.matchMedia).mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList);
  act(() => root.render(<Transcript messages={[]} />));
  act(() => root.render(<Transcript messages={[message("reply", 1)]} />));
  expect(animate).not.toHaveBeenCalled();
  expect(container.textContent).toBe("reply");
});
