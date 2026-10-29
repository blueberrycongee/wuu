# Token Speed example

This independently installable desktop plugin shows the current conversation's estimated
output speed in the expanded right tool rail. It demonstrates a public UI contribution, host
events, React supplied by Wuu, localized labels, and cleanup. It is an example, not a
bundled or automatically enabled plugin.

Install the directory or publish it as a development plugin:

```sh
wuu plugin install ./examples/plugins/token-speed
# Or, for local development:
wuu plugin dev ./examples/plugins/token-speed
```

The host must include the `wuu.default-preview/workspace.rail` slot documented in
[Desktop UI plugins](../../../docs/en/customize/desktop-plugins.md); older builds without
that slot cannot display this example. No build dependencies or runtime service are needed.
`npm run build` checks the JavaScript syntax.

The readout estimates tokens per second from observable streamed text, reasoning, and
tool-call arguments over a two-second window. It excludes tool results and may miss output
that a model provider does not stream. It estimates token counts from character lengths;
the result is neither an exact tokenizer count nor billed usage. It shows `—` until the
current turn has observable output, `0` after two seconds without output during an active
turn, and `—` again when the turn ends. Hover for a description; its accessible label
also retains the full estimate.

Digits and units occupy separate lines within a toolbar control's width. Values use
compact `k/s` and `M/s` units, with fewer digits in narrow controls. Changes ease over
320 ms and respect the system's reduced-motion setting. Measurements update independently
of animation frames. The rail disappears when collapsed or when its preview provider is
disabled.

The plugin reads active-tab and turn state through `application.composerPresentation`,
then observes live host deltas. It ignores replayed notifications, retains at most 24
turns with 50 ms sample buckets, and stops its refresh timer once output goes stale.
It uses Wuu's React instance and public contribution APIs without host DOM queries.

Samples are kept only in memory, keyed by thread and turn. The plugin clears them on
connection reset, server exit, or deactivation; it does not write samples to storage or
send them elsewhere.
