# Troubleshooting

Start with the failing action and its exact error. Note the Wuu version, selected engine, provider, and workspace before changing settings. Avoid deleting the state directory or repeatedly launching the same task: both can make diagnosis harder, and a task may already be running in the background.

## Desktop startup

Quit the app completely and reopen it. Confirm that the installed build matches your platform and comes from the expected release source. If macOS blocks it, follow the [installation guide](../getting-started/installation.md) rather than disabling Gatekeeper globally.

For a persistent initialization failure, keep the version and visible error. If it began after enabling a plugin, use [plugin recovery](../customize/plugins.md#recovery-and-troubleshooting) and try the default interface.

## Model or engine unavailable

Check whether the session uses the Wuu engine or an external Codex or Claude Code executable. Configuring a model provider does not install or authenticate an external engine.

For a Wuu provider, verify the accepted model ID, connection protocol, API prefix, and authentication. An environment-backed API key must exist in the process that launched Wuu; an export in another terminal may not affect an already-running desktop app. A gateway that returns text may still fail streaming or tool calls. See [model services](../getting-started/model-services.md).

`wuu models` is specifically a live lookup for `openai-codex`, not a general connection test for every provider.

## Wrong project or missing history

Check the selected desktop workspace or CLI `--workdir`. A forked worktree has its own execution root, while a conversation without a project uses Wuu's scratch location. Default session lists are workspace-scoped.

If the project folder moved, use **Relocate…** on its existing sidebar entry. Adding another project with a similar name does not repair the old workspace identity. Do not redirect a missing worktree to the main checkout without first understanding which files the task changed.

## File or command refused

Read the error for the blocked path, permission mode, or sandbox backend. Read only blocks mutation; confined command execution also requires a working filesystem sandbox. An unavailable backend is a different problem from an out-of-scope path.

Register the intended workspace or correct the command before considering broader access. Unconfined is a deliberate authority change, not a general error-recovery switch. Sensitive-file guards and external-engine controls have separate rules; see [permissions](../reference/permissions.md).

## No new output

A command may have moved into a managed background process after the synchronous timeout. Inspect its state and incremental output rather than starting a duplicate. Large output can have a full-log reference even when only a tail is displayed.

A queued prompt waits for the receiving conversation. A provider failure, held turn, running tool, and completed turn are different states; use the visible error and saved trace to distinguish them. If the app is disconnected from its backend, reconnecting the UI does not prove that the underlying command stopped.

## Read saved diagnostics

```bash
wuu version --long
wuu session list --json
wuu session show --json --last
wuu session trace --json --last
```

Run these in the relevant workspace, or supply `--workdir` before positional arguments. Trace reads saved events without invoking the model or replaying tool side effects. For execution-run manifests, use `wuu runs`; for scheduled tasks, inspect the Automation page's run history.

## Share a useful report

Include the version and operating system, desktop or CLI entry point, selected engine, minimal reproduction steps, expected result, actual result, and the relevant redacted error. Use [GitHub Issues](https://github.com/blueberrycongee/wuu/issues) for ordinary bugs.

Do not upload the entire `~/.wuu` or `WUU_HOME` directory. Configuration, credentials, conversations, source excerpts, tool output, artifacts, and logs may be private. Share only the necessary fragments after review. Report suspected security vulnerabilities privately through [SECURITY.md](../../../SECURITY.md).
