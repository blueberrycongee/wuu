import { Blobatar } from "blobatar/react";
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WuuMascot, WuuMascotRuntimeProvider, WUU_MASCOT_ACTIVITY_PROP_LAYOUT } from "./WuuMascot";
import { EmptyConversationHome } from "./LoadingViews";
import { OnboardingMascotStage } from "./OnboardingMascotStage";


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

    rerender(home("anthropic", "claude-sonnet-4"));
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
    expect(host.querySelector(".wuu-mascot-activity-prop-thinking")).not.toBeNull();
    const thinkingLayout = WUU_MASCOT_ACTIVITY_PROP_LAYOUT.thinking;
    expect(
      host
        .querySelector(".wuu-mascot-activity-prop-thinking .wuu-mascot-activity-motion > g")
        ?.getAttribute("transform"),
    ).toContain(`translate(${thinkingLayout.x} ${thinkingLayout.y})`);
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
    expect(host.querySelector(".wuu-mascot-activity-prop-edit")).not.toBeNull();
    const editLayout = WUU_MASCOT_ACTIVITY_PROP_LAYOUT.edit;
    expect(
      host
        .querySelector(".wuu-mascot-activity-prop-edit .wuu-mascot-activity-motion > g")
        ?.getAttribute("transform"),
    ).toContain(`translate(${editLayout.x} ${editLayout.y})`);
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
