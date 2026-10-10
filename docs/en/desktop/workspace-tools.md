# Files, changes, terminals, and browser

Use the workspace panels to inspect what actually happened during a task. A conversation records the agent's activity; the files and Git diff show what is on disk now.

The left sidebar keeps the visibility and width you choose when you resize the window or zoom the interface. Use its toggle or drag its divider to collapse it.

Expanding the right panel adds the current conversation as the first tab beside the tool tabs. Select it to return to the conversation without leaving the expanded layout; select a tool tab to switch back. The conversation draft is preserved. Exiting the expanded layout restores the conversation and tools side by side.

## Side chat

Use `/side` or **Ask in side chat** to open the side chat tab in the right sidebar. It shares the sidebar width and expanded layout with files and other tools. File selections keep their quote card, and the original file stays available in its tab.

Side chat follows the selected main conversation; each conversation keeps its own side history and draft. Switching tool tabs or collapsing the sidebar preserves the draft and lets a running answer continue. Closing the side chat tab preserves its history; use the chat's stop or reset action to interrupt or clear it. In the expanded layout, the side chat tab has its own input, while the main conversation remains available through its first tab.

## App zoom

Press **Command + + / −** on macOS (**Ctrl + + / −** on other platforms) to
resize the entire desktop interface. **Command/Ctrl + =** also zooms in;
**Command/Ctrl + 0** restores 100%. Zoom changes in 10-percentage-point steps
between 50% and 200%. A percentage appears briefly in the center of the window.
Your zoom is remembered across launches, independently of UI and code font sizes.
These shortcuts apply to Wuu windows, not embedded browser pages.

## Files

Open **Files** or enter `/files` to browse the workspace. Select a file to view supported text, code, images, or documents. This view follows the current file, not its content at the time of an earlier message.

In a single conversation, opening a workspace file keeps the conversation visible and places its input at the bottom of the right preview. Requests from this input include the selected file as context. The draft belongs to the conversation: closing the preview returns it to the conversation input. Use the preview’s conversation menu to return to the full conversation, or minimize and restore the floating input while reading. The optional full-panel button gives the document more space.

For a PDF delivered in the current conversation, the preview input sends that exact snapshot as a PDF attachment. Workspace-file previews instead identify the selected file by its workspace path. Switching to another tool removes the preview’s implicit file context from subsequent requests.

### Agent file search

The agent's `grep` and `glob` tools search current files when `offset` is omitted or `0`. This also applies after external edits or reopening a conversation; a previous search cache does not establish that files are unchanged.

Later pages are a snapshot continuation, not a refresh. Keep the search arguments and pass both values from `page.next`: `offset` and `expected_revision`. Missing or mismatched tokens are rejected. A new first page can invalidate an older continuation even when the workspace revision summary is unchanged. If a continuation is stale, restart at `offset: 0` without `expected_revision`. Searches observe files as they are read, not an atomic filesystem snapshot during concurrent edits.

## Delivered artifacts

An agent can present a requested image, chart, or document as an output in the conversation. Images, including SVG, appear inline with a larger preview on click; other files appear as output cards.

Click an image to open its preview. Pinch the trackpad or hold Ctrl/Command while scrolling to zoom around the pointer; use two-finger scrolling or drag to move an enlarged image. Double-click to enlarge or return to fit. The toolbar offers **Fit to window**, **Actual size**, **Rotate**, and **Save image as…**. Save As lets you choose a filename and directory and keeps the original bytes, including animation; preview rotation does not change the saved file.

Keyboard controls: **+ / −** zoom, **0** fits the image, **1** shows actual size, **R** rotates, arrow keys pan, **Command/Ctrl + S** saves, and **Esc** closes the preview.

These outputs are saved snapshots. Changing or deleting the original file later does not change the delivered version. A normal file link or diff is not an artifact snapshot.

On desktop, click a file output card to open its snapshot in a workspace tab beside the conversation. PDF, HTML, images, audio, and text use their supported viewers; other formats retain a download action. HTML previews do not run scripts. Text previews load up to 2 MiB; download larger files to read them in another application.

When a live turn finishes successfully with exactly one delivered file, desktop can open it automatically after checking that the snapshot is available. Automatic preview supports PDF, HTML, images, and text up to 20 MiB (2 MiB for non-HTML text). It does not fetch ordinary links or open file edits. Multiple files, unsupported formats, failed or interrupted turns, and historical conversations require a click.

Automatic previews leave an occupied panel and foreground browser alone. Using the panel during the turn, opening an overlay, or switching to a narrow window suppresses automatic opening for that turn. Navigation cancels pending preview checks, and closing a preview does not reopen it on later updates. Downloads always require an explicit action.

For agents and integrations, the built-in `present_artifact` tool accepts an existing local file:

```json
{"path":"output/chart.svg"}
```

The limit is 256 MiB. Normal read permissions and sensitive-path restrictions apply, and the tool does not fetch URLs. Presenting a file does not inspect its appearance or send its image bytes to the model; visual verification is a separate step.

Select text in a Markdown, code, or plain-text preview to use **Add to conversation**,
**Comment**, or **Edit**. Adding to the conversation adds a quote card to the attachment tray without
sending it or expanding the excerpt into the input. Open the card to
read the original text; its remove button removes the selection. Sending the message
includes the original text and source location. Comments appear below the document preview and as a
quote card in the attachment tray; open the card to inspect the location,
quoted text, and comment. You can edit or remove comments before sending several
comments together with your message.

Choose **Ask in side chat** from a file selection to attach the passage to a side question. This requires an existing main conversation; the side composer shows the same quote card.

**Edit** opens a small instruction box at the selection. Submitting it sends an
independent request to the current conversation without consuming your existing
draft or attachments. A running task queues the request. The request instructs the agent
to read the latest file before editing, and the preview refreshes when the turn finishes. A failed
submission keeps your instruction available to retry.

Source locations refer to the file version captured when you selected the text.
Comments retain their original excerpt if the file changes. Complex rendered blocks
may be referenced as a whole when a precise source selection is unavailable. These
selection actions do not yet apply to PDF or image previews.

## Review

Open **Review** or enter `/diff` to inspect the current Git changes. Check the changed paths, additions, deletions, and any sensitive data before committing. Compare the agent's reported checks with their actual command results.

Changed files are grouped by folder and the list refreshes as the workspace changes. A wide panel shows the diff beside the list; in a narrow one, choose a file to open its diff and use the back button to return. In the diff header, the arrows step to the previous or next file, the circle marks the file as viewed, and the pencil opens it in the editor. Viewed marks clear when a file’s status or added/deleted line counts change and are not kept after Wuu restarts. Edits with the same status and line counts do not clear a mark; review the current diff before committing.

The diff shown for one turn can differ from the current workspace diff because later work may have changed the same files. Base your final review on the current repository state.

## Terminal

Open **Terminal** or enter `/terminal` to run a shell in the workspace. Commands you type there use your OS permissions, including when the agent is in read-only mode. Review a command before running it; do not use the terminal simply to bypass an agent permission denial.

Agent commands also have activity entries and results in the conversation. Long output can be stored as a log reference. For long-running processes, use their process controls to inspect output, send input, or stop them. See [commands and background tasks](../reference/agent-command-system.md).

## Browser

Open **Browser** or enter `/browser` to view a page in the workspace panel beside the conversation. That panel and the page the agent is using are the same tab: the address bar, back, forward, and reload all drive it. Clicking a web link in a message, a turn source capsule, or a compact browser activity row also opens or focuses that panel and navigates to the page. Hold Command (Ctrl on Windows) or middle-click to open it in the system browser instead. If you are already using another workspace tool, the page still navigates in that tab and does not steal the panel.

Desktop browser automation is available by default. The active conversation automatically previews the Agent's page in a floating card, including during background browser actions; switching conversations hides it. The preview does not require an explicit visibility request and stays visible across further actions. Hiding or minimizing Wuu also hides the card; showing or restoring the window brings it back. Dragging the card snaps it to a corner of that column, clear of the composer. The page keeps its layout size and is zoomed so the whole page fits in the card. Dragging an edge or corner changes the card, and the zoom follows. Pointer events on the card move or resize it; they do not click or scroll the page. The card does not paint the page scrollbar or automatically open the side panel. Open the browser or expand the card to use that same page in the panel. Set `WUU_ENABLE_BROWSER=0` before launching Wuu to hide the automation tool.

Each conversation owns its browser tabs, including pages opened in a new window. Listing or cleaning up tabs in one conversation leaves other conversations' pages alone. Tabs still share the app's browser profile and signed-in sessions; task ownership is not a separate login profile.

Page-opened windows keep their original content and opener connection, including script-filled blank pages and forms submitted to a new window. When a turn finishes or is interrupted, Wuu closes temporary task pages and releases automation control. Pages you opened or took over, pages retained for handoff, and deliverables stay available. Keeping a popup also keeps its live opener so messaging and review context continue working. A later instruction can use a retained page with fresh control authority.

The agent normally reads page text and controls without attaching screenshots to the model. When a task needs visual evidence, such as a canvas, chart, or page layout, `observe` and `screenshot` accept `include_image: true`. This sends the page pixels to the selected image-capable model using the same size limits as local image reads. Text-only models can continue using ordinary page observations. The result includes delivered image dimensions and CSS viewport dimensions for coordinate mapping; page pixels may contain private content and are not covered by DOM text redaction. Reset pinch zoom before requesting visual evidence; ordinary page zoom is supported.

Clicking or typing in the Agent's page, submitting an address, or using its navigation buttons pauses the current task and leaves the page available for you to use. Hovering and scrolling alone do not pause it. Send your next instruction in the conversation when you are ready to continue; background updates do not restore browser input. Use the conversation's Stop action to interrupt a task. The browser has no separate control-transfer buttons or ownership status bar.

The floating preview and workspace panel share an animated pointer. It stays legible when the page is scaled, moves to the target before input, and shows click, typing, and scroll feedback. Direct page input clears it. Reduced-motion settings disable travel and idle movement.

To route only the embedded browser through a proxy, set `WUU_BROWSER_PROXY` before launching the desktop. For a source build:

```bash
WUU_BROWSER_PROXY=http://proxy.example:7897 npm run dev --prefix desktop
```

Use the address and port of your own proxy. This setting does not configure model API or app-server traffic. If browser requests fail after enabling it, check the proxy and restart Wuu with the corrected setting.

## Find an action

Type `/` in the composer to search workspace actions, prompt shortcuts, and skills. Disabled entries explain missing prerequisites such as a workspace, an idle conversation, or engine support.
