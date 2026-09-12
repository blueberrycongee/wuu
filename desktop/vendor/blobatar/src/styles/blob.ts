import type { Palette } from "../color";
import type { FacePerspective } from "../render";
import { blobPath, blobSegs, segsBounds, superellipse, superellipseSegs } from "../shape";
import { surfaceEye } from "../surface";
import type { Traits } from "../traits";

/**
 * A soft body and two capsule eyes.
 *
 * The silhouette carries the identity here, so it comes from a vocabulary of
 * six: a plain round, a tilted box, a lopsided organic pebble, a lumpy cloud, a
 * petalled sun, and a round body with a nub growing off it. Everything is drawn
 * in one fill color inside a single `<g>`, which means overlapping parts union
 * visually with no boolean geometry and no clip paths.
 *
 * Every eye dimension is expressed as a fraction of the body radius rather than
 * in absolute units, and the radius itself is solved backwards from the
 * finished silhouette (see SPAN) — every blobatar claims the same footprint,
 * so no two shapes share a body radius and absolute eye sizes would drift
 * between all of them.
 */

export type Shape = "round" | "boxy" | "organic" | "cloud" | "sun" | "nub";

/**
 * Weighted rather than uniform: rounds and pebbles are the everyday shapes, and
 * suns and clouds are the ones you want to be pleased to see. Thresholds are
 * frozen per major, exactly like a `pick` array.
 */
function shapeOf(v: number): Shape {
  return v < 0.28
    ? "round"
    : v < 0.58
      ? "organic"
      : v < 0.72
        ? "boxy"
        : v < 0.84
          ? "nub"
          : v < 0.93
            ? "cloud"
            : "sun";
}

/**
 * The silhouette's larger bounding-box side, in viewBox units, for every shape
 * and every seed. No backdrop plate frames the figure, so this *is* the size
 * one avatar reads next to another — before it was fixed, a seeded radius and
 * a per-shape core made suns render at ~70% of a round's footprint. 78 keeps
 * the near-full-bleed presence the largest shapes already had, while leaving
 * headroom for the centred hover and breathe transforms to stay in frame.
 */
const SPAN = 78;

export function layout(t: Traits, perspective?: FacePerspective) {
  const shape = shapeOf(t("shape"));
  const ratio = t.num("body.ratio", 0.92, 1.08);
  const n = shape === "round" && (perspective?.strength ?? 0) > 0
    ? 2
    : shape === "boxy" ? t.num("body.n", 3.4, 6) : t.num("body.n", 1.9, 2.5);
  const rot = shape === "boxy" ? t.num("body.rot", -20, 20) : 0;
  // Lopsided by ±16%, which is enough to read as hand-drawn and not so much
  // that the eyes can end up on a bulge instead of the face.
  const radii = Array.from(
    { length: t.int("body.pts", 6, 8) },
    (_, i) => 1 + t.jitter(`body.r${i}`, 0.16),
  );

  // Decoration geometry at unit body radius, relative to the body centre — the
  // same trait reads the petal placement below has always made, just unscaled
  // so the silhouette can be measured before the radius exists.
  const deco: { dx: number; dy: number; pr: number }[] = [];
  if (shape === "sun") {
    const count = t.int("sun.n", 6, 9);
    const dist = t.num("sun.dist", 1.0, 1.08);
    const pr = t.num("sun.r", 0.2, 0.26);
    const off = t.num("sun.rot", 0, 2 * Math.PI);
    for (let i = 0; i < count; i++) {
      const a = off + (2 * Math.PI * i) / count;
      deco.push({ dx: Math.cos(a) * dist, dy: Math.sin(a) * dist, pr });
    }
  } else if (shape === "cloud") {
    // Lobes ride the upper half only, so the silhouette stays a cloud rather
    // than a flower.
    const count = t.int("cloud.n", 4, 6);
    for (let i = 0; i < count; i++) {
      const a = Math.PI + (Math.PI * (i + 0.5)) / count;
      deco.push({
        dx: Math.cos(a) * 0.8,
        dy: Math.sin(a) * 0.5,
        pr: t.num(`cloud.r${i}`, 0.44, 0.62),
      });
    }
  } else if (shape === "nub") {
    const count = t.int("nub.n", 1, 2);
    for (let i = 0; i < count; i++) {
      const a = t.num(`nub.a${i}`, 0, 2 * Math.PI);
      deco.push({
        dx: Math.cos(a) * 0.88,
        dy: Math.sin(a) * 0.88,
        pr: t.num(`nub.r${i}`, 0.24, 0.4),
      });
    }
  }

  // The silhouette is the whole avatar — no backdrop frames it — so its
  // bounding box *is* the size the eye compares against the avatar next to it.
  // That box used to be a dice roll of seeded radius, per-shape core and
  // decorations reaching past both. Solve the radius backwards instead:
  // measured at unit scale on the exact curves, then scaled so the larger
  // bounding-box side lands on SPAN for every shape and every seed.
  const ub =
    shape === "organic" || shape === "cloud"
      ? segsBounds(blobSegs(0, 0, 1, ratio, radii, shape === "cloud" ? 0 : rot))
      : segsBounds(superellipseSegs({ cx: 0, cy: 0, rx: 1, ry: ratio, n, rot }));
  let minX = ub.minX;
  let maxX = ub.maxX;
  let minY = ub.minY;
  let maxY = ub.maxY;
  for (const d of deco) {
    minX = Math.min(minX, d.dx - d.pr);
    maxX = Math.max(maxX, d.dx + d.pr);
    minY = Math.min(minY, d.dy - d.pr);
    maxY = Math.max(maxY, d.dy + d.pr);
  }
  const r = SPAN / Math.max(maxX - minX, maxY - minY);
  const rx = r;
  const ry = r * ratio;

  const body = {
    cx: 50 + t.jitter("body.x", 1.5),
    cy: 50 + t.jitter("body.y", 1.5),
    rx,
    ry,
    n,
    rot,
    radii,
  };

  // Where the eye pair sits as a unit. Gaze is deliberately a small effect: at
  // blobatar sizes it reads as jitter rather than as direction, and the budget it
  // used to spend is worth more in the gap below.
  const gx = t.jitter("gaze.x", 0.09) * rx;
  const gy = t.num("gaze.y", -0.2, 0.08) * ry;

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
  const tight =
    shape === "organic" || shape === "cloud"
      ? Math.min(...body.radii) * 0.95
      : 1;
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

  // Petals and lumps ride on a ring just outside the core, so they read as
  // part of the same creature rather than as satellites.
  const petals = deco.map((d) => ({
    cx: body.cx + r * d.dx,
    cy: body.cy + r * d.dy,
    r: r * d.pr,
  }));

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
    petals,
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
  const core =
    l.shape === "organic" || l.shape === "cloud"
      ? blobPath(
          b.cx,
          b.cy,
          b.rx,
          b.ry,
          b.radii,
          l.shape === "cloud" ? 0 : b.rot,
        )
      : superellipse(b);

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
    const path = `<path d="${l.perspective ? surfaceEye(e, b, mo ? undefined : l.perspective).path : superellipse(e)}"/>`;
    if (!mo) return path;
    return `<g class="mo-eye" style="--mo-wrap:${i ? 1 : -1};--mo-lean:${r2(e.rot)};transform-origin:${r2(e.cx)}px ${r2(e.cy)}px">${path}</g>`;
  };

  const body =
    `<g fill="${p.head}">` +
    // Decoration first so the core sits on top and the eyes always land on it.
    // Petals are true circles, so <circle> costs about a quarter of what the
    // equivalent four-segment path would — and a sun carries up to nine of them.
    l.petals
      .map((d) => `<circle cx="${r2(d.cx)}" cy="${r2(d.cy)}" r="${r2(d.r)}"/>`)
      .join("") +
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
