import { describe, expect, it } from "vitest";
import {
  browserPiPAnchors,
  browserPiPCardSize,
  browserPiPClampOrigin,
  browserPiPDragCommand,
  browserPiPFitCard,
  browserPiPNearestAnchor,
  browserPiPOrigin,
  browserPiPResizeCommand,
  browserPiPResizeRect,
  browserPiPScreenRect,
  browserPiPSizeForAspect,
  browserPiPSnapPoint,
  PIP_CARD_SIZE,
} from "./browserPiPPlacement";

const host = { x: 0, y: 0, width: 800, height: 600 };
const card = { width: 100, height: 100 };

describe("browser preview placement", () => {
  it("parks each corner one margin inside the conversation column", () => {
    const anchors = browserPiPAnchors(host, [], card);
    const origin = (alignment: string) => {
      const anchor = anchors.find((item) => item.alignment === alignment);
      if (!anchor) throw new Error(alignment);
      return browserPiPOrigin(anchor, card);
    };
    expect(origin("top-left")).toEqual({ x: 24, y: 24 });
    expect(origin("top-right")).toEqual({ x: 676, y: 24 });
    expect(origin("bottom-left")).toEqual({ x: 24, y: 476 });
    expect(origin("bottom-right")).toEqual({ x: 676, y: 476 });
  });

  it("lifts the bottom-right corner above an obstacle that covers it", () => {
    const anchors = browserPiPAnchors(host, [{ x: 600, y: 520, width: 200, height: 80 }], card);
    const anchor = anchors.find((item) => item.alignment === "bottom-right");
    expect(anchor).toBeDefined();
    expect(browserPiPOrigin(anchor!, card)).toEqual({ x: 676, y: 408 });
    expect(browserPiPOrigin(anchors.find((item) => item.alignment === "top-left")!, card)).toEqual({ x: 24, y: 24 });
  });

  it("shrinks the card so both margins still fit in a narrow column", () => {
    expect(browserPiPCardSize({ x: 0, y: 0, width: 200, height: 180 })).toEqual({ width: 132, height: 132 });
    expect(browserPiPCardSize(host)).toEqual(PIP_CARD_SIZE);
  });

  it("lets a flick choose the corner the pointer is heading toward", () => {
    const anchors = browserPiPAnchors(host, [], card);
    const mid = { x: 350, y: 476 };
    expect(browserPiPNearestAnchor(anchors, mid, card, { x: 0, y: 0 })?.alignment).toBe("bottom-left");
    expect(browserPiPNearestAnchor(anchors, mid, card, { x: 2000, y: 0 })?.alignment).toBe("bottom-right");
  });

  it("keeps a drag inside the screen work area and lands the snap on its target", () => {
    expect(browserPiPClampOrigin({ x: -40, y: 900 }, card, { x: 0, y: 0, width: 500, height: 400 }))
      .toEqual({ x: 0, y: 300 });
    expect(browserPiPSnapPoint({ x: 10, y: 20 }, { x: 80, y: 90 }, { x: 400, y: 0 }, 0)).toEqual({ x: 10, y: 20 });
    expect(browserPiPSnapPoint({ x: 10, y: 20 }, { x: 80, y: 90 }, { x: 400, y: 0 }, 1)).toEqual({ x: 80, y: 90 });
  });

  it("sizes the card to the page aspect and keeps that ratio inside a narrow column", () => {
    expect(browserPiPSizeForAspect(4 / 3)).toEqual({ width: 320, height: 240 });
    expect(browserPiPSizeForAspect(2)).toEqual({ width: 320, height: 160 });
    expect(browserPiPFitCard(host, { width: 400, height: 200 })).toEqual({ width: 400, height: 200 });
    expect(browserPiPFitCard({ x: 0, y: 0, width: 200, height: 600 }, { width: 400, height: 200 }))
      .toEqual({ width: 152, height: 76 });
  });

  it("resizes from the opposite corner while keeping the page aspect", () => {
    const start = { x: 100, y: 80, width: 200, height: 100 };
    const limits = { aspect: 2, frame: { x: 0, y: 0, width: 1000, height: 800 } };
    const grown = browserPiPResizeRect(start, "nw", { x: -40, y: -20 }, limits);
    expect(grown.width / grown.height).toBeCloseTo(2, 1);
    expect(grown.x + grown.width).toBe(300);
    expect(grown.y + grown.height).toBe(180);
    expect(grown.width).toBeGreaterThan(start.width);
    expect(browserPiPResizeCommand("wuu-pip://resize?phase=move&edge=se&x=3&y=4"))
      .toEqual({ phase: "move", edge: "se", x: 3, y: 4 });
    expect(browserPiPResizeCommand("wuu-pip://resize?phase=move&edge=nope&x=1&y=2")).toBeNull();
  });

  it("projects client rectangles by the window zoom and parses drag commands", () => {
    expect(browserPiPScreenRect({ x: 10, y: 20, width: 30, height: 40 }, { x: 100, y: 200 }, 2))
      .toEqual({ x: 120, y: 240, width: 60, height: 80 });
    expect(browserPiPDragCommand("wuu-pip://drag?phase=end&x=4&y=5&vx=6&vy=-7"))
      .toEqual({ phase: "end", x: 4, y: 5, vx: 6, vy: -7 });
    expect(browserPiPDragCommand("wuu-pip://close")).toBeNull();
  });
});
