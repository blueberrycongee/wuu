---
name: cua-mac
description: Observe and control native macOS apps through Accessibility, ScreenCaptureKit, and native input. Use when a task requires interacting with a Mac app that has no safer CLI, API, or dedicated connector.
---

# Computer Use for Mac

Use the `computer` tool from the `cua-mac` plugin for native macOS app interaction.

## Control model

- Prefer a dedicated API, connector, or CLI when it can complete the task.
- Start with `list_apps` when the target app is unclear, otherwise call `observe` with its display name or bundle identifier.
- `observe` returns a `Target app="..."` header. Copy that exact canonical value into `app` on every later call; do not replace it with the window title, localized display name, or a guessed English name.
- Include `app` on every observe, click, drag, key, typing, scroll, value, selection, action, and wait call. The target is not inherited from the previous call.
- Prefer fresh `element_id` values from the latest Accessibility snapshot.
- Use semantic element actions before coordinate input. Prefer `activate_control` with an exact accessibility `description` or `title` for named buttons and menu items; this avoids copying numeric element IDs incorrectly. Add `role` when the label is not unique. For multiple ordered actions, prefer one `sequence` call so the runtime preserves order, checks control between steps, settles UI changes, and reports partial completion. Every step must declare `risk` as `safe`, `external_side_effect`, or `destructive`; non-safe steps require `confirmed=true` only after the user has actually confirmed. Use `press_keys` for a simple ordered key string. Never issue ordered UI actions as parallel tool calls.
- Inside a sequence, put the selector directly on each step, for example `{"action":"activate_control","description":"4","risk":"safe"}`. Do not rename `description` to `label`, `name`, or `control`.
- Input actions mean only that the requested event was delivered. They do not prove that the application accepted it or reached the intended state. Call `observe` after an action when its outcome matters, when you need a fresh screenshot, or when you need new element IDs. Do not infer success from the absence of an error.
- Reuse element IDs only within the state established by the latest successful `observe`. If an app restarts, an action fails, or the visible layout changes unexpectedly, observe again before choosing the next action.
- Prefer `element_id` or a known keyboard control. For visually located targets, use `coordinate_space="normalized"` on a 0-1000 grid (`x/visible_width*1000`, `y/visible_height*1000`); this remains valid if a provider resizes or compresses the image while preserving its aspect ratio. Use `coordinate_space="screenshot"` only for pixels known to come from the original screenshot dimensions, or `coordinate_space="screen"` for global values copied from an AX `frame=(x,y,w,h)`. The tool handles Retina and window mapping. Never mix coordinate spaces or infer a display scale.
- Treat AX and pixels as separate evidence. An AX tree can remain unchanged while a weak-AX app visibly changes. After a coordinate action, use a fresh `observe` and compare the new screenshot; do not use `wait_for_change` when the expected change may be visual-only.
- If a coordinate click produces no visible result, do not repeatedly click guessed nearby points. Refresh once, re-read the screenshot dimensions and visible target, then choose a materially different semantic, keyboard, menu, or corrected-coordinate path. Stop and report the unverified step if no such path is grounded in fresh evidence.
- For `scroll`, provide `x`, `y`, and `coordinate_space` when the window has more than one scrollable region; the point must sit inside the intended list or document. If the fresh screenshot does not move after one retry, do not repeat the same scroll at nearby guessed points. Try one grounded alternative such as an element-targeted scroll or a known PageDown/arrow control, then stop or request a foreground attempt only when that visible takeover is justified.
- The runtime chooses the lowest-disruption control level for you; do not try to force it. Level 1 is background Accessibility (AXPress, set_value, selections, secondary actions). Level 2 is background directed input — keyboard and mouse events delivered straight to the target process with no activation, so under the default policy typing, clicking, and scrolling do not steal focus or move the real pointer. Level 3 is a visible foreground takeover, used only when you set `foreground_policy` to `allow` or `require`.
- Leave `foreground_policy` omitted (or set it to `avoid`) by default. Levels 1 and 2 keep the user's active app frontmost and the real pointer still, and never return `requires_foreground` for an ordinary background action.
- After observing the post-action state, first try a different semantic, keyboard, or corrected-coordinate path when the intended change did not occur. Escalate to `foreground_policy="allow"` only when the task truly cannot be completed in the background, since that repeats the action as a visible foreground takeover.
- To run a task the user should never see, call `conceal_app` right after the target app has a window (observe first, or launch then observe). It parks the app's windows off-screen: the user sees nothing on any display, while Accessibility control, observation, and the live picture-in-picture keep working. Keep operating with background AX actions as usual. Call `reveal_app` to restore the original window positions when finished, or before any action that legitimately needs the foreground. A foreground action (`foreground_policy` `allow`/`require`) reveals concealed windows automatically. Concealment cannot remove the app's Dock icon, Cmd-Tab entry, or Mission Control presence — those follow the target app and are outside the tool's control; if a window refuses the off-screen position the tool reports it stayed visible.
- Set `foreground_policy="allow"` only when the task cannot be completed in the background and a temporary foreground takeover is acceptable; the action then runs at level 3 (the app is activated and global input is posted). Use `require` only when the user explicitly asked to show or take over the target app — it brings the window forward before acting. Because `allow`/`require` re-run the action in the foreground rather than reusing the background attempt, do not switch to them speculatively for an action that may already have taken effect. After any `foreground_native` action, observe again because focus and layout may have changed.

## Observation and execution evidence

Wuu supplies the current control generation and defaults observation to `mode="ax"` for text-only BYOK models. Multimodal models can choose `ax`, `vision`, or `both`. This contract is identical in Direct and Code Mode; use the current nested tool catalog in Code Mode.

`observe` returns `snapshot_id` and a bounded page of AX text. Use `query_snapshot` with that ID plus `offset` or `query` to retrieve more of the stored tree without changing element IDs. If traversal was truncated, observe a `root_element_id` from that snapshot to expand a subtree. A new observation replaces the old snapshot.

Pass `snapshot_id` whenever using `element_id`, `root_element_id`, or coordinates. Input consumes those references. A stale snapshot, changed process, changed window geometry, or released control requires a new observation. Keyboard sequences and fresh exact-label selectors can continue without carrying consumed IDs. You choose when to observe; `after="ax"`, `"vision"`, or `"both"` can attach a new observation to an input result. In a `sequence`, Wuu supplies the latest observation's snapshot to subsequent steps unless a step explicitly supplies one.

Delivery and verification are separate. `delivery="delivered"` means input was sent, not that the task succeeded. Optional `expect` checks exact AX role/title/description/value and `exists` within `timeout`; `wait_for` checks the same condition without input. `verification="matched"` confirms only that stated condition. `timed_out`, `ambiguous`, or `unavailable` do not prove the action had no effect. For visual-only outcomes, inspect a new screenshot.

Cancellation can return `delivery="unknown"` with `input_units_attempted`; an interrupted action can have partial effects. Never automatically replay it. A cancelled post-action observation can still accompany delivered input. After takeover is released, observe again before continuing. Closing the PiP dismisses the preview; stopping control cancels the activity.

## Permissions

Do not call `permission_status` proactively. Start with the requested observation or action. If the tool reports missing Accessibility or Screen Recording access, explain exactly which macOS permission is missing and use the returned Settings URL. Do not claim an action succeeded when permission was denied.

## Risk

Ordinary observation and reversible edits do not need extra confirmation. Confirm immediately before an irreversible or externally consequential action such as deleting data, sending a message, publishing, changing security settings, or completing a payment.
