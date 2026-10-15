# Next-message suggestions

The optional **Next message** plugin predicts one message you might type after a successful live reply. It is installed but disabled by default. Enable it in **Settings → Extensions**, then open its settings page to choose a configured model alias.

The suggestion appears as gray text inside an empty composer, even when it is not focused. **Tab** inserts it into the draft when the composer has focus, without sending; you can edit it before sending. **Escape** dismisses it. Typing, changing conversations, starting another turn, or disabling the plugin cancels pending work and prevents stale results from entering the draft. Slash commands and menus retain keyboard priority. No suggestion leaves ordinary Tab navigation unchanged.

## Model, data and cost

A blank model alias resolves the workspace's current configured main model for each prediction, so later workspace model changes take effect automatically. It does not select a dedicated cheap model. A configured alias remains selected when the main model changes; an unavailable alias produces no suggestion and does not fall back silently.

Each eligible turn makes at most one attempt. The request allows up to 15 seconds and 8 KiB of UTF-8 input including the prediction prompt and JSON encoding. It asks for at most 128 output tokens where the provider supports that limit. Codex omits this limit, so its output and charges may be higher. The displayed suggestion is at most 100 Unicode characters, preferably one sentence and at most two short sentences. Oversized context is skipped rather than cut mid-message. Provider charges can still apply after cancellation. The plugin may return no suggestion even on success.

The configured provider receives recent completed visible user messages and final assistant answers, newest turn included, within the same 8 KiB request budget. Earlier complete turns are dropped when the budget is full; a too-large newest turn is skipped. For attachment-bearing source turns, only image/file counts are sent as hints, never attachment bytes, names, or paths. Unsent drafts, tool output and reasoning are excluded. The plugin creates no private session, uses no tools, and does not read or write a notebook. Only the model alias is persisted; source text is not saved by the plugin. Ordinary host/provider usage accounting still applies.

## When it runs

The plugin must observe a matching live start and successful completion while enabled. Restoring an old conversation or enabling/reloading the plugin after completion does not trigger prediction. Failed, interrupted, internal, generated and incomplete source turns are skipped. A source turn containing only attachments is skipped because it has no visible user text for prediction. The composer must have exactly empty text and no attachments, queued messages or pending input.

Registered workspaces, no-project conversations, split panes and side chats are supported. Side chats use their own history and identity, never their owner's main-thread history. Workspace root, conversation and source-turn identities must match. Input changes abort the original promise through the runtime/model service; losing or regaining focus does not restart a prediction. Recent identities are kept only in memory, so deduplication is scoped to a running plugin generation.

The plugin reads the host's visible conversation projection, not physical audit records. The bounded window can still miss older context, and suggestions can be wrong. Synthetic tests verify behavior and cancellation, not real-model suggestion quality.
