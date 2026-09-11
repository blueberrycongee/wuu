# Theme token reference

Wuu Desktop derives its appearance from a set of public CSS custom properties
(design tokens). Plugins can declare themes through `contributes.themes` that
override these tokens, or apply overrides at runtime with `registerThemeTokens`;
see [plugin themes](plugin-authoring.md).

This page is generated from
[`config/desktop-theme-contract.json`](../../../config/desktop-theme-contract.json)
and the host stylesheet dependency graph by
`scripts/generate-theme-surface-matrix.ts` (run
`make generate-theme-surface-matrix`); do not edit it by hand. Per-declaration
coverage detail lives in
[`config/desktop-theme-surface-matrix.json`](../../../config/desktop-theme-surface-matrix.json).

## Overriding tokens

An override is just setting the variable; the host keeps a default for every
token, so anything you do not override keeps the built-in appearance:

```css
:root {
  --wuu-color-canvas: #f6f6f4;
  --wuu-color-text: #1c1c1a;
}
```

## Token list

The contract defines **82 public tokens** (**7**
legacy compatibility aliases) and **16 syntax-highlighting
tokens**; **54** of them are currently wired into host surfaces.
"Wired" means host styles already reference the token and overrides take effect;
"Not wired (reserved)" means the token is declared but the host does not consume
it yet, so overrides do not change anything.

### Color

| token | Description | Host |
| --- | --- | --- |
| `--wuu-paper` | Legacy name; use `--wuu-color-canvas` instead | Wired |
| `--wuu-ink` | Legacy name; use `--wuu-color-text` instead | Not wired (reserved) |
| `--wuu-ink-soft` | Legacy name; use `--wuu-color-text-muted` instead | Wired |
| `--wuu-hairline` | Legacy name; use `--wuu-color-border-subtle` instead | Wired |
| `--wuu-surface-muted` | Legacy name; use `--wuu-color-surface-muted` instead | Not wired (reserved) |
| `--wuu-accent` | Legacy name; use `--wuu-color-accent` instead | Wired |
| `--wuu-accent-press` | Legacy name; use `--wuu-color-accent-pressed` instead | Not wired (reserved) |
| `--wuu-color-canvas` | Base background of the window and panel layers | Wired |
| `--wuu-color-surface` | Background of content surfaces such as messages and lists | Wired |
| `--wuu-color-surface-muted` | Muted surface background for hover states and sectioning | Wired |
| `--wuu-color-surface-elevated` | Elevated surface for inputs, popovers, and cards | Wired |
| `--wuu-color-overlay-backdrop` | Backdrop of modal overlays | Wired |
| `--wuu-color-text` | Primary text color | Wired |
| `--wuu-color-text-muted` | Secondary text color | Wired |
| `--wuu-color-live-highlight` | Highlight for live states such as streaming output | Wired |
| `--wuu-color-border-subtle` | Subtle borders | Wired |
| `--wuu-color-border-strong` | Strong borders | Wired |
| `--wuu-color-accent` | Accent color for primary actions and selection | Wired |
| `--wuu-color-accent-pressed` | Pressed state of the accent color | Not wired (reserved) |
| `--wuu-color-focus` | Focus ring | Wired |
| `--wuu-color-success` | Success semantic color | Wired |
| `--wuu-color-warning` | Warning semantic color | Wired |
| `--wuu-color-danger` | Danger semantic color | Wired |
| `--wuu-color-info` | Info semantic color | Wired |
| `--wuu-color-link` | Link text | Wired |
| `--wuu-color-link-hover` | Link hover state | Wired |
| `--wuu-color-on-accent` | Content color on accent surfaces (icons, text) | Wired |
| `--wuu-control-secondary-background` | Secondary control background | Wired |
| `--wuu-control-field-background` | Input field background | Wired |
| `--wuu-control-icon-background` | Icon button background | Wired |
| `--wuu-badge-neutral-background` | Neutral badge background | Wired |
| `--wuu-inline-code-background` | Inline code background | Wired |
| `--wuu-message-user-background` | User message bubble background | Wired |
| `--wuu-message-user-color` | User message text color | Wired |
| `--wuu-nav-item-hover-background` | Navigation item hover background | Wired |
| `--wuu-nav-item-hover-ring` | Navigation item hover ring | Wired |
| `--wuu-skill-mark-browser-a` | Browser skill mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-browser-b` | Browser skill mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-browser-c` | Browser skill mark gradient end color | Not wired (reserved) |
| `--wuu-skill-mark-commit-a` | Commit skill mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-commit-b` | Commit skill mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-commit-c` | Commit skill mark gradient end color | Not wired (reserved) |
| `--wuu-skill-mark-presentation-a` | Presentation skill mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-presentation-b` | Presentation skill mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-presentation-c` | Presentation skill mark gradient end color | Not wired (reserved) |
| `--wuu-skill-mark-creator-a` | Skill creator mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-creator-b` | Skill creator mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-creator-c` | Skill creator mark gradient end color | Not wired (reserved) |
| `--wuu-skill-mark-plugin-a` | Plugin mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-plugin-b` | Plugin mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-plugin-c` | Plugin mark gradient end color | Not wired (reserved) |
| `--wuu-skill-mark-default-a` | Default skill mark gradient start color | Not wired (reserved) |
| `--wuu-skill-mark-default-b` | Default skill mark gradient middle color | Not wired (reserved) |
| `--wuu-skill-mark-default-c` | Default skill mark gradient end color | Not wired (reserved) |

### Typography

| token | Description | Host |
| --- | --- | --- |
| `--wuu-font-family-ui` | UI font family | Not wired (reserved) |
| `--wuu-font-family-mono` | Monospace font family (code) | Not wired (reserved) |
| `--wuu-font-size-ui` | UI font size | Not wired (reserved) |
| `--wuu-font-size-body` | Font size for body text such as messages | Not wired (reserved) |
| `--wuu-line-height-body` | Body text line height | Not wired (reserved) |

### Spacing

| token | Description | Host |
| --- | --- | --- |
| `--wuu-space-unit` | Base spacing unit | Not wired (reserved) |
| `--wuu-message-actions-block-gap` | Gap between message action blocks | Not wired (reserved) |
| `--wuu-message-actions-overlay-gap` | Gap inside the message-actions overlay | Not wired (reserved) |
| `--wuu-message-actions-control-gap` | Gap between message action controls | Not wired (reserved) |
| `--wuu-message-actions-inline-offset` | Inline offset of message actions | Not wired (reserved) |
| `--wuu-message-action-size` | Message action button size | Not wired (reserved) |

### Density

| token | Description | Host |
| --- | --- | --- |
| `--wuu-space-density` | UI density scale | Not wired (reserved) |

### Radius

| token | Description | Host |
| --- | --- | --- |
| `--wuu-message-action-radius` | Message action button radius | Not wired (reserved) |
| `--wuu-message-user-radius` | User message bubble radius | Not wired (reserved) |
| `--wuu-radius-inner` | Inner highlight and nested radius | Not wired (reserved) |
| `--wuu-radius-control` | Control radius | Not wired (reserved) |
| `--wuu-radius-panel` | Panel radius | Not wired (reserved) |
| `--wuu-radius-overlay` | Overlay radius | Not wired (reserved) |

### Border

| token | Description | Host |
| --- | --- | --- |
| `--wuu-message-user-border` | User message bubble border | Wired |
| `--wuu-border-subtle` | Subtle border token for specific surfaces | Wired |
| `--wuu-border-strong` | Strong border token for specific surfaces | Wired |

### Elevation

| token | Description | Host |
| --- | --- | --- |
| `--wuu-message-user-shadow` | User message bubble shadow | Wired |
| `--wuu-elevation-panel` | Panel elevation shadow | Wired |
| `--wuu-elevation-overlay` | Overlay elevation shadow | Wired |

### Motion

| token | Description | Host |
| --- | --- | --- |
| `--wuu-motion-duration-fast` | Fast motion duration | Not wired (reserved) |
| `--wuu-motion-duration-normal` | Normal motion duration | Not wired (reserved) |
| `--wuu-motion-easing-standard` | Standard easing curve | Not wired (reserved) |

### Content

| token | Description | Host |
| --- | --- | --- |
| `--wuu-content-max-width` | Max width of the main content area | Not wired (reserved) |

## Syntax highlighting

Code highlighting colors use `--wuu-syntax-*`; `--hljs-*` are the early
compatible names and both families take effect.

| token | Description | Host |
| --- | --- | --- |
| `--hljs-keyword` | Keyword | Wired |
| `--hljs-function` | Function name | Wired |
| `--hljs-string` | String | Wired |
| `--hljs-number` | Number | Wired |
| `--hljs-comment` | Comment | Wired |
| `--hljs-tag` | HTML tag | Wired |
| `--hljs-literal` | Literal | Wired |
| `--hljs-meta` | Meta | Wired |
| `--wuu-syntax-keyword` | Keyword | Wired |
| `--wuu-syntax-function` | Function name | Wired |
| `--wuu-syntax-string` | String | Wired |
| `--wuu-syntax-number` | Number | Wired |
| `--wuu-syntax-comment` | Comment | Wired |
| `--wuu-syntax-tag` | HTML tag | Wired |
| `--wuu-syntax-literal` | Literal | Wired |
| `--wuu-syntax-meta` | Meta | Wired |
