# Wuu mascot

Run `npm run lab:mascot` from `desktop` to check the production mascot at hero,
avatar and process-row sizes. Change state, provider, model and visibility while
watching the same instance. Use the browser's reduced-motion emulation as well.

Open `/?study=accessories` for the accessory study: body comparison, curated colour
pairs, five body shapes, pointer following, camera angles and actual display sizes.
Run `node_modules/.bin/electron dev/mascot/capture-accessories.cjs` for optional rendered
checks and screenshots in `artifacts/mascot-accessories`. If the default port is busy,
use `npm run lab:mascot -- --port 5189 --strictPort` and set
`MASCOT_PREVIEW_URL=http://127.0.0.1:5189` for the capture command.

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

`AgentAvatarMark` uses the same face and state transitions with a configured
body shape, accessory and hue. An uploaded portrait is a personal identity, not
the Wuu character.

Onboarding owns its capability equipment and companion arrangement. Pass SVG
artwork through `equipment`: author it in a 100×100 frame around a body centred
at (50,50), radius 40. The mascot fits it to the actual body and mounts it above
the face in the same moving SVG group. Do not overlay another SVG or calculate
Blobatar geometry in a page. Equipment can cover the face, so keep it around the
rim and check it at the actual display sizes.

```tsx
<WuuMascot identityHue={202} accessory="leaf" activity="thinking" visible={busy} />
```

`useMascotMorph` owns the shared procedural artwork, transitions, reduced-motion
handling and visibility gating. Hidden/offscreen marks suspend painting, and
reduced motion shows a still representative pose. Unknown progress uses a
rotating arc; it does not imply a completion percentage. The receive/upload/bounce
variants have no product event yet.
