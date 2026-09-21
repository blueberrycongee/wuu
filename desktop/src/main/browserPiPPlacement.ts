// Corner placement for the browser preview card.
//
// The card lives in the conversation column (the message scroller), not the
// whole window. Four corners sit `PIP_ANCHOR_MARGIN` inside that rectangle.
// Obstacles (composer, environment panel, status pills) are inflated by
// `PIP_OBSTACLE_PAD` and push a corner aside, preferring the direction that
// keeps the card near its original corner. A drag follows the pointer inside
// the screen work area; releasing it eases the card to the nearest corner,
// with a short look-ahead so a flick wins over a slightly closer corner.

export const PIP_ANCHOR_MARGIN = 24;
export const PIP_OBSTACLE_PAD = 12;
export const PIP_CARD_SIZE = { width: 250, height: 250 };
export const PIP_MAX_EDGE = 320;
export const PIP_MIN_SIZE = { width: 160, height: 120 };
export const PIP_SNAP_LOOKAHEAD_S = 0.12;
export const PIP_SNAP_MS = 280;
export const PIP_RESIZE_EDGES = ["n", "s", "e", "w", "ne", "nw", "se", "sw"] as const;
export type PipResizeEdge = (typeof PIP_RESIZE_EDGES)[number];

const OBSTACLE_ITERATIONS = 6;
const OVERLAP_BIAS = 1e9;
const OVERLAP_WEIGHT = 1e4;
const PRIORITY_WEIGHT = 1e6;

export const PIP_ALIGNMENTS = ["top-left", "top-right", "bottom-left", "bottom-right"] as const;
export type PipAlignment = (typeof PIP_ALIGNMENTS)[number];

export interface PipRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PipPoint {
  x: number;
  y: number;
}

export interface PipSize {
  width: number;
  height: number;
}

export interface PipAnchor {
  alignment: PipAlignment;
  point: PipPoint;
}

export interface BrowserPiPScreenLayout {
  host: PipRect;
  obstacles: PipRect[];
  visibleFrame: PipRect;
}

interface LocalRect {
  left: number;
  top: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

// A preview whose longer side is `maxEdge`, keeping the page's aspect ratio.
export function browserPiPSizeForAspect(aspect: number, maxEdge = PIP_MAX_EDGE): PipSize {
  const ratio = clamp(Number.isFinite(aspect) && aspect > 0 ? aspect : 4 / 3, 0.45, 2.4);
  if (ratio >= 1) {
    return { width: maxEdge, height: Math.max(1, Math.round(maxEdge / ratio)) };
  }
  return { width: Math.max(1, Math.round(maxEdge * ratio)), height: maxEdge };
}

// Shrink a chosen size into the column without changing its aspect ratio.
export function browserPiPFitCard(host: PipRect, preferred: PipSize): PipSize {
  const aspect = preferred.width / Math.max(1, preferred.height);
  const maxW = Math.max(1, host.width - PIP_ANCHOR_MARGIN * 2);
  const maxH = Math.max(1, host.height - PIP_ANCHOR_MARGIN * 2);
  let width = Math.min(preferred.width, maxW);
  let height = width / aspect;
  if (height > maxH) {
    height = maxH;
    width = height * aspect;
  }
  return {
    width: Math.max(1, Math.round(width)),
    height: Math.max(1, Math.round(height)),
  };
}

export function browserPiPCardSize(host: PipRect, preferred: PipSize = PIP_CARD_SIZE): PipSize {
  const availableW = host.width - PIP_ANCHOR_MARGIN * 2;
  const availableH = host.height - PIP_ANCHOR_MARGIN * 2;
  if (!(availableW > 0) || !(availableH > 0) || preferred.width <= 0 || preferred.height <= 0) {
    return {
      width: Math.max(1, Math.round(Math.min(preferred.width, Math.max(1, host.width)))),
      height: Math.max(1, Math.round(Math.min(preferred.height, Math.max(1, host.height)))),
    };
  }
  const scale = Math.min(1, availableW / preferred.width, availableH / preferred.height);
  return {
    width: Math.max(1, Math.round(preferred.width * scale)),
    height: Math.max(1, Math.round(preferred.height * scale)),
  };
}

export function browserPiPAnchors(host: PipRect, obstacles: PipRect[], card: PipSize): PipAnchor[] {
  const localObstacles = obstacles.map((obstacle) => obstacleInHost(host, obstacle));
  return PIP_ALIGNMENTS.map((alignment) => resolveAnchor(alignment, host, card, localObstacles));
}

export function browserPiPOrigin(anchor: PipAnchor, card: PipSize): PipPoint {
  switch (anchor.alignment) {
    case "top-left":
      return anchor.point;
    case "top-right":
      return { x: anchor.point.x - card.width, y: anchor.point.y };
    case "bottom-left":
      return { x: anchor.point.x, y: anchor.point.y - card.height };
    case "bottom-right":
      return { x: anchor.point.x - card.width, y: anchor.point.y - card.height };
  }
}

export function browserPiPNearestAnchor(
  anchors: readonly PipAnchor[],
  origin: PipPoint,
  card: PipSize,
  velocity: PipPoint,
): PipAnchor | undefined {
  if (anchors.length === 0) return undefined;
  const sample = {
    x: origin.x + velocity.x * PIP_SNAP_LOOKAHEAD_S,
    y: origin.y + velocity.y * PIP_SNAP_LOOKAHEAD_S,
  };
  let best = anchors[0]!;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const anchor of anchors) {
    const target = browserPiPOrigin(anchor, card);
    const distance = (target.x - sample.x) ** 2 + (target.y - sample.y) ** 2;
    if (distance < bestDistance) {
      best = anchor;
      bestDistance = distance;
    }
  }
  return best;
}

// Scale the card from the corner opposite the dragged edge, keeping `aspect`
// (width / height). A north-west drag of a bottom-right card leaves that
// corner where it is and changes both sides together.
export function browserPiPResizeRect(
  start: PipRect,
  edge: PipResizeEdge,
  pointerDelta: PipPoint,
  limits: { aspect: number; frame: PipRect },
): PipRect {
  const aspect = Number.isFinite(limits.aspect) && limits.aspect > 0
    ? limits.aspect
    : start.width / Math.max(1, start.height);
  const pin = pinnedCorner(edge);
  const pinPoint = rectCorner(pin, start);
  const growW = (edge.includes("e") ? pointerDelta.x : 0) + (edge.includes("w") ? -pointerDelta.x : 0);
  const growH = (edge.includes("s") ? pointerDelta.y : 0) + (edge.includes("n") ? -pointerDelta.y : 0);
  const fromW = start.width + growW;
  const fromH = (start.height + growH) * aspect;
  const widthTarget = (edge.includes("e") || edge.includes("w")) && (edge.includes("n") || edge.includes("s"))
    ? (fromW + fromH) / 2
    : (edge.includes("e") || edge.includes("w")) ? fromW : fromH;
  let width = clamp(widthTarget, 80, Math.max(80, limits.frame.width));
  let height = width / aspect;
  if (height > limits.frame.height) {
    height = limits.frame.height;
    width = height * aspect;
  }
  width = Math.max(1, Math.round(width));
  height = Math.max(1, Math.round(height));
  return browserPiPClampRect(placeFromCorner(pin, pinPoint, width, height), limits.frame);
}

function pinnedCorner(edge: PipResizeEdge): PipAlignment {
  const north = edge.includes("n");
  const south = edge.includes("s");
  const east = edge.includes("e");
  const west = edge.includes("w");
  if (north && west) return "bottom-right";
  if (north && east) return "bottom-left";
  if (south && west) return "top-right";
  if (south && east) return "top-left";
  if (east) return "top-left";
  if (west) return "top-right";
  if (south) return "top-left";
  return "bottom-left";
}

function rectCorner(alignment: PipAlignment, rect: PipRect): PipPoint {
  switch (alignment) {
    case "top-left":
      return { x: rect.x, y: rect.y };
    case "top-right":
      return { x: rect.x + rect.width, y: rect.y };
    case "bottom-left":
      return { x: rect.x, y: rect.y + rect.height };
    case "bottom-right":
      return { x: rect.x + rect.width, y: rect.y + rect.height };
  }
}

function placeFromCorner(alignment: PipAlignment, corner: PipPoint, width: number, height: number): PipRect {
  switch (alignment) {
    case "top-left":
      return { x: corner.x, y: corner.y, width, height };
    case "top-right":
      return { x: corner.x - width, y: corner.y, width, height };
    case "bottom-left":
      return { x: corner.x, y: corner.y - height, width, height };
    case "bottom-right":
      return { x: corner.x - width, y: corner.y - height, width, height };
  }
}

export function browserPiPClampRect(rect: PipRect, frame: PipRect): PipRect {
  const width = Math.min(rect.width, Math.max(1, frame.width));
  const height = Math.min(rect.height, Math.max(1, frame.height));
  return {
    width,
    height,
    x: clamp(rect.x, frame.x, frame.x + frame.width - width),
    y: clamp(rect.y, frame.y, frame.y + frame.height - height),
  };
}

export function browserPiPClampOrigin(origin: PipPoint, size: PipSize, frame: PipRect): PipPoint {
  return {
    x: clamp(origin.x, frame.x, Math.max(frame.x, frame.x + frame.width - size.width)),
    y: clamp(origin.y, frame.y, Math.max(frame.y, frame.y + frame.height - size.height)),
  };
}

// Quadratic from `start` toward `end`. Velocity bows the path, and t = 1
// still lands on `end`.
export function browserPiPSnapPoint(start: PipPoint, end: PipPoint, velocity: PipPoint, t: number): PipPoint {
  const clamped = clamp(t, 0, 1);
  const control = {
    x: start.x + clamp(velocity.x * 0.08, -120, 120),
    y: start.y + clamp(velocity.y * 0.08, -120, 120),
  };
  const remain = 1 - clamped;
  return {
    x: remain * remain * start.x + 2 * remain * clamped * control.x + clamped * clamped * end.x,
    y: remain * remain * start.y + 2 * remain * clamped * control.y + clamped * clamped * end.y,
  };
}

export function browserPiPSnapEase(t: number): number {
  const clamped = clamp(t, 0, 1);
  return 1 - (1 - clamped) ** 3;
}

export function browserPiPScreenRect(rect: PipRect, contentOrigin: PipPoint, zoom: number): PipRect {
  const factor = Number.isFinite(zoom) && zoom > 0 ? zoom : 1;
  return {
    x: contentOrigin.x + rect.x * factor,
    y: contentOrigin.y + rect.y * factor,
    width: rect.width * factor,
    height: rect.height * factor,
  };
}

export function browserPiPHostClient(payload: unknown): { host: PipRect; obstacles: PipRect[] } | null {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as { host?: unknown; obstacles?: unknown };
  const host = finiteRect(record.host);
  if (!host) return null;
  const obstacles = Array.isArray(record.obstacles)
    ? record.obstacles.flatMap((item) => {
      const rect = finiteRect(item);
      return rect ? [rect] : [];
    })
    : [];
  return { host, obstacles };
}

export function browserPiPResizeCommand(url: string): {
  phase: "start" | "move" | "end";
  edge: PipResizeEdge;
  x: number;
  y: number;
} | null {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return null;
  }
  if (parsed.protocol !== "wuu-pip:" || parsed.hostname !== "resize") return null;
  const phase = parsed.searchParams.get("phase");
  const edge = parsed.searchParams.get("edge");
  if (phase !== "start" && phase !== "move" && phase !== "end") return null;
  if (!edge || !(PIP_RESIZE_EDGES as readonly string[]).includes(edge)) return null;
  const x = Number(parsed.searchParams.get("x"));
  const y = Number(parsed.searchParams.get("y"));
  if (![x, y].every(Number.isFinite)) return null;
  return { phase, edge: edge as PipResizeEdge, x, y };
}

export function browserPiPDragCommand(url: string): {
  phase: "start" | "move" | "end";
  x: number;
  y: number;
  vx: number;
  vy: number;
} | null {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return null;
  }
  if (parsed.protocol !== "wuu-pip:" || parsed.hostname !== "drag") return null;
  const phase = parsed.searchParams.get("phase");
  if (phase !== "start" && phase !== "move" && phase !== "end") return null;
  const x = Number(parsed.searchParams.get("x"));
  const y = Number(parsed.searchParams.get("y"));
  const vx = Number(parsed.searchParams.get("vx") ?? 0);
  const vy = Number(parsed.searchParams.get("vy") ?? 0);
  if (![x, y, vx, vy].every(Number.isFinite)) return null;
  return { phase, x, y, vx, vy };
}

function resolveAnchor(
  alignment: PipAlignment,
  host: PipRect,
  card: PipSize,
  obstacles: LocalRect[],
): PipAnchor {
  const parked = clampLocal(cornerLocal(alignment, host, card), host, card);
  let rect = parked;
  for (let i = 0; i < OBSTACLE_ITERATIONS; i += 1) {
    const hit = obstacles.find((obstacle) => overlapArea(rect, obstacle) > 0);
    if (!hit) break;
    rect = escapeObstacle(alignment, host, card, hit, obstacles, rect, parked);
  }
  return { alignment, point: cornerPoint(alignment, host, rect) };
}

function escapeObstacle(
  alignment: PipAlignment,
  host: PipRect,
  card: PipSize,
  obstacle: LocalRect,
  obstacles: LocalRect[],
  rect: LocalRect,
  source: LocalRect,
): LocalRect {
  const candidates = shifts(alignment, rect, obstacle).map((candidate) => ({
    priority: candidate.priority,
    rect: clampLocal(candidate.rect, host, card),
  }));
  let best = candidates[0];
  let bestScore = best == null ? Number.POSITIVE_INFINITY : placementScore(best, obstacles, source);
  for (const candidate of candidates.slice(1)) {
    const score = placementScore(candidate, obstacles, source);
    if (score < bestScore) {
      best = candidate;
      bestScore = score;
    }
  }
  return best?.rect ?? rect;
}

function shifts(
  alignment: PipAlignment,
  rect: LocalRect,
  obstacle: LocalRect,
): Array<{ priority: number; rect: LocalRect }> {
  const below = localBox(rect.left, obstacle.bottom, rect);
  const above = localBox(rect.left, obstacle.top - rect.height, rect);
  const right = localBox(obstacle.right, rect.top, rect);
  const left = localBox(obstacle.left - rect.width, rect.top, rect);
  switch (alignment) {
    case "top-left":
      return [
        { priority: 0, rect: below },
        { priority: 1, rect: right },
        { priority: 2, rect: left },
        { priority: 3, rect: above },
      ];
    case "top-right":
      return [
        { priority: 0, rect: below },
        { priority: 1, rect: left },
        { priority: 2, rect: right },
        { priority: 3, rect: above },
      ];
    case "bottom-left":
      return [
        { priority: 0, rect: above },
        { priority: 1, rect: right },
        { priority: 2, rect: left },
        { priority: 3, rect: below },
      ];
    case "bottom-right":
      return [
        { priority: 0, rect: above },
        { priority: 1, rect: left },
        { priority: 2, rect: right },
        { priority: 3, rect: below },
      ];
  }
}

function placementScore(
  candidate: { priority: number; rect: LocalRect },
  obstacles: LocalRect[],
  source: LocalRect,
): number {
  const overlap = obstacles.reduce((sum, obstacle) => sum + overlapArea(candidate.rect, obstacle), 0);
  return (overlap > 0 ? OVERLAP_BIAS : 0)
    + overlap * OVERLAP_WEIGHT
    + candidate.priority * PRIORITY_WEIGHT
    + (candidate.rect.left - source.left) ** 2
    + (candidate.rect.top - source.top) ** 2;
}

function cornerLocal(alignment: PipAlignment, host: PipRect, card: PipSize): LocalRect {
  switch (alignment) {
    case "top-left":
      return localBox(PIP_ANCHOR_MARGIN, PIP_ANCHOR_MARGIN, card);
    case "top-right":
      return localBox(host.width - card.width - PIP_ANCHOR_MARGIN, PIP_ANCHOR_MARGIN, card);
    case "bottom-left":
      return localBox(PIP_ANCHOR_MARGIN, host.height - card.height - PIP_ANCHOR_MARGIN, card);
    case "bottom-right":
      return localBox(host.width - card.width - PIP_ANCHOR_MARGIN, host.height - card.height - PIP_ANCHOR_MARGIN, card);
  }
}

function cornerPoint(alignment: PipAlignment, host: PipRect, rect: LocalRect): PipPoint {
  switch (alignment) {
    case "top-left":
      return { x: host.x + rect.left, y: host.y + rect.top };
    case "top-right":
      return { x: host.x + rect.right, y: host.y + rect.top };
    case "bottom-left":
      return { x: host.x + rect.left, y: host.y + rect.bottom };
    case "bottom-right":
      return { x: host.x + rect.right, y: host.y + rect.bottom };
  }
}

function clampLocal(rect: LocalRect, host: PipRect, card: PipSize): LocalRect {
  return localBox(
    clamp(rect.left, PIP_ANCHOR_MARGIN, Math.max(PIP_ANCHOR_MARGIN, host.width - card.width - PIP_ANCHOR_MARGIN)),
    clamp(rect.top, PIP_ANCHOR_MARGIN, Math.max(PIP_ANCHOR_MARGIN, host.height - card.height - PIP_ANCHOR_MARGIN)),
    card,
  );
}

function obstacleInHost(host: PipRect, obstacle: PipRect): LocalRect {
  return edges({
    left: obstacle.x - host.x - PIP_OBSTACLE_PAD,
    top: obstacle.y - host.y - PIP_OBSTACLE_PAD,
    right: obstacle.x - host.x + obstacle.width + PIP_OBSTACLE_PAD,
    bottom: obstacle.y - host.y + obstacle.height + PIP_OBSTACLE_PAD,
  });
}

function overlapArea(a: LocalRect, b: LocalRect): number {
  const width = Math.min(a.right, b.right) - Math.max(a.left, b.left);
  const height = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
  return width <= 0 || height <= 0 ? 0 : width * height;
}

function localBox(left: number, top: number, size: PipSize): LocalRect {
  return {
    left,
    top,
    right: left + size.width,
    bottom: top + size.height,
    width: size.width,
    height: size.height,
  };
}

function edges(rect: { left: number; top: number; right: number; bottom: number }): LocalRect {
  return {
    ...rect,
    width: rect.right - rect.left,
    height: rect.bottom - rect.top,
  };
}

function finiteRect(value: unknown): PipRect | null {
  if (!value || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  const x = Number(record.x);
  const y = Number(record.y);
  const width = Number(record.width);
  const height = Number(record.height);
  if (![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) return null;
  return { x, y, width, height };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}
