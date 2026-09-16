# Troubleshooting

Start with the symptom below. Before reporting
a problem, do not upload an entire `~/.wuu`; it may contain source code, sessions,
tool output, and credential information.

## The desktop app will not start

1. Quit wuu completely, then reopen it.
2. Confirm the app comes from the official [GitHub
   Releases](https://github.com/blueberrycongee/wuu/releases).
3. If macOS blocks the app, follow **Open Anyway** in the
   [installation guide](../getting-started/installation.md). Do not disable Gatekeeper.
4. If initialization remains stuck, record the error message and app version for a report.

## The model service is unavailable

- **Missing API key:** in the desktop, check **Settings → Model providers**; in the
  CLI, check that the environment variable named by `api_key_env` actually exists in
  the process that started wuu.
- **Model does not exist:** use the model ID the server accepts, not a product display
  name.
- **Can chat but cannot change files:** confirm that the model and compatible gateway
  fully support tool calling, not just text conversation.
- **Custom endpoint fails:** check the protocol type, the API prefix, and whether the
  gateway forwards streaming responses and tool results unchanged.

## The wrong files or sessions appear

In the desktop, check the current project; in the CLI, check the current directory or
`--workdir`. Sessions are filtered by workspace by default. After a directory has been
moved, use **Relocate…** on the project in the sidebar instead of adding it again as a
new project with the same name.

## The agent cannot modify files

Check the [permission mode](../reference/permissions.md) and the target directory.
Read-only mode blocks writes; standard mode requires the target to be in a registered
workspace. Read the tool error for the blocked path or an unavailable sandbox backend.
Do not switch straight to `unconfined` to work around an error.

## Commands take a long time

The command may be continuing as a background process. Watch the process state and
incremental output in the message stream; do not start the same command repeatedly just
because the UI shows no new text. When full output is too long, the result provides a
log reference.

## CLI self-checks

```bash
wuu --version
wuu session list --json
wuu session show --json --last
wuu session trace --json --last
```

`session trace` replays already-saved events without calling the model or tools again.
To inspect automation run records, use `wuu runs` and `wuu runs read RUN_ID`.

## Where local data lives

Default user state is under `~/.wuu`, including configuration, authentication,
sessions, memory, and logs. When `WUU_HOME` is set, these paths move as a whole to the
specified directory. Before including them in a problem report, copy only the minimal
fragments needed to solve the problem, and remove API keys, OAuth information, source
code, and private conversations.

If you still cannot resolve it, provide the following in [GitHub
Issues](https://github.com/blueberrycongee/wuu/issues):

- the wuu version and operating system;
- whether you use the desktop or the CLI;
- minimal reproduction steps;
- redacted errors and relevant log fragments;
- expected versus actual behavior.

Do not file security vulnerabilities publicly; report them privately according to the
repository's [SECURITY.md](../../../SECURITY.md).
