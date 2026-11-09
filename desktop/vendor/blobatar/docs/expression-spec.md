# Expression spec

Named poses a consumer sets and the library holds — `idle`, `happy`, `sad`,
`mad`, `surprised`, `wink`, `sleepy`, `smug`, `unsure`, `scared`, `love`, `shy`,
`sick` — and the morph between them.

The first four are the original roster and are what §2 was written against; the
next six were added as pure pose data (§3.1) and the last three came with the
generalization of the tint (§3.2).

> Six defects found after this first landed are fixed; see
> [expression-followups.md](./expression-followups.md) for what they were.
> The amplitudes, the per-eye differentials, the tint and the tremor come from
> [expression-exaggeration.md](./expression-exaggeration.md), which is done and
> folded back into this document.

Separate from [motion-spec.md](./motion-spec.md), which covers the idle loop.
The two share plumbing and almost nothing else: idle motion is ambient and gated
on hover, an expression is triggered and gated on nothing. A blobatar can be sad
and still breathing. Read §3 of the motion spec (the amp model) before §4 here.

## 1. What an expression is, and is not

**It is a value, not a name.** Expressions are imported from
`blobatar/expression` and passed in — `blobatar()` never imports that module, it
calls through whatever object it is handed. So a consumer who imports `happy`
ships `happy`, and one who imports nothing carries no pose code at all. As a
plain string option this cost every caller ~420 B for a feature most will never
use; see [ADR-0002](../../../docs/adr/0002-expressions-as-passed-in-values.md).

**It is a state, not an event.** Set `expression={happy}` and the blobatar stays
happy until you change it. The library holds no timers, has no queue, and never
returns to `idle` on its own. A burst — react, hold, release — is
`setExpression(happy)` plus your own `setTimeout`, which is four lines in your
code and zero bytes in this bundle. That is deliberate: the alternative is a
library owning a timer the consumer cannot see, plus a policy for what a second
trigger does to the first.

**It adds nothing and removes nothing.** Every channel moves something the
blobatar already has, so a `blob` grows no mouth when it is happy. The eleven
channels are eye scale (x, y), eye tilt, eye offset (x, y), three right-eye
differentials, a rigid body offset, a tremor amplitude, and heat.

Excluded on purpose:

- **Petals.** A sun's nine petals are silhouette. Animating them independently
  reads as wind, or as the creature coming apart.
- **Body scale and lean.** Removed rather than never built — see §2.1.
- **Path data.** Interpolating paths puts geometry on the main thread every
  frame, which is the same reason the breathe layer is a scale and not a shape.

**Colour is no longer excluded, and that was a deliberate trade.** The original
spec ruled it out on two grounds: it is the most legible cue available, and it
would void the contrast guarantee. The first is why `mad` now uses it; the
second turned out to be a reason to *extend* the guarantee rather than to decline
the feature, and `test/color.test.ts` now verifies the tinted pairs at 1°
resolution across every hue, tone, heat **and target**. See §11 and §3.2.

**It is `blob` only.** `character` accepts the option and ignores it — asserted,
so a half-applied pose can never ship quietly. `character` is where expressions
would be _easy_ (it has brows and a mouth) and it has no motion layer at all;
building one was a larger job than this feature.

## 2. The ceiling, which is the whole design problem

Two capsule eyes and a soft body. No brows. Anger reads through brows in every
face ever drawn, so `mad` is the hard case and always was.

The first tuning pass got this wrong in an instructive way. The plan leaned on
**tilt** as the pseudo-brow: tops leaning outward brings the inner edges down,
which is the shape of an angry brow. Rendered across seeds at 44 and 96px, it
carried almost no signal. A blob eye is a _tall_ capsule, and rotating a tall
capsule by 15° barely changes its silhouette — it is still a vertical bar. Both
`sad` and `mad` came out looking like `idle` with a slightly different eye
shape, and indistinguishable from each other.

What actually carries, in descending order:

1. **Eye height.** The load-bearing channel; everything else is trim.
2. **Eye convergence.** Eyes pulled together read as focus and glare; pushed
   apart, as vacancy. This was missing from the first pass and is what finally
   separated `sad` from `mad`.
3. **Eye offset.** Dropping the pair low reads as downcast.
4. **Tilt** — but only once the capsule is flat. See §2.2.
5. **Body offset.** Lift versus sink. Cheap, and it never distorts anything.

Consequence for the roster: **`sad` and `mad` are separated by three channels
disagreeing at once**, never by one. If a future expression is separated by a
single channel, it will not survive 44px.

### 2.1 The body stopped moving, and the reason generalises

`bsx`, `bsy` and `skew` scaled and leaned the whole creature. They are gone.
Three things settled it, and the third is the one worth carrying forward:

- They rank fourth of five above. They were always trim.
- They are the only channels with **no headroom**. Frame containment binds at
  roughly `bsx: 1.08`, and extending the containment test to model `skew` —
  which moves x _by_ y, so a per-axis check cannot see it at all — showed `mad`
  leaning about two units outside the viewBox at its old values. They break the
  frame before they get loud enough to read.
- **In this variant the silhouette is the identity.** Six shapes, a seeded
  lopsidedness, a seeded lean and a jittered body centre are what make a grid
  read as a crowd rather than as one blobatar repeated. Squashing that
  per-expression spends the thing that distinguishes blobatars in order to say
  something the eyes were already saying better.

That last argument is the same one §9 makes against seeded expression strength,
pointed the other way: identity belongs to the seed, expression belongs to the
message, and a channel that lets one overwrite the other is the wrong channel.

`bdy` survives because it is a rigid translate — it moves the creature without
deforming it.

### 2.2 Tilt was not a dead channel, it was a dead channel _on a tall capsule_

The first pass concluded tilt carried almost no signal, and at `esy: 0.5` on a
capsule whose natural aspect is 2.55:1 (median `ry:rx` across 4000 seeds) that
was correct — the result is still a vertical bar, and rotating a vertical bar
barely changes its silhouette.

Flatten it to `esy: 0.26` first and the same rotation is a completely different
gesture, because now there is a bar for the angle to be _on_. Tilt is also cheap
at that shape: a flat capsule sweeps sideways far less per degree, so 33° costs
about 0.2 units of the fusion clearance that a tall one would spend several on.

This is why the reference `\ /` is reachable at all, and it is a general shape:
**a channel that measures as useless may be measuring the amplitude of a
different channel.**

**And the sign flips with the orientation.** On a portrait capsule a positive
tilt leans both tops outward and brings the inner edges down — the angry
direction, which is what the channel was named for. On a landscape bar the same
rotation raises the inner ends, so past square the meanings swap: negative is
`\ /` and positive is `/ \`.

This is not subtle in the render and completely invisible to the suite. Every
geometric guard here — clearance, containment, the composition gate — is
sign-blind, because a mirrored pose occupies exactly the same space as the pose
it mirrors. The first version of these amplitudes shipped `happy` and `sad`
wearing angry brows and `mad` wearing a sad one, and every test passed. The only
thing that caught it was rendering the roster and looking at it.

Which is the standing instruction for this whole file: **the numbers are not the
deliverable, legibility is.** Render the grid at 120px and at 40px before
believing a tuning pass.

Honest limit, unchanged: `happy` reads as happy. `sad` and `mad` read as
_distinct from idle and from each other_ — louder now, and still not the way a
mouth would. That is the ceiling of the variant, not of the tuning.

### 2.3 A brow is absolute, so the seeded lean has to go — `lock`

The sign problem above has a twin, and the same guards are blind to it. Tilt
carries the brow, a brow is a _direction_, and `styles/blob.ts` already leans
each eye by up to 12° in a direction the seed picked. Added to a pose, that lean
is not identity any more, it is noise on the one channel carrying the meaning:

- `mad` is `\ /` at −33°. Meet a seed leaning +12° and the pair comes out nearly
  parallel — `\ \`, which reads as bored, not angry.
- The seed leaning the other way gets a −45° `mad`, angrier than anyone else's.

Both are the failure §9 already forbids, arriving through a channel nobody was
watching: **per-seed expression strength**. The 12° is real identity on an idle
face and worth keeping there; it is worth nothing on a brow.

So a pose can claim its tilt absolute. `lock` is how much of the seeded lean it
overrides — 0 keeps the additive behaviour, 1 cancels the lean and puts the eyes
at exactly the angle the pose names. All three loud poses lock; `idle`, being the
identity, does not, so an unposed blobatar leans exactly as its seed drew it and
the lean returns intact the moment an expression clears.

It is a `<number>` rather than a switch so that it interpolates: the lean eases
away over the morph with everything else, and eases back on the way out.

The two rendering paths cancel it in different places and must agree. `bakePose`
leaves the lean out of the eye's `rot`. CSS cannot — the lean is baked into the
path's own coordinates by `superellipse` — so `.mo-eye` subtracts it on `rotate`,
_outside_ the scale bracket, which leaves that bracket still operating in the
capsule's drawn frame where it belongs. Both collapse to `R(tilt · wrap) · scale`
about the eye's own centre, and check A of the composition gate measures the two
against each other.

Cost at the guards: fusion clearance improves for `happy` and `sad` (4.62 → 5.05,
8.37 → 9.40) and tightens slightly for `mad` (3.13 → 2.93, limit 2), because the
seeds that used to lean _away_ from `mad`'s V no longer get that discount.

## 3. The poses

Defined once, in `src/expression.ts`. Scales are factors, `tilt` is degrees,
offsets are viewBox units, `lock`, `heat` and `shake` are 0–1 amounts. `edx`
and `tilt` are mirrored per side; the `*2` channels apply to the right eye only.

|         | esx  | esy  | tilt | edy  | edx  | esx2  | esy2  | tilt2 | lock | heat | shake | bdy  |
| ------- | ---- | ---- | ---- | ---- | ---- | ----- | ----- | ----- | ---- | ---- | ----- | ---- |
| `idle`  | 1    | 1    | 0    | 0    | 0    | 0     | 0     | 0     | 0    | 0    | 0     | 0    |
| `happy` | 1.72 | 0.30 | 8    | −1.5 | 1.5  | 0.08  | 0.05  | −16   | 1    | 0    | 0     | −2.2 |
| `sad`   | 0.60 | 0.56 | 26   | 3.6  | 1.9  | −0.05 | −0.07 | −7    | 1    | 0    | 0     | 2.6  |
| `mad`   | 1.85 | 0.26 | −33  | 0.4  | 0.6  | 0     | −0.03 | 5     | 1    | 0.62 | 0.55  | 0.8  |

### 3.1 The second roster

Added later and additive by construction — every pose is data on channels that
already existed, so `motion.css` learned nothing new:

|             | esx  | esy  | tilt | edy   | edx  | esx2  | esy2  | tilt2 | lock | heat | shake | bdy  |
| ----------- | ---- | ---- | ---- | ----- | ---- | ----- | ----- | ----- | ---- | ---- | ----- | ---- |
| `surprised` | 1.34 | 1.2  | −6   | −1.05 | 0.5  | 0.05  | 0.07  | 3     | 1    | 0    | 0     | −1.4 |
| `wink`      | 1.32 | 0.76 | 5    | −0.6  | 0.8  | 0.26  | −0.56 | −11   | 1    | 0    | 0     | −1.1 |
| `sleepy`    | 1.14 | 0.22 | 0    | 2.4   | 0.3  | −0.04 | 0.03  | 4     | 1    | 0    | 0     | 1.2  |
| `smug`      | 1.3  | 0.42 | 18   | −0.5  | 0.5  | 0.06  | −0.06 | −36   | 1    | 0    | 0     | −1   |
| `unsure`    | 0.95 | 1.02 | 4    | −0.2  | 0.3  | 0.24  | −0.44 | −18   | 1    | 0    | 0     | 0    |
| `scared`    | 0.78 | 0.96 | −12  | −1.5  | −0.8 | −0.04 | 0.05  | 4     | 1    | 0    | 0.35  | −0.6 |

Four things about that table are the design rather than the tuning:

- **`surprised` is the only pose that goes _up_ on `esy`.** Everything in the
  first roster lives between 0.26 and 0.56, so a pose at 1.26 cannot be confused
  with any of them at any size. It is also the first pose in this feature where
  **containment binds rather than fusion** — growing the pair and lifting it push
  the eye corners toward the outline, which is why its `esy` came back down from
  1.34 to 1.20 across two tuning passes. It is the only pose in the library whose
  amplitude is capped by the frame rather than by legibility.
- **`wink` and `unsure` are built _on_ the differentials**, not seasoned with
  them. `happy` sets `esy2` to 0.05 explicitly to stay short of a wink; `wink` is
  that channel eleven times over. This is the shape no amount of amplitude on a
  symmetric pose can imitate, and the payoff for §5.1 existing at all.
- **`sleepy`'s nearest neighbour is `mad`, not `sad`.** Both are landscape bars,
  and at 44px the bar is most of what reads. It survives on `tilt` (0 against
  −33), `edy` (+2.4 against +0.4), and the tint and tremor it does not carry —
  three channels, per the §2 rule. Its `tilt: 0` under `lock: 1` is not a no-op:
  a level pair is what reads as lidded, and a seed's 12° lean turns that into
  suspicion.
- **`scared` spends `shake` without `heat`**, which is the point of listing it
  next to `mad`: a tremor is arousal, not anger, and a consumer who wants a
  frightened blobatar should not pay ~700 B of colour code for it. At 0.35
  against `mad`'s 0.55 it is a shiver rather than a rage.

`smug` is the one that needed a second pass. It is `happy`'s parallel-tilt trick
(`tilt2 = −2 × tilt`) turned up: at 10° the lean did not survive 40px and the
pose read as a narrower `happy`, so it sits at 18°. A symmetric tilt is a brow
and reads as an emotion; a parallel tilt is a head cocked and reads as an
attitude, which is the whole distance between this pose and `mad`.

Measured headroom for the second roster, same 4000 seeds:

| guard                                 | surprised | wink | sleepy | smug | unsure | scared | limit  |
| ------------------------------------- | --------- | ---- | ------ | ---- | ------ | ------ | ------ |
| worst fusion clearance, viewBox units | 4.27      | 5.16 | 6.10   | 4.39 | 4.79   | 3.46   | > 2    |
| worst eye-corner reach, × body radius | 1.05      | 0.89 | 0.73   | 0.81 | 0.90   | 0.88   | < 1.12 |

`scared` is the tight one on clearance for the obvious reason — convergence
spends clearance directly. It narrows the eyes on the way in and buys most of it
back.

### 3.2 The tinting roster, and the generalization that allowed it

`mad` was the only pose that spent colour for two releases. The reason was never
that anger is special — it was that `hot()` was *a red* with the walk that keeps
the contrast guarantee (§11) baked into it. Splitting those apart is the whole
enabling change: `hot(head, eye)` became `tinted(head, eye, Tint)`, where a
`Tint` is four numbers — a hue to arrive at, a lightness to head toward, how far
of the way to travel (`pull`), and a chroma floor.

|         | esx  | esy  | tilt | edy  | edx   | esx2  | esy2  | tilt2 | lock | heat | shake | bdy  |
| ------- | ---- | ---- | ---- | ---- | ----- | ----- | ----- | ----- | ---- | ---- | ----- | ---- |
| `love`  | 0.86 | 1.28 | −14  | −0.5 | −0.35 | 0.05  | 0.06  | 6     | 1    | 0.6  | 0     | −1.6 |
| `shy`   | 0.62 | 0.5  | 10   | 1.4  | −0.2  | −0.05 | −0.04 | −8    | 1    | 0.55 | 0     | 0.9  |
| `sick`  | 1.25 | 0.34 | 20   | 1.8  | 0.8   | 0.05  | −0.05 | −6    | 1    | 0.6  | 0.18  | 1.4  |

| target  | h   | l    | pull | c    | worn by |
| ------- | --- | ---- | ---- | ---- | ------- |
| `HOT`   | 27  | 0.58 | 0.6  | 0.18 | `mad`   |
| `ROSE`  | 358 | 0.72 | 0.55 | 0.16 | `love`  |
| `BLUSH` | 12  | 0.84 | 0.4  | 0.1  | `shy`   |
| `BILE`  | 142 | 0.66 | 0.6  | 0.13 | `sick`  |

The targets live in `color.ts` beside `HOT` rather than in `expression.ts`, so
the module that owns the guarantee owns every endpoint it has to hold for. They
are exported as `TINTS`, and `test/color.test.ts` iterates that list — a target
added to the roster and not to `TINTS` would be a tint nothing verifies.

**`pull` separates the targets as much as `h` does.** `BLUSH` travels only 0.4
of the way and lands pale on purpose: a shy blobatar that goes as red as an angry
one is an angry one.

#### The rule for spending colour

Colour is the loudest channel in the vocabulary and the only one that does not
have to fight two capsule eyes for legibility, which makes it exactly the channel
most likely to be spent instead of doing the work. So the §2 rule applies to it
unchanged, in this form: **a tint is never the only thing separating two poses.**
Every pose here would still be a different pose in greyscale.

That is not a principle stated after the fact — both of the first drafts broke
it, and rendering the roster in a contact sheet is what caught them:

- **`love` was wide and tall**, which is `surprised`. Same face, with the tint
  carrying the entire meaning. It is now narrow and drawn together (0.86, −0.35)
  against `surprised`'s wide and spread (1.34, +0.5): startled *by* you against
  looking *at* you.
- **`shy` was a pink `sick`** — both flat-ish eyes low over a sunk body,
  separated by hue alone. `shy` is now small and converged, `sick` is wide bars
  slumped into a `/ \`.

The check that found both is worth keeping: score every pair of poses by weighted
channel distance with `heat` excluded, and look at the closest ones. The tightest
pair in the roster is now `sick`/`sleepy`, which disagree on tilt (20 against 0),
tremor and tint.

`sick`'s tilt being *positive* is the §2.2 sign trap, not a typo: on a landscape
bar +20 raises the inner ends into a worried `/ \`, where `mad`'s −33 drops them
into an angry `\ /`.

#### What a tinting pose costs

Measured through the same synthetic consumers `scripts/size.ts` uses:

| import         | gz     | delta |
| -------------- | ------ | ----- |
| `blob` only    | 3660 B | —     |
| `+ happy`      | 3989 B | +329  |
| `+ mad`        | 4715 B | +726  |
| `+ love`       | 4782 B | +67   |
| `+ shy`        | 4845 B | +63   |
| `+ sick`       | 4898 B | +53   |
| whole roster   | 5155 B |       |

**The second tinting pose costs 67 B, not 726.** That is the generalization
paying for itself: one walk, four targets. The first tinting pose in a bundle
still carries the whole colour path — `tinted`, `mixHex`, `fromHex` and the OKLab
matrices — and a consumer who imports only cool poses still pays none of it.

`happy`'s `tilt2` is exactly `−2 × tilt`, which is not a coincidence and not a
third asymmetric flourish. The mirroring makes the left eye `−tilt` and the
right `tilt + tilt2`; setting the differential to `−2t` makes both `−t`, so the
pair tilts **in parallel** rather than symmetrically. That is the
`proud-flat.png` reading — jaunty rather than expressive — and it is only
reachable because the differential exists.

Measured headroom at these values, across 4000 seeds:

| guard                                     | happy | sad  | mad  | limit  |
| ----------------------------------------- | ----- | ---- | ---- | ------ |
| worst fusion clearance, viewBox units     | 5.05  | 9.40 | 2.93 | > 2    |
| worst eye-corner reach, × body radius     | 0.92  | 0.78 | 0.86 | < 1.12 |

**Fusion is the binding guard and containment is not**, which is the opposite of
what the first amplitude pass assumed. Widening the eyes costs clearance at
roughly 1.5 units per 0.5 of `esx`, and a positive `edx` buys it back at about
1:2 — which is why `mad` spreads the pair rather than converging it. The
widening already closes the inner gap far more than a convergence would, so the
glare survives; spending clearance on both fuses the face.

The roster is **frozen per major**, exactly like the shape thresholds and the
tone set. A fifth expression is additive and safe; renaming one is not.

`idle` is exported like the rest, and emits nothing: every one of its channels
equals the identity, so `poseVars` skips all of them (§6).

## 4. One table, two serializers

The pose is defined once and consumed twice, and both serializers ride on the
expression value rather than being imported by the renderer — that indirection is
what keeps the core free of them.

**Animated** — `poseVars()` emits the moving channels as registered custom
properties. Channels a pose leaves at their identity are skipped, since an
omitted declaration already _is_ the identity. Because they are registered as `<number>`, they interpolate, and the
morph is what a transition on them does. There are no per-expression keyframes
and no `.mo-happy` selectors anywhere in `motion.css`; the stylesheet never
learns which expression is on.

**Static** — `bakePose()` bakes the eye channels into the eye geometry and hands
`bdy` back as one `transform` attribute on a wrapper `<g>`. Baking is exact
rather than approximate because the CSS composes in the same order the geometry
does: `superellipse` scales then rotates, and `.mo-eye` applies the pose scale
on `transform`, the tilt on `rotate` and the offset on `translate` — which
resolve in exactly that order, outermost first.

That attribute used to be a three-transform chain derived from the CSS —
`T(50,50) · scale · translate(0,bdy) · skew · T(-50,-50)` — and needed the
origin sandwich to compose about the middle of the frame. With the deforming
channels gone it is one rigid translate, which commutes with everything and
needs no origin at all.

**A third serializer, for the one channel that is not geometry.** `tint()`
returns the palette the pose wears. The static path puts it in the `fill`
attributes; the animated path cannot, because nothing in `parts.inner` may vary
with the expression, so it goes out as `--mo-head`/`--mo-eye` and two `fill`
rules in the stylesheet pick it up. Two serializations of one decision, which is
why `scripts/probe-compose.ts` grew check E to compare them.

All three ride on the expression value rather than being imported, so a consumer
who imports no tinting pose never pulls the colour code in — measured: `hot`,
`mixHex` and `fromHex` are absent from the `blob + happy` bundle.

## 5. Composition, and why the hover gate is invisible to it

Every `transform` slot in the motion layer was already claimed. The pose needs
none of them, because **every element has free individual transform properties**:

| channel               | element                    | property                      |
| --------------------- | -------------------------- | ----------------------------- |
| `bdy`                 | `.mo-bob`                  | `translate`                   |
| `esx`, `esy` (+ `*2`) | `.mo-eye`                  | `transform`                   |
| `tilt` (+ `tilt2`, `lock`) | `.mo-eye`                 | `rotate`                      |
| `edx`, `edy`          | `.mo-eye`                  | `translate`                   |
| `shake`               | `.mo-root`                 | `translate` (`mo-shake`)      |
| `heat`                | body group, `.mo-eyes`     | `fill`                        |

One new DOM node per eye, and no new classes. `.mo-eye` is now a `<g>` wrapping
the capsule rather than the capsule itself, because the first three rows above
have to be *plain declarations*: the eye scale used to ride inside `mo-blink`'s
keyframes and the tilt inside `mo-wrap`'s, and Gecko substitutes a keyframe's
`var()` from the transition's endpoint rather than its current value — those two
channels snapped in Firefox while every other one eased. The idle loops keep the
shape underneath, selected as `.mo-eye > *`; the wrapper costs ~8 B per eye. The
body group is selected as `.mo-bob > g:not(.mo-eyes)` rather than carrying a
`mo-body` class of its own: a class costs bytes in the markup of every animated
blobatar _and_ in the core renderer that decides whether to emit it, which every
static consumer pays for too. Markup is paid per blobatar and this stylesheet once
per app, which settles that trade — see
[ADR-0003](../../../docs/adr/0003-expression-composes-into-existing-elements.md).

### 5.1 Per-eye asymmetry, without per-eye markup

A pose has one set of eye channels and two eyes. The obvious way to break that
symmetry — per-eye values in each eye's inline `style` — **is forbidden**: that
markup lives in `parts.inner`, and nothing in `parts.inner` may vary with the
expression or the morph stops existing (§1 of the follow-ups).

The way through is that each eye already carries one value that differs per eye
and never varies with the pose: `--mo-wrap`, which is −1 on the left and +1 on
the right. Mapped to 0/1 it becomes a selector:

```css
--mo-sel: calc((var(--mo-wrap, 1) + 1) / 2);
--mo-y: calc(var(--mo-esy) + var(--mo-esy2) * var(--mo-sel));
```

Both endpoints ship as inherited registered `<number>`s, so both still
interpolate and the morph carries an asymmetric pose exactly as it carries a
symmetric one. The markup never moves.

Two decisions inside that:

- **Differentials, not endpoints.** An identity of 0 means a symmetric pose
  states nothing extra and `poseVars` skips all three. A pair of endpoints would
  force every pose in the roster to state both eyes for every channel.
- **`tilt2` is added before the mirroring**, not after — `--mo-t` is multiplied
  by `--mo-wrap` downstream, so a differential added afterwards would flip sign
  per side and cancel back into symmetry. `bakePose` does the same, and
  `test/expression.test.ts` pins both.

Asymmetry is limited to `esx`, `esy` and `tilt`. A per-eye vertical offset was
considered and left out: at these amplitudes it reads as a misprint rather than
as character.

### 5.2 A tremor on a state that is held

An expression is set and held — no timers, no self-termination, no notion of
firing again. A shake is transient by nature, so the naive shape is a one-shot
animation, which cannot work here: `animation-iteration-count: 1` will not
replay when the same expression is re-selected, because nothing in the DOM
changes, and giving the library a "fire again" concept means giving it a queue
and a policy for what a second trigger does to the first.

So the tremor is not an event. It is an **amplitude on a loop that always runs**,
exactly like `--mo-amp` and every idle layer: `@keyframes mo-shake` resolves to
`0 0` at `--mo-shake: 0`, so a calm blobatar oscillates between two poses that are
the same pose. Nothing to start, nothing to restart, nothing to replay — and
because the amplitude is a registered `<number>`, the tremor _arrives_ over the
same 420ms the rest of the pose takes rather than switching on.

It is not gated on `--mo-amp`, unlike the idle layers: an expression has to
survive an unhovered grid.

Cost is one more always-running animation per animated blobatar, on `.mo-root`'s
free `translate`. That is the honest price and it is the same bargain the other
five make.

`edx`/`edy` sit on each eye rather than on the `.mo-eyes` group because that
group's `transform` is reserved for the gaze layer (§4.5 of the motion spec) and
its `translate` is the saccade's. Two eyes moving by the same amount is the pair
moving, so nothing is lost.

**The operator differs per channel and getting it backwards is silent.** Scales
multiply, offsets and rotations add. Check the algebra at `--mo-amp: 0`, which
is every unhovered blobatar in a grid: breathe collapses to `scale(bsx, bsy)` and
bob to `translate(0, bdy)`. The pose survives the gate intact — not by a special
case, but because that is what multiplying by an identity does. Multiply an
_offset_ by amp instead and it silently disappears for every unhovered blobatar.

Hovering a posed blobatar therefore adds breathing and blinking on top of the
expression rather than changing it. A `happy` blobatar that blinks closes from
where it already is, because the blink multiplies the squint.

## 6. `idle` is free

`expression: "idle"` emits **byte-identical markup** to omitting the option, and
`poseVars("idle")` returns `{}`. Every registered property's `initial-value` is
its identity, so an absent declaration _is_ the idle pose. Clearing an
expression transitions back toward those initials rather than snapping.

This is what makes the feature a minor rather than a major: no existing blobatar
moves by a single byte. `test/expression.test.ts` asserts it.

## 7. Timing — a deliberate inversion

Entering an expression: **420ms** `cubic-bezier(0.4, 0.2, 0.3, 1)`.
Returning to idle: **560ms** `ease-in-out`.

**Duration is a function of amplitude.** These were 240ms and 360ms, which was
right for a pose that squashed an eye to 0.6 and reads as a cut at 0.26 — the
same time over twice the distance. Nothing was wrong with the transition; there
was simply more of it to watch and no longer to watch it in. Worth stating
because it is the second time this feature has shipped a morph that "did not
run" and the second time the transition was working perfectly: the first was a
front-loaded curve (§6 of the follow-ups), this was a duration inherited from a
quieter roster.

The curve is not the obvious hard ease-out for the reason §6 of the follow-ups
sets out, and the pacing is now measured as a fraction of whatever duration is
set rather than at fixed milliseconds — duration is a taste dial and the gate
deliberately does not own it, while front-loading and dead starts are defects
and it does.

This inverts the house rule. §4.1 of the motion spec establishes that hover
_exits faster than it enters_ — "on the way in the user is deciding, on the way
out the system is just getting out of the way." That is right for a pointer
response and wrong here: an expression is a message the consumer sent, and
yanking it off the face in 160ms reads as a glitch rather than as a creature
settling.

The mechanism is the same one the hover asymmetry uses — a transition takes the
duration declared in the state it is heading _to_. `.mo-expr` is on the root for
any non-idle expression, so adopting one reads 420ms out of that rule and
clearing it reads 560ms out of the base rule. `.mo-expr` selects no pose of its
own; it exists only to carry the clock.

The two duration lists are stated once each, in `--mo-md` and `--mo-me`, because
`transition-*` lists match up by index and the `:hover` rule otherwise has to
restate every entry to change one. That is why ten channels cost fewer bytes
than the previous nine did.

Interruption is free. `happy → mad` mid-morph retargets from the current
computed value, because that is what CSS transitions do — a genuine argument for
transitions over keyframes here, since keyframes restart from zero.

## 8. Accessibility

**An expression is decorative and does not reach assistive technology.** `title`
names _who the blobatar is_ and is unchanged by the pose; folding a transient UI
state into an identity means every expression change re-announces a name.

Whatever the expression reflects — a reaction, a status, an error — already has
a real place in the DOM. If it does not, that is the bug.

Under `prefers-reduced-motion: reduce` the pose is adopted **at full strength,
instantly**. Reduced motion removes the _morph_, not the expression: a user who
gets no expression has lost information rather than been spared decoration.
That is why the reduced-motion block restates the pose — most of it lives in
keyframes the blanket reset wipes — and why `transition: none` there is the
whole implementation of "snap instead of ease".

Two of the newer channels split on that rule, in opposite directions, and the
split is the rule working rather than an exception to it:

- **The tremor goes.** It is ambient decoration by any reading, and it is the
  single most likely thing in this library to make someone unwell. The reset's
  `animation: none` on `.mo-root` removes it with no special case.
- **The tint stays, at full strength.** It is the message, not the decoration —
  and for `mad` it is the most legible part of the message. Only the `fill`
  transition is killed, so it snaps like every other channel.

## 9. Considered and rejected

**A mouth, or brows, hidden at idle.** Would solve §2 outright. Rejected because
those elements ship in the markup of every animated blobatar whether or not an
expression ever fires, against a 590–1060 byte figure — and because a blob with
a mouth is a different product, not a new pose.

**Seeded expression strength.** The idle layer's seeded phases exist because
ambient motion in unison reads as a mechanism rather than a crowd. That argument
does not transfer: an expression is a message, and one user's `happy` landing at
40% of another's is not personality, it is the same signal rendered
inconsistently per user. The variation is already there for free — `eye.scale`,
`eye.stretch`, `eye.ratio` and `eye.lean` are all seeded, so one transform lands
differently on every blobatar.

**A per-seed tilt clamp.** Built, measured, removed. The worry was real on
paper: `mad` tilts hard on top of a seeded lean already at its clearance
ceiling, and two tall capsules meeting in the middle is the one failure
`styles/blob.ts` calls unsurvivable. Measured, it never happens — `mad` flattens
the capsules on the way in, and a flat capsule sweeps sideways far less per
degree than the tilt adds. That margin got *wider* at 33°, not narrower, which
is the same observation §2.2 makes from the other end. The clamp engaged on 3.2%
of seeds and quietly made their `mad` milder than everyone else's, which is the
seeded-strength mistake above arriving through the back door. Replaced by an
assertion on the margin itself: `mad` is the tight one at 3.13 units against a
floor of 2, so the test is a live guard.

**A single authored red for every hot blobatar.** The obvious tint, and it cannot
hold the contrast guarantee: `blob` flips its eye between near-black and
near-white depending on the body's lightness, and no fixed red clears 4.5:1
against both. It also erases the tone set at exactly the moment the grid is
loudest — every angry blobatar converging on one colour is the identity/message
confusion of §2.1 all over again. The hot pair is derived per seed instead, and
meets a mid red halfway rather than landing on it.

**`color-mix()` in the stylesheet, driven by a registered `--mo-heat`.** This is
the shape the design brief proposed and it is genuinely elegant on the way in:
one number interpolates and the morph carries the colour for free. It pops on the
way out. The hot endpoint is a custom property the expression emits, so clearing
the expression deletes it in the same frame `--mo-heat` starts easing back, and
the fill snaps to base while every other channel takes 560ms. Resolving the
colour in TypeScript and transitioning `fill` is symmetric in both directions and
needs no registration at all — see §11.

**Baking the pose into the animated path too.** Presentation attributes lose to
CSS, so emitting both would be safe and would give an app that forgot
`import "blobatar/motion.css"` a correct static pose instead of a blank face.
Rejected at ~40 bytes per blobatar: the README already makes the stylesheet a hard
requirement in bold, so this pays on every blobatar in a 400-blobatar grid to soften
an error that is already documented loudly.

**Implying `animate`.** Convenient, and it would silently flip a 400-blobatar grid
from 400 `<img>`s to 400 inline SVG trees — precisely the failure the rendering
mode section of the README exists to prevent.

## 10. Budget

Measured through `scripts/size.ts`:

| import                           | gz            |
| -------------------------------- | ------------- |
| `blobatar/blob`, no expressions | 3733 B        |
| `+ happy`                        | 4041 B (+308) |
| `+ sad`                          | 4093 B (+52)  |
| `+ mad`                          | 4792 B (+699) |
| `mad` alone                      | 4689 B        |

The first expression carries the shared serializer and bake; a cool one after it
is just its numbers.

**`mad` costs 699 B, and it is the only one that does.** That is the colour code
— `hot`, `mixHex`, `fromHex` and the OKLab matrices — reached only through
`tint` on the expression value. It is worth stating plainly rather than
averaging away: a tinting expression is roughly fourteen times the price of a
cool one, and a consumer who imports `happy` and `sad` pays none of it. The
`mad alone` row is the number that matters if you only want the angry one.

Consumers who import no expression at all pay ~120 B — the dispatch in
`render.ts`, the `tint` call beside it, and the `expressive` marker on the
variant, which is the floor for the option existing at all.

`motion.css` costs ~380 B gz for the whole feature, paid once per app regardless
of how many expressions are imported: the stylesheet is generic over the poses
and never learns which ones exist. The exaggeration pass added ~130 B of that —
the per-eye derivation, the tremor keyframes and the two `fill` rules — partly
offset by hoisting the transition lists into `--mo-md`/`--mo-me`.

The `blob + happy` row in the size gate exists specifically to catch the
regression where expressions creep back into the core — if `blob only` and
`blob + happy` ever converge, the indirection has been lost.

## 11. Colour, and the guarantee it was excluded to protect

The original spec ruled colour out on two grounds. Both were right; only one was
a reason not to build it.

> **Color.** The most legible cue available, and the one that would void the
> contrast guarantee.

The first half is the argument *for*. Two capsule eyes and no brows is a thin
vocabulary for anger (§2), and colour is the one channel that does not have to
fight the geometry for legibility.

The second half is a reason to extend the guarantee, not to decline the feature
— provided the extension is real. Quietly half-keeping it would have been the
worst outcome of the three: a tint that lands on top of a contrast-checked
palette is not itself contrast-checked, and nothing would have said so.

### What ships

`heat` is a 0–1 amount on the pose. `mad` sits at 0.62, and three more poses
spend it — see §3.2 for the roster and the four targets.

The tint's endpoint is derived from the blobatar's own resolved palette rather
than authored once. The body keeps its own hue-neutral character by meeting a mid red
**halfway** in lightness: holding its lightness outright leaves a pastel pink
rather than angry, and travelling the whole way collapses the tone set. The eye
endpoint is then pushed until every point along the mix clears 4.5:1.

**Every point, not both ends.** A straight line in OKLab between two passing
pairs is not itself a passing pair — the body travels further than the eye, so
the two lightnesses can close on each other in the middle of a 420ms transition
that is perfectly legible at both stops. `tinted()` walks the mix and fixes the
worst point; `test/color.test.ts` verifies it at 1° hue resolution, six tones and
eleven heats, and separately that a tinted body still clears 1.5:1 against a
near-black page.

Two further assertions exist because the guarantee alone is satisfiable by doing
nothing: the hue has to arrive in the reds, and there has to be chroma left for
it to arrive in. `ensureContrast` and the gamut mapper both reach for lightness
and chroma, and either could quietly turn "angry" into "beige".

### Where the mix happens, and why not in CSS

In TypeScript, resolved to a finished pair of colours before the stylesheet sees
anything. `heat` is the one pose channel with no custom property.

The CSS-side alternative is in §9. The short version: a hot endpoint held in a
custom property vanishes the instant the expression is cleared, which snaps the
fill mid-morph. A plain `transition: fill` is symmetric for nothing.

### How it reaches the shapes

The fills are `fill="#…"` presentation attributes inside `parts.inner`, which
CSS cannot read and which nothing in `parts.inner` may vary with the expression.
So the stylesheet restates `fill` from `--mo-head`/`--mo-eye`, which the renderer
emits on every animated `blob` — tinted when the pose tints, and byte-identical
to the attribute when it does not.

Emitted unconditionally, at ~30 B per animated blobatar, because a `var()` with
nothing behind it makes `fill` invalid at computed-value time, and `fill` is
inherited: the body would render black. That is the cost of the rules being
always-valid rather than class-gated, and class-gating is what reintroduces the
pop.

The attribute stays and is not dead weight — it is what an app that forgot
`import "blobatar/motion.css"` renders, and what the static path has always
used.
