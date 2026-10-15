# Native document editors

Use this guidance for TextEdit and other AppKit document editors that expose AX text fields or text areas.

- Observe the intended document window and editor. Before keyboard input or paste in a newly opened background window, click the editor using its fresh element reference, then observe focus and selection. Setting AX focus alone may not establish the window's keyboard destination.
- Use `set_value` only when replacing the entire exposed value is intended and supported. Use `type_text` for literal typing and `paste` for longer or formatted content. A plain-text document may accept only the plain-text representation of rich clipboard content.
- Use `select_text` for a unique match. The returned `selection_range` uses UTF-16 offsets; a zero-length range is an insertion point. Inspect `selected_text` or the range before a replacement that could affect existing content.
- When matching text repeats, supply immediate `prefix` or `suffix` context. After insertion, use the new snapshot to verify the surrounding text and caret position.
- Native menus may sit outside the document's AX subtree. Observe with `scope="app", mode="ax"` after opening a menu, then choose the freshly observed item by exact label. Follow the current menu's shortcut when one is shown.
- Save dialogs create a new interaction context. Observe the sheet, verify the destination and filename, act once, and inspect any overwrite or error prompt before reporting that the document was saved.
