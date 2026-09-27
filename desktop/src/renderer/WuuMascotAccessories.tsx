import { palette } from "blobatar";
import type { CSSProperties } from "react";

/** These IDs also appear in saved agent identities. */
export const WUU_MASCOT_ACCESSORIES = ["none", "beanie", "hard-hat", "headset", "leaf"] as const;
export type WuuMascotAccessory = typeof WUU_MASCOT_ACCESSORIES[number];
type WornAccessory = Exclude<WuuMascotAccessory, "none">;

type Body = { cx: number; cy: number; rx: number; ry: number };
type Point = [number, number];
export type AccessoryFit = {
  top: number;
  leafTop: number;
  left: number;
  right: number;
  crownX: number;
  crownWidth: number;
  headsetBand: string;
  headsetEarX: number;
  headsetEarY: number;
};

// Authored on a radius-32 circle. The rear paint plane lets the silhouette
// occlude whatever sits inside it, so this band is warped onto the measured
// body before it is drawn. A circular chord through a tapered crown would
// otherwise vanish mid-arc and look like it is cutting through the head.
const AUTHORED_HEADSET_BAND = "M-30-8L-31-25Q-31-37-20-36L23-31Q34-30 33-19L32 1";
const AUTHORED_HEADSET_EAR: Point = [32, 1];
const ROUND_FIT: AccessoryFit = {
  top: -32, leafTop: -32, left: -32, right: 32, crownX: 0, crownWidth: 1,
  headsetBand: AUTHORED_HEADSET_BAND, headsetEarX: 0, headsetEarY: 0,
};

function lerp(a: Point, b: Point, t: number): Point {
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t];
}

function quad(p0: Point, c: Point, p1: Point, t: number): Point {
  const u = 1 - t;
  return [
    u * u * p0[0] + 2 * u * t * c[0] + t * t * p1[0],
    u * u * p0[1] + 2 * u * t * c[1] + t * t * p1[1],
  ];
}

function sampleAuthoredHeadset(): Point[] {
  const start: Point = [-30, -8];
  const segments: Array<(t: number) => Point> = [
    t => lerp(start, [-31, -25], t),
    t => quad([-31, -25], [-31, -37], [-20, -36], t),
    t => lerp([-20, -36], [23, -31], t),
    t => quad([23, -31], [34, -30], [33, -19], t),
    t => lerp([33, -19], AUTHORED_HEADSET_EAR, t),
  ];
  const points: Point[] = [start];
  for (const sample of segments) {
    for (let i = 1; i <= 8; i++) points.push(sample(i / 8));
  }
  return points;
}

function formatHeadsetPath(points: Point[]): string {
  return points.map((point, index) => {
    const x = Number(point[0].toFixed(1));
    const y = Number(point[1].toFixed(1));
    return `${index ? "L" : "M"}${x},${y}`;
  }).join("");
}

function warpHeadset(inside: (x: number, y: number) => boolean): Pick<AccessoryFit, "headsetBand" | "headsetEarX" | "headsetEarY"> | null {
  const radius = (ux: number, uy: number): number => {
    for (let d = 64; d >= 0; d -= 0.5) {
      if (inside(ux * d, uy * d)) return d;
    }
    return 0;
  };
  const warped: Point[] = [];
  for (const [x, y] of sampleAuthoredHeadset()) {
    const r = Math.hypot(x, y);
    if (r < 1e-6) continue;
    const bodyR = radius(x / r, y / r);
    if (bodyR < 1) continue;
    const next = bodyR + r - 32;
    warped.push([x / r * next, y / r * next]);
  }
  if (warped.length < 8) return null;
  const ear = warped[warped.length - 1]!;
  return {
    headsetBand: formatHeadsetPath(warped),
    headsetEarX: ear[0] - AUTHORED_HEADSET_EAR[0],
    headsetEarY: ear[1] - AUTHORED_HEADSET_EAR[1],
  };
}

// Read the rendered core and petals once per identity. Reusing their fill avoids
// a second implementation of the avatar generator's organic contour geometry.
export function measureAccessoryFit(layer: SVGGElement, body: Body): AccessoryFit {
  const shapes = [...layer.children].filter((node): node is SVGGeometryElement =>
    (node.tagName === "path" || node.tagName === "circle") && "isPointInFill" in node);
  // Non-rendering environments (including SSR tests) have no SVG geometry API.
  if (!shapes.length) return ROUND_FIT;
  const point = layer.ownerSVGElement!.createSVGPoint();
  const inside = (x: number, y: number) => {
    point.x = body.cx + x * body.rx / 32;
    point.y = body.cy + y * body.ry / 32;
    return shapes.some(shape => shape.isPointInFill(point));
  };
  const edge = (horizontal: boolean, fixed: number, reverse: boolean) => {
    for (let d = 64; d >= -64; d -= 0.5) {
      const moving = reverse ? -d : d;
      if (inside(horizontal ? moving : fixed, horizontal ? fixed : moving)) return moving;
    }
    return 0;
  };
  // A cap bridges the forehead; a dip between two lobes is not its support.
  const top = Math.min(...[-16, -8, 0, 8, 16].map(x => edge(false, x, true)));
  const span = (y: number) => {
    const left = edge(true, y, true);
    const right = edge(true, y, false);
    return { x: (left + right) / 2, width: Math.max(0.65, Math.min(1.5, (right - left) / (2 * Math.sqrt(32 ** 2 - 20 ** 2)))) };
  };
  const crown = span(top + 12);
  // Fit the full ear cushion height, including bodies that widen below the eyes.
  const left = Math.min(...[-12, 0, 14].map(y => edge(true, y, true)));
  const right = Math.max(...[-12, 0, 14].map(y => edge(true, y, false)));
  if (right - left < 8 || top > -8) return ROUND_FIT;
  return {
    top, leafTop: edge(false, 4, true), left, right, crownX: crown.x, crownWidth: crown.width,
    ...(warpHeadset(inside) ?? { headsetBand: AUTHORED_HEADSET_BAND, headsetEarX: 0, headsetEarY: 0 }),
  };
}

function fitTransform(accessory: Exclude<WornAccessory, "headset">, fit: AccessoryFit): string {
  switch (accessory) {
    case "beanie":
    case "hard-hat": return `translate(${fit.crownX} ${fit.top + 32}) scale(${fit.crownWidth} 1)`;
    case "leaf": return `translate(0 ${fit.leafTop + 32})`;
  }
}

// Authored pairs, not generated complementary colours. Keep each piece's
// signature colour unless it sits in the same hue family as the body.
const ACCESSORY_COLORS = {
  beanie: [222, 52],
  "hard-hat": [52, 222],
  headset: [14, 222],
  leaf: [96, 14],
} as const satisfies Record<WornAccessory, readonly [number, number]>;
const SWATCHES = Object.fromEntries([14, 52, 96, 222].map(hue => [hue, palette(hue).head]));

export function mascotAccessoryColor(accessory: WornAccessory, bodyHue: number): string {
  const [primary, alternate] = ACCESSORY_COLORS[accessory];
  const hue = Number.isFinite(bodyHue) ? ((bodyHue % 360) + 360) % 360 : 14;
  const distance = Math.abs(hue - primary);
  return SWATCHES[Math.min(distance, 360 - distance) < 35 ? alternate : primary]!;
}

function AccessoryArt({ accessory, rear, fit }: { accessory: WornAccessory; rear: boolean; fit: AccessoryFit }): JSX.Element | null {
  if (rear) {
    switch (accessory) {
      case "beanie": return <path className="wuu-accessory-trim" transform="translate(0 -3) rotate(8)" d="M-27-17C-30-29-18-36 0-36C18-36 30-29 27-17Q0-26-27-17Z" />;
      case "headset": return <path className="wuu-accessory-line wuu-accessory-headband" d={fit.headsetBand} />;
      default: return null;
    }
  }
  switch (accessory) {
    case "beanie": return <g transform="translate(0 -3) rotate(8)">
      <path className="wuu-accessory-fill" d="M-27-23C-28-34-22-44-11-46C-5-47 0-44 6-43C20-42 27-35 27-23L25-19H-25Z" />
      <path className="wuu-accessory-trim" d="M-27-25Q0-33 27-25Q30-24 29-21L27-15Q0-23-27-15L-29-21Q-30-24-27-25Z" />
    </g>;
    case "hard-hat": return <g transform="translate(0 -3) rotate(-6)">
      <path className="wuu-accessory-fill" d="M-26-22C-26-35-17-43-3-43C12-43 24-34 25-21L22-18H-23Z" />
      <path className="wuu-accessory-trim" d="M-5-42Q-5-46-1-46H3Q6-46 6-42L5-27H-5Z" />
      <path className="wuu-accessory-fill" d="M-27-24Q0-29 27-22L31-17Q32-14 28-14Q0-22-29-17Q-33-17-32-20Z" />
    </g>;
    case "headset": return <g transform={`translate(${fit.headsetEarX} ${fit.headsetEarY})`}>
      <path className="wuu-accessory-line" d="M32 10Q32 28 9 29" />
      <rect className="wuu-accessory-fill" x="30" y="-12" width="12" height="26" rx="6" transform="rotate(8 36 1)" />
      <rect className="wuu-accessory-trim" x="3" y="26" width="9" height="6" rx="3" />
    </g>;
    case "leaf": return <>
      <path className="wuu-accessory-fill" d="M-2-33C-17-32-24-40-20-48C-7-50 7-46 5-37Q3-33-2-33Z" />
      <path className="wuu-accessory-line" d="M-14-43Q0-39 4-30" />
    </>;
  }
}

export function MascotAccessory({ accessory, layer, body, bodyHue, fit }: {
  accessory: WornAccessory;
  layer: "rear" | "front";
  body: Body;
  bodyHue: number;
  fit: AccessoryFit;
}): JSX.Element {
  // Both paint planes share a radius-32 chart, fitted to the actual identity.
  const style = { "--wuu-accessory-color": mascotAccessoryColor(accessory, bodyHue) } as CSSProperties;
  return <g className={`wuu-mascot-accessory wuu-mascot-accessory-${accessory}`} style={style} aria-hidden="true">
    <g transform={`translate(${body.cx} ${body.cy}) scale(${body.rx / 32} ${body.ry / 32})`}>
      <g className={accessory === "leaf" ? undefined : "wuu-accessory-motion"}>
        {accessory === "headset"
          ? <AccessoryArt accessory={accessory} rear={layer === "rear"} fit={fit} />
          : <g transform={fitTransform(accessory, fit)}><AccessoryArt accessory={accessory} rear={layer === "rear"} fit={fit} /></g>}
      </g>
    </g>
  </g>;
}
