# Expressions compose into existing elements, not new ones

Every `transform` slot in the motion layer was already claimed — `.mo-breathe`
by breathe, `.mo-bob` by bob, `.mo-eye` by blink, `.mo-eyes` by the saccade — so
an expression pose had to either get its own wrapper `<g>` elements or share.
It shares, using the **individual transform properties** (`scale`, `translate`,
`rotate`) that were free on each of those elements, plus small additions to two
existing keyframe sets. No new DOM nodes.

The trade is bytes against bytes, and the two are not paid at the same rate:
markup is paid **per blobatar**, this stylesheet **once per app**. A 400-blobatar
grid pays for two extra `<g>` elements four hundred times; it pays for the
uglier keyframes once. That is the same argument the wrap layer already used to
justify 180 bytes of written-out chains in `motion.css`.

## Consequences

**The composition operator differs per channel, and getting it wrong is
silent.** Scales multiply into the existing keyframes, offsets and rotations
add. This is what makes the hover gate transparent to the pose: at
`--mo-amp: 0` — every unhovered blobatar in a grid — the idle keyframes collapse
to identity and the pose survives untouched, because that is what multiplying by
1 and adding 0 do. Multiply an _offset_ by `--mo-amp` by mistake and that
channel silently vanishes for the majority of blobatars on the page, in the one
state hardest to notice while developing.

`prefers-reduced-motion` has to restate the pose. The block resets `animation`,
`transform`, `translate`, `rotate` and `scale` wholesale, and most of the pose
lives in exactly those. The restatement is var references rather than duplicated
constants, so there is still only one definition — but it is a second _site_,
and a new channel must be added there too or it will be missing for
reduced-motion users only.

The stylesheet is harder to read than a set of per-expression rules would be.
That is accepted; `motion.css` is already a file where the comments carry the
reasoning and the declarations are dense.

## The rule held under pressure

Three channels added later each looked like they needed a node or a class, and
none of them got one:

- **Per-eye asymmetry** looked like it needed per-eye markup, which is not merely
  expensive here but *forbidden* — nothing in `parts.inner` may vary with the
  expression. It rides on `--mo-wrap`, a per-eye constant that was already in the
  markup for the wrap layer, mapped from ±1 to 0/1 so it can select which eye a
  differential lands on.
- **The tremor** took `.mo-root`'s free `translate`, the last unclaimed
  individual transform property in the tree. It does cost one more always-running
  animation per animated blobatar — the first real addition to that count — which
  is the price of a held state having no start and no replay.
- **Colour** needed a selector for the body group and did not get a class. The
  group is addressed as `.mo-bob > g:not(.mo-eyes)`, because a class costs bytes
  in every animated blobatar's markup *and* in the core renderer that decides
  whether to emit it — which every static consumer pays for too. `.mo-bob` is
  emitted by one style and has exactly two children, so this is not a guess about
  the DOM shape; it is the whole of it.

The trade in the header is the reason each of those went the way it did, and it
is worth re-reading before adding the first node.
