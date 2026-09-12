import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentRelationshipGraph } from "./AgentRelationshipGraph";

// Avatar animation has its own frame loop; this suite exercises graph
// movement, cooling, and selection independently of that decoration.
vi.mock("./AgentAvatarMark", () => ({ AgentAvatarMark: () => null }));

function pointerEvent(type: string, x: number, y: number): Event {
  const event = new MouseEvent(type, { bubbles: true, clientX: x, clientY: y });
  Object.defineProperty(event, "pointerId", { value: 1 });
  return event;
}

function translateOf(element: Element): { x: number; y: number } {
  const match = /translate\((-?[\d.e]+) (-?[\d.e]+)\)/.exec(element.getAttribute("transform") ?? "");
  if (!match) throw new Error(`no translate in ${element.getAttribute("transform")}`);
  return { x: Number(match[1]), y: Number(match[2]) };
}

describe("AgentRelationshipGraph", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => 1);
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => undefined);
    Object.defineProperty(SVGElement.prototype, "setPointerCapture", { configurable: true, value: vi.fn() });
    Object.defineProperty(SVGElement.prototype, "releasePointerCapture", { configurable: true, value: vi.fn() });
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("stops the force simulation after it cools", () => {
    const frames: FrameRequestCallback[] = [];
    let nextFrameID = 1;
    vi.mocked(window.requestAnimationFrame).mockImplementation((callback) => {
      frames.push(callback);
      return nextFrameID++;
    });

    const agents = [
      { id: "agent-1", name: "Andy", avatar_key: "abstract-1" },
      { id: "agent-2", name: "Le", avatar_key: "abstract-2" },
    ] as NamedAgent[];
    const rooms = [{
      id: "room-1",
      name: "general",
      members: agents.map((agent) => ({ member_type: "agent" as const, member_id: agent.id })),
    }] as ChannelRoom[];

    act(() => root.render(
      <AgentRelationshipGraph
        agents={agents}
        rooms={rooms}
        onSelectAgent={vi.fn()}
        ariaLabel="Relationship graph"
        zoomInLabel="Zoom in"
        zoomOutLabel="Zoom out"
        resetViewLabel="Reset"
      />,
    ));

    // Frame timestamps and performance.now must share a clock: the graph's
    // camera tween uses both, alongside the cooling simulation.
    let now = performance.now();
    vi.spyOn(performance, "now").mockImplementation(() => now);
    let processedFrames = 0;
    act(() => {
      while (frames.length > 0 && processedFrames < 160) {
        now += 16.67;
        frames.shift()?.(now);
        processedFrames += 1;
      }
    });

    expect(processedFrames).toBeGreaterThan(0);
    expect(processedFrames).toBeLessThan(160);
    expect(frames).toHaveLength(0);
  });

  it("keeps the grabbed point under the cursor while dragging", () => {
    const onSelectAgent = vi.fn();
    const agents = [
      { id: "agent-1", name: "Andy", avatar_key: "abstract-1" },
      { id: "agent-2", name: "Le", avatar_key: "abstract-2" },
    ] as NamedAgent[];
    const rooms = [{
      id: "room-1",
      name: "general",
      members: agents.map((agent) => ({ member_type: "agent" as const, member_id: agent.id })),
    }] as ChannelRoom[];

    act(() => root.render(
      <AgentRelationshipGraph
        agents={agents}
        rooms={rooms}
        onSelectAgent={onSelectAgent}
        ariaLabel="Relationship graph"
        zoomInLabel="Zoom in"
        zoomOutLabel="Zoom out"
        resetViewLabel="Reset"
      />,
    ));

    expect(container.querySelector(".channel-agent-graph-toolbar")).toBeNull();
    expect(container.querySelector(".channel-agent-graph-settings-toggle")).not.toBeNull();

    const svg = container.querySelector<SVGSVGElement>(".channel-agent-graph-canvas")!;
    vi.spyOn(svg, "getBoundingClientRect").mockReturnValue({
      left: 0, top: 0, width: 960, height: 560, right: 960, bottom: 560, x: 0, y: 0, toJSON: () => ({}),
    });

    // First open fits the camera to the pre-relaxed content exactly once:
    // the viewport scale stays inside the fit clamp and the reset button
    // shows the matching live percentage.
    const viewport = container.querySelector(".channel-agent-graph-viewport")!;
    const viewportTranslate = translateOf(viewport);
    const fittedScale = Number(/scale\(([\d.]+)\)/.exec(viewport.getAttribute("transform") ?? "")?.[1]);
    expect(fittedScale).toBeGreaterThanOrEqual(0.55);
    expect(fittedScale).toBeLessThanOrEqual(1.6);
    const controls = container.querySelectorAll<HTMLButtonElement>(".channel-agent-graph-controls button");
    expect(controls[1]?.textContent).toBe(`${Math.round(fittedScale * 100)}%`);

    const node = container.querySelector<SVGGElement>('[aria-label="Andy"]')!;
    const clickNode = container.querySelector<SVGGElement>('[aria-label="Le"]')!;
    expect(node.querySelector(".channel-agent-graph-hit-target")).not.toBeNull();
    expect(node.querySelector("foreignObject")?.getAttribute("pointer-events")).toBe("none");
    act(() => {
      clickNode.dispatchEvent(pointerEvent("pointerdown", 350, 280));
      clickNode.dispatchEvent(pointerEvent("pointerup", 350, 280));
    });
    expect(onSelectAgent).toHaveBeenCalledWith(agents[1]);
    expect(window.cancelAnimationFrame).not.toHaveBeenCalled();

    // Grabbing the node's centre moves it with the cursor: a screen-space
    // delta of (+120,-60) moves the node by delta/scale in world units.
    const before = translateOf(node);
    const grabX = before.x * fittedScale + viewportTranslate.x;
    const grabY = before.y * fittedScale + viewportTranslate.y;
    act(() => {
      node.dispatchEvent(pointerEvent("pointerdown", grabX, grabY));
      node.dispatchEvent(pointerEvent("pointermove", grabX + 120, grabY - 60));
      node.dispatchEvent(pointerEvent("pointerup", grabX + 120, grabY - 60));
    });

    const first = translateOf(node);
    expect(first.x).toBeCloseTo(before.x + 120 / fittedScale, 3);
    expect(first.y).toBeCloseTo(before.y - 60 / fittedScale, 3);
    expect(Array.from(container.querySelectorAll("line")).some((line) =>
      Math.abs(Number(line.getAttribute("x1")) - first.x) < 0.01 || Math.abs(Number(line.getAttribute("x2")) - first.x) < 0.01
    )).toBe(true);

    // Grabbing off-centre must not snap the node centre to the cursor: the
    // same screen delta from a (+12,+8) grab point yields the same node
    // displacement, not a jump to the grab point.
    const offGrabX = first.x * fittedScale + viewportTranslate.x + 12;
    const offGrabY = first.y * fittedScale + viewportTranslate.y + 8;
    act(() => {
      node.dispatchEvent(pointerEvent("pointerdown", offGrabX, offGrabY));
      node.dispatchEvent(pointerEvent("pointermove", offGrabX + 60, offGrabY + 40));
      node.dispatchEvent(pointerEvent("pointerup", offGrabX + 60, offGrabY + 40));
    });

    const second = translateOf(node);
    expect(second.x).toBeCloseTo(first.x + 60 / fittedScale, 3);
    expect(second.y).toBeCloseTo(first.y + 40 / fittedScale, 3);
    expect(window.cancelAnimationFrame).not.toHaveBeenCalled();

    // Zoom buttons move the percentage.
    act(() => controls[2]?.click());
    const zoomedScale = Math.min(2.5, fittedScale * 1.25);
    expect(controls[1]?.textContent).toBe(`${Math.round(zoomedScale * 100)}%`);
    act(() => controls[0]?.click());
    expect(controls[1]?.textContent).toBe(`${Math.round(fittedScale * 100)}%`);
  });

  it("shows an agent snapshot on hover and opens details from the node", () => {
    vi.useFakeTimers();
    const agents = [
      { id: "agent-1", name: "Galileo", avatar_key: "abstract-1", activity_status: "idle" },
      { id: "agent-2", name: "Qin", avatar_key: "abstract-2", activity_status: "thinking" },
    ] as NamedAgent[];
    const rooms = [{
      id: "room-1",
      name: "product",
      members: agents.map((agent) => ({ member_type: "agent" as const, member_id: agent.id })),
    }] as ChannelRoom[];

    const onSelectAgent = vi.fn();
    act(() => root.render(
      <AgentRelationshipGraph
        agents={agents}
        rooms={rooms}
        insights={{
          "agent-1": {
            agent_id: "agent-1",
            window_days: 7,
            files_changed: 12,
            additions: 386,
            deletions: 94,
            input_tokens: 12000,
            output_tokens: 3200,
            workspace: "wuu",
            languages: [{ name: "TypeScript", lines: 480, share: 1 }],
            attribution_partial: true,
          },
          "agent-2": {
            agent_id: "agent-2",
            window_days: 7,
            files_changed: 0,
            additions: 0,
            deletions: 0,
            input_tokens: 0,
            output_tokens: 0,
            workspace: "wuu",
            languages: [],
            attribution_partial: false,
          },
        }}
        inheritedProvider="openai"
        inheritedModel="gpt-5.2"
        onSelectAgent={onSelectAgent}
        ariaLabel="Relationship graph"
        zoomInLabel="Zoom in"
        zoomOutLabel="Zoom out"
        resetViewLabel="Reset"
      />,
    ));

    const node = container.querySelector<SVGGElement>('[aria-label="Galileo"]')!;
    act(() => {
      node.dispatchEvent(pointerEvent("pointerover", 0, 0));
      vi.advanceTimersByTime(140);
    });
    const card = container.querySelector<HTMLElement>('[data-testid="channel-agent-preview-card"]')!;
    expect(card).not.toBeNull();
    expect(card.textContent).toContain("Galileo");
    expect(card.textContent).toContain("gpt-5.2");
    expect(card.textContent).toContain("TypeScript");
    expect(card.textContent).toContain("386");
    expect(container.querySelectorAll(".channel-agent-graph-node.is-active").length).toBeGreaterThan(0);
    expect(container.querySelectorAll(".channel-agent-graph-links .is-active").length).toBeGreaterThan(0);
    act(() => node.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(onSelectAgent).toHaveBeenCalledWith(agents[0]);

    const idleNode = container.querySelector<SVGGElement>('[aria-label="Qin"]')!;
    act(() => {
      idleNode.dispatchEvent(pointerEvent("pointerover", 0, 0));
      vi.advanceTimersByTime(140);
    });
    expect(card.textContent).toContain("Qin");
    expect(card.textContent).toContain("近 7 天");
    expect(card.textContent).toContain("暂无代码变更");
    expect(card.textContent?.match(/近 7 天/g)).toHaveLength(1);
  });
});
