# Nested border-radius audit (desktop)

Design rule (from `desktop/src/renderer/styles/base.css`):
concentric nesting → `outer ≈ inner + padding`. Same token on outer+inner with non-zero inset is usually wrong.

Token scale: xs=8 (inner), sm=12 (control), md/lg=22 (panel), overlay≈md.

## Already correct
- Sidebar notification hold ring: `calc(var(--radius-sm) ± 2px)` for inset/outset rings.
- Token comments already name “parent-minus-padding concentric calcs”.

## Priority mismatches

1. **Composer shell vs workspace bar** (`composer.css`)
   - Outer `.composer-frame`: `--radius-md` (22px)
   - Sibling/overlap `.composer-workspace-bar`: hardcoded `18px 18px 0 0` with `margin: 0 14px -10px` and padding `4px 8px 12px`
   - Fails: hardcode bypasses scale; 18≠22−gap; looks like a second outer radius fighting the frame.

2. **Ask-user card** (`.user-question-card`)
   - Outer: `--radius-lg` (22px), padding 16–18px
   - Inner options: `--radius-xs` (8px) — OK-ish vs padding
   - Inner actions/buttons: `--radius-sm` (12px)
   - Concentric target for 16px pad ≈ 6px inner; sm=12 is too round for the pad (outer too tight / inner too soft).

3. **Composer frame vs header controls**
   - Frame 22px; expand/header chips often `--radius-sm` (12) at ~8px inset
   - Target ≈14px; 12 is close but not systematic (`calc(var(--radius-md) - 8px)` missing).

4. **Channel message bubbles** (`channels.css`)
   - `.channel-message-bubble`: hardcoded `18px` (not token)
   - Nested media/chips use 8/12/token mix and more hardcodes (7px, 8px, 12px)
   - Parallel to harness bubbles (`--radius-md` / speech-bubble mix) → two radius dialects.

5. **Collaboration rail contacts** (`sidebar.css`)
   - Row `14px` hardcode, unread pill `9px` hardcode — not on product scale; 14 vs 9 with small inset is ad hoc.

6. **Mobile session sheet** (`mobile-sidebar.css`)
   - Dialog `20px` hardcode + padding 16; inner controls 8/12 hardcodes — off-token and non-concentric.

7. **Camera sheet** (web): `40px 40px 0 0` hardcode top radii — huge vs inner controls; needs deliberate nested scale.

8. **Scattered hardcodes** (~40) in turns/settings/channels that skip `--radius-*` roles.

## Recommended fix direction (phase 2)
- Add helpers: `--radius-nested: calc(var(--radius-md) - var(--pad));` or per-component `calc(parent - pad)`.
- Replace hardcodes with tokens or explicit concentric calc.
- Unify channel bubble with harness message radius tokens.
