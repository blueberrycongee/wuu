# Three-pane typography preview

From `desktop/`, run `npx vite --config dev/three-pane/vite.config.ts` and open
`/dev/three-pane/` on the reported port. The config also compiles source-distributed
avatar dependencies with the automatic JSX runtime used by production.

This development-only entry mounts production `ProjectGroup`, `TurnView`,
`ConversationTitleActions`, and `WorkspaceRightPanel` with the complete renderer stylesheet. Files and
conversations are synthetic; no app-server, saved desktop preferences, or live
workspace files are used. The composer is a layout specimen and cannot send.
File-tree selection does not open a real file.

The 1085 × 919 viewport contains a 36px test toolbar and an 1085 × 883 app
surface: 252px navigation, 353px conversation, and 480px workspace. These are
comparison dimensions, not overrides for the production app's saved panel sizes.
The fixture shell omits native window controls and the full application tab bar.

Compare light/dark and 14/20px, then inspect a wider 1440px viewport and a 900px
window with the right panel closed. Check project indentation, long names,
running/unread/forked rows, hover actions, keyboard focus, scrolling, the tool
picker, and file-tree search. `?theme=dark&size=20` selects those preview settings.
Titlebar information and sidebar buttons expose their selected states; the
workspace expand button switches between expand and restore. Add `compact` to
preview the compact titlebar menu, or `globalized` to start with an expanded panel.
Add `empty` for the empty-session greeting and synthetic usage overview; add
`new-user` as well to preview the overview with no recorded usage.
The information button only toggles its selected state; it does not open live
environment data.

Production resizing, collapsed drawers, and split-session behavior remain the
responsibility of the app-level tests, not this fixed-width comparison shell.
