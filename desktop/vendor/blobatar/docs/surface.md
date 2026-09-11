# Curved face geometry

A perspective-enabled round mascot uses an ellipsoidal face. The eye chart is
specified in the familiar 100-unit viewBox. A front camera recovers that chart
exactly: changing to surface rendering does not require redrawing each expression.

The rendering order is:

1. Apply expression dimensions, local eye tilt, convergence, and blink to the chart.
2. Lift every eye-contour sample onto a unit sphere by camera-ray intersection.
3. Rotate the surface by yaw and pitch. `strength` scales these angles.
4. Project through a camera at distance 4, normalizing the limb to the body radii.
5. Hide samples behind the camera horizon and close partial eyes along the limb.

The eye contour is sampled from the existing cubic geometry, at 16 points per
quadrant. Its bend, foreshortening, and partial visibility follow the surface;
there is no minimum eye-width clamp or hand-authored perspective skew. `_layout`
returns projected centers and bounding half-extents; projected eyes also expose
`contour` and `path` for measurements that need the actual outline.

Round surface bodies use an ellipse silhouette. Their restrained light contours
come from the same sphere's normals under a fixed diffuse light, with no specular
highlight or cast shadow. Other silhouette families retain their authored bodies;
the ellipsoidal face model does not claim to reconstruct those bodies in 3D.

The React adapter reads interpolated numeric CSS properties and updates only the
existing eye paths. CSS retains timing, body motion, and palette transitions.
Activity and pointer attention both supply angles, and expressions and blinking
precede projection. Static SVG and live frames use the same geometry functions.
Animation reads are batched before writes. Offscreen or hidden instances stop
sampling; reduced motion applies the final pose without ambient animation.

Use `npm run lab:mascot` from `desktop`, then select **曲面造型** to compare the live
surface, static export, and 24–96 px previews while adjusting yaw, pitch, and pose.

Validation from this package:

- `bun test`: chart inversion, curvature, foreshortening, horizon clipping,
  expression-before-projection ordering, and stable animated markup.
- `bun run probe:surface`: Chrome checks for static/live agreement, intermediate
  morph geometry, node/portal retention, blink, pointer attention, and reduced
  motion. Set `CHROME` if Chrome is not in a standard location; set
  `SURFACE_SCREENSHOT` to write a PNG. The probe fails if no browser is available.

The existing `probe-compose.ts` currently references an absent
`scripts/probe/entry.tsx`; it is not a passing validation for this change.
