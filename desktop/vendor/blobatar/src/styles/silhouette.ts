import { segsBounds, segsPath, superellipseSegs, type Seg } from "../shape";

export const SHAPES = [
  { id: "round", trait: 0.1 },
  { id: "rounded-square", trait: 0.3 },
  { id: "capsule", trait: 0.5 },
  { id: "triangle", trait: 0.7 },
  { id: "diamond", trait: 0.9 },
] as const;
export type Shape = (typeof SHAPES)[number]["id"];

type Point = [number, number];

function roundedPolygon(points: Point[], inset: number): Seg[] {
  const toward = (a: Point, b: Point): Point => {
    const distance = Math.hypot(b[0] - a[0], b[1] - a[1]);
    return [a[0] + (b[0] - a[0]) * inset / distance, a[1] + (b[1] - a[1]) * inset / distance];
  };
  const corners = points.map((p, i) => ({
    p,
    start: toward(p, points[(i + points.length - 1) % points.length]!),
    end: toward(p, points[(i + 1) % points.length]!),
  }));
  return corners.flatMap(({ p, start, end }, i): Seg[] => {
    const next = corners[(i + 1) % corners.length]!.start;
    // Convert a quadratic corner to cubic so bounds and paint share one curve.
    const control = (a: Point): Point => [a[0] + (p[0] - a[0]) * 2 / 3, a[1] + (p[1] - a[1]) * 2 / 3];
    return [[start, control(start), control(end), end], [end, end, next, next]];
  });
}

function outline(shape: Shape): Seg[] {
  if (shape === "triangle") return roundedPolygon([[0, -1], [1, 0.85], [-1, 0.85]], 0.58);
  if (shape === "diamond") return roundedPolygon([[0, -0.8], [1, 0], [0, 0.8], [-1, 0]], 0.48);
  if (shape === "rounded-square") return roundedPolygon([[-1, -1], [1, -1], [1, 1], [-1, 1]], 0.8);
  if (shape === "capsule") {
    const r = 0.7;
    const k = r * 0.55228475;
    const x = 1 - r;
    return [
      [[-x, -r], [-x, -r], [x, -r], [x, -r]],
      [[x, -r], [x + k, -r], [1, -k], [1, 0]],
      [[1, 0], [1, k], [x + k, r], [x, r]],
      [[x, r], [x, r], [-x, r], [-x, r]],
      [[-x, r], [-x - k, r], [-1, k], [-1, 0]],
      [[-1, 0], [-1, -k], [-x - k, -r], [-x, -r]],
    ];
  }
  return superellipseSegs({ cx: 0, cy: 0, rx: 1, ry: 1, n: 2 });
}

export function silhouette(shape: Shape) {
  const segments = outline(shape);
  const bounds = segsBounds(segments);
  // Optical sizing: tapered bodies need more width than a filled-out square.
  // Keep room for motion in the shared viewBox without forcing equal bounds.
  const sizes: Record<Shape, [number, number]> = {
    round: [78, 78],
    "rounded-square": [74, 74],
    capsule: [88, 64],
    triangle: [88, 78],
    diamond: [92, 72],
  };
  const [width, height] = sizes[shape];
  const sx = width / (bounds.maxX - bounds.minX);
  const sy = height / (bounds.maxY - bounds.minY);
  const mx = (bounds.minX + bounds.maxX) / 2;
  const my = (bounds.minY + bounds.maxY) / 2;
  const path = segsPath(segments.map(segment => segment.map(([x, y]): Point => [50 + (x - mx) * sx, 50 + (y - my) * sy])));
  return { path, cx: 50, cy: 50, rx: width / 2, ry: height / 2 };
}
