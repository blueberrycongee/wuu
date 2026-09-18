# Nested border-radius audit (desktop)

Design rule (from `desktop/src/renderer/styles/base.css`):
concentric nesting → `outer ≈ inner + padding`. Same token on outer+inner with non-zero inset is usually wrong.

Token scale: xs=8 (inner), sm=12 (control), md/lg=22 (panel), overlay≈md.

**Status:** CSS applied (2026-09-13) on `design/nested-radius-system`.

## Already correct
- Sidebar notification hold ring: `calc(var(--radius-sm) ± 2px)` for inset/outset rings.
- Token comments already name “parent-minus-padding concentric calcs”.
- Collaboration new menu: shell `--radius-sm` + pad 4 + item `--radius-xs`.
- Channel recipient picker: shell `--radius-sm` + pad 3 + button `--radius-xs`.
- Nested `.rich-code` inside `.rich-code-block` forces `border-radius: 0`.

## Applied (this change)

1. **Channel + user bubbles vs code blocks (P0)**
   - `.channel-message-bubble`: hardcoded `18px` → `var(--radius-md)` (align with harness).
   - Nested `.user-message .rich-code-block` and `.channel-message-bubble .rich-code-block`:
     `var(--radius-sm)` so the 22px card token no longer matches the padded bubble.

2. **Fork dialog (P0)**
   - `.fork-dialog-option`: `--radius-md` → `--radius-sm` (shell overlay 22 + pad 10).

3. **Sidebar name dialog (P0)**
   - `.sidebar-name-dialog-input`: `--radius-md` → `--radius-sm` (shell 22 + pad 18).

4. **Composer workspace bar (P1)**
   - `.composer-workspace-bar` top radii: hardcoded `18px` →
     `calc(var(--radius-md) - 4px)` (concentric with the `--radius-md` frame).

5. **Conversation actions menu (P1)**
   - `.conversation-actions-menu > button`: `--radius-sm` → `--radius-xs`
     so hover rows nest under the `--menu-radius` shell (matches other overlay menus).

6. **Channel agent hover card (P1)**
   - `.channel-agent-hover-card`: hardcoded `12px` → `calc(var(--radius-xs) + 10px)`.
   - Action buttons: hardcoded `7px` → `var(--radius-xs)` (8 + 10 pad ≈ outer).

## Still open (not in this CSS pass)

- Ask-user `.user-question-card` (22 / pad 16 / inner 8–12) — slightly short outer.
- `.channel-agent-proposal` (22 / pad 18 / inner xs).
- `.run-debug-panel` nested rows (22 / ~14 / sm).
- Systemic overlay menus that already use `--radius-xs` rows under `--menu-radius`
  shells (22 / pad 4–6 / 8): outer > inner, not fully concentric. Left as the
  existing menu recipe rather than a wide visual rewrite.
- Collaboration rail 14/9 hardcodes, mobile session sheet 20px, camera sheet
  40px tops, other scattered hardcodes.

## Recommended later
- Add helpers: `--radius-nested: calc(var(--radius-md) - var(--pad));` or per-component `calc(parent - pad)`.
- One shared overlay-menu recipe if we decide to move rows toward `calc(var(--menu-radius) - pad)`.

## Follow-up (2026-09-13)
- Compact floating menus (select / codex-runtime / composer context & project / mention / conversation-actions): shell uses `--menu-shell-radius` = `calc(--radius-xs + --menu-inset)` instead of panel `--menu-radius` (22), so row hover (xs) nests concentrically under ~6px pad.

## Follow-up (2026-09-15)
- TODO hover card: use the compact shell recipe (`--menu-shell-radius`, default 14px, with `--menu-inset`, default 6px) instead of the 22px panel radius. Task rows use `--radius-xs` (8px); the current task gets a theme-aware highlight. Header and row content share an 8px horizontal inset, and long task text/explanations wrap within the scrollable card.
