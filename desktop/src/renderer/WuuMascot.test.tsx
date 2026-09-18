import { Blobatar } from "blobatar/react";
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WuuMascot, WuuMascotRuntimeProvider, modelMascotAccessory } from "./WuuMascot";
import { EmptyConversationHome } from "./LoadingViews";
import { OnboardingMascotStage } from "./OnboardingMascotStage";
import { AgentAvatarMark } from "./AgentAvatarMark";


let container: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactElement): HTMLDivElement {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(node);
  });
  return container;
}

function rerender(node: ReactElement): void {
  act(() => {
    root!.render(node);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
}

afterEach(() => {
  unmount();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function facePaths(svg: Element): string {
  // Status props (pencil, question mark) portal into a sibling of `.mo-eyes`
  // and are supposed to remount. Compare only the blobatar-owned body and
  // eyes so an activity switch cannot look like a face rebuild.
  const body = svg.querySelector(".mo-bob > g:not(.mo-eyes):not(.wuu-mascot-layer) path");
  return [body, ...svg.querySelectorAll(".mo-eye path")]
    .map((path) => path?.getAttribute("d") ?? "")
    .join("|");
}

describe("WuuMascot activity morph", () => {
  it("skips transcript geometry work and restores the same identity across static/dynamic switches", () => {
    vi.useFakeTimers();
    vi.stubGlobal("IntersectionObserver", class { observe() {} disconnect() {} });
    const reduced = new EventTarget() as MediaQueryList;
    Object.defineProperty(reduced, "matches", { value: false, writable: true });
    vi.stubGlobal("matchMedia", () => reduced);
    const sample = vi.fn((distance: number) => ({ x: distance, y: 50 }));
    const length = vi.fn(() => 100);
    const names = ["getTotalLength", "getPointAtLength"] as const;
    const original = names.map(name => Object.getOwnPropertyDescriptor(SVGElement.prototype, name));
    Object.defineProperties(SVGElement.prototype, {
      getTotalLength: { configurable: true, value: length },
      getPointAtLength: { configurable: true, value: sample },
    });
    const avatar = (dynamic: boolean, avatarKey = "abstract-1") => (
      <AgentAvatarMark seed="transcript" avatarKey={avatarKey} status="thinking"
        disableMorph={!dynamic} motion={dynamic ? "expressive" : "static"} />
    );
    try {
      const host = render(avatar(false));
      const svg = host.querySelector("svg")!;
      const identity = facePaths(svg);
      const authored = svg.querySelector<SVGGElement>(".mo-root")!.style.cssText;
      act(() => vi.advanceTimersByTime(1600));
      expect(length).not.toHaveBeenCalled();
      expect(sample).not.toHaveBeenCalled();

      rerender(avatar(true));
      expect(sample).toHaveBeenCalled();
      expect(host.querySelector("svg")).toBe(svg);
      act(() => vi.advanceTimersByTime(200));
      const animated = svg.querySelector("[data-mascot-morph-art]")!.innerHTML;
      act(() => vi.advanceTimersByTime(200));
      expect(svg.querySelector("[data-mascot-morph-art]")!.innerHTML).not.toBe(animated);

      rerender(avatar(false));
      expect(host.querySelector("svg")).toBe(svg);
      expect(facePaths(svg)).toBe(identity);
      expect(svg.querySelector("[data-mascot-morph-art]")).toBeNull();
      expect(svg.querySelector<SVGGElement>(".mo-root")!.style.cssText).toBe(authored);
      sample.mockClear();
      act(() => vi.advanceTimersByTime(1600));
      expect(sample).not.toHaveBeenCalled();
      expect(svg.querySelector<SVGGElement>(".mo-root")!.style.cssText).toBe(authored);

      rerender(avatar(false, "abstract-5"));
      expect(facePaths(host.querySelector("svg")!)).not.toBe(identity);
      expect(sample).not.toHaveBeenCalled();
      rerender(avatar(true, "abstract-5"));
      expect(sample).toHaveBeenCalled();
      Object.defineProperty(reduced, "matches", { value: true });
      act(() => { reduced.dispatchEvent(new Event("change")); vi.advanceTimersByTime(50); });
      const art = host.querySelector("[data-mascot-morph-art]")!;
      const parked = art.innerHTML;
      act(() => vi.advanceTimersByTime(1000));
      expect(art.innerHTML).toBe(parked);
      unmount();
      expect(art.isConnected).toBe(false);
    } finally {
      unmount();
      names.forEach((name, index) => {
        if (original[index]) Object.defineProperty(SVGElement.prototype, name, original[index]!);
        else Reflect.deleteProperty(SVGElement.prototype, name);
      });
    }
  });

  it("keeps brand scenes independent of the surrounding runtime while allowing explicit accessories", () => {
    const scene = (model: string, accessory?: "beanie") => (
      <WuuMascotRuntimeProvider provider="openai" model={model}>
        <WuuMascot brand accessory={accessory} />
      </WuuMascotRuntimeProvider>
    );
    const host = render(scene("gpt-5"));
    const svg = host.querySelector("svg")!;
    const colour = svg.style.getPropertyValue("--mo-head");
    expect(svg.querySelector(".wuu-mascot-accessory")).toBeNull();
    rerender(scene("claude-sonnet-4"));
    expect(svg.style.getPropertyValue("--mo-head")).toBe(colour);
    expect(svg.querySelector(".wuu-mascot-accessory")).toBeNull();
    rerender(scene("claude-sonnet-4", "beanie"));
    expect(svg.querySelector(".wuu-mascot-accessory")).not.toBeNull();
  });

  it("carries onboarding equipment with the face and updates capabilities without replacing either", () => {
    const host = render(<OnboardingMascotStage pluginIDs={["goal"]} />);
    const svg = host.querySelector("svg")!;
    const eye = svg.querySelector(".mo-eye path");
    const headband = svg.querySelector('[data-onboarding-capability="goal"]')!;
    expect(headband).not.toBeNull();
    expect(headband.closest(".mo-bob")).toBe(eye?.closest(".mo-bob"));
    rerender(<OnboardingMascotStage pluginIDs={["goal", "memory", "subagent"]} />);
    expect(svg.querySelector(".mo-eye path")).toBe(eye);
    expect(svg.querySelector('[data-onboarding-capability="goal"]')).toBe(headband);
    expect(svg.querySelector('[data-onboarding-capability="memory"]')).not.toBeNull();
    rerender(<OnboardingMascotStage pluginIDs={[]} />);
    expect(svg.querySelector("[data-onboarding-capability]")).toBeNull();
    expect(svg.querySelector(".mo-eye path")).toBe(eye);
  });

  it("updates the empty home's runtime colour and worn model accessory without rebuilding its face", () => {
    const home = (provider: string, model: string) => (
      <WuuMascotRuntimeProvider provider={provider} providers={["openai", "anthropic"]} model={model}>
        <EmptyConversationHome title="Hello" />
      </WuuMascotRuntimeProvider>
    );
    const host = render(home("openai", "gpt-5"));
    const svg = host.querySelector("svg")!;
    const eyes = [...svg.querySelectorAll(".mo-eye path")];
    const colour = svg.style.getPropertyValue("--mo-head");
    const accessory = svg.getAttribute("data-wuu-mascot-accessory");
    const art = svg.querySelector(".wuu-mascot-accessory")!;
    expect(art).not.toBeNull();

    rerender(home("anthropic", "gpt-5"));
    expect(host.querySelector("svg")).toBe(svg);
    expect(svg.style.getPropertyValue("--mo-head")).not.toBe(colour);
    expect(svg.querySelector(".wuu-mascot-accessory")).toBe(art);
    const nextColour = svg.style.getPropertyValue("--mo-head");

    // Models may share an accessory; exercise a switch without pinning buckets.
    const nextModel = Array.from({ length: 32 }, (_, index) => `model-${index}`)
      .find(model => modelMascotAccessory(model) !== accessory);
    expect(nextModel).toBeDefined();
    rerender(home("anthropic", nextModel!));
    expect(svg.style.getPropertyValue("--mo-head")).toBe(nextColour);
    expect(svg.getAttribute("data-wuu-mascot-accessory")).not.toBe(accessory);
    expect(svg.querySelector(".wuu-mascot-accessory")).not.toBeNull();
    expect([...svg.querySelectorAll(".mo-eye path")]).toEqual(eyes);
  });

  it("cancels an exit on reactivation, then removes and restores the complete mascot", () => {
    vi.useFakeTimers();
    const host = render(<WuuMascot visible activity="thinking" accessory="beanie" />);
    const svg = host.querySelector("svg");
    rerender(<WuuMascot visible={false} accessory="beanie" />);
    act(() => vi.advanceTimersByTime(80));
    rerender(<WuuMascot visible activity="read" accessory="beanie" />);
    act(() => vi.advanceTimersByTime(1_000));
    expect(host.querySelector("svg")).toBe(svg);
    expect(host.querySelector(".wuu-mascot-accessory")).not.toBeNull();
    rerender(<WuuMascot visible={false} accessory="beanie" />);
    act(() => vi.advanceTimersByTime(1_000));
    expect(host.querySelector("svg")).toBeNull();
    rerender(<WuuMascot visible accessory="headset" />);
    expect(host.querySelector(".wuu-mascot-layer-rear path")).not.toBeNull();
    expect(host.querySelector(".wuu-mascot-layer-front rect")).not.toBeNull();
  });

  it("changes an explicit colour without replacing the body, eyes or worn accessory", () => {
    const host = render(<WuuMascot identityHue={14} accessory="beanie" />);
    const svg = host.querySelector("svg")!;
    const body = svg.querySelector(".mo-root");
    const art = svg.querySelector(".wuu-mascot-accessory");
    const paths = facePaths(svg);
    const colour = svg.style.getPropertyValue("--mo-head");
    const accessoryColour = (art as SVGGElement).style.getPropertyValue("--wuu-accessory-color");
    rerender(<WuuMascot identityHue={52} accessory="beanie" />);
    expect((art as SVGGElement).style.getPropertyValue("--wuu-accessory-color")).toBe(accessoryColour);
    rerender(<WuuMascot identityHue={202} accessory="beanie" />);
    const paintPlanes = [...svg.querySelectorAll<SVGGElement>(".wuu-mascot-accessory")];
    const nextAccessoryColour = paintPlanes[0].style.getPropertyValue("--wuu-accessory-color");
    expect(nextAccessoryColour).not.toBe(accessoryColour);
    expect(paintPlanes.every(plane => plane.style.getPropertyValue("--wuu-accessory-color") === nextAccessoryColour)).toBe(true);
    expect(svg.style.getPropertyValue("--mo-head")).not.toBe(colour);
    expect(svg.querySelector(".mo-root")).toBe(body);
    expect(svg.querySelector(".wuu-mascot-accessory")).toBe(art);
    expect(facePaths(svg)).toBe(paths);
  });

  it("lets work and reduced motion cancel autonomous glances without changing the activity", () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0);
    const preference = new EventTarget() as MediaQueryList;
    Object.defineProperty(preference, "matches", { value: false, writable: true });
    vi.stubGlobal("matchMedia", () => preference);
    const host = render(<WuuMascot ambient accessory="none" />);
    const svg = host.querySelector("svg")!;
    const initial = facePaths(svg);
    act(() => vi.advanceTimersByTime(3_200));
    expect(facePaths(svg)).not.toBe(initial);
    expect(svg.getAttribute("data-wuu-mascot-activity")).toBe("idle");
    act(() => {
      Object.defineProperty(preference, "matches", { value: true });
      preference.dispatchEvent(new Event("change"));
    });
    expect(facePaths(svg)).toBe(initial);
    act(() => vi.advanceTimersByTime(10_000));
    expect(facePaths(svg)).toBe(initial);
    rerender(<WuuMascot ambient activity="thinking" accessory="none" />);
    const working = facePaths(svg);
    act(() => vi.advanceTimersByTime(10_000));
    expect(facePaths(svg)).toBe(working);
    expect(svg.getAttribute("data-wuu-mascot-activity")).toBe("thinking");
  });

  it("turns the surface in place and preserves accessory portal nodes", () => {
    const host = render(<WuuMascot activity="thinking" accessory="none" />);
    const svg = host.querySelector("svg")!;
    const thinkingPaths = facePaths(svg);
    const rootGroup = svg.querySelector(".mo-root");
    const eyeNodes = [...svg.querySelectorAll(".mo-eye path")];
    const portal = svg.querySelector(".wuu-mascot-layer-front");

    expect(svg.getAttribute("data-wuu-mascot-activity")).toBe("thinking");
    expect(thinkingPaths.length).toBeGreaterThan(0);

    rerender(<WuuMascot activity="edit" accessory="none" />);

    const next = host.querySelector("svg")!;
    expect(next).toBe(svg);
    expect(next.querySelector(".mo-root")).toBe(rootGroup);
    expect(facePaths(next)).not.toBe(thinkingPaths);
    expect([...next.querySelectorAll(".mo-eye path")]).toEqual(eyeNodes);
    expect(next.querySelector(".wuu-mascot-layer-front")).toBe(portal);
    expect(next.getAttribute("data-wuu-mascot-activity")).toBe("edit");
    expect(host.querySelector(".wuu-mascot-activity-prop-thinking")).toBeNull();

  });

  it("updates the camera for an activity even when the eye expression stays the same", () => {
    const host = render(<WuuMascot activity="read" accessory="none" />);
    const svg = host.querySelector("svg")!;
    const path = svg.querySelector(".mo-eye path")!;
    const before = path.getAttribute("d");
    rerender(<WuuMascot activity="tool" accessory="none" />);
    expect(svg.querySelector(".mo-eye path")).toBe(path);
    expect(path.getAttribute("d")).not.toBe(before);
  });
});

it("restores authored contours when switching an animated avatar out of surface mode", () => {
  const props = { name: "surface-toggle", traits: { shape: 0.8 }, animate: "always" as const };
  const host = render(<Blobatar {...props} />);
  const flat = [...host.querySelectorAll(".mo-eye path")].map((path) => path.getAttribute("d"));
  rerender(<Blobatar {...props} perspective={{ yaw: 35, strength: 1 }} />);
  expect([...host.querySelectorAll(".mo-eye path")].map((path) => path.getAttribute("d"))).not.toEqual(flat);
  rerender(<Blobatar {...props} />);
  expect([...host.querySelectorAll(".mo-eye path")].map((path) => path.getAttribute("d"))).toEqual(flat);
});
