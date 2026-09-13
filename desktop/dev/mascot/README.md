# Wuu mascot

Run `npm run lab:mascot` from `desktop` to check the production mascot at hero,
avatar and process-row sizes. Change state, provider, model and visibility while
watching the same instance. Use the browser's reduced-motion emulation as well.

Open `/collaboration.html` for the Agent motion preview. It contains 16 procedural
morph studies plus the liquid and resting character states. Switch directly
between shapes, pause, play the sequence, or restore the character. Compare the
selected motion at 24, 32, and 48 pixels and across three configured identities.
`MorphAvatar` delegates to the production `WuuMascot`; preview and product share
one contour renderer. Hidden/offscreen avatars suspend painting, and reduced
motion shows a still representative pose.

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
<WuuMascot identityHue={202} accessory="leaf" activity="thinking" visible={busy} />
```

The collaboration preview also mounts the real AgentAvatarMark and
ProcessSurfaceMascot side by side. Its product-state selector follows the same
activity mapping as Collaboration and Harness. `useMascotMorph` owns the shared
procedural artwork, transitions, reduced-motion handling and visibility gating.
Unknown progress uses a rotating arc; it does not imply a completion percentage.
The additional receive/upload/bounce variants remain available to preview until
a product surface supplies the corresponding event.
