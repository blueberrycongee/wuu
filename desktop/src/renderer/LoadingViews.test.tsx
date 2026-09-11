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
  it("shows the approved icon composition with separate animated eyes", () => {
    const view = render(
      <EmptyConversationHome title="Hello">
        <div className="hero-composer" />
      </EmptyConversationHome>,
    );

    const mascot = view.querySelector<SVGSVGElement>("svg.empty-home-mascot");
    const eyes = mascot?.querySelector(".wuu-icon-gaze");
    expect(mascot).not.toBeNull();
    expect(mascot?.getAttribute("aria-hidden")).toBe("true");
    expect(mascot?.getAttribute("viewBox")).toBe("0 0 1024 1024");
    expect(eyes?.parentElement?.getAttribute("transform")).toBe(
      `translate(${icon.bodyX + icon.radius * icon.faceX} ${icon.bodyY + icon.radius * icon.faceY}) rotate(${icon.tilt})`,
    );
    expect(eyes?.parentElement?.getAttribute("fill")).toBe(icon.eyeColor);
    expect(eyes?.querySelectorAll(".wuu-icon-blink rect")).toHaveLength(2);
    expect(mascot?.querySelector('stop[offset="0.52"]')?.getAttribute("stop-color")).toBe(icon.bodyColor);
    expect(mascot?.querySelectorAll(`g[fill="${icon.markColor}"] rect`)).toHaveLength(3);
  });
});
