# Composer attachment tray preview

From `desktop/`, run `npx vite --host 127.0.0.1` and open
`/dev/composer-attachments/` on the reported port.

The page mounts the production `Composer` with the real draft pipeline
(`useComposerDraftState`) and pastes through the textarea's own paste
handler, so images take the optimistic-preview and encode path and long
text folds exactly as it does in the app. The screenshot, PDF, video and
text are synthetic; nothing is sent and no app-server is involved.

Query parameters select the starting state: `theme=dark`, `size=20`,
`width=560`, `hero`, `queued` (adds a pending queue drawer), and `seed`
(starts with one of each attachment). Check paste and removal motion,
horizontal overflow, the stacked queue drawer, hover and keyboard focus on
cards, and reduced motion. Production conversation scrolling and send
placement are not reproduced here.
