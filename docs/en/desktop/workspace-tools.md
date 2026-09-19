# Files, changes, terminal, and browser

The desktop keeps the tools you need to review task results next to the current
workspace, so you can verify the agent's actual changes and verification results.

## Files

Open the **Files** panel or enter `/files` to browse the project tree. After selecting
a file you can view text, code, images, and supported documents in a tab. The file
panel shows the current state on disk, not a historical snapshot of a message.

Select text in a Markdown, code, or plain-text preview to use **Add to conversation**,
**Comment**, or **Edit**. Adding to the conversation attaches a selected-text tag without
sending it or expanding the excerpt into the input. Hover, focus, or click the tag to
read the original text; its remove button removes the selection. Sending the message
includes the original text and source location. Comments appear below the document preview and as a
comment tag in the composer; hover, focus, or click the tag to inspect the location,
quoted text, and comment. You can edit or remove comments before sending several
comments together with your message.

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

Open the **Review** panel or enter `/diff` to see the current Git working-tree
changes. Before committing or releasing, at least confirm:

- only the expected files changed;
- there is no debug content, generated junk, or sensitive information;
- deletions and renames match expectations;
- the verification the agent claims to have run actually has results.

The per-turn file changes in a message and the workspace's current overall diff can
differ: a later task may modify the same file again. The final check is based on the
current state of disk and Git.

## Terminal

Open the **Terminal** panel or enter `/terminal` to use a shell in the current
workspace. Commands started by the agent and background processes also leave activity
and results in the message stream; long output may be saved as a log reference instead
of being expanded in full.

Commands you type in this terminal run with your operating-system permissions, even
when the Agent is in read-only mode. Agent commands are subject to the selected
[permission mode](../reference/permissions.md); do not use the terminal to run an
unreviewed command just because the Agent could not run it.

## Browser

The browser panel lets you view web pages beside your work. While an Agent operates
a page, choose **Take control of browser** to operate manually, then **Return browser
control to Agent** to hand it back. **Stop browser activity** ends the current automation.
Automatic browser operation requires a supported runtime and tools.

The built-in browser can use a local proxy such as Clash on its own, without changing
the app-server's or model service's network connections. Set `WUU_BROWSER_PROXY` before
starting the desktop, for example to the mixed port Clash Verge commonly uses:

```bash
WUU_BROWSER_PROXY=http://127.0.0.1:7897 npm run dev --prefix desktop
```

For a packaged build, launch the app from a terminal with the same environment
variable. The proxy only applies to the session used by wuu's built-in browser; if the
proxy port is unavailable, the desktop and API service still start normally, but
browser requests fail — fix the port and restart the desktop.

## Find entries with `/`

Type `/` in the input box to search the operations, workflows, and skills the current
version provides. The menu disables entries that do not apply given the current
workspace, whether a task is running, and the runtime's capabilities, so it is more
reliable than a static command list.
