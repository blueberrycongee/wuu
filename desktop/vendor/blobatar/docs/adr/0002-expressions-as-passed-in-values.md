# Expressions are passed-in values, applied as registered custom properties

Adding triggerable expressions (`happy`, `sad`, `mad`) to the `blob` variant, we
made each one a **value imported from `blobatar/expression`** rather than a
string naming an entry in a table the core owns:

```ts
import { blobatar } from "blobatar/blob";
import { happy } from "blobatar/expression";

blobatar(seed, { expression: happy });
```

Each expression is a plain object carrying its pose plus the two functions that
apply it, so `blobatar()` calls through what it is handed and imports nothing. A
consumer who imports no expression carries no pose code; each one imported costs
what it actually weighs.

Each pose is defined **once** and serialized two ways: as registered CSS custom
properties for the animated path, and baked into geometry for the static one.
There are no per-expression `@keyframes` and no `.mo-happy` selectors —
`motion.css` is generic over the poses and never learns which expression is on.
The morph is not implemented anywhere: each channel is registered via `@property`
as `<number>`, so a change interpolates, and the transition between any two
expressions is what a transition on those numbers does.

## Considered options

**A string option on `blobatar()`** — `expression: "happy"` — was built first and
measured at **+420 B gz in the core**, paid by every consumer including those
who never set it. `expression` has to work without `animate` (or it could not
exist in the string API, and `prefers-reduced-motion` would have no pose to fall
back to), so it could not hide behind the passed-in indirection that already
keeps `animate.ts` out of static bundles. Rejected on that number: the core
budget exists precisely to stop a feature most callers never use from landing in
everyone's bundle.

**Per-expression entry points** (`blobatar/expression/happy`) would give the
same granularity through the exports map, at the cost of three more public paths
to keep stable and shared machinery that still has to live somewhere.

**Per-expression `@keyframes`** is the obvious CSS shape and defines each pose
twice — once in CSS for the animated path, once in TypeScript for the static
one. Two definitions of `happy` drift within a release and nothing catches it:
both render, just differently.

## Consequences

Measured: 3733 B with no expressions, +308 B for the first (the shared
serializer and bake), **+52 B for each cool one after** — and +699 B for `mad`,
which is the only one that tints. The `blob + happy` row in `scripts/size.ts`
exists to catch this regressing; if it ever converges with `blob only`, the
indirection has been lost.

That 699 is the ADR working, not failing. The whole point of expressions being
values is that a feature only one of them uses is a cost only its importers pay,
and the colour code is the first thing large enough to make that visible.

The exports are plain object literals over shared function references, **not
calls to a `make()` factory**. A top-level function call is not provably
side-effect-free, so a factory would keep every expression alive whether or not
it was imported — the same trap already documented on `_parts` in `blobatar.ts`.
Anyone tidying this file into a factory will silently undo the whole ADR.

The custom property names (`--mo-esx`, `--mo-esy`, `--mo-tilt`, `--mo-edx`,
`--mo-edy`, `--mo-esx2`, `--mo-esy2`, `--mo-tilt2`, `--mo-shake`, `--mo-bdy`,
plus `--mo-head`/`--mo-eye` for the tint) are **public surface**. A consumer can
set them directly for a pose outside the roster, and renaming them is breaking.
That is the price of a stylesheet generic over poses.

The list is not the one this ADR was written against: `--mo-bsx`, `--mo-bsy` and
`--mo-skew` were removed in the exaggeration pass — see §2.1 of the expression
spec — which is a breaking change to that surface and was taken pre-1.0
deliberately.

Adding an expression is a TypeScript-only change: one object literal. Adding a
_channel_ is the expensive direction — a registration, an entry in each of the
three transition lists, a composition site in the stylesheet, a line in the
reduced-motion restatement, a line in the bake, and a case in
`scripts/probe-compose.ts`. Skip the last and a channel that only one of the two
rendering paths applies will look completely fine in `bun test`.

`heat` is the exception that proves the shape: it is a pose channel with **no**
custom property, because colour is resolved in TypeScript and reaches the
stylesheet already finished. It rides on a third serializer, `tint`, alongside
`vars` and `bake` — and it is the reason a consumer who imports `mad` pays 699 B
where one who imports `sad` pays 52.

Variants opt in with an `expressive` marker (`styles/blob.ts`) rather than the
renderer sniffing the layout. `character` has no motion layer, and its `eyes` are
a different shape entirely, so a pose applied there would corrupt the face rather
than quietly do nothing.
