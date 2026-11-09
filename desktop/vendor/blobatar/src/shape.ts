/**
 * The single primitive.
 *
 * |x/a|^n + |y/b|^n = 1 covers the whole part vocabulary: n=2 is an ellipse
 * (eyes, pupils), n≈4 a squircle (head, background), n→large a rectangle
 * (brows, mouth lines). One shape function, one continuous knob, so "head
 * shape" is a numeric trait rather than a set of hand-drawn alternatives.
 */

export interface Superellipse {
  cx: number;
  cy: number;
  rx: number;
  ry: number;
  /** Squareness. Useful range is roughly 1.6 (soft diamond) to 8 (near-rect). */
  n?: number;
  /** Degrees, clockwise. Baked into the coordinates so the SVG needs no transform. */
  rot?: number;
}

const r2 = (v: number) => {
  const s = Math.round(v * 100) / 100;
  return Object.is(s, -0) ? "0" : String(s);
};

/** A cubic Bézier segment: anchor, control, control, anchor. */
export type Seg = [number, number][];

/**
 * Approximates each quadrant with one cubic Bézier.
 *
 * The control offset is chosen so the curve passes exactly through the
 * superellipse's 45° point: B(0.5) = a(4+3k)/8 must equal a·2^(-1/n).
 * At n=2 this yields 0.5523 — the standard circle constant — which is a good
 * sign the derivation is right. Four segments instead of a 24-point sampled
 * polyline keeps each shape at ~130 bytes of path data.
 *
 * Returned as segments rather than markup so `segsBounds` can measure the
 * exact same curve the serializer draws.
 */
export function superellipseSegs({ cx, cy, rx, ry, n = 4, rot = 0 }: Superellipse): Seg[] {
  // Above n≈5.55 the control offset exceeds the radius, and the curve bulges
  // outside the bounding box instead of squaring off — an inflated-looking
  // corner rather than a sharper one. Clamping k trades exactness at the 45°
  // point for a shape that always stays within its stated bounds; past that
  // point a superellipse is visually a rounded rect anyway.
  const k = Math.min(1, (8 * Math.pow(2, -1 / n) - 4) / 3);
  const a = rx;
  const b = ry;
  const ak = a * k;
  const bk = b * k;

  // Anchor, control, control — walking the four quadrants.
  const pts: [number, number][] = [
    [a, 0],
    [a, bk], [ak, b], [0, b],
    [-ak, b], [-a, bk], [-a, 0],
    [-a, -bk], [-ak, -b], [0, -b],
    [ak, -b], [a, -bk], [a, 0],
  ];

  const t = (rot * Math.PI) / 180;
  const cos = Math.cos(t);
  const sin = Math.sin(t);
  const at = (i: number): [number, number] => {
    const [x, y] = pts[i]!;
    return [cx + x * cos - y * sin, cy + x * sin + y * cos];
  };

  const segs: Seg[] = [];
  for (let i = 0; i < 12; i += 3) segs.push([at(i), at(i + 1), at(i + 2), at(i + 3)]);
  return segs;
}

/** Serializes a closed Bézier figure — the one path format every shape shares. */
export function segsPath(segs: Seg[]): string {
  let d = `M${r2(segs[0]![0]![0])} ${r2(segs[0]![0]![1])}`;
  for (const s of segs)
    d += `C${r2(s[1]![0])} ${r2(s[1]![1])} ${r2(s[2]![0])} ${r2(s[2]![1])} ${r2(s[3]![0])} ${r2(s[3]![1])}`;
  return d + "Z";
}

export function superellipse(se: Superellipse): string {
  return segsPath(superellipseSegs(se));
}

/**
 * A quadratic arc, stroked — used only for smiles and frowns, where a closed
 * superellipse would need a boolean subtraction to get the same read.
 */
export function arc(cx: number, cy: number, w: number, depth: number): string {
  return `M${r2(cx - w)} ${r2(cy)}Q${r2(cx)} ${r2(cy + depth)} ${r2(cx + w)} ${r2(cy)}`;
}

/**
 * An organic closed curve: radii sampled around a circle, joined by a closed
 * Catmull-Rom spline converted to cubic Béziers.
 *
 * The superellipse handles everything symmetric; this handles everything that
 * needs to look hand-drawn. `radii` are multipliers of the base radius, one per
 * vertex, so a seed perturbing them by ±15% produces the lopsided pebble shapes
 * without any noise function — the vertex count alone controls how lumpy it is.
 *
 * Catmull-Rom rather than a Bézier fit because it interpolates its points
 * exactly, so the radii mean what they say and containment stays predictable.
 *
 * Returned as segments rather than markup so `segsBounds` can measure the
 * exact same curve the serializer draws.
 */
export function blobSegs(
  cx: number,
  cy: number,
  rx: number,
  ry: number,
  radii: number[],
  rot = 0,
): Seg[] {
  const n = radii.length;
  const t0 = (rot * Math.PI) / 180;
  const p: [number, number][] = radii.map((m, i) => {
    const a = t0 + (2 * Math.PI * i) / n;
    return [cx + rx * m * Math.cos(a), cy + ry * m * Math.sin(a)];
  });

  const at = (i: number) => p[((i % n) + n) % n]!;
  const segs: Seg[] = [];
  for (let i = 0; i < n; i++) {
    const [x0, y0] = at(i - 1);
    const [x1, y1] = at(i);
    const [x2, y2] = at(i + 1);
    const [x3, y3] = at(i + 2);
    segs.push([
      [x1, y1],
      [x1 + (x2 - x0) / 6, y1 + (y2 - y0) / 6],
      [x2 - (x3 - x1) / 6, y2 - (y3 - y1) / 6],
      [x2, y2],
    ]);
  }
  return segs;
}

export function blobPath(
  cx: number,
  cy: number,
  rx: number,
  ry: number,
  radii: number[],
  rot = 0,
): string {
  const segs = blobSegs(cx, cy, rx, ry, radii, rot);
  let d = `M${r2(segs[0]![0]![0])} ${r2(segs[0]![0]![1])}`;
  for (const s of segs)
    d += `C${r2(s[1]![0])} ${r2(s[1]![1])} ${r2(s[2]![0])} ${r2(s[2]![1])} ${r2(s[3]![0])} ${r2(s[3]![1])}`;
  return d + "Z";
}

/**
 * Tight axis-aligned bounds of a closed Bézier figure.
 *
 * A cubic's per-axis extrema sit at its derivative's roots, and the derivative
 * is quadratic — so each segment's extremes are solved exactly rather than
 * sampled. That exactness is what the layout's size normalization and the
 * frame-containment test both rely on: a sampled bound would be off by however
 * lumpy the seed made the spline.
 */
export function segsBounds(segs: Seg[]): { minX: number; maxX: number; minY: number; maxY: number } {
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  const visit = (x: number, y: number) => {
    if (x < minX) minX = x;
    if (x > maxX) maxX = x;
    if (y < minY) minY = y;
    if (y > maxY) maxY = y;
  };
  const at = (s: Seg, t: number) => {
    const u = 1 - t;
    const w0 = u * u * u;
    const w1 = 3 * u * u * t;
    const w2 = 3 * u * t * t;
    const w3 = t * t * t;
    return [
      w0 * s[0]![0] + w1 * s[1]![0] + w2 * s[2]![0] + w3 * s[3]![0],
      w0 * s[0]![1] + w1 * s[1]![1] + w2 * s[2]![1] + w3 * s[3]![1],
    ] as const;
  };
  for (const s of segs) {
    visit(s[0]![0], s[0]![1]);
    visit(s[3]![0], s[3]![1]);
    for (const axis of [0, 1] as const) {
      const p0 = s[0]![axis];
      const p1 = s[1]![axis];
      const p2 = s[2]![axis];
      const p3 = s[3]![axis];
      // d/dt: 3(-p0+3p1-3p2+p3)t² + 6(p0-2p1+p2)t + 3(p1-p0).
      const qa = 3 * (-p0 + 3 * p1 - 3 * p2 + p3);
      const qb = 6 * (p0 - 2 * p1 + p2);
      const qc = 3 * (p1 - p0);
      const roots: number[] = [];
      if (Math.abs(qa) < 1e-9) {
        if (Math.abs(qb) > 1e-9) roots.push(-qc / qb);
      } else {
        const disc = qb * qb - 4 * qa * qc;
        if (disc >= 0) {
          const sq = Math.sqrt(disc);
          roots.push((-qb + sq) / (2 * qa), (-qb - sq) / (2 * qa));
        }
      }
      for (const t of roots) {
        if (t > 0 && t < 1) {
          const [x, y] = at(s, t);
          visit(x, y);
        }
      }
    }
  }
  return { minX, maxX, minY, maxY };
}
