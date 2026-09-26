# Connecting a model service

A provider tells Wuu where to send model requests and which credentials to use. A model is an identifier accepted by that provider. You can keep several models under one provider without duplicating its connection settings.

These settings apply to the **Wuu** engine. Selecting an [external engine](external-engines.md), such as Codex, Claude Code, or Devin, runs that program with its own authentication and configuration. Reusing a subscription credential in a Wuu provider is a different choice from running the corresponding external engine.

## Add a provider in the desktop app

1. Open **Settings → Model providers → Add provider**.
2. Choose the service type. Use **OpenAI-compatible** or **Anthropic-compatible** for a service that implements that API.
3. Enter an identifier, an available model ID, the API endpoint, and credentials. Include an API prefix such as `/v1` when your service requires it.
4. Save the provider, return to the composer, and select the provider and model for the conversation.
5. Send a small request that includes a tool call, such as reading a project file, to check more than text-only replies.

The first-run form offers a simpler connection setup. Use Settings for a custom endpoint and additional model options. A model listed in a configuration is not a guarantee that your account can access it.

The composer's selection belongs to the conversation, or to the draft before its first message. Settings also lets you set workspace defaults; changing a conversation's model does not silently replace those defaults.

## Current OpenAI and Anthropic models

The catalog includes `gpt-6-sol`, `gpt-6-luna`, `claude-opus-5-5`, and `claude-fable-5-1`. Existing conversation and workspace selections remain unchanged; select a new model when you want to use it.

GPT-6 Sol and Luna support reasoning levels from `none` through `max`, defaulting to `medium`. Their Fast entries use the same model with priority processing and a different price. Wuu defaults an unspecified official OpenAI connection to Responses for these models. If you explicitly selected Chat Completions, choose Responses for reasoning with tools; Chat Completions supports their tool calls only at `none`. Custom endpoints retain their configured transport. See the official [Sol](https://developers.openai.com/api/docs/models/gpt-6-sol) and [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) specifications.

Claude Opus 5.5 and Fable 5.1 always use adaptive thinking. Wuu maps a saved `none` selection to `low`; their defaults are `medium` and `high`, respectively. Wuu requests readable thinking summaries and lets the API drop thinking blocks invalidated by context changes while retaining valid signed blocks. These models reject forced tool choice, so Wuu expresses a required closing tool call as an instruction with automatic tool selection; this does not guarantee that the model calls it. See the official [Opus 5.5](https://platform.claude.com/docs/en/models/opus-5-5/migration-guide) and [Fable 5.1](https://platform.claude.com/docs/en/models/fable-5-1/migration-guide) migration guides.

## Use an existing subscription

| Connection | Setup |
|---|---|
| Codex subscription | Sign in with Codex CLI, then choose to reuse the detected login during first-run setup. In configuration, use the `openai-codex` provider with `reuse_codex_credentials`. Wuu's desktop does not start OpenAI OAuth login itself. |
| xAI SuperGrok | Add an **xAI SuperGrok** provider and follow the browser login. For the CLI, run `wuu login xai` and select `--provider xai-subscription`. |
| Grok Build | Run `grok login`, then select the detected provider in Wuu or pass `--provider grok-build`. If the login expires, sign in again with Grok CLI; Wuu does not refresh or modify those credentials. |

SuperGrok subscription login, Grok CLI login, and an `XAI_API_KEY` are separate credential sources. Use the connection that matches your account. File editing and command execution also require the selected service and model to support tool calling.

## Check subscriptions in the desktop app

**Settings → Subscriptions** groups installed external agents and built-in
subscription services. Choose models directly from each row. ACP agents that
need account setup offer a sign-in button; choose a method to start authentication,
which may open your browser. A model-loading failure is not proof that credentials
are missing, and a static CLI model list does not establish login status.

Codex account allowances come from the installed CLI and show remaining
percentages and reset times. Refresh after a reset or when a snapshot is over
five minutes old. Sources without account-quota support have no allowance meter;
ACP context-window occupancy and local token counts are not subscription quota.
This page shows concise loading and sign-in failures rather than request history
or raw agent logs. Use the refresh button to retry loading the catalog and quotas.

## Configure the CLI

Create a user configuration once:

```bash
wuu init
```

The file is `~/.wuu/config.json`, or `$WUU_HOME/config.json` when `WUU_HOME` is set. Edit an existing file instead of running `wuu init --force`, which overwrites it.

Under `providers`, check `base_url`, `model`, and `api_key_env`. The generated configuration initially selects `openai` with GPT-6 Sol over Responses; its Anthropic entry uses Claude Opus 5.5. Replace the example model if your account needs a different one. Set the named environment variable before running:

```bash
export OPENAI_API_KEY="your API key"
cd /path/to/project
wuu exec --provider openai --permission-mode read_only "Read this project and explain how to run its tests"
```

`--provider` selects a configured identifier, and `--model` overrides the model for this run. See [configuration](../reference/configuration.md) for precedence and the limits on project-level settings.

## Turn completion and extra requests

Wuu follows the selected API's completion signals. A normal stop ends the turn even if the response is empty; it does not prove that the task is complete. Empty text or a commentary message alone does not trigger another billable request.

Anthropic Messages can explicitly request continuation with `pause_turn`. Responses-compatible services can use the optional `end_turn: false` extension on a successfully completed response; a missing, null, or true value does not request continuation. This extension is not required by the standard OpenAI Responses API. Chat Completions uses its own `finish_reason`; Wuu does not infer continuation from gateway-native reasons or fields belonging to another API. Output limits, filtering, errors, and unknown stop reasons do not by themselves request continuation.

Each continuation is another model request and may incur charges. Wuu allows up to eight consecutive automatic tool-free continuations, then reports an error if the service still requests another. Client tool execution resets this count; configured step limits and cancellation still apply. An unfinished response cannot replace conversation history as a compact summary.

## Let the agent inspect local images

Ask the agent to inspect a local image by its path, for example: “Read
`screenshots/settings.png` and check the alignment.” With Wuu's built-in tools,
`read_file` returns PNG, JPEG, static GIF, and WebP files as visual input to an
image-capable model. You do not need to attach the image in the composer first.
Relative paths use the session workspace; absolute paths must satisfy the same
file scope and sensitive-path rules as other reads. Generated images in the
session artifact directory can also be read.

The file's bytes determine its format. Large images are resized to a longest
side of 2048 pixels using the shared attachment processor; the result reports
source and delivered dimensions. A read accepts up to 20 MiB of source data and
40 million source pixels, and the normalized image must fit the 2 MiB inline
tool-result limit. Crop or resize a file if the tool reports a limit. Corrupt,
animated, and unsupported image formats fail; SVG remains source text. Line
ranges and continuations apply to text, not images.

A model explicitly marked as text-only receives an unsupported-image marker,
not the pixels. Choose an image-capable model to inspect the image. Original
image results remain in history when the model changes. With optional PTC,
successful image and audio tool results are attached automatically:

```javascript
await tools.read_file({path: "screenshots/settings.png"});
```

`present_artifact` displays a deliverable to the user; it does not inspect the
image for the model. External agent engines use their own file and image tools.

## Optional programmatic tool calling

In **Settings → General → Programmatic tool calling**, enable PTC to let the
built-in engine compose tool calls in a JavaScript or erasable TypeScript
program. It is off by default. Each model family can follow the global switch,
enable PTC, or disable it. An explicit family choice wins over the global
switch; switching models resolves the setting again. Changes require idle
turns and apply to the next turn. External engines keep their own tools.

When enabled, the model calls `run_code` with `code`, a short `description`,
and an optional `timeout_ms`. Its description lists the current tool bindings
and argument schemas. The program uses `await tools[name](args)`; the result is
a Wuu tool-result object with `content` and optional `structured_content`.
Unavailable and disabled tools are excluded. Family-specific editing tools
remain available through the bindings. Context reset, when available, remains
a separate top-level control. There are no family-specific performance claims
or automatic opt-ins.

Only printed values, a JSON return value, and successful image/audio outputs
become the program's observation. Intermediate tool calls still pass through
the normal permission, scheduling, event, and recording pipeline. Catch
`ToolCallError` to handle a failed binding. Await writes and dependent work in
order; use bounded parallel batches for independent reads.

Each program starts a fresh Node process with an empty environment. Native
APIs are available through `await import(...)`. Filesystem writes use the same
session process sandbox as command tools; network access and all file reads
are not isolated by that sandbox. See the [security model](../reference/security-model.md).
The default elapsed deadline is 120 seconds, including tool and approval
waits, with a 600-second maximum. Programs have no persistent state or
`yield`/`wait` continuation. Cancellation stops the program and its active
nested calls; completed effects are not rolled back or automatically replayed.
Printed/returned text is limited to 1 MiB; media also obeys the shared rich
result limits.

Desktop uses its bundled runtime. CLI use requires Node.js 22.19 or later on
`PATH`, or a user-configured `ptc.node_executable`. In user configuration:

```json
{
  "ptc": {
    "enabled": false,
    "families": { "gpt": true }
  }
}
```

Normal project configuration cannot change PTC settings or its executable.
The retired `code_mode` setting is accepted for configuration migration but
never enables PTC; saving PTC settings removes it. Old persistent cells and
the previous execution/wait tools are no longer supported.

## Large tool results

Text file reads use `NUMBER|CONTENT` on the first displayed line and on file lines divisible by ten; other lines use `|CONTENT`. Everything after the first `|` is source text, including indentation and literal pipes. Each continuation page starts with its own line-number anchor, and range metadata still identifies every displayed line.

Wuu keeps the original tool result and gives the model a stable, bounded view. Large ordinary text results show a continuous first page with a `read_file` continuation; following it reads the saved result without running the original tool again. Pages prefer complete lines and can split a long line without breaking Unicode characters. Continuations reject changed content rather than silently mixing versions. Images and other supported media retain their separate provider representation.

Built-in views preserve useful structure: search pages keep whole records and snapshot cursors, and shell output prioritizes recent error evidence. Results are settled before the tool ledger records them, including extension results and execution errors, so later requests and replay keep the same view. Wuu does not cut these pages again to fit a batch-wide text limit; conversation capacity remains the responsibility of context management. Paging can require extra model requests, and Wuu retains the full result if it cannot safely save or page it. Smaller pages are not a guarantee of lower total cost.

## Check a failed connection

“Credentials configured” means a credential is available locally, not that the provider has accepted it. Check the selected provider, endpoint, model ID, and account access. An environment variable must be visible to the process launching Wuu; a desktop app opened from the Dock may not inherit variables set in a terminal. You can also save an API key in Settings.

If text replies work but tools fail, check the service's support for tool calling and streaming. A compatible API format alone does not establish that every model supports the same capabilities.

Prompts, selected context, attachments, and tool results can leave your machine through the configured endpoint. The provider's pricing and data policies apply; with a gateway, the gateway receives those requests. Keep real API keys out of project files and Git history.

## Fast mode in the model popover

For supported provider/model pairs, the lightning button changes processing speed without changing the model or reasoning effort. Speed is saved with the conversation and the draft selection. Reset inherits provider options; an explicit off overrides an accelerated default. The same controls are available through `/fast on`, `/fast off`, and `/fast status`.

Wuu recognizes catalog aliases that share an API model and declare acceleration options. Existing `-fast` model selections continue working. Custom services can explicitly declare `providers.<provider>.models.<model>.fast_mode: true` once their endpoint supports the corresponding protocol; `false` hides inferred support. OpenAI-compatible requests use `service_tier: "priority"` / `"default"`. Anthropic requests use `speed: "fast"` / `"standard"`, with the required Fast mode beta header only when enabled. The direct Anthropic API capability follows its [documented supported models](https://platform.claude.com/docs/en/build-with-claude/fast-mode); compatible endpoints must declare support. This setting does not turn a reasoning level into a speed tier.
