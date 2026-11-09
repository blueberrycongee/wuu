import type { Palette } from "../color";
import type { FacePerspective } from "../render";
import { superellipse } from "../shape";
import { SHAPES, silhouette } from "./silhouette";
import { surfaceEye } from "../surface";
import type { Traits } from "../traits";

/** Five canonical bodies; seeded variation belongs to the face, not the contour. */
export type { Shape } from "./silhouette";

export function layout(t: Traits, perspective?: FacePerspective) {
  const shape = SHAPES[Math.min(SHAPES.length - 1, Math.floor(t("shape") * SHAPES.length))]!.id;
  const body = silhouette(shape);
  const { rx, ry } = body;
  // Projection must stay on an inscribed face surface: a bounding-box ellipse
  // would carry turning eyes through the sloping sides of these bodies.
  const face = shape === "triangle"
    ? { cx: body.cx, cy: body.cy + ry * 0.27, rx: rx * 0.46, ry: ry * 0.49 }
    : shape === "diamond"
      ? { cx: body.cx, cy: body.cy, rx: rx * 0.66, ry: ry * 0.66 }
      : body;

  // Where the eye pair sits as a unit. Gaze is deliberately a small effect: at
  // blobatar sizes it reads as jitter rather than as direction, and the budget it
  // used to spend is worth more in the gap below.
  const gx = t.jitter("gaze.x", 0.09) * rx;
  const gy = (shape === "triangle" ? 0.23 : 0) * ry + t.num("gaze.y", -0.08, 0.08) * ry;

  // Compact product surfaces are the primary use case for this fork. The eyes
  // stay small capsule pills — a crisp narrow pair reads at 24–32 px better
  // than a tall soft one, because contrast and parallel edges do the work that
  // area used to. Seeded differences in shape, spacing, and expression remain.
  const er0 = t.num("eye.rx", 0.075, 0.11) * rx;
  const eyeRatio = t.num("eye.ratio", 1.9, 2.8);
  // The second eye differs from the first in both overall size and in how tall
  // it is for that size, drawn separately so a pair can read as big-and-round
  // next to small-and-narrow rather than as one capsule scaled twice.
  const scale = t.num("eye.scale", 0.78, 1.24);
  const stretch = t.num("eye.stretch", 0.85, 1.18);

  // The gap is measured from the eye's own edge outward, not from the body
  // center. Drawn independently, a large eye and a small gap co-occur and
  // produce two capsules crammed together with no room left to tilt — and
  // because the lean bound below is derived from that clearance, those same
  // seeds also came out untilted. Deriving the gap fixes both at once.
  const clearance = t.num("eye.gap", 0.1, 0.24) * rx;
  // Every bound below is taken over the larger of the two eyes, since either one
  // can be the larger now.
  const wide = er0 * Math.max(1, scale);
  const tall = er0 * eyeRatio * Math.max(1, scale * stretch);
  // The larger pair needs a little more centre separation so wide expressions
  // remain two readable eyes instead of collapsing into one dark mark.
  const gap0 = wide + rx * 0.04 + clearance;

  // Containment by construction rather than by hope. Each range is safe on its
  // own, but their simultaneous extremes are not, and a 2000-seed test only
  // samples that corner — it does not rule it out. Measuring the cluster against
  // the tightest radius the body actually reaches and scaling it as a unit makes
  // the guarantee hold across the whole space.
  const tight = shape === "triangle" ? 0.58 : shape === "diamond" ? 0.65 : 1;
  const need = (Math.abs(gx) + gap0 + Math.hypot(wide, tall)) / rx;
  const fit = need > tight * 0.9 ? (tight * 0.9) / need : 1;

  const er = er0 * fit;
  const gap = gap0 * fit;
  const eyeRy = er * eyeRatio;

  // Lean is bounded by that clearance rather than drawn freely. A tall capsule
  // tilted hard sweeps sideways by ry·sin(lean), and two of them meeting in the
  // middle of the face is the one failure this style cannot survive. The 12°
  // ceiling is a taste bound on top of that geometric one: past roughly that
  // much, the pair stops reading as a tilt and starts reading as a mistake.
  const MAX_LEAN = 12;
  const room = Math.max(0, Math.min(1, (clearance * fit) / (tall * fit)));
  const bound = Math.min(MAX_LEAN, (Math.asin(room) * 180) / Math.PI);
  const lean = t.num("eye.lean", -1, 1) * bound;
  // The second eye's own tilt is clamped to the same ceiling so the difference
  // between the two never pushes either past it.
  const lean2 = Math.max(
    -MAX_LEAN,
    Math.min(MAX_LEAN, lean + t.jitter("eye.lean2", 3.5)),
  );

  const eyes = [
    {
      cx: body.cx + gx - gap,
      cy: body.cy + gy,
      rx: er,
      ry: eyeRy,
      n: t.num("eye.n", 3.5, 6),
      rot: lean,
    },
    {
      cx: body.cx + gx + gap,
      cy: body.cy + gy + t.jitter("eye.dy", 0.04) * ry,
      rx: er * scale,
      ry: eyeRy * scale * stretch,
      n: t.num("eye.n", 3.5, 6),
      rot: lean2,
    },
  ];

  return {
    shape,
    body,
    face,
    eyes,
    perspective,
  };
}

export type Layout = ReturnType<typeof layout>;

/**
 * `mo` keeps the authored chart in stable nodes. Surface animation writes
 * projected contours into those nodes; static rendering projects here.
 *
 * The nesting is not decoration. An element has one `transform` property, so
 * hover-lift, breathe and bob have to live on separate elements or they
 * overwrite each other. Eyes get their own class because blink scales each one
 * about its own center; applied to the shared group, they slide toward the
 * group center instead of closing.
 *
 * The hover-lift element — `.mo-root` — is deliberately *not* emitted here. It
 * is the one element whose class varies with the expression, and the caller
 * renders it so that variation never touches this string. See `makeParts`.
 */
export function render(l: Layout, p: Palette, mo?: boolean): string {
  const b = l.body;
  const core = b.path;

  const r2 = (v: number) => Math.round(v * 100) / 100;

  // `--mo-wrap` is which side of the face this eye is on: -1 left, +1 right. The
  // wrap layer needs to treat the two eyes differently — the one leading into a
  // turn foreshortens harder, and on a diagonal glance they tilt toward each
  // other rather than together — and a sign per eye lets one `@keyframes` serve
  // both. A class per side would work too and cost a selector; this costs 16
  // bytes and no ids, which the no-collision guarantee depends on.
  //
  // `--mo-lean` is this eye's own tilt, and it is not decoration either.
  // `superellipse` bakes rotation into the coordinates, so a leaned capsule
  // arrives in the DOM already tilted and its element-local axes are the
  // viewport's. A `scaleY` on it — blink's, or an expression's — would then
  // squash along screen-Y and shear the capsule instead of closing it. The
  // stylesheet counter-rotates around every such scale, and this is the angle it
  // needs. ~16 B per animated blobatar; see `@keyframes mo-blink`.
  //
  // `transform-origin` is this eye's own centre, stated in user units, and it is
  // the one per-eye value that exists to work around an engine rather than to
  // describe the blobatar. The wrapper used to take `transform-box: fill-box` and
  // `transform-origin: center` like the shape below it — but a `<g>`'s fill box
  // is its children's *rendered* geometry, and Gecko recomputes it as they move.
  // A blink shrinks the shape to ~12% of its height, the wrapper's origin
  // follows it, and the pose's anisotropic scale — 1.72 × 0.30 on `happy` —
  // turns that small shift into ~30 viewBox units of travel and back. Invisible
  // at idle, because an idle wrapper's transform is the identity and an identity
  // does not care where its origin is; loud under every expression, which is
  // what made it read as a morph bug. Measured, `mad` on Firefox: the left eye
  // left the frame entirely for the length of a blink. ~26 B per eye.
  //
  // **The animated eye is a `<g>` around the shape, not the shape itself**, and
  // the extra node is what makes the morph run in Firefox. The pose and the idle
  // loops used to share one element, which left the pose's scale and tilt with
  // nowhere to live but inside `@keyframes` — and Gecko resolves a keyframe's
  // `var()` against the transition's *endpoint*, so those two channels snapped
  // while every other one eased. The wrapper carries the pose as plain
  // declarations and the shape underneath keeps the loops. ~8 B per eye; see
  // `.mo-eye` in `motion.css` for the measurement.
  const eye = (e: Layout["eyes"][number], i: number) => {
    // Animated surface paths start in their authored chart. The React adapter
    // updates their geometry in place after expression and camera interpolation.
    const path = `<path d="${l.perspective ? surfaceEye(e, l.face, mo ? undefined : l.perspective).path : superellipse(e)}"/>`;
    if (!mo) return path;
    return `<g class="mo-eye" style="--mo-wrap:${i ? 1 : -1};--mo-lean:${r2(e.rot)};transform-origin:${r2(e.cx)}px ${r2(e.cy)}px">${path}</g>`;
  };

  const body =
    `<g fill="${p.head}">` +
    `<path d="${core}"/>` +
    `</g>` +
    // The eye group already existed to share a fill, and it is exactly the
    // element the saccade layer needs: both eyes must move as one, because
    // independent movement reads as a lazy eye instantly. Blink stays on the
    // individual paths underneath it.
    `<g fill="${p.eye}"${mo ? ` class="mo-eyes"` : ""}>` +
    l.eyes.map(eye).join("") +
    `</g>`;

  return mo
    ? `<g class="mo-breathe"><g class="mo-bob">${body}</g></g>`
    : body;
}

/**
 * No backdrop by default. The body *is* the blobatar here, and a plate behind a
 * near-full-bleed shape just adds a rim of color that fights the silhouette.
 */
export const background = false as const;
