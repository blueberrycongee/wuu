# Handoff: push the expressions further

**Status: done.** The roster is loud, all three references are addressed, and the
design has moved into [expression-spec.md](./expression-spec.md), which is now
the current document. This one is kept as the record of what was measured and
where the brief turned out to be wrong — see [What shipped](#what-shipped) at the
end before trusting anything above it.

The roster reads too politely. This is the brief for making it read loudly,
written for a session picking the work up cold.

Read first, in this order:

1. [expression-spec.md](./expression-spec.md) — the design being extended.
2. [expression-followups.md](./expression-followups.md) — six defects found and
   fixed since. §7 of that document (retuning `sad` and `mad`) is **absorbed
   into this brief**; do not do it separately, because the amplitudes below
   replace those numbers wholesale.
3. [motion-spec.md](./motion-spec.md) §7 — the markup contract, which constrains
   nearly every option below in a way that is not obvious.

---

## The references

Three frames the roster should be able to hold, in
[`references/`](./references/).

| file                                       | what it shows                                                                                             |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| [`proud-flat.png`](./references/proud-flat.png)   | Eyes flattened into wide horizontal bars, ~4:1 landscape, slightly tilted, one sitting lower than the other |
| [`angry-red.png`](./references/angry-red.png)     | A hard `\ /` brow-down V, thick strokes — **and the whole body turned red**. Described as also shaking      |
| [`asymmetric.png`](./references/asymmetric.png)   | One eye left tall and round, the other flattened to a tilted line. The two eyes are doing different things  |

The gap in one sentence: our poses scale eyes by 1.06–1.14 and squash them to
0.5–0.72, symmetrically. The references scale by ~2 and squash to ~0.2,
sometimes only on one side.

---

## What is reachable today, and what is not

### Reachable: amplitude (`proud-flat.png`)

Just bigger numbers in the existing channels. Measured across 4000 seeds — the
capsule's natural aspect is **2.03 / 2.55 / 3.12** (p10 / median / p90, `ry:rx`,
so it is a portrait capsule), which means reaching a 4:1 *landscape* bar needs
`esx / esy ≈ 10`. `esx: 2.0, esy: 0.2` gets there.

The guard that stops you is the fusion one, and only that one. Worst-seed
clearance between the two capsules, in viewBox units — `test/expression.test.ts`
currently requires **> 2**:

```
esx   esy  tilt   edx=0   edx=2   edx=4
1.14  0.60   -6     4.4     8.4    12.4     ← happy today
1.06  0.50   15     4.3     8.3    12.3     ← mad today (2.5 at its own edx: -0.9)
1.60  0.35    0     2.8     6.8    10.8
2.00  0.25    0     1.2     5.2     9.2
2.50  0.20    0    -2.5     1.5     5.5     ← fuses
3.00  0.18    0    -6.7    -2.7     1.3     ← fuses badly
```

So `esx: 2.0` costs 3 units of clearance and a **positive `edx` buys it back at
1:2** — spreading the pair by 2 units returns 4. The reference pair does sit
wide, so this is the pose agreeing with the geometry rather than a workaround.

Containment is **not** a problem, which is worth knowing before you spend time
on it. Worst eye-corner reach as a multiple of the body radius, against the
1.12 the test allows:

```
happy today                   0.82
mad today                     0.74
proud-ish (esx 2.0, esy 0.2)  0.85   /  0.95 with the pair pushed apart
angry V  (esx 1.6, tilt 32)   0.90
```

Plenty of room. **Push amplitude, spend it on `edx`, and re-check the fusion
margin — that is the whole loop for this half.**

Also raise the tilt ceiling: the references sit around 30–35° against `mad`'s
15°. The clearance table above already includes a 30° case (it costs ~0.2
units — tilt is cheap once the capsule is flat, because a flat capsule sweeps
sideways far less per degree).

### Needs a decision: per-eye asymmetry (`asymmetric.png`)

`Pose` mirrors `tilt` and `edx` per side and applies everything else to both
eyes equally. There is no way to say "flatten the right one only."

**The trap:** the obvious fix — emitting per-eye values into each eye's inline
`style` — is forbidden. That markup is inside `parts.inner`, and nothing in
`parts.inner` may vary with the expression, or the morph stops existing. That is
§1 of the follow-ups, it cost real time, and it is guarded by a test.

**The way through** is already in the file. Each eye carries a constant
`--mo-wrap` of -1 or +1, which does not vary with expression. So ship *both*
endpoints as inherited pose properties and let each eye pick its own:

```css
--mo-esx-eye: calc(
  var(--mo-esx) + (var(--mo-esx-b) - var(--mo-esx)) * (var(--mo-wrap) + 1) / 2
);
```

`-1` selects `--mo-esx`, `+1` selects `--mo-esx-b`, both are registered
`<number>`s so both still interpolate, and the markup never moves. Costs one
extra `@property` and one extra emitted declaration per asymmetric channel —
measure before doing this to all of them; asymmetry is probably only wanted on
`esx`/`esy`/`tilt`.

### Needs a decision: colour (`angry-red.png`)

This one is a genuine conflict with two existing guarantees, and should not be
implemented without deciding to weaken them on purpose.

1. **The spec excludes colour from `Pose` explicitly**, on the grounds that it
   would be the most legible cue available *and* would void the contrast
   guarantee that `test/color.test.ts` verifies at 1° resolution across every
   hue and tone.
2. **The fills are markup.** They are `fill="#…"` attributes inside
   `parts.inner`, so making them vary with expression breaks the same invariant
   as above.

If it goes ahead, the shape that satisfies both:

- Emit the resolved palette as custom properties alongside the motion vars
  (`--mo-head`, `--mo-eye`), and set `fill` from CSS. A CSS rule beats a
  presentation attribute, so the static markup does not have to change at all —
  which keeps the determinism guarantee intact. Verify that claim early; it is
  the load-bearing one.
- Add a single registered `<number>` channel, e.g. `--mo-heat: 0…1`, and mix:
  `fill: color-mix(in oklab, var(--mo-head), var(--mo-hot) calc(var(--mo-heat) * 100%))`.
  One number interpolates, so the morph carries the colour for free.
- **Extend `test/color.test.ts` to cover tinted pairs.** A tint that lands on
  top of a contrast-checked palette is not itself contrast-checked, and the
  guarantee is either kept or dropped — quietly half-keeping it is the bad
  outcome.

### Needs a decision: shake (`angry-red.png`)

Conflicts with the model rather than the markup. An expression is documented as
_set and held_ — no timers, no self-termination, `setExpression(mad)` stays mad
until the consumer says otherwise. A shake is transient by nature.

Notes for whoever designs it:

- `.mo-root` has `translate`, `rotate` and `scale` free; only `transform` is
  taken (by the hover lift). A shake keyframe on `translate` composes with
  everything already there and needs no new element.
- A one-shot `animation-iteration-count: 1` on `.mo-expr` will not replay when
  the same expression is re-selected, because nothing in the DOM changes. If
  re-triggering matters, that is a real API question — and the library
  deliberately has no notion of "fire again".
- It must be gated by `prefers-reduced-motion`. The current rule keeps the pose
  and drops the morph; a shake is decorative under that rule and should go.

---

## Constraints that are not negotiable without saying so

These are all guarded, and each cost something to establish:

- **Nothing that varies with `expression` may appear in `parts.inner`.** The
  morph does not survive it. `test/expression.test.ts` pins it.
- **Determinism.** Existing blobatars must not move. `idle` stays byte-identical
  to no expression at all.
- **Tree-shaking.** Expressions are values, not names — a consumer who imports
  one ships one. +36 B per extra pose, gated by the `blob + happy` size row.
- **Reduced motion keeps the pose and drops the morph.** Every new channel needs
  its restatement in that block, or it silently vanishes for those users. This
  has already been missed once (`--mo-edx`, §5).
- **The static and animated paths must agree.** `bakePose` and the CSS compose
  the same pose two different ways, and they have diverged before by 25px.

## How to check your work

```
bun run check     # tests, size budgets, then the composition gate
```

The last of those is `scripts/probe-compose.ts` — real Chrome over CDP. It
renders the static bake and the animated composition side by side across the
most-leaned seeds and compares transformed outline points (0.01px today), then
drives real React through an expression change and watches the morph frame by
frame. **Every new channel needs a case added to it.** A channel that only the
CSS applies, or only `bakePose` applies, looks completely fine in `bun test`.

Two size budgets are near their ceiling and will need raising with a reason:
`react` (5741/5750) and `blob only` (3696/3700).

## Judging it

The numbers are not the deliverable — legibility is. Use the tuning grid
(`apps/demo`) in `sad|mad` paired mode, which exists precisely because these two
are the pair at risk of collapsing into each other. Exaggeration makes that
easier, not harder, so if two poses still read alike after this work the problem
is the channel choice and not the amplitude.

The one thing the references do not show is a grid. These are single large
blobatars; most blobatars are 32px in a list. Check both before signing off.

---

## What shipped

All three references are addressed, and the amplitudes above landed roughly as
predicted. Four things did not, and they are the useful part of this record.

### 1. The body channels were removed, not tuned

The brief treated `bsx`/`bsy`/`skew` as part of the roster and spent frame budget
on them. They are gone. Two arguments the brief did not make:

- Extending the frame-containment test to model `skew` — it moves x *by* y, so
  the per-axis check that was there could not see it at all — showed `mad`
  already leaning about two units outside the viewBox at its *old*, timid
  values. They break the frame before they get loud enough to read.
- In `blob` the silhouette is the identity. Deforming it per-expression spends
  the thing that distinguishes blobatars to say something the eyes say better.

`bdy` stayed: a rigid translate moves the creature without deforming it. See
§2.1 of the spec.

### 2. The tilt ceiling was not a taste bound, it was an amplitude bug

The brief says "also raise the tilt ceiling" as a trim item. It is more than
that. The first tuning pass concluded tilt carried almost no signal, and that
conclusion was measured at `esy: 0.5` — where it is correct, because rotating a
tall capsule barely changes its silhouette. At `esy: 0.26` the same rotation is a
different gesture entirely, *and* it is cheap, because a flat capsule sweeps
sideways far less per degree.

Generalisable: **a channel that measures as useless may be measuring the
amplitude of a different channel.** Spec §2.2.

### 3. The colour mechanism is the opposite of the one proposed

The brief proposed a registered `--mo-heat` and `color-mix()` in the stylesheet.
Elegant on the way in, and it pops on the way out — the hot endpoint is a custom
property the expression emits, so clearing the expression deletes it in the same
frame the heat starts easing back. The fill snaps while everything else takes
560ms.

The mix is resolved in TypeScript instead and the stylesheet only ever sees a
finished colour, carried by `transition: fill`. The brief's instruction to
"verify that claim early" about CSS beating presentation attributes was right and
the claim held; it was the *interpolation* that did not survive contact.

The guarantee was extended rather than dropped, and across the whole mix rather
than at its ends — the middle of a transition is where it would actually fail,
and it is the one place nobody points a contrast checker. Spec §11.

### 4. The shake did not need a "fire again" API

The brief treats re-triggering as an open API question. It is not one. A tremor
is an *amplitude on a loop that always runs* — the same shape `--mo-amp` and
every idle layer already use — so it resolves to the identity when calm, has
nothing to start or restart, and interpolates in over the same 420ms as the rest
of the pose. Set-and-held survives intact. Spec §5.2.

### Also worth knowing

- **Duration is a function of amplitude.** Doubling the travel and keeping 240ms
  made the morph read as a cut again, for the third time and the second distinct
  reason. Now 420/560ms, and the gate's pacing check measures fractions of
  whatever duration is set rather than fixed milliseconds — duration is a taste
  dial and the gate should not own it.
- **The gate grew two legs** (D tremor, E colour) and one bug fix: it captured
  an animation's `startTime` without awaiting `ready`, which reports `null` and
  made a check pass or fail depending on what ran before it.
- **Budgets rose**, all with reasons in `scripts/size.ts`. The one worth knowing:
  `mad` costs 699 B where a cool expression costs 52, because the colour code
  rides on it. A consumer who imports `happy` and `sad` pays none of that.
