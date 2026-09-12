# Wuu mascot

Run `npm run lab:mascot` from `desktop` to check the production mascot at hero,
avatar and process-row sizes. Change state, provider, model and visibility while
watching the same instance. Use the browser's reduced-motion emulation as well.

Callers render `WuuMascot` and pass `activity`; the component owns expression,
gaze, props and state transitions. `visible` retains the character during exit,
so keep the component mounted while changing that prop. `ambient` enables
occasional idle glances; `followPointer` adds pointer attention. Both respect
reduced motion.

Provider colour and model accessories come from `WuuMascotRuntimeProvider` by
default. For an independent identity, pass `identityHue` and `accessory`.
`brand` pins the colour to the app icon and defaults to no model accessory;
an explicit `accessory` still takes precedence. Saved accessory IDs, artwork and fit
live in `WuuMascotAccessories.tsx`. Pages own placement and business labels;
they should not create another face, animation timer or expression table.

`WuuIconMascot` is the approved icon artwork. Keep its authored eye angle,
framing and animation separate from these activity poses.

Agent avatars use the same face and state transitions, with user-selected body
shapes and accessories. Uploaded portraits and participant fallback avatars are
personal identities, not the Wuu character.

Onboarding owns its capability equipment and companion arrangement. Pass SVG
artwork through `equipment`: author it in a 100×100 frame around a body centred
at (50,50), radius 40. The mascot fits it to the actual body and mounts it above
the face in the same moving SVG group. Do not overlay another SVG or calculate
Blobatar geometry in a page. Equipment can cover the face, so keep it around the
rim and check it at the actual display sizes.

```tsx
<WuuMascot identityHue={202} accessory="sprout" activity="thinking" visible={busy} />
```
