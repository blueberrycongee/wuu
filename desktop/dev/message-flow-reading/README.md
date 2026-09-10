# Message flow reading fixture

With the desktop development server running, open
`http://localhost:5173/dev/message-flow-reading/` (use its actual port if different).

This page imports production `RichContent`, `StreamingMarkdown`, the font-size
application helper and the complete renderer stylesheet. Its own CSS styles only
the test controls and outer layout. It is a separate development entry, guarded
by `import.meta.env.DEV`, and is not included in the production build inputs.

Compare 14px and 20px at 1280px and 420px viewport widths, then check the 13px/20px range limits,
dark mode, ordinary Markdown, chat bubbles and the file preview. Use the cursor
toggle to compare live/settled geometry. “逐段播放” sends actual incremental updates
through the stream text store, including blank lines inside a loose list.

Inspect hanging list indents, multi-paragraph items, numbered lists starting at
9, task checkboxes, multi-paragraph quotes, inline paths, code and tables. Prose
and its headings should keep the same left edge; wide content may extend right.
No text should be clipped at larger font sizes, and completing the stream should
preserve the existing list/paragraph DOM and geometry.
