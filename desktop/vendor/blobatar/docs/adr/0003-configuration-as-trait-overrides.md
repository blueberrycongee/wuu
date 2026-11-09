# Configuration is trait overrides, not props

Making the blobatar configurable — so a consumer can fix the eye separation, the
eye size, the silhouette, any of it — we added **one option that pins traits by
their existing keys**, rather than a named prop per knob:

```ts
blobatar(user.email, { traits: { shape: 0.95, "eye.ratio": 0 } });
```

Each value is the position in `[0, 1)` the hash would otherwise have produced
for that key. The layout is unchanged and unaware: it still calls
`t.num("eye.gap", 0.1, 0.24)` and still gets a number in that range. Overriding
the *input* to the layout rather than any part of the layout itself is the whole
design.

The seam is four lines in `traits.ts` — a lookup before `stream()`, and a clamp.

## Why this shape

The layout already addressed every value by string key, for an unrelated reason:
keying by string is what makes the trait namespace append-only, so a new trait in
a minor cannot disturb existing blobatars (see `hash.ts`). That decision turns out
to have already built the configuration surface. Every knob anyone could want was
already named, already declared over a range, already read in one place.

So the cost of making *everything* configurable is one branch. Measured: **+1 B
gz on the core** before the budget bump, because the branch gzips against the
reader that was already there. That number is the argument.

## Considered options

**A prop per knob** — `eyeGap`, `eyeSize`, `eyeRatio`, `headShape`, … — is the
obvious API and the one that was asked for. Rejected on two counts. It puts
~25 named options and their plumbing in the core bundle, paid by every consumer
including the overwhelming majority who configure nothing; the core budget exists
to stop exactly that (see ADR 0002, where the same argument cost `expression` its
place as a string option). And it is a second vocabulary that has to be kept in
sync with the trait keys by hand, forever, with nothing to catch the drift.

**Overriding the `layout` function** — letting a caller pass their own — was the
first framing considered, and is worse than it looks. It makes the return shape,
the containment arithmetic (`fit`, the lean bound), and the per-shape decoration
all public API, frozen at 1.0. And a caller who got the arithmetic slightly wrong
would get eyes outside the body with nothing to stop them. Overriding the input
keeps every invariant in `styles/blob.ts` running over configured blobatars.

**Overriding the layout's output** — patching final coordinates — has the same
problem in a more tempting package. It is what `expression.bake` does, but a pose
is authored against the geometry once, in-repo, and tested; a consumer's patch is
neither.

**A compact shareable string** — `~a7Kd9…`, trait positions quantized to 6 bits,
one base64url character each — was designed and set aside. It needs a *second*
stable contract (a frozen trait *order*, a version byte, a decoder in the core
budget) to re-express a contract that already exists in a form humans can read.
An object literal is diffable, hand-editable, and sparse for free. Nothing is
foreclosed: the trait map is the underlying representation either way, so an
encoder can be added later as an opt-in entry point over a format already
shipped.

## Consequences

**Sparse is the default and the point.** Keys left out still come from the seed,
so `{ shape: 0.95 }` means "always a sun, everything else per seed" — lock what
carries the brand, and every user still gets their own creature. Pin every trait
and the seed stops mattering, which is the "one fixed blobatar" case.

**Containment survives configuration, and the tests had to be extended to say
so.** 6000 seeds sample the interior of the space densely and its corners barely
at all, because hashing spreads values out. A caller writing an override map goes
straight to the corners — "biggest eyes, widest gap, roundest body" is what
everyone tries first. `geometry.test.ts` now sweeps all-extremes, every
single-key extreme against them, and a deterministic scatter, through the same
invariants. It passed without a single change to `styles/blob.ts`: `fit` and the
lean bound were already doing the work.

The flip side is that an extreme value can land short of where it was asked for,
because `fit` scales the eye cluster as a unit. `_layout()` reports what actually
resolved, which is what an editor needs so a clamped slider does not read as
broken.

**Overrides are clamped, not trusted.** `t.pick` and `t.int` index and floor, so
exactly `1` selects one past the end of an array and one past `max`. That value
looks entirely reasonable to whoever typed it, and it is the top of every slider.
NaN falls to `0` through the same comparison, so a bad parse renders a blobatar
rather than a path full of `NaN`.

**Trait keys become public surface, and so do the ranges.** The keys were already
append-only-stable. The ranges were not previously load-bearing in public: a
stated position is relative to them, so retuning `t.num("eye.gap", 0.1, 0.24)`
now moves configured blobatars as well as seeded ones. Both were already frozen per
major; this makes the second one explicit and harder to forget.

**`shape` is still derived.** There is no `shape` option and CONTEXT.md's rule
stands — a caller overrides the `shape` *trait*, and the same thresholds in
`shapeOf` turn it into a silhouette. The six band midpoints an editor's shape
buttons would write are pinned in `traits.test.ts`, so retuning the bands fails a
test rather than silently moving everyone's saved config.

**`hue` and `tone` now overlap with the traits behind them.** They state the same
two values in friendlier units — degrees, and a swatch position — and they win.
Two ways to say one thing is a wart; removing them would be a breaking change for
a much more commonly used option than this one.

**Both variants get it for free**, including `character`, whose trait keyspace is
disjoint and whose features come through `t.pick` — the reader an unclamped
override breaks first.

**Macro controls belong in the editor, not here.** Nobody wants eight sliders for
`body.r0`–`body.r7`; they want one "lumpiness" amplitude that writes all eight.
That mapping is opinionated and will be retuned often, which is exactly why it
does not belong in a library whose ranges are frozen per major. The encoding is
complete and dumb; the editor is curated and small.
