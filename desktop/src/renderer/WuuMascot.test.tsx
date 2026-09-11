import { Blobatar } from "blobatar/react";
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { WuuMascot, WUU_MASCOT_ACTIVITY_PROP_LAYOUT } from "./WuuMascot";


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

afterEach(unmount);

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
