import * as React from "react";
import { _layout } from "blobatar";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PluginAvatar } from "./PluginAvatar";
import { PublicIcon } from "./PublicIcon";
import { PluginHost } from "./plugins/PluginHost";
import { createPluginUIKit, type PluginUIAvatarProps } from "../shared/workbench";

let container: HTMLDivElement | undefined;
let root: Root | undefined;

function render(node: React.ReactNode): HTMLDivElement {
  if (!root) {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  }
  React.act(() => root!.render(node));
  return container!;
}

afterEach(() => {
  React.act(() => root?.unmount());
  container?.remove();
  root = undefined;
  container = undefined;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const bodySelector = ".mo-bob > g:not(.mo-eyes) > path";
const circlePath = _layout("circle-contract", { traits: { shape: 0.1 } }).body.path;

describe("Plugin avatar public contract", () => {
  it("shares one circular identity between the public icon and injected UI avatar", () => {
    const host = new PluginHost({ react: React, avatarComponent: PluginAvatar });
    const ui = host.ui;
    const rendered = render(<><PublicIcon name="mascot" /><ui.Avatar size={64} /></>);
    const marks = rendered.querySelectorAll("[data-agent-avatar-id]");
    expect(marks).toHaveLength(2);
    expect(marks[0].getAttribute("data-agent-avatar-id")).toBe(marks[1].getAttribute("data-agent-avatar-id"));
    const faces = rendered.querySelectorAll("[data-wuu-mascot-accessory]");
    expect(faces).toHaveLength(2);
    for (const face of faces) {
      expect(face.getAttribute("data-wuu-mascot-accessory")).toBe("headset");
      expect(face.getAttribute("data-wuu-mascot-provider-hue")).toBe("222");
      expect(face.querySelector(bodySelector)?.getAttribute("d")).toBe(circlePath);
      expect(face.querySelectorAll(".mo-eye")).toHaveLength(2);
      expect(face.querySelector(".wuu-mascot-accessory")).not.toBeNull();
    }
    expect(faces[0].querySelector(".mo-eyes")?.innerHTML).toBe(faces[1].querySelector(".mo-eyes")?.innerHTML);
    expect(marks[0].getAttribute("data-agent-avatar-motion")).toBe("static");
  });

  it("keeps the same sphere and headphones through work, failure and recovery without morph artwork", () => {
    vi.useFakeTimers();
    const rendered = render(<PluginAvatar size={64} motion="ambient" />);
    const svg = rendered.querySelector("svg")!;
    const body = svg.querySelector(bodySelector)!;
    const accessory = svg.querySelector(".wuu-mascot-accessory")!;
    expect(accessory).not.toBeNull();
    for (const status of ["thinking", "sending", "responding", "queued", "waiting", "failed", "interrupted", "idle"] as const) {
      render(<PluginAvatar size={64} status={status} motion="ambient" />);
      React.act(() => vi.advanceTimersByTime(500));
      expect(rendered.querySelector("svg")).toBe(svg);
      expect(svg.querySelector(bodySelector)).toBe(body);
      expect(body.getAttribute("d")).toBe(circlePath);
      expect(svg.querySelector(".wuu-mascot-accessory")).toBe(accessory);
      expect(svg.querySelectorAll(".mo-eye")).toHaveLength(2);
      expect(rendered.querySelector("[data-agent-avatar-state]")?.getAttribute("data-agent-avatar-state")).toBe(status);
      expect(svg.getAttribute("data-wuu-mascot-activity")).toBe(status);
      expect(svg.querySelector("[data-mascot-morph-art]")).toBeNull();
    }
  });

  it("does not deform an ambient identity during long idle periods", () => {
    vi.useFakeTimers();
    const sample = vi.fn(() => ({ x: 50, y: 11 }));
    const geometry = {
      getTotalLength: { configurable: true, value: () => 245 },
      getPointAtLength: { configurable: true, value: sample },
      getBBox: { configurable: true, value: () => ({ x: 11, y: 11, width: 78, height: 78 }) },
    };
    const originals = Object.keys(geometry).map(name => [name, Object.getOwnPropertyDescriptor(SVGElement.prototype, name)] as const);
    Object.defineProperties(SVGElement.prototype, geometry);
    try {
      const rendered = render(<PluginAvatar size={64} motion="ambient" />);
      React.act(() => vi.advanceTimersByTime(45_000));
      expect(sample).not.toHaveBeenCalled();
      expect(rendered.querySelector("[data-wuu-liquid], [data-mascot-morph-art]")).toBeNull();
      expect(rendered.querySelector(bodySelector)?.getAttribute("d")).toBe(circlePath);
    } finally {
      for (const [name, descriptor] of originals) {
        if (descriptor) Object.defineProperty(SVGElement.prototype, name, descriptor);
        else Reflect.deleteProperty(SVGElement.prototype, name);
      }
    }
  });

  it("leaves other public icons and unknown-icon recovery unchanged", () => {
    const rendered = render(<><PublicIcon name="sparkles" /><PublicIcon name="unknown-future-icon" /></>);
    expect(rendered.querySelector('[data-icon="sparkles"]')).not.toBeNull();
    expect(rendered.querySelector('[data-icon="plugin-blocks"]')).not.toBeNull();
    expect(rendered.querySelector("[data-agent-avatar-id]")).toBeNull();
  });

  it("supports hosts without an avatar renderer with a static fallback", () => {
    const ui = createPluginUIKit(React);
    const props: PluginUIAvatarProps = { size: 48, status: "thinking", motion: "ambient", className: "legacy-avatar" };
    const rendered = render(<ui.Avatar {...props} />);
    const fallback = rendered.querySelector('[data-wuu-component="plugin-ui-avatar"]');
    expect(fallback).not.toBeNull();
    expect(fallback?.getAttribute("aria-hidden")).toBe("true");
    expect(fallback?.querySelector("circle")).not.toBeNull();
    expect(fallback?.querySelector("animate")).toBeNull();
  });
});
