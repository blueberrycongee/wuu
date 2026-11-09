# Notion pages and blocks

Use this guidance after observing the Notion desktop app. Match labels in the current UI; names and available actions can differ by locale and release.

- Distinguish the page title, search field, and block editor before typing. Click the intended block from a fresh snapshot, then inspect focus or the caret. A text-looking AX node is not necessarily an editable field.
- For a particular block type, enter `/` in the intended block and observe the command menu before selecting its named item. Do not assume a fixed menu order or send a long key sequence through an unobserved menu.
- Use `paste` for a prepared passage. Markdown and HTML provide rich clipboard content, but do not guarantee a particular block structure. Inspect headings, lists, links, and the final block after insertion. Use the block menu to correct structure only where the observed result requires it.
- Escape while editing can select the whole block. Confirm whether the caret, a text range, or one or more blocks are selected before replacing or deleting content. If AX does not expose a usable text range, use a fresh screenshot and a grounded keyboard or menu action; do not force `select_text` onto a non-text node.
- Locate the destination before moving a block. Observe the destination and resulting order after the move; a completed drag alone does not prove the block landed correctly.
- After navigation or a save indicator changes, use a fresh observation. Check any visible saving/error state before reporting completion; an AX quiet interval does not prove server persistence.

Product behavior reference: [Notion writing and editing basics](https://www.notion.com/help/writing-and-editing-basics).
