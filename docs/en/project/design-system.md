# Wuu design system

Wuu prioritizes sustained reading and frequent actions. Paper and ink tones form the foundation; shape and typography establish identity; color communicates status and necessary interaction cues. Establish hierarchy with position, space, type size, and weight before adding borders or shadows.

This specification and its visual boards maintain the desktop foundations for designers, developers, and coding agents. They are not a reskin proposal or a claim that every existing screen already conforms. Native phone interfaces share the information and interaction principles, not literal desktop pixel dimensions.

## Visual boards

The boards cover color, space/radius/elevation, and typography using Wuu's own variables, not the palette or fonts of external references. Each has light and dark high-resolution PNG versions, with bilingual labels in one shared asset set.

| Board | Light | Dark |
| --- | --- | --- |
| Color | [Full image](../assets/design-system/colour-light.png) | [Full image](../assets/design-system/colour-dark.png) |
| Space, radius, elevation | [Full image](../assets/design-system/geometry-light.png) | [Full image](../assets/design-system/geometry-dark.png) |
| Typography | [Full image](../assets/design-system/type-light.png) | [Full image](../assets/design-system/type-dark.png) |

![Wuu light colors: neutral surfaces, text, boundaries, status, and current interaction tokens](../assets/design-system/colour-light.png)

![Wuu spacing scale, radius roles, elevation, and control size floors](../assets/design-system/geometry-light.png)

![Wuu type roles, sizes, weights, and line heights](../assets/design-system/type-light.png)

Boards show built-in themes at standard density with current default font settings. Colors come from browser-computed values; translucent colors include their opacity and must be understood against their background. Type samples and spacing blocks are enlarged 2× for inspection; labels show actual CSS dimensions, not image pixels. The board's own headings, margins, and canvas are not product tokens.

## Specification and implementation

**Use roles rather than copying numbers from images.** A token is a named design value with a purpose: `--ink-soft`, for example, means secondary text. Components consume it through `var(...)`. Themes and preferences may change its resolved value without changing that purpose.

| Source | Responsibility |
| --- | --- |
| [base.css](../../../desktop/src/renderer/styles/base.css) | Color roles, light defaults, radius, elevation, weight, and motion |
| [theme.css](../../../desktop/src/renderer/styles/theme.css) | Dark overrides, including text, surfaces, and shadow recipes |
| [spacing.css](../../../desktop/src/renderer/styles/spacing.css) | Spacing roles, density boundaries, narrow-screen insets, and touch floors |
| [appearance.css](../../../desktop/src/renderer/styles/appearance.css) | Effective UI type ratios, independent code size, and icon sizes |
| [AppearancePreferences.ts](../../../desktop/src/renderer/AppearancePreferences.ts), [protocol](../../../packages/protocol/src/index.ts) | Font preferences, sizes, and runtime defaults |
| [Shared components and previews](desktop-ui.md) | Control behavior, keyboard interaction, real states, and acceptance entry points |

These are internal renderer roles, not all public API promises. Extensions use the [public theme contract](../customize/theme-surface-matrix.md), not arbitrary internal variables. Documentation explains purpose, code defines values, and boards are regenerable views of the code. When they disagree, inspect the actual cascade and preferences before updating all affected representations; do not just edit a label on an image.

## Color

### Identity and emphasis

Wuu's identity is monochrome-first, with paper, ink, and neutral surfaces rather than a mandatory signature hue. The black-and-white app icon, consistent shapes, typography, and space provide recognition. Neutral does not mean a flat gray interface: use readable tonal contrast and clear hierarchy, with light and dark themes expressing the same roles.

Brand identity, action emphasis, and status are separate concerns. Primary actions can use strong foreground/background contrast without a saturated brand fill. Links, keyboard focus, and selection need recognizable interaction cues; success, warning, and danger need distinct meanings. Color supports these purposes rather than decorating every heading or selected item. Never rely on color alone for an essential distinction.

### Color roles

| Role | Variables | Use |
| --- | --- | --- |
| Canvas and surfaces | `--paper`, `--surface-1` through `--surface-4` | Base, secondary, and emphasized surfaces; surface numbers are not shadow levels |
| Text | `--ink-strong`, `--ink`, `--ink-soft` | Important content, body, secondary descriptions; keep body text readable and normally weighted |
| Quieter text | `--ink-tertiary`, `--ink-muted`, `--ink-faint` | Select by purpose; low contrast must not conceal crowding or carry essential information |
| Three line tiers | `--hairline-soft`, `--hairline`, `--hairline-strong` | Within-surface separators, independent frames, stronger control boundaries; no local gray per line |
| Functional boundary and focus | `--control-boundary`, `--focus-ring` | Identify controls and keyboard focus, distinct from decorative separators |
| Selection | `--selection-surface` | A clear selection surface, not a borrowed warning or success color |
| Status | `--success`, `--warning`, `--danger`, `--info` | Success, caution, danger, information; combine with text or icons, never color alone |

Success, warning, and danger tints are `--success-soft`, `--warning-soft`, and `--danger-soft`. Status colors are not general-purpose body colors or automatically readable on arbitrary fills. Use appropriate content roles such as `--ink-on-accent` on colored surfaces, then check the actual pair. Dark mode adjusts surfaces, text, and status independently rather than inverting light colors.

### Current implementation accents

The renderer currently defines `--wuu-accent` and `--wuu-accent-press` with vermillion defaults, plus `--interaction-accent` for sliders. These describe existing implementation, not a requirement that Wuu's brand be red. The boards retain their computed values in a compact implementation reference, separate from the neutral foundation and status palette.

Existing accent uses include status indicators as well as controls. Review each use by purpose before changing it; do not globally replace red with gray or repurpose status tokens as brand colors. This specification does not change product styles or theme overrides.

## Typography

Use the system UI font with CJK fallback and respect user font choices. Code, editors, and diffs retain independent monospace typography rather than scaling mechanically with UI text. Avoid unrelated typefaces for headings, prose, and numbers; use tabular numbers or monospace where numerical alignment requires them.

The current runtime defaults are **14.5px UI** and **11px code**. Ratios in `appearance.css` use 14 as their denominator: 14px is the proportional reference, not the current startup default. Valid saved preferences remain authoritative and must not be reset to the documented defaults.

| Role | Variables | Relationship to UI size U | Value at U = 14.5px |
| --- | --- | --- | --- |
| Hero | `--font-hero` | round(U × 32 / 14) | 33px |
| Display | `--font-display` | round(U × 24 / 14) | 25px |
| Heading | `--font-heading` | round(U × 20 / 14) | 21px |
| Title | `--font-title` | round(U × 16 / 14) | 17px |
| UI and body | `--font-ui`, `--font-body`, `--font-content` | U | 14.5px |
| Menus and compact controls | `--font-menu`, `--font-sm` | round(U × 13 / 14) | 13px |
| Metadata | `--font-xs` | max(12px, round(U × 12 / 14)) | 12px |
| Code | `--appearance-code-size` | Independent preference | 11px |

Derived roles round to whole pixels. The saved UI size may be a half step, but fractional derived sizes would put line boxes and 1px edges between device pixels.

Use 400 for ordinary text, 500 for compact menu items, and 500 or 600 selectively for headings and emphasis. Reserve 700 for occasional strong emphasis. Base line-height roles are 1.35 for UI, 1.55 for body, and 1.6 for metadata; individual reading surfaces may have semantic overrides, not one forced line height for every component. Conversation reading and process leading are whole-pixel lengths derived from the message size (1.8 and 1.6, rounded to 2px), so stacked rows and cards share edges. Message headings step to 1.5, 1.25, and 1.1 times the body size, rounded to whole pixels, so hierarchy does not rest on weight alone. Preserve natural letter spacing instead of shrinking type or tightening tracking to repair layout.

## Spacing and layout

The standard unit is 4px, multiplied by density to form `--space-1`. The existing scale is 4, 8, 12, 16, 20, 24, 32px through `--space-1/2/3/4/5/6/8`. Prefer semantic roles; use scale steps only when local adjustment is genuinely needed.

| Relationship | Variables | Standard density |
| --- | --- | --- |
| Pane edge to first and last glyph | `--pane-inset` | 20px |
| Page inset | `--page-padding` | 32px |
| Between groups | `--section-gap` | 32px |
| Heading to content | `--section-heading-gap` | 16px |
| Card and panel insets | `--card-padding`, `--panel-padding` | 16px, 20px |
| Compact block/inline padding | `--compact-padding-block`, `--compact-padding-inline` | 8px, 12px |
| Control block/inline padding | `--control-padding-block`, `--control-padding-inline` | 4px, 12px |
| Menu inset and item gap | `--menu-inset`, `--menu-item-gap` | 6px, 8px |

At widths up to 560px, page, pane, and panel insets become 16px and card insets 12px, still affected by density. Shared fields and menu rows have a 32px floor, or 44px for coarse pointers, and round up to the 4px grid. They grow further with text and content; fixed heights must not clip text. These field and menu-row roles do not assign one size to every icon button.

### Alignment axes

Every edge that reads as aligned must come from one shared role, not from separately tuned values that happen to be close.

- **Pane inset.** In chrome rows such as sidebar rows, titlebars, and tab bars, the first and last visible glyph sits `--pane-inset` from the pane edge. Align ink, not button boxes: a control whose box is larger than its glyph subtracts its optical offset, such as `--control-toolbar-glyph-offset` for toolbar buttons.
- **Reading column.** Unframed content (paragraphs, headings, lists, process rows, answer actions) shares the column's leading edge. Message tables have no side frame, so their outer cell text also starts on that edge.
- **Framed content.** Code blocks, the composer, message bubbles, and cards place text and icon glyphs `--card-padding` from the outer edge; a frame's border is part of that measurement. A code block's language label, its code, and the composer's text therefore share one axis.
- **Accessory columns.** Sidebar trailing accessories of different sizes share one center axis. Headings reserve their accessory's footprint whether or not the section has one, so group spacing does not depend on which headings offer an action.

Align peer labels, icon columns, and trailing actions. Indentation expresses hierarchy, independent of running or unread status. Reserve the real footprint of indicators and actions plus a reading gap. Give each spacing relationship one owner rather than accumulating wrapper gaps, child margins, and invisible drag targets. Use appropriate reading-width roles such as `--content-column-width`, not one mandatory width for every page.

## Radius and elevation

| Role | Variables | Default |
| --- | --- | --- |
| Inner highlights, compact chips, media | `--radius-xs`, `--radius-media` | 8px |
| Controls and small cards | `--radius-sm` | 12px |
| Panels and composer surfaces | `--radius-md`, `--radius-lg` | 22px; lg aliases md |
| Compact menu shell | `--menu-shell-radius` | Inner radius + menu inset; 14px at standard density |
| Panel overlays and dialogs | `--menu-radius` | Follows panel by default; theme-overridable |
| Pills and circles | `--radius-pill`, `--radius-circle` | 999px, 50% |

Relate nested corners through their inset instead of assigning the same radius to every layer. Pills suit elongated controls; circles suit square avatars, dots, and knobs.

Choose elevation by purpose: `--shadow-soft` for light controls, `--shadow-card` for cards, `--shadow-composer` for the composer, `--shadow-pop` for popovers, and `--shadow-modal` for dialogs. Attached trays and edge drawers use directional roles. Light mode uses restrained ambient shadows. Dark mode defines surfaces with inset highlights while overlays retain black ambient shadows. Hover, focus, and expansion do not automatically increase elevation; ordinary content does not need shadows everywhere.

## Icons, motion, and controls

Controls reuse [WuuIcons](../../../desktop/src/renderer/WuuIcons.tsx), not a competing stroke style. Standard drawings use a 24-unit canvas and 1.75 stroke. Use `--icon-size-*` roles with bounded growth as UI text increases. Sizes round to even pixels so glyphs center in even control boxes; at the 14px reference and the 14.5px default they are 12, 14, 16, 18, and 20px. Still inspect apparent size, centering, and stroke density.

Motion provides feedback and explains position changes: `--motion-fast` 120ms for immediate feedback, `--motion-base` 180ms for menus and content swaps, `--motion-slow` 280ms for structural moves, and `--motion-slower` 440ms for larger folds. Entrances decelerate with `--ease-out` and exits accelerate away with `--ease-in`, shorter than the entrance. Prefer existing semantic aliases and the shared entrance and exit keyframes described in [desktop UI maintenance](desktop-ui.md#motion). The OS setting and the in-app Motion preference both reduce motion, with the same result. Frequent actions must not wait for decorative animation; preserve established component-specific motion contracts.

Reuse [SelectMenu](../../../desktop/src/renderer/SelectMenu.tsx), [SettingsRow](../../../desktop/src/renderer/SettingsRow.tsx), [Modal](../../../desktop/src/renderer/Modal.tsx), and their styles. Use existing shared button and field classes rather than private copies. Controls need accessible names and applicable hover, pressed, selected, disabled, keyboard-focus, and error states. Loading retains position and dimensions. Combine states only where meaningful; selection and pressing are different concepts.

Keyboard focus uses the shared 2px `--focus-ring` mechanism; pointer clicks do not add an extra frame. Menus support keyboard movement, selection, dismissal, and focus return; dialogs use shared focus management. Place errors near their fields with a recovery action or explanation. Icons do not replace necessary instructions.

## Acceptance and maintenance

Use contrast targets of at least 4.5:1 for ordinary text, 3:1 for large text, and 3:1 for necessary control boundaries and state indicators. Decorative separators are not functional boundaries. The boards show actual tokens, not proof that every pair meets these targets or that the whole product has passed accessibility certification.

After changing a component, follow [desktop UI maintenance](desktop-ui.md): check light/dark themes, default/large text, wide/narrow windows, long/empty content, scrolling, fixed regions, and relevant coexisting states. Inspect clipping, overlap, targets, and keyboard focus. Static boards explain foundations; they do not replace real component and interaction acceptance.

When foundation variables change, update both language versions and regenerate the boards. From the repository root:

```bash
node desktop/scripts/generate-design-system.cjs
make docs-policy-check
make build-docs
```

The [generator](../../../desktop/scripts/generate-design-system.cjs) uses installed desktop Electron and esbuild, loads the four foundation stylesheets, and applies runtime default sizes and fonts. A disposable profile avoids product accounts and personal preferences. Six generated PNGs live in `docs/en/assets/design-system/` and are shared by both languages; do not paint over their values. Generation requires a graphical environment. Font glyphs depend on the platform, so inspect regeneration differences on the same platform.

Before adding a component, look for existing roles and shared implementations. Add a variable only for a recurring, semantically different need. Review purpose, preferences, real states, and readability rather than pixel similarity to an external reference.
