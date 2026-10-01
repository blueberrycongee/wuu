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

For Anthropic-compatible connections, an explicit credential in configuration
(or its named environment variable) takes priority over a saved credential.
Selecting either source prevents an unrelated ambient API key or bearer token
from being added to the request. Explicitly configuring both headers remains
supported. Saving a new API key in Settings replaces the previous bearer token,
including its configured environment-variable reference.

## Current OpenAI and Anthropic models

The catalog includes `gpt-6.1-sol`, `gpt-6-sol`, `gpt-6-luna`, `claude-opus-5-5`, and `claude-fable-5-1`. Existing conversation and workspace selections remain unchanged; select a new model when you want to use it.

GPT-6.1 Sol supports `low`, `medium` (default), `high`, `xhigh`, and `max`; it does not support `none` or `minimal`. Tool calls require Responses, which Wuu selects when an official OpenAI connection has no explicit transport. Explicit and custom-endpoint transports remain unchanged: select Responses before using tools. Its Fast entry sends `gpt-6.1-sol` with priority processing at twice the standard API price; Fast is unavailable with EU data residency. Standard cached input costs $0.10 per million tokens, with higher rates above 272K input tokens. See the official [GPT-6.1 Sol specification](https://developers.openai.com/api/docs/models/gpt-6.1-sol).

To use GPT-6.1 Sol through an existing Codex subscription, reuse your local Codex login and refresh the subscription model list. Wuu adds the Fast alias when the account advertises the base model; the API catalog alone does not establish subscription access. Subscription requests keep Codex authentication and the subscription context-budget policy, rather than requiring an OpenAI API key or assuming the API input limit. API prices are not subscription quota estimates. The external Codex engine continues to use its own model discovery and login.

GPT-6 Sol and Luna support reasoning levels from `none` through `max`, defaulting to `medium`. Their Fast entries use the same model with priority processing and a different price. Wuu defaults an unspecified official OpenAI connection to Responses for these models. If you explicitly selected Chat Completions, choose Responses for reasoning with tools; Chat Completions supports their tool calls only at `none`. Custom endpoints retain their configured transport. See the official [Sol](https://developers.openai.com/api/docs/models/gpt-6-sol) and [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) specifications.

Claude Opus 5.5 and Fable 5.1 always use adaptive thinking. Wuu maps a saved `none` selection to `low`; their defaults are `medium` and `high`, respectively. Wuu requests readable thinking summaries and lets the API drop thinking blocks invalidated by context changes while retaining valid signed blocks. These models reject forced tool choice, so Wuu expresses a required closing tool call as an instruction with automatic tool selection; this does not guarantee that the model calls it. See the official [Opus 5.5](https://platform.claude.com/docs/en/models/opus-5-5/migration-guide) and [Fable 5.1](https://platform.claude.com/docs/en/models/fable-5-1/migration-guide) migration guides.

## Use an existing subscription

| Connection | Setup |
|---|---|
| Codex subscription | Sign in with Codex CLI, then choose to reuse the detected login during first-run setup. In configuration, use the `openai-codex` provider with `reuse_codex_credentials`. Wuu's desktop does not start OpenAI OAuth login itself. In Settings → Subscriptions, open the account’s **…** menu. “Use local Codex login” selects the local login over Wuu’s saved credentials without changing the model. “Check login again” verifies authentication by fetching the model list. Wuu reads the local login on each request, so refreshed credentials take effect without a restart. |
| xAI SuperGrok | Add an **xAI SuperGrok** provider and follow the browser login. For the CLI, run `wuu login xai` and select `--provider xai-subscription`. |
| Grok Build | Run `grok login`, then select the detected provider in Wuu or pass `--provider grok-build`. If the login expires, sign in again with Grok CLI; Wuu does not refresh or modify those credentials. |

SuperGrok subscription login, Grok CLI login, and an `XAI_API_KEY` are separate credential sources. Use the connection that matches your account. File editing and command execution also require the selected service and model to support tool calling.

## Check subscriptions in the desktop app

**Settings → Subscriptions** groups installed external agents and configured
model services into compact cards. Connections to the same model service share
a card, with separate account sections and credential-source labels. Each account
keeps its own subscription, plan windows, or prepaid balance; different accounts
and currencies are never added together. Quota windows appear side by side when
space permits, and stack in narrow cards. Supported accounts retain their existing
model controls; Codex credential actions belong to the selected account’s **…** menu.
ACP agents that need account setup offer a sign-in button; choose a method to start authentication,
which may open your browser. A model-loading failure is not proof that credentials
are missing, and a static CLI model list does not establish login status.

Quota readers use the selected connection's credential source. Supported sources
are ChatGPT/Codex, Grok Build, Anthropic OAuth or local Claude Code, Kimi Code,
Zhipu/Z.ai plans, and DeepSeek/OpenRouter balances. Browser SuperGrok and arbitrary
compatible endpoints do not currently expose supported quota details.

The page shows remaining percentages, reset times, account labels when available,
and compact credential attribution. Reset countdowns keep the next renewal visible;
the exact date and time zone are available in its tooltip and accessible name.
Fresh observation times live in the credential tooltip; stale readings show their
age directly. Missing values remain unknown; zero remaining means exhausted.
Refresh after a reset or when a snapshot is over five minutes old. A temporary
failure keeps the last successful observation when the reader can still identify
the account, clearly marked for refresh without an active allowance meter;
rejected authentication clears the old allowance.
Saved observations survive restarts and cannot be reused for another account.
ACP context-window occupancy and local token counts are not subscription quota.
This page shows concise loading and sign-in failures rather than raw agent logs.
Quota refreshes do not submit model requests, change defaults, or select a spending
route. Use the refresh button to retry loading the catalog and quotas.

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
image results remain in history when the model changes. With PTC,
successful image and audio tool results are attached automatically:

```javascript
await tools.read_file({path: "screenshots/settings.png"});
```

`present_artifact` displays a deliverable to the user; it does not inspect the
image for the model. External agent engines use their own file and image tools.

## Programmatic tool calling

The built-in engine uses programmatic tool calling (PTC) by default. Ordinary
file, command, search, browser, API and extension tools run through `run_code`.
**Settings → Runtime → Programmatic tool calling** provides a global switch
and model-family overrides. Explicit saved choices are preserved; configurations
that omit the switch use the default. Changes require idle turns and apply on
the next turn. External engines keep their own tools.

Interaction, explicit artifact delivery, context replacement, workspace changes,
and agent/session lifecycle controls remain separate direct tools. Extensions
can declare `direct_only` for those controls. A direct-only tool cannot be called
from a program; ordinary tools cannot bypass PTC while it is enabled. Available
bindings retain their exact names, permissions and model-family edit primitives.

### Discover, execute and inspect results

Call `run_code` with `code`, a short `description`, and optional `timeout_ms`.
Its description contains a bounded catalog preview. `await searchTools(query,
{limit: 8, offset: 0})` returns `tools`, `total` and optional `next_offset`; an
empty query pages through all bindings. `await describeTool(name)` returns the
exact description and `input_schema`. Call `await tools[name](args)`; bracket
access preserves names containing punctuation without aliases or collisions.

Results retain `content`, optional `structured_content`, and optional
`model_text` for the compact model projection. Catch `ToolCallError` to inspect
`toolName`, `message`, and the canonical `result` for a completed tool failure.
Transport and discovery failures have no synthetic result. Only printed values,
a JSON return value and successful image/audio outputs become the program's
observation. Explicit deliverables use the separate `present_artifact` tool.
Nested hook context still reaches the next model request even if code does not
print it. Every effect keeps its normal permission, hook, scheduling and audit
path. Await writes and dependencies in order; use bounded parallel batches for
independent reads.

### State and long-running work

Every program starts a fresh isolated interpreter. `store(key, value)` stages a
lossless JSON value, `load(key)` returns a clone or `undefined`, and `remove(key)`
deletes a key and reports whether it existed. Mutating a loaded value does not
change stored state until `store` is called again. Successful completion commits
staged changes; errors, cancellation and resource-limit failures preserve the
previous state. Completed tool effects are never rolled back or replayed.
Identical programs and nested tool calls may run again to advance checkpoints,
observe changed state, or retry a failed operation. Repetition alone does not
block execution; each call still passes its normal permission and resource checks.

State is explicitly stored, memory-only, and isolated by conversation, actor and
workspace. Rebuilding the same conversation or changing its model retains that
scope; a fork, another actor or another workspace cannot read it. Restarting the
runtime clears it. Archiving retains it. Permanent deletion releases the handling
core's state for that conversation, its workers and attached side chats. Do not
store credentials. Each scope supports 256 keys and
1 MiB of JSON; the shared session runtime supports 64 retained scopes and 16 MiB.
Overlapping programs in one scope are rejected. Limits fail visibly without
silent eviction; use `remove` or overwrite a checkpoint to reclaim space.

Programs have no default total timeout. A positive `timeout_ms` limits elapsed
time, including tool and approval waits; an earlier calling-context deadline
still applies. Cancellation stops the interpreter and its active nested calls.
There is no JavaScript `yield`/`wait` continuation or saved execution stack.
For long commands, use `bash` with `run_in_background`, keep returned process
IDs in `store`, and read, write or stop them through `process` in later programs.
`process` supports bounded waits for new output, and managed completion retains
its normal conversation wake-up behavior. Agent/session controls similarly use
their own durable task handles. Await every nested call before returning;
unawaited calls are cancelled when their program ends.

### Isolation, limits and installation

The interpreter has no native APIs, imports, filesystem, network, environment or
timers, including in Unconfined mode. It has a 128 MiB memory limit; the host
process keeps the session process sandbox as defense in depth. See the
[security model](../reference/security-model.md).

Printed/returned text is limited to 1 MiB; media obeys shared rich-result limits.
Discovery returns at most 20 summaries per page. Exact tool details are limited
to 256 KiB and fail visibly rather than returning partial schemas. A catalog
supports 10,000 tools and 32 MiB of metadata. Catalogs are pinned for each model
run; dispatch still checks live permissions and availability. Remote execution
requires protocol version 3; upgrade older workers before using them.

Desktop includes its runtime. CLI ordinary tools require Node.js 22.19 or later
on `PATH`, or `ptc.node_executable` in user configuration. Missing or unsupported
runtimes fail visibly; there is no silent switch to another execution model.
To choose direct tool calls instead, explicitly disable PTC globally or for a
family, for example:

```json
{
  "ptc": {
    "enabled": true,
    "families": { "local": false }
  }
}
```

Normal project configuration cannot change these settings or the executable.
The retired `code_mode` field is ignored and removed when PTC settings are
saved; it cannot reactivate a broader runtime or restore old execution cells.

## Large tool results

Text file reads use `NUMBER|CONTENT` on the first displayed line and on file lines divisible by ten; other lines use `|CONTENT`. Everything after the first `|` is source text, including indentation and literal pipes. Each continuation page starts with its own line-number anchor, and range metadata still identifies every displayed line.

For files using CRLF throughout, `edit_file` accepts LF excerpts copied from these reads and preserves CRLF on replacement lines. Text and indentation must still match exactly, with a unique match unless `replace_all` is requested. Files with mixed line endings require exact bytes and are not normalized.

Wuu keeps the original tool result and gives the model a stable, bounded view. Large ordinary text results show a continuous first page with a `read_file` continuation; following it reads the saved result without running the original tool again. Pages prefer complete lines and can split a long line without breaking Unicode characters. Continuations reject changed content rather than silently mixing versions. Images and other supported media retain their separate provider representation.

Built-in views preserve useful structure: search pages keep whole records and snapshot cursors, and shell output prioritizes recent error evidence. Results are settled before the tool ledger records them, including extension results and execution errors, so later requests and replay keep the same view. Wuu does not cut these pages again to fit a batch-wide text limit; conversation capacity remains the responsibility of context management. Paging can require extra model requests, and Wuu retains the full result if it cannot safely save or page it. Smaller pages are not a guarantee of lower total cost.

## Check a failed connection

“Credentials configured” means a credential is available locally, not that the provider has accepted it. Check the selected provider, endpoint, model ID, and account access. An environment variable must be visible to the process launching Wuu; a desktop app opened from the Dock may not inherit variables set in a terminal. You can also save an API key in Settings.

If text replies work but tools fail, check the service's support for tool calling and streaming. A compatible API format alone does not establish that every model supports the same capabilities.

Prompts, selected context, attachments, and tool results can leave your machine through the configured endpoint. The provider's pricing and data policies apply; with a gateway, the gateway receives those requests. Keep real API keys out of project files and Git history.

## Fast mode in the model popover

For supported provider/model pairs, the compact lightning button in the popover header changes processing speed without changing the model or reasoning effort. Speed is saved with the conversation and the draft selection. Reset inherits provider options; an explicit off overrides an accelerated default. The same controls are available through `/fast on`, `/fast off`, and `/fast status`.

Wuu recognizes catalog aliases that share an API model and declare acceleration options. Existing `-fast` model selections continue working. Custom services can explicitly declare `providers.<provider>.models.<model>.fast_mode: true` once their endpoint supports the corresponding protocol; `false` hides inferred support. OpenAI-compatible requests use `service_tier: "priority"` / `"default"`. Anthropic requests use `speed: "fast"` / `"standard"`, with the required Fast mode beta header only when enabled. The direct Anthropic API capability follows its [documented supported models](https://platform.claude.com/docs/en/build-with-claude/fast-mode); compatible endpoints must declare support. This setting does not turn a reasoning level into a speed tier.
