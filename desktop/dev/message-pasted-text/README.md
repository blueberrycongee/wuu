# Sent-message pasted-text card — design draft

From `desktop/`, run `npx vite --host 127.0.0.1` and open
`/dev/message-pasted-text/` on the reported port.

Side-by-side mockups of the `pasted_text` attachment inside sent user
messages: the current row card plus options A–D, each composed against a
text bubble, an image grid, file chips, multiple pastes, a plugin-titled
paste, and the expanded state. Cards are clickable to feel the
collapse/expand interaction.

Query parameters: `theme=dark`, `style=im`, `width=560`, `size=20`.

The "current" section mounts the real `ThreadItemView`; option sections
reuse production classes (`.user-message-block`, `.user-message-attachments`,
`.user-message-pasted-text*`, `.message-image`, `.message-file`) plus
fixture-local `.mpt-*` rules from `fixture.css`. Nothing here is a
production component — this page is a design surface only.
