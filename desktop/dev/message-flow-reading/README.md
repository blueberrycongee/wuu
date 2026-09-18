# Message flow reading fixture

With the desktop development server running, open
`http://localhost:5173/dev/message-flow-reading/` (use its actual port if different).

This page imports production `RichContent`, `StreamingMarkdown`, the font-size
application helper, `TurnView`, `ThreadItemView` and the complete renderer stylesheet. Its own CSS styles only
the test controls and outer layout. It is a separate development entry, guarded
by `import.meta.env.DEV`, and is not included in the production build inputs.

Compare 14px and 20px at 1280px and 420px viewport widths, then check the 13px/20px range limits,
dark mode, ordinary Markdown, chat bubbles, outgoing user messages and the file preview.
Use the user-message surface to inspect link, inline code, quote and table contrast
on both themes. The edit surface mounts the production inline editor with a
synthetic attachment and no submission callback; it never sends a message. Use the cursor
toggle to compare live/settled geometry. “逐段播放” sends actual incremental updates
through the stream text store, including blank lines inside a loose list.

Inspect hanging list indents, multi-paragraph items, numbered lists starting at
9, task checkboxes, multi-paragraph quotes, inline paths, code and tables. Prose
and its headings should keep the same left edge; wide content may extend right.
No text should be clipped at larger font sizes, and completing the stream should
preserve the existing list/paragraph DOM and geometry.

The complete-conversation surface includes consecutive user messages and a
timestamp. The completion surface switches a real final-answer component between
live and settled states; its action row must not move the following message.
On touch devices actions remain visible, while keyboard focus reveals desktop
actions without changing their footprint.

The production input frame is wider than the reading column at every width.
Message text aligns with the textarea's text origin, not the frame edge. Both
consume the shared session text inset; narrow panes retain that relationship.

With the development server running, execute
`./node_modules/.bin/electron scripts/message-flow-layout-e2e.cjs` from `desktop/`.
Set `WUU_FIXTURE_ORIGIN` if using a different server address. This checks rendered
action targets, keyboard/touch discovery, queued-message spacing and completion
geometry across light/dark, 14/20px and 1280/420px. It writes measurements under
`out/message-flow-layout/`; it does not certify visual appearance.

After an Electron build, `scripts/session-composer-width-e2e.cjs` checks the
message/input text axis in the real app, including split/side threads, safe areas
and classic scrollbars. `scripts/composer-height-e2e.cjs` checks growing and capped
inputs, end-of-history clearance with attachments, and the floating jump control
while reading history. The completion fixture also accepts `notice=1` to verify
that a wrapping transport notice retains its actual footprint, while ordinary
completion adds no phantom notice space. These use synthetic data, not a live
app-server.

Run the composer-height check again with `WUU_HEIGHT_RUNNING_E2E=1` to cover
ongoing process output. Running and completed turns use the same bottom reading
clearance; neither relies on an extra turn-boundary margin at the end.

`?surface=lifecycle&lateTerminal=1` starts an unclassified multi-paragraph
stream and confirms it as the final answer when the stream toggle is cleared.
`scripts/stream-settlement-layout-e2e.cjs` compares paragraph positions, dimensions
and total turn height across this handoff at default/large sizes and wide/narrow
windows in both themes. Run it with the current checkout's Wuu Dev executable.
