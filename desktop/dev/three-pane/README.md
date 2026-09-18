# Three-pane typography preview

With the current desktop development server running, open
`http://localhost:5173/dev/three-pane/`.

This development-only entry mounts production `ProjectGroup`, `TurnView`, and
`WorkspaceRightPanel` components with the complete renderer stylesheet. Files and
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

Production resizing, collapsed drawers, and split-session behavior remain the
responsibility of the app-level tests, not this fixed-width comparison shell.
