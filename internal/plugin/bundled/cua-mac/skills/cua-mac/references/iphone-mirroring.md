# iPhone Mirroring

Use this workflow for Apple's iPhone Mirroring app (`com.apple.ScreenContinuity`).
It controls the phone through a Mac window; a successful Mac input delivery does
not establish that the phone received a touch.

## Establish the connected screen

Observe the app with `mode="both"` when the model can read images; use `mode="ax"`
for a text-only model. Keep the returned canonical app target. If the window
shows setup, authentication, a disconnected phone, or a paused connection,
resolve that state before choosing phone coordinates. Ask the user to complete
required device authentication or connection steps. The iPhone must be nearby
and locked; unlocking it stops mirroring. Do not change authentication settings
as an input workaround.

Distinguish Mac window controls (close, retry, toolbar and menus) from controls
inside the phone screen. Seeing those Mac controls in AX does not establish
that phone content is exposed. Inspect available phone elements and their actions;
expand a truncated subtree before concluding that content is absent. VoiceOver
support exists on macOS 15.1 / iOS 18.1 and later, but does not guarantee that each
phone control is exposed to this tool. Do not turn VoiceOver on speculatively.

## Choose and verify input

1. Prefer a phone element's exposed action. For navigation, the Mac app supports
   `command+1` (Home), `command+2` (App Switcher), and `command+3` (Spotlight).
   Observe after navigation before choosing a new phone target.
2. When phone elements are unavailable, use the current screenshot and normalized
   coordinates inside the phone content. Use `click` for a tap, `click` with
   `duration` for a long press, and a targeted `scroll` for scrolling. A text-only
   model without usable phone elements must report that limitation instead of
   guessing coordinates from the Mac window frame.
3. After input, inspect the actual phone result with a fresh observation. An
   unchanged Mac AX tree, `delivery="delivered"`, or a quiet settling interval
   does not establish success or failure on the phone. Allow delayed remote
   updates to appear before deciding whether to retry.
4. If a background action visibly failed, check the target and use an exposed
   semantic action or known shortcut when available. If that cannot complete the
   task and visible control is acceptable, use the built-in foreground path:
   reveal a concealed app, observe again, then send the intended input with
   `foreground_policy="allow"`. Native input activates the Mac app and uses global
   input, which can move the user's pointer. Set the policy on each input that
   needs it; a successful foreground action does not change the default for later
   calls.
5. Verify the phone result again. If delivery remains uncertain, especially for
   sending, purchasing, or deleting, inspect the resulting state before any
   repeat. Stop if neither semantic nor foreground input produces a verified
   result. If the user requires background-only control, report the limitation
   instead of taking over the foreground.

Use `click` with an element and fresh `snapshot_id` (or fresh coordinates) for a
foreground click fallback. Check the returned `mechanism`: a frameless element can
still fall back to `background_ax`. If that action visibly failed and a native click
is needed, use fresh screenshot coordinates instead. `activate_control` and
`perform_action` use AX actions; they are not substitutes for a native pointer
event. For typing, first establish the phone field's focus and verify the
resulting text. Do not assume that a Mac clipboard paste reached the phone;
Universal Clipboard is a separate dependency.

Use the existing computer tool for these input paths. Do not replace an ineffective
background click with ad hoc `osascript`, System Events, or shell mouse scripts:
those do not establish a separate phone automation channel and bypass the tool's
snapshot, cancellation, and control ownership checks. Concealing the window does
not make an app accept background input.

## References

- [Apple: iPhone Mirroring requirements, navigation and limitations](https://support.apple.com/en-us/120421)
- [AppleVis: VoiceOver demonstration on macOS 15.1 / iOS 18.1](https://www.applevis.com/podcasts/demonstration-overview-iphone-mirroring)

These describe available interactions, not a guarantee of background synthetic
input support. Validate the current connected devices rather than assuming a
particular OS version accepts every input path.
