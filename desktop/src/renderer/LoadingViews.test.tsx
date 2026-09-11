import icon from "../../../assets/app-icon-source.json";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { EmptyConversationHome, RuntimeLoading, ViewSwitchLoading } from "./LoadingViews";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function render(view: JSX.Element): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root?.render(view));
  return container;
}

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  container?.remove();
  container = undefined;
});

describe("RuntimeLoading", () => {
  it("uses the animated approved icon on the loading surface", () => {
    const view = render(<RuntimeLoading status="connecting" />);

    const glass = view.querySelector(".wuu-launch-glass");
    const mascot = glass?.querySelector<SVGSVGElement>("svg.wuu-launch-mascot");

    expect(glass).not.toBeNull();
    expect(mascot).not.toBeNull();
    expect(mascot?.querySelector(".wuu-icon-gaze")).not.toBeNull();
    expect(mascot?.querySelectorAll(".wuu-icon-blink")).toHaveLength(2);
    expect(view.querySelector(".wuu-launch-mark")).toBeNull();
    expect(view.querySelector(".wuu-launch-rail")).toBeNull();
  });

  it("keeps the compact legacy loader for view switches", () => {
    const view = render(<ViewSwitchLoading />);

    expect(view.querySelector(".wuu-launch-mark")).not.toBeNull();
    expect(view.querySelector(".wuu-launch-rail")).not.toBeNull();
    expect(view.querySelector(".wuu-launch-glass")).toBeNull();
  });
});

describe("EmptyConversationHome", () => {
  it("keeps the greeting as a full round mascot in the icon palette", () => {
    const view = render(
      <EmptyConversationHome title="Hello">
        <div className="hero-composer" />
      </EmptyConversationHome>,
    );

    const mascot = view.querySelector<SVGSVGElement>("svg.empty-home-mascot");
    expect(mascot).not.toBeNull();
    expect(mascot?.getAttribute("aria-hidden")).toBe("true");
    expect(mascot?.getAttribute("viewBox")).toBe("0 0 100 100");
    expect(mascot?.style.getPropertyValue("--mo-head")).toBe(icon.bodyColor);
    expect(mascot?.style.getPropertyValue("--mo-eye")).toBe(icon.eyeColor);
    expect(mascot?.querySelector(".mo-eyes")).not.toBeNull();
    expect(mascot?.hasAttribute("data-wuu-mascot-follows-pointer")).toBe(true);
    expect(mascot?.getAttribute("data-wuu-mascot-accessory")).toBe("none");
    expect(mascot?.querySelector("clipPath")).toBeNull();
    expect(mascot?.classList.contains("wuu-icon-mascot")).toBe(false);
  });
});
