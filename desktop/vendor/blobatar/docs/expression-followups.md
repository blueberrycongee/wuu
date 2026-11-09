# Expression follow-ups

Three defects found by eye after the expression feature landed. **All three are
fixed.** Fixing them turned up two more, both older and neither expression-
specific (§4, §5), and a sixth surfaced once the morph was finally visible
enough to judge (§6). All six are fixed.

Read [expression-spec.md](./expression-spec.md) first — it explains the design
these broke.

The through-line: every one of these lives in the gap between what the renderer
emits and what a CSS engine does with it, and `bun test` cannot see across that
gap from strings alone. So the lasting artifact here is not the six fixes, it is
`scripts/probe-compose.ts` — a headless-Chrome gate, driven over CDP so it runs
on a real clock, that renders the static bake and the animated composition side
by side and compares where the pixels land, then watches a real expression
change frame by frame. It is part of `bun run check`. Every fix below was
verified by reverting it and watching that gate fail.

The seventh item (§7) is closed too, by the exaggeration pass —
[expression-exaggeration.md](./expression-exaggeration.md), which pushed the
roster to read much louder and replaced those amplitudes wholesale.

---

## 1. The morph never ran — `idle → happy` was a hard swap ✅

### What happened

`rootClass()` appended `mo-expr` to the root `<g>` when a non-idle expression
was set. That class lived **inside the string handed to
`dangerouslySetInnerHTML`**, so changing expression changed the markup string,
and React replaced the `<svg>`'s entire inner DOM.

A brand-new element has no previous computed value, and transitions never run on
an element's first style resolution — the same rule `motion.css` already relies
on to avoid a start-up ramp on `.mo-always`. So the pose was simply _born_ at its
final value. There was no transition to see.

### Why the earlier verification missed it

The transition mechanism was checked in an isolated harness by seeking the
animation (`a.currentTime = 120`) rather than watching it run, and on the site by
sampling once at t=300ms — after a 240ms transition would have finished either
way. Both are consistent with a hard swap. **The mechanism worked; the wiring
around it did not.**

### The invariant, now restored and pinned

> Nothing that varies with `expression` may appear inside `parts.inner` on the
> animated path.

`test/expression.test.ts` asserts it directly, over 50 seeds × the whole roster.

### The fix

Option (a) from the original triage: the root `<g>` moved out of the innerHTML
string and into React. `makeParts` now returns `cls`, `bg` and `inner`
separately, and the adapter renders

```tsx
<svg style={vars}>
  {title ? <title>{title}</title> : null}
  {bg ? <path d={bg.d} fill={bg.fill} /> : null}
  <g className={cls} dangerouslySetInnerHTML={html} />
</svg>
```

Two things the original triage did not anticipate:

- **The title and the backdrop had to come out too.** They are siblings of the
  root `<g>`, not children — `<title>` names the element it is the first child
  of, and a backdrop inside the root would hover-lift with the creature — and
  `dangerouslySetInnerHTML` is all-or-nothing, so they could not stay in the
  same string. The backdrop is returned as geometry (`{d, fill}`) rather than
  markup for exactly this reason.

- **Option (a) alone did not fix it.** React compares props by _reference_, and
  `dangerouslySetInnerHTML={{__html: …}}` is a fresh object literal on every
  render — so React re-assigned `innerHTML` whenever anything else about the
  blobatar changed, byte-identical string or not, and that assignment rebuilt the
  subtree just as thoroughly as a changed string would have. The object has to
  be memoized on the string:

  ```tsx
  const html = useMemo(() => ({ __html: parts?.inner ?? "" }), [parts?.inner]);
  ```

  This is the half that a markup-only test can never catch, and it is why §1 and
  §3 both needed the browser gate to confirm rather than to find.

Cost: 80 B gzipped on the `react` entry, budgeted in `scripts/size.ts`.

### Verified

`✓ B nothing is added or removed, only written to — 9 attribute writes: class on
<g>, style on <svg>`. Reverting the `useMemo` alone brings back
`g children (+1/-1)`.

---

## 2. Scaled eyes deformed on the wrong axis ✅

### What happened

`superellipse()` bakes rotation directly into the emitted coordinates
(`src/shape.ts`, the `cos`/`sin` in `at()`). So by the time a leaned eye reached
the DOM, its path `d` was already tilted and the element's local axes were the
**viewport's**, not the capsule's.

| path                             | order                                            | result                                        |
| -------------------------------- | ------------------------------------------------ | --------------------------------------------- |
| static (`bakePose`)              | `rx *= esx; ry *= esy` **then** `rot`            | scales along the capsule's own axes — correct |
| animated (`@keyframes mo-blink`) | `scaleX()/scaleY()` on already-rotated geometry  | scales along screen axes — wrong              |

Across 4000 seeds the seeded eye lean is a **median of 5.9°, p90 11°, max 12°** —
most blobatars are visibly tilted, so most blobatars showed it.

**Blink had the same bug and always had.** `scaleY(0.08)` on a 12°-tilted capsule
closes it along screen-Y instead of across the capsule. Subtle at blink
amplitudes, obvious at `mad`'s `esy: 0.5`, which is why expressions surfaced it.
One fix covers both.

### The fix

`styles/blob.ts` emits a per-eye `--mo-lean` alongside `--mo-wrap` (~16 B per
animated blobatar), and every eye scale in `motion.css` is bracketed by it:

```
rotate(lean) · scaleX(esx) scaleY(esy) · rotate(−lean)
```

**Note the order** — the original triage wrote this chain reversed. For a path
whose geometry is already `R(lean)·q`, the transform that reproduces
`R(lean)·S·q` is `R(lean)·S·R(−lean)`, not `R(−lean)·S·R(lean)`. The reversed
form yields `R(−lean)·S·R(2·lean)·q`, which is wrong everywhere except where the
capsule's own symmetry hides it. The gate measures 5px of divergence on the
reversed form and 0.01px on this one.

Applied in `@keyframes mo-blink` (all three stops, identical function lists so
they still interpolate componentwise) and in the reduced-motion restatement.

The alternative — emitting the eye path **unrotated** and applying the lean as a
transform — remains rejected: `.mo-eye` has no free transform slot left
(`transform` is blink's, `rotate`/`scale` are wrap's), and it would change the
static markup for every blobatar, breaking the determinism guarantee.

---

## 3. Breathing restarted when an expression fired ✅

Same root cause as §1, and fixed by the same change — including the `useMemo`
half, without which it persisted. The `.mo-breathe` element now survives an
expression change, so its animation is never re-created and the seeded phase
offset — "the single most load-bearing 40 bytes in the motion layer" — survives
with it.

The composition itself was always correct: breathe multiplies into the pose
(`scale` on `.mo-breathe` against the keyframe's `transform`), so an expression
is _supposed_ to keep breathing.

### Verified

`✓ B breathe is not restarted — same Animation, startTime held at 941ms, now
50ms into its loop`. Both halves are asserted: a restart gives a new `Animation`
object, and a silent reschedule gives a new `startTime`.

---

## 4. Every body transform pivoted on the corner of the frame ✅

**Found by the gate, not by eye. Pre-existing, and not expression-specific.**

SVG's UA stylesheet sets `transform-origin: 0 0`, where CSS's initial value is
`50% 50%`. Nothing in `motion.css` overrode it for `.mo-root`, `.mo-breathe` or
`.mo-bob` — only `.mo-eye` had its own. So:

- The hover reaction's `scale(1.04)` moved the blobatar ~2 units down-right as it
  grew, instead of growing in place.
- The breathe drifted diagonally rather than pulsing — at 2.2% about (0,0) that
  is ~1.1 units of travel, an order of magnitude more than the 0.03-unit error
  the code comment was worrying about.
- Every body channel of every expression (`bsx`, `bsy`, `skew`) composed about
  the wrong point, putting the animated body 2–5 units away from where the
  static bake drew it.

The comment on `.mo-breathe` asserted "(50, 50)" the whole time. It was
describing the intent, and there was nothing to check it against until
`bakePose` — which is explicit about scaling around (50, 50) — gave the
composition something exact to be compared with.

Fixed with one shared rule stating both properties (`transform-box` too: its
initial value is `view-box` now but was not always, and a `<g>` has no border
box). Body divergence went from 21.47px to 0.00px.

---

## 5. `--mo-edx` was never applied on the animated path ✅

`.mo-eye`'s base `translate` carried only `--mo-edy`. The reduced-motion
restatement carried both, and `bakePose` applies both — so eye convergence, one
of the three channels `sad` and `mad` are built on, worked statically and under
reduced motion and silently did nothing on the animated path. Now:

```css
translate: calc(var(--mo-edx) * var(--mo-wrap, 1) * 1px) calc(var(--mo-edy) * 1px);
```

`--mo-wrap`'s sign mirrors it per side, matching `bakePose`.

---

## 6. The eyes read as a hard swap even once the morph ran ✅

Reported by eye after §1–§5 landed: the body morphed, the eyes still looked like
a cut.

They were not a cut. Measured frame by frame on a real clock, the eye
interpolated the whole way — every frame, correct values. The problem was the
_curve_, and the reason it hit the eyes and not the body is that the channels
travel wildly different distances: `bsx` moves 4%, `esy` moves 40%. Under
`cubic-bezier(0.2, 0.8, 0.2, 1)`:

```
t      0    16    33    51    68    82    99   116  …  240ms
eyeH  77.6  77.7  68.7  60.9  55.4  52.0  49.9  48.6     46.2
      └── 53% of the travel in three frames ──┘  └─ 140ms of invisible tail ─┘
```

On a 4% body change that reads as a settle. On a 40% eye squash the movement is
over before the eye appears to have left, which is indistinguishable from no
transition. The curve had never actually been watched — until §1 was fixed there
was no transition to watch, so 240ms and that easing were chosen on paper.

Enter now uses `cubic-bezier(0.3, 0.4, 0.2, 1)`: 25% of the travel at 50ms
against the old 53%, complete at the same 240ms, and still no dead start (an
ease-in-out would make a triggered expression feel late). The return is
unchanged — `ease-in-out` over 360ms, and the asymmetry it exists for is intact.

`scripts/probe-compose.ts` grew check C for this, and it is the one that would
have caught it: it samples the rendered eye height frame by frame through a real
expression change with the idle layers frozen, and asserts the travel is spread
across the duration rather than dumped into the first three frames. Existence
was never the question; the old ease passes "a transition is running" easily.

**The duration is a taste dial.** 240ms enter / 360ms return still comes from the
spec, and the curve above is one defensible reading of it rather than the only
one. Check C bounds the shape, not the feel.

---

## 7. `sad` and `mad` wanted retuning — done, as part of a larger pass ✅

**Closed.** The whole roster was retuned in the exaggeration pass, which replaced
these amplitudes wholesale rather than nudging them, removed the deforming body
channels, and added per-eye asymmetry, a tint and a tremor. See
[expression-exaggeration.md](./expression-exaggeration.md) for what was measured
and [expression-spec.md](./expression-spec.md) for the design as it now stands.

The original entry follows, because its reasoning is why the retune had to wait.

Point 4 of the original plan still stands, and §4–§6 above make it stronger
rather than weaker: those numbers were tuned against rendered output in which
the eyes sheared on the wrong axis, the body pivoted on the wrong point, `edx`
did nothing at all on the animated path, and the morph itself was never seen.
Three of the channels `sad` and `mad` are separated by are among the four that
changed behaviour.

It is no longer worth doing on its own, though. The roster is also being pushed
to read much louder, which replaces these amplitudes wholesale rather than
nudging them — see
[expression-exaggeration.md](./expression-exaggeration.md), which absorbs this
item and carries the measurements for how far the channels can actually go.

---

## 8. The eyes did not morph in Firefox — only the eyes ✅

**Symptom.** `esx`, `esy` and `tilt` snapped to the target pose on the first
frame of a morph while `edx`, `edy`, `bdy` and the colour eased normally, which
reads worse than nothing moving: the face arrives in two pieces. Chrome was
unaffected, which is why the composition gate had been green for the whole life
of the feature — it ran headless Chrome and nothing else.

**Cause.** Gecko substitutes a keyframe's `var()` from the transition's
*endpoint* rather than from its current value. Measured on the hero, idle →
`mad`: `--mo-esx` walked 1 → 1.85 across the full 420ms while the rendered eye
box went 29.3×58.2 → 39.3×24.1 in one frame and then held. Isolated, the same
expression as a plain declaration interpolates in Firefox and the identical one
inside `@keyframes` jumps:

```
t     --esx     plain declaration   inside @keyframes
86    1.209     scaleX 1.197        scaleX 2
252   1.624     scaleX 1.594        scaleX 2
419   2         scaleX 1.955        scaleX 2
```

Those three channels were the only ones living in keyframes — the eye scale in
`mo-blink`, the tilt in `mo-wrap` — because `.mo-eye` had no free transform slot
left for them.

**The fix.** Give it one. `.mo-eye` is now a `<g>` around the capsule instead of
the capsule itself: the wrapper holds the pose as plain `translate` / `rotate` /
`transform` declarations, and the shape underneath (`.mo-eye > *`) keeps blink
and wrap, whose keyframes now read only `--mo-amp` and the seeded `--mo-look-*`.

That was written as "none of which is ever transitioned", which is wrong:
`--mo-amp` is transitioned on hover, and `--mo-shake` — also read from a
keyframe — is a pose channel. The true invariant is that **no keyframe reads a
channel that positions anything**; what is left in them are amplitude scalars,
where endpoint substitution costs an abrupt start rather than a creature drawn
in the wrong place. See `.mo-eye` in `motion.css`. The lean bracket appears on both, and
composes as `R·S·R⁻¹ · R·B·R⁻¹ = R·(S·B)·R⁻¹`, so a squinting creature still
blinks from where it already is. Cost: ~8 B of markup per eye, and the
reduced-motion block got *smaller* — with ambient values in keyframes and pose
values in base declarations, `animation: none` now removes exactly the right set
and the restatement it needed is gone. This retires the "no free transform slot"
argument in §2.

**The gate now runs both engines.** `scripts/probe-compose.ts` launches Chrome
and Firefox, and the page posts its verdicts back over HTTP instead of being
read out over CDP — one `fetch` works in any engine and deleted the protocol
client. Check A is the one thing Firefox cannot measure: `getScreenCTM()` there
omits the translation `transform-box: fill-box` implies, so a correctly rendered
posed eye reads as hundreds of pixels out. It probes for that and prints `~`
rather than a tick. The static-versus-CSS agreement was verified in Firefox by
screenshot instead — all four poses, pixel-identical to the bake.

WebKit remains unmeasured; it cannot be driven from this machine.

---

## What is fine and was not touched

- The pose data model, the passed-in-value entry point, and the tree-shaking —
  still +36 B per extra expression, still gated by the `blob + happy` row in
  `scripts/size.ts` ([ADR-0002](../../../docs/adr/0002-expressions-as-passed-in-values.md)).
- The composition strategy — individual transform properties
  ([ADR-0003](../../../docs/adr/0003-expression-composes-into-existing-elements.md)).
  "No new DOM nodes" survived until §8, which added one `<g>` per eye to get the
  pose out of keyframes; the strategy it was protecting — no per-expression
  markup, no per-expression classes — is intact.
- `idle` being free and byte-identical to omitting the option.
- The reduced-motion restatement, other than the two corrections above.
