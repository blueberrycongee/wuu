# Wuu brand guidelines

[Full manual](../../../brand/manual/index.html) (Chinese; open in a browser) · [Assets and build commands](../../../brand/README.md)

These guidelines contain brand assets and application proposals. Adoption in the desktop app, website and docs site needs separate review.

![Manual cover](../assets/brand/manual-cover.png)

## The ball

| Use | Colour | Eyes |
| --- | --- | --- |
| Brand symbol | Ink; paper on dark backgrounds | Fixed, looking up and right |
| Wuu in a conversation | Ink; paper in dark theme | Follow the activity state |
| Other agents, models and providers | One of seven agent colours | Ink; follow the activity state |

- Choose assets by displayed diameter: display above 40 px, small at 21–40 px, micro at 20 px and below
- Keep the eye position, tilt and proportions; do not centre them upright
- Use at most one brand ball per layout, except agent groups. Use product progress controls for downloads and saves
- Do not add a mouth, blush, limbs, gradients, highlights, shadows or outlines; keep product accessories off the brand symbol

![Ball construction](../assets/brand/manual-ball-construction.png)

## Wordmark and lockups

Use the supplied wordmark files; do not retype them in a font. Write **Wuu** in prose and `wuu` for the command.

X is the wordmark x-height; D is the ball diameter:

| Item | Specification |
| --- | --- |
| Horizontal lockup (default) | Ball 1.4 X, gap 0.34 X |
| Stacked lockup | Near-square formats |
| Clear space | 1 X around lockups; 0.25 D around the ball |
| Minimum lockup | 7 px x-height on screen; 14 mm wide in print |
| Minimum ball | 12 px on screen; 4 mm in print |

![Lockups](../assets/brand/manual-lockups.png)

## Colour

| Group | Use | Limits |
| --- | --- | --- |
| Ink `#141411`, paper `#F7F7F4` | Logo, Wuu avatar, primary buttons, body text | No other hues or gradients |
| Stone neutrals | Grounds, surfaces, lines, secondary text, selection | Keep essential text readable |
| Agent colours | Other agents' balls, illustrations, per-agent charts | No text, controls, status or Wuu |
| Status colours | Success, warning, danger, info | Include text or an icon; no decoration |
| Interaction colours | Ink primary button, 2 px blue focus ring | Do not colour every selection |

Neutral contrast ratios: body 17.2 : 1, secondary 8 : 1, tertiary 4.8 : 1, placeholder 3 : 1. Agent colours use OKLCH lightness 0.84; include names to distinguish them. On failure, keep the ball colour and use its eye pose, red text and an icon.

![Colour roles](../assets/brand/manual-colour-roles.png)

![Agent colours](../assets/brand/manual-agent-colours.png)

## Typography

| Typeface | Use | Licence |
| --- | --- | --- |
| Hanken Grotesk | Latin brand headings and body text | SIL OFL 1.1, bundled |
| Source Han Sans SC / Noto Sans CJK SC | Chinese | SIL OFL 1.1, install from official releases |
| Fragment Mono | Commands, paths, variable names; ligatures off | SIL OFL 1.1, bundled |
| System font | Product UI, following user settings | Operating system |

Add spaces between Chinese and Latin text and between numbers and units. Use full-width Chinese punctuation, monospace for commands and paths, and weight for emphasis.

![Typefaces](../assets/brand/manual-type-families.png)

## Motion

| Motion | Timing | Use |
| --- | --- | --- |
| Gaze change | 180 ms, ease-out | State changes |
| Blink | 60 + 40 + 90 ms, every 3.8–8.2 s | Rest, listening, waiting for confirmation |
| Settle | 280 ms, squash at most 6 % | Once per completed turn |
| Working drift | 1600 ms period | Thinking and working |

Fifteen activities map to eight poses. With reduced motion, switch directly to the final pose, keep state text, and stop blinks, settles and loops. [Reference implementation](../../../brand/assets/motion/wuu-ball.js)

![Motion states](../assets/brand/manual-motion-states.png)

## Applications

- Keep the approved app icon in `assets/app-icon-source.*`. Its gradient, 28° eyes and motion marks are not for logos, avatars or illustrations. Do not substitute the app icon for the logo
- Covers, heroes and social images may crop the ball on up to two edges. Keep both eyes visible and text off the ball

![App icon](../assets/brand/manual-app-icon.png)

![Product interface proposal](../assets/brand/manual-product-light.png)

![Website proposal](../assets/brand/manual-website.png)

## Maintenance and licensing

Edit values in `brand/tokens/tokens.json`. Run `npm --prefix brand run build` and `npm --prefix brand run render`, then inspect the output; do not edit exports. See [brand/README.md](../../../brand/README.md) for requirements. Product tokens remain governed by the [design system](design-system.md).

Brand assets ship under MIT. MIT grants no trademark rights; brand-use terms need maintainer confirmation.

Not yet verified: trademarks, user research, app icons below 32 px, print colours, Windows/macOS rendering, and agent colour-vision simulation.
