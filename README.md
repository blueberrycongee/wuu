# wuu

[简体中文](README_zh.md) · [Documentation](https://blueberrycongee.github.io/wuu/en/) · [Releases](https://github.com/blueberrycongee/wuu/releases)

wuu is an open-source desktop app for working with AI agents on local projects. Connect a model provider, choose a folder, and ask an agent to read code, make changes, or run commands. Review the files, diffs, and results in the app.

You can return to a conversation to continue the work. Plugins add tools and desktop features; see [Extend Wuu](docs/en/customize/index.md).

![wuu desktop app](https://github.com/user-attachments/assets/2d9030aa-ca03-42b1-9333-f79cc5aff95b)

## Get started

The desktop preview supports Apple silicon Macs. Download it from [GitHub Releases](https://github.com/blueberrycongee/wuu/releases/latest), move `wuu.app` to `/Applications`, and open it. This is an unsigned preview (ad-hoc signatures only), without Apple Developer ID or notarization; if macOS blocks it, follow the [installation guide](docs/en/getting-started/installation.md).

Connect your model provider in Settings, then add a local project folder as a workspace. Try a small task and check the changes and test results. The [quick start](docs/en/getting-started/index.md) walks through an example.

## CLI

The desktop app includes its own core. If you also want to use wuu in a terminal or script, install Go as specified in [go.mod](go.mod), then build the CLI:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu init
```

Make sure Go's binary directory is on your `PATH` and [configure a model provider](docs/en/getting-started/model-services.md#configure-the-cli), then run:

```bash
cd /path/to/your/project
wuu exec --permission-mode read_only "review this project and explain how to run its tests"
```

The [`wuu exec` guide](docs/en/automation/exec.md) covers scripts, JSONL output, and session controls.

## Your files and data

wuu reads and changes local files and runs commands within the active permission mode. Prompts and relevant context are sent to your chosen model provider. Sessions and settings are stored under `~/.wuu` by default. Read the [security model](docs/en/reference/security-model.md) before working with sensitive data or untrusted projects.

## Contribute

See [Contributing](CONTRIBUTING.md) to work on wuu, or [open an issue](https://github.com/blueberrycongee/wuu/issues) to report a problem. Report security vulnerabilities using [SECURITY.md](SECURITY.md).

Licensed under [MIT](LICENSE). Agent avatars use [blobatar](https://github.com/Alain00/blobatar), also MIT-licensed.
