# Files, changes, terminals, and browser

Use the workspace panels to inspect what actually happened during a task. A conversation records the agent's activity; the files and Git diff show what is on disk now.

## Files

Open **Files** or enter `/files` to browse the project. Select a file to view supported text, code, images, or documents. This view follows the current file, not its content at the time of an earlier message.

### Agent file search

The agent's `grep` and `glob` tools search current files when `offset` is omitted or `0`. This also applies after external edits or reopening a conversation; a previous search cache does not establish that files are unchanged.

Later pages are a snapshot continuation, not a refresh. Keep the search arguments and pass both values from `page.next`: `offset` and `expected_revision`. Missing or mismatched tokens are rejected. A new first page can invalidate an older continuation even when the workspace revision summary is unchanged. If a continuation is stale, restart at `offset: 0` without `expected_revision`. Searches observe files as they are read, not an atomic filesystem snapshot during concurrent edits.

## Delivered artifacts

An agent can present a requested image, chart, or document as an output in the conversation. Images, including SVG, appear inline with a larger preview on click; other files appear as output cards.

These outputs are saved snapshots. Changing or deleting the original file later does not change the delivered version. A normal file link or diff is not an artifact snapshot.

On desktop, click a file output card to open its snapshot in a workspace tab beside the conversation. PDF, HTML, images, audio, and text use their supported viewers; other formats retain a download action. HTML previews do not run scripts. Text previews load up to 2 MiB; download larger files to read them in another application.

When a live turn finishes successfully with exactly one delivered file, desktop can open it automatically after checking that the snapshot is available. Automatic preview supports PDF, HTML, images, and text up to 20 MiB (2 MiB for non-HTML text). It does not fetch ordinary links or open file edits. Multiple files, unsupported formats, failed or interrupted turns, and historical conversations require a click.

Automatic previews leave an occupied panel and foreground browser alone. Using the panel during the turn, opening an overlay, or switching to a narrow window suppresses automatic opening for that turn. Navigation cancels pending preview checks, and closing a preview does not reopen it on later updates. Downloads always require an explicit action.

For agents and integrations, the built-in `present_artifact` tool accepts an existing local file:

```json
{"path":"output/chart.svg"}
```

The limit is 256 MiB. Normal read permissions and sensitive-path restrictions apply, and the tool does not fetch URLs. Presenting a file does not inspect its appearance or send its image bytes to the model; visual verification is a separate step.

## Review

Open **Review** or enter `/diff` to inspect the current Git changes. Check the changed paths, additions, deletions, and any sensitive data before committing. Compare the agent's reported checks with their actual command results.

The diff shown for one turn can differ from the current workspace diff because later work may have changed the same files. Base your final review on the current repository state.

## Terminal

Open **Terminal** or enter `/terminal` to run a shell in the workspace. Commands you type there use your OS permissions, including when the agent is in read-only mode. Review a command before running it; do not use the terminal simply to bypass an agent permission denial.

Agent commands also have activity entries and results in the conversation. Long output can be stored as a log reference. For long-running processes, use their process controls to inspect output, send input, or stop them. See [commands and background tasks](../reference/agent-command-system.md).

## Browser

Open **Browser** or enter `/browser` to view a page in the workspace panel beside the conversation. That panel and the page the agent is using are the same tab: the address bar, back, forward, and reload all drive it. Clicking a web link in a message, a turn source capsule, or a compact browser activity row also opens or focuses that panel and navigates to the page. Hold Command (Ctrl on Windows) or middle-click to open it in the system browser instead. If you are already using another workspace tool, the page still navigates in that tab and does not steal the panel.

Desktop browser automation is available by default. While the agent is browsing, the page stays in a floating card inside the conversation column. Dragging the card snaps it to a corner of that column, clear of the composer. The side panel does not open or close because of it. Opening the browser yourself, or expanding the card, docks that same page in the panel; the address bar, back, forward, and reload all drive it. Clicking the page, editing the address, or using the navigation buttons takes control, and the page stays in the panel. **Take control of browser** switches to manual control, **Return browser control to Agent** gives control back while keeping the page visible, and **Stop browser activity** stops that activity. Set `WUU_ENABLE_BROWSER=0` before launching Wuu to hide the automation tool.

To route only the embedded browser through a proxy, set `WUU_BROWSER_PROXY` before launching the desktop. For a source build:

```bash
WUU_BROWSER_PROXY=http://proxy.example:7897 npm run dev --prefix desktop
```

Use the address and port of your own proxy. This setting does not configure model API or app-server traffic. If browser requests fail after enabling it, check the proxy and restart Wuu with the corrected setting.

## Find an action

Type `/` in the composer to search workspace actions, prompt shortcuts, and skills. Disabled entries explain missing prerequisites such as a workspace, an idle conversation, or engine support.
