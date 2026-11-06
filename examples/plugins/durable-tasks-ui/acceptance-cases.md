# Remote task view acceptance cases

Enumerated before implementation:

- Local/disabled routes must fail before opening a connection. The view must never start an agent or install a runtime.
- A response for a different session, malformed version, or stale connection must never render.
- Refresh is replace-all using the host revision. Reconnect reloads authoritative state; no event replay or locally invented cursor.
- Disconnect preserves the last snapshot visibly stale and disables decisions. Reattach refreshes before enabling them.
- Metadata-only rendering must never permit blind approve/deny. In the decision transport model, double clicks send one decision. A transport failure has unknown outcome; inspect authoritative state before offering any retry.
- Approved, denied, expired, fenced, and non-approval waits cannot be approved. Expiry is host authoritative.
- Unmount stops refresh and detaches this view only; late responses cannot update it.
- Empty, load failure/retry, long identifiers, task failure, absent usage and mixed wait states remain readable.
- Unrecognized fields, prompts, arguments, outputs and errors are not rendered. Cross-session records fail closed.
- Render light/dark, 15/22px, 1080/360px with the real module and synthetic transport. Check overflow, focus, button states and screenshots. This does not prove remote authentication.
