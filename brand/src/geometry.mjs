// Construction of the Wuu ball, wordmark and lockups. Every brand asset and every
// figure in the manual is drawn from these functions, so the numbers in
// tokens/tokens.json are the only place the geometry is defined.
import CanvasKitInit from "canvaskit-wasm";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { isDeepStrictEqual } from "node:util";

export const tokens = JSON.parse(readFileSync(new URL("../tokens/tokens.json", import.meta.url), "utf8"));
const ck = await CanvasKitInit();
export const WuuBall = createRequire(import.meta.url)("../assets/motion/wuu-ball.js");
// The browser runtime carries its own copy of the optical sets so it works from file://.
if (!isDeepStrictEqual(WuuBall.OPTICAL, tokens.ball.optical) || !isDeepStrictEqual(WuuBall.BREAKPOINTS, tokens.ball.opticalBreakpoints)) {
  throw new Error("assets/motion/wuu-ball.js optical sets differ from tokens.json → ball");
}

const P = (d) => ck.Path.MakeFromSVGString(d);
const op = (a, b, kind) => ck.Path.MakeFromOp(a, b, ck.PathOp[kind]);
const union = (...ps) => ps.reduce((a, b) => op(a, b, "Union"));
const subtract = (a, b) => op(a, b, "Difference");
function transform(p, m) {
  const b = new ck.PathBuilder();
  b.addPath(p);
  b.transform(m);
  return b.snapshot();
}
const move = (p, dx, dy) => transform(p, [1, 0, dx, 0, 1, dy, 0, 0, 1]);
function bounds(p) {
  const [x0, y0, x1, y1] = p.computeTightBounds();
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}
// Two decimals keep the SVG compact; at 1000-unit construction scale that is
// far below any rendered pixel.
const toD = (p) => p.toSVGString().replace(/-?\d+\.\d+/g, (m) => String(+(+m).toFixed(2)));

// ---------------------------------------------------------------------------
// Ball

/** Resolve an optical size ("display" | "small" | "micro") for a rendered diameter in px. */
export const opticalSize = (px) => WuuBall.opticalSize(px);

/** Eye capsules for a pose, in the ball's 100-unit frame (see assets/motion/wuu-ball.js). */
export const eyeRects = (size = "display", pose = {}) => WuuBall.eyeRects(size, pose);

// Capsule as an exact path: straight sides and semicircular ends, rotated about its centre.
function capsuleD({ cx, cy, width, height, rotate }) {
  const r = width / 2, half = Math.max(0, height / 2 - r);
  const a = (rotate * Math.PI) / 180, c = Math.cos(a), s = Math.sin(a);
  const pt = (x, y) => `${fmt(cx + x * c - y * s)} ${fmt(cy + x * s + y * c)}`;
  return `M${pt(-r, -half)}A${fmt(r)} ${fmt(r)} 0 0 1 ${pt(r, -half)}L${pt(r, half)}A${fmt(r)} ${fmt(r)} 0 0 1 ${pt(-r, half)}Z`;
}

const DISC = "M50 0A50 50 0 1 1 50 100A50 50 0 1 1 50 0Z";

/** Path data for the ball in its 100-unit frame. knockout is the disc with the eyes as holes (evenodd). */
export function ballPaths(size = "display", pose = {}) {
  const eyes = eyeRects(size, pose).map(capsuleD).join("");
  return { body: DISC, eyes, knockout: DISC + eyes };
}

/**
 * SVG for the ball. mode "two-tone" draws body and eyes in their own colours;
 * "knockout" cuts the eyes out of the body so one colour works on any ground.
 */
export function ballSVG({ px = 100, size, pose, body = tokens.color.brand.ink, eye = tokens.color.brand.paper, mode = "two-tone", title = "Wuu", attrs = "" } = {}) {
  const s = size ?? opticalSize(px);
  const p = ballPaths(s, pose);
  const label = title ? ` role="img" aria-label="${title}"` : ` aria-hidden="true"`;
  const art = mode === "knockout"
    ? `<path d="${p.knockout}" fill="${body}" fill-rule="evenodd"/>`
    : `<path d="${p.body}" fill="${body}"/><path d="${p.eyes}" fill="${eye}"/>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="${px}" height="${px}"${label}${attrs}>${art}</svg>`;
}

// ---------------------------------------------------------------------------
// Wordmark

// Rounded-bottom box; k sets how square the bowl is (0.55 ≈ circle).
function bowlBox(x0, x1, y0, y1, k, depth) {
  const rx = (x1 - x0) / 2, ry = rx * depth, cx = x0 + rx, yc = y1 - ry;
  return P(
    `M${x0} ${y0}L${x0} ${yc}C${x0} ${yc + ry * k} ${cx - rx * k} ${y1} ${cx} ${y1}` +
      `C${cx + rx * k} ${y1} ${x1} ${yc + ry * k} ${x1} ${yc}L${x1} ${y0}Z`,
  );
}

/**
 * The wordmark "wuu", drawn from one bowl. The w is two bowls sharing a shorter,
 * lighter middle stem; each u is the same bowl with a flat spur to the baseline.
 * Units: x-height = 1000 construction units, y down, baseline at 0.
 */
export function wordmarkPath() {
  const w = tokens.wordmark;
  const XH = 1000, top = -XH, over = w.overshoot, k = w.bowlSquareness, depth = w.bowlDepth;
  const { stem, bowl, wMiddleStem: mid, wCounter, uWidth, wuSpace, uuSpace } = w;
  const a1 = stem, b0 = a1 + wCounter, b1 = b0 + mid, c0 = b1 + wCounter, c1 = c0 + stem;
  let letterW = union(bowlBox(0, b1, top, over, k, depth), bowlBox(b0, c1, top, over, k, depth));
  letterW = subtract(letterW, bowlBox(a1, b0, top - 10, over - bowl, k, depth));
  letterW = subtract(letterW, bowlBox(b1, c0, top - 10, over - bowl, k, depth));
  letterW = subtract(letterW, P(`M${b0 - 1} ${top - 20}H${b1 + 1}V${top + XH * w.wMiddleCut}H${b0 - 1}Z`));
  const letterU = (x) => {
    const shape = union(bowlBox(x, x + uWidth, top, over, k, depth), P(`M${x + uWidth - stem} ${top}H${x + uWidth}V0H${x + uWidth - stem}Z`));
    return subtract(shape, bowlBox(x + stem, x + uWidth - stem, top - 10, over - bowl, k, depth));
  };
  const u1x = c1 + wuSpace, u2x = u1x + uWidth + uuSpace;
  const path = union(letterW, letterU(u1x), letterU(u2x));
  const b = bounds(path);
  return { path, width: b.w, top: b.y, bottom: b.y + b.h, xHeight: XH };
}

/** Wordmark SVG scaled so its x-height is xh px. */
export function wordmarkSVG({ xh = 40, color = tokens.color.brand.ink, title = "Wuu" } = {}) {
  const wm = wordmarkPath();
  const d = toD(move(wm.path, 0, -wm.top));
  const h = wm.bottom - wm.top, s = xh / wm.xHeight;
  const label = title ? ` role="img" aria-label="${title}"` : ` aria-hidden="true"`;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${fmt(wm.width)} ${fmt(h)}" width="${fmt(wm.width * s)}" height="${fmt(h * s)}"${label}><path d="${d}" fill="${color}"/></svg>`;
}

const fmt = (n) => String(+n.toFixed(2));

// ---------------------------------------------------------------------------
// Lockups. All proportions are multiples of the wordmark x-height (X).

/**
 * Horizontal lockup: ball, gap, wordmark. The ball is centred on the x-height band,
 * so it overhangs the wordmark equally above and below.
 */
export function lockupGeometry(kind = "horizontal") {
  const L = tokens.lockup[kind];
  const wm = wordmarkPath();
  const X = wm.xHeight, D = L.ball * X, wmW = wm.width, wmH = wm.bottom - wm.top;
  if (kind === "horizontal") {
    const width = D + L.gap * X + wmW;
    const ballY = -X / 2 - D / 2; // centred on x-height band (top -X, baseline 0)
    const top = Math.min(ballY, wm.top), bottom = Math.max(ballY + D, wm.bottom);
    return { kind, X, D, width, top, bottom, ball: { x: 0, y: ballY }, word: { x: D + L.gap * X, y: 0 }, wm, wmW, wmH };
  }
  const width = Math.max(D, wmW);
  const ballY = -X - L.gap * X - D;
  return { kind, X, D, width, top: ballY, bottom: wm.bottom, ball: { x: (width - D) / 2, y: ballY }, word: { x: (width - wmW) / 2, y: 0 }, wm, wmW, wmH };
}

export function lockupSVG({ kind = "horizontal", xh = 40, ink = tokens.color.brand.ink, eye = tokens.color.brand.paper, mode = "two-tone", clear = 0, title = "Wuu" } = {}) {
  const g = lockupGeometry(kind);
  const s = xh / g.X;
  const size = opticalSize(g.D * s);
  const bp = ballPaths(size, tokens.ball.brandPose);
  const ballScale = g.D / 100;
  const pad = clear * g.X;
  const vbX = -pad, vbY = g.top - pad, vbW = g.width + 2 * pad, vbH = g.bottom - g.top + 2 * pad;
  const ballArt = mode === "knockout"
    ? `<path d="${bp.knockout}" fill="${ink}" fill-rule="evenodd"/>`
    : `<path d="${bp.body}" fill="${ink}"/><path d="${bp.eyes}" fill="${eye}"/>`;
  const label = title ? ` role="img" aria-label="${title}"` : ` aria-hidden="true"`;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${fmt(vbX)} ${fmt(vbY)} ${fmt(vbW)} ${fmt(vbH)}" width="${fmt(vbW * s)}" height="${fmt(vbH * s)}"${label}>` +
    `<g transform="translate(${fmt(g.ball.x)} ${fmt(g.ball.y)}) scale(${fmt(ballScale)})">${ballArt}</g>` +
    `<path transform="translate(${fmt(g.word.x)} 0)" d="${toD(g.wm.path)}" fill="${ink}"/></svg>`;
}

// ---------------------------------------------------------------------------
// App icon: Apple's 1024 grid, 824 body with continuous-corner radius.

export function squirclePath(x, y, size, radius) {
  // Continuous corner approximated with the smoothing used by macOS icon templates.
  const r = radius, k = 0.62;
  const x1 = x + size, y1 = y + size, e = r * 1.28;
  return `M${x + e} ${y}H${x1 - e}C${x1 - e + e * k} ${y} ${x1} ${y + e - e * k} ${x1} ${y + e}` +
    `V${y1 - e}C${x1} ${y1 - e + e * k} ${x1 - e + e * k} ${y1} ${x1 - e} ${y1}` +
    `H${x + e}C${x + e - e * k} ${y1} ${x} ${y1 - e + e * k} ${x} ${y1 - e}` +
    `V${y + e}C${x} ${y + e - e * k} ${x + e - e * k} ${y} ${x + e} ${y}Z`;
}

export function appIconSVG({ variant = "light", px = 1024, grid = false, geometry = {} } = {}) {
  const I = { ...tokens.appIcon, ...geometry };
  const v = I.variants[variant];
  const body = squirclePath(I.inset, I.inset, I.body, I.radius);
  const D = I.ballDiameter, bx = I.ballCenter[0] - D / 2, by = I.ballCenter[1] - D / 2;
  // Small renditions use the optical eye set for the ball's rendered diameter.
  const bp = ballPaths(opticalSize((D / 1024) * px), tokens.ball.brandPose);
  const guides = grid ? iconGrid(I) : "";
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="${px}" height="${px}" role="img" aria-label="Wuu">` +
    `<defs><clipPath id="wuu-icon-body"><path d="${body}"/></clipPath></defs>` +
    `<path d="${body}" fill="${v.ground}"/>` +
    (v.edge ? `<path d="${body}" fill="none" stroke="${v.edge}" stroke-width="2"/>` : "") +
    `<g clip-path="url(#wuu-icon-body)"><g transform="translate(${bx} ${by}) scale(${D / 100})"><path d="${bp.body}" fill="${v.ball}"/><path d="${bp.eyes}" fill="${v.eye}"/></g></g>` +
    guides + `</svg>`;
}

function iconGrid(I) {
  const c = "rgba(236,72,56,.55)";
  const [cx, cy] = I.ballCenter, r = I.ballDiameter / 2;
  return `<g fill="none" stroke="${c}" stroke-width="2">` +
    `<rect x="${I.inset}" y="${I.inset}" width="${I.body}" height="${I.body}"/>` +
    `<line x1="512" y1="0" x2="512" y2="1024"/><line x1="0" y1="512" x2="1024" y2="512"/>` +
    `<circle cx="${cx}" cy="${cy}" r="${r}" stroke-dasharray="8 8"/>` +
    `<line x1="${cx}" y1="${cy - r - 30}" x2="${cx}" y2="${cy + r + 30}" stroke-dasharray="4 6"/>` +
    `<line x1="${cx - r - 30}" y1="${cy}" x2="${cx + r + 30}" y2="${cy}" stroke-dasharray="4 6"/></g>`;
}

export { toD, bounds, move, union, subtract, P };

// ---------------------------------------------------------------------------
// Raster output (PNG bytes) for icons, favicons and pixel-level previews.

function paintPath(canvas, d, color, m) {
  const paint = new ck.Paint();
  paint.setAntiAlias(true);
  paint.setColor(ck.parseColorString(color));
  let p = P(d);
  if (m) p = transform(p, m);
  canvas.drawPath(p, paint);
  paint.delete();
}

/** PNG of the ball at px (optical size chosen from px), on a transparent ground. */
export function ballPNG({ px, size, body = tokens.color.brand.ink, eye = tokens.color.brand.paper, pose = {} }) {
  const surface = ck.MakeSurface(px, px);
  const canvas = surface.getCanvas();
  canvas.clear(ck.TRANSPARENT);
  const p = ballPaths(size ?? opticalSize(px), pose), k = px / 100;
  paintPath(canvas, p.body, body, [k, 0, 0, 0, k, 0, 0, 0, 1]);
  paintPath(canvas, p.eyes, eye, [k, 0, 0, 0, k, 0, 0, 0, 1]);
  const bytes = surface.makeImageSnapshot().encodeToBytes();
  surface.delete();
  return Buffer.from(bytes);
}

/** PNG of the app icon at px, rendered from the same geometry as appIconSVG. */
export function appIconPNG({ variant = "light", px = 1024 } = {}) {
  const I = tokens.appIcon, v = I.variants[variant];
  const surface = ck.MakeSurface(px, px);
  const canvas = surface.getCanvas();
  canvas.clear(ck.TRANSPARENT);
  const k = px / 1024, m = [k, 0, 0, 0, k, 0, 0, 0, 1];
  const body = squirclePath(I.inset, I.inset, I.body, I.radius);
  paintPath(canvas, body, v.ground, m);
  if (v.edge) {
    const paint = new ck.Paint();
    paint.setAntiAlias(true);
    paint.setStyle(ck.PaintStyle.Stroke);
    paint.setStrokeWidth(2 * k);
    paint.setColor(ck.parseColorString(v.edge));
    canvas.drawPath(transform(P(body), m), paint);
    paint.delete();
  }
  canvas.save();
  canvas.clipPath(transform(P(body), m), ck.ClipOp.Intersect, true);
  const D = I.ballDiameter, s = (D / 100) * k, bx = (I.ballCenter[0] - D / 2) * k, by = (I.ballCenter[1] - D / 2) * k;
  const p = ballPaths(opticalSize((D / 1024) * px), tokens.ball.brandPose);
  paintPath(canvas, p.body, v.ball, [s, 0, bx, 0, s, by, 0, 0, 1]);
  paintPath(canvas, p.eyes, v.eye, [s, 0, bx, 0, s, by, 0, 0, 1]);
  canvas.restore();
  const bytes = surface.makeImageSnapshot().encodeToBytes();
  surface.delete();
  return Buffer.from(bytes);
}
