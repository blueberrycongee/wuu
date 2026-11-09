# Tool Card Skin

Tool Card Skin wraps command tool activity through `registerUIContribution` in
both the default conversation and side-conversation preview. It reads the
immutable public `ToolActivitySnapshot` from `context.snapshot` and returns
`children` for calls outside `command.bash`.

The native content remains mounted inside the card, preserving output, actions,
streaming, and errors. Disable the plugin to remove its contributions and CSS.
It can compose with other wrappers targeting the same activity; `order` controls
nesting. See the [desktop plugin contract](../../../docs/en/customize/desktop-plugins.md).
