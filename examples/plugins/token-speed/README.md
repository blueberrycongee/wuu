# Token Speed example

This independently installable desktop plugin shows the current conversation's estimated
output speed beside the runtime controls. It demonstrates a public UI contribution, host
events, React supplied by Wuu, localized labels, and cleanup. It is an example, not a
bundled or automatically enabled plugin.

Install the directory or publish it as a development plugin:

```sh
wuu plugin install ./examples/plugins/token-speed
# Or, for local development:
wuu plugin dev ./examples/plugins/token-speed
```

The host must include the `wuu.default-composer/composer.runtime` slot documented in
[Desktop UI plugins](../../../docs/en/customize/desktop-plugins.md); older builds without
that slot cannot display this example. No build dependencies or runtime service are needed.
`npm run build` checks the JavaScript syntax.

The readout estimates tokens per second from observable streamed text, reasoning, and
tool-call arguments over a two-second window. It excludes tool results and may miss output
that a model provider does not stream. It estimates token counts from character lengths;
the result is neither an exact tokenizer count nor billed usage. It shows `—` until the
current turn has observable output and `0` after two seconds without output. Hover or
focus the readout for a description. A small viewport can abbreviate the visible number;
the accessible label retains the full value. Older shells without CSS anchor positioning
use a stacked rail layout.

Samples are kept only in memory, keyed by thread and turn. The plugin clears them on
connection reset, server exit, or deactivation; it does not write samples to storage or
send them elsewhere.
