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

## Use an existing subscription

| Connection | Setup |
|---|---|
| Codex subscription | Sign in with Codex CLI, then choose to reuse the detected login during first-run setup. In configuration, use the `openai-codex` provider with `reuse_codex_credentials`. Wuu's desktop does not start OpenAI OAuth login itself. |
| xAI SuperGrok | Add an **xAI SuperGrok** provider and follow the browser login. For the CLI, run `wuu login xai` and select `--provider xai-subscription`. |
| Grok Build | Run `grok login`, then select the detected provider in Wuu or pass `--provider grok-build`. If the login expires, sign in again with Grok CLI; Wuu does not refresh or modify those credentials. |

SuperGrok subscription login, Grok CLI login, and an `XAI_API_KEY` are separate credential sources. Use the connection that matches your account. File editing and command execution also require the selected service and model to support tool calling.

## Configure the CLI

Create a user configuration once:

```bash
wuu init
```

The file is `~/.wuu/config.json`, or `$WUU_HOME/config.json` when `WUU_HOME` is set. Edit an existing file instead of running `wuu init --force`, which overwrites it.

Under `providers`, check `base_url`, `model`, and `api_key_env`. The generated configuration initially selects `openai`; replace its example model if your account needs a different one. Set the named environment variable before running:

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

## Large tool results

Wuu keeps the original tool result and gives the model a stable, bounded view. Large ordinary text results show a continuous first page with a `read_file` continuation; following it reads the saved result without running the original tool again. Pages prefer complete lines and can split a long line without breaking Unicode characters. Continuations reject changed content rather than silently mixing versions. Images and other supported media retain their separate provider representation.

Built-in views preserve useful structure: search pages keep whole records and snapshot cursors, and shell output prioritizes recent error evidence. Results are settled before the tool ledger records them, including extension results and execution errors, so later requests and replay keep the same view. Wuu does not cut these pages again to fit a batch-wide text limit; conversation capacity remains the responsibility of context management. Paging can require extra model requests, and Wuu retains the full result if it cannot safely save or page it. Smaller pages are not a guarantee of lower total cost.

## Check a failed connection

“Credentials configured” means a credential is available locally, not that the provider has accepted it. Check the selected provider, endpoint, model ID, and account access. An environment variable must be visible to the process launching Wuu; a desktop app opened from the Dock may not inherit variables set in a terminal. You can also save an API key in Settings.

If text replies work but tools fail, check the service's support for tool calling and streaming. A compatible API format alone does not establish that every model supports the same capabilities.

Prompts, selected context, attachments, and tool results can leave your machine through the configured endpoint. The provider's pricing and data policies apply; with a gateway, the gateway receives those requests. Keep real API keys out of project files and Git history.

## Auto model selection

In **Settings → Model services → Auto**, choose a classifier and execution models for simple, medium, and complex tasks. Each choice refers to an existing provider and model, with optional reasoning settings. The choices may use different services. Enable Auto, then select **Auto** in a Wuu conversation's model menu, or make it the default for new conversations.

Each new task first calls the classifier with bounded recent dialogue, the initial objective, and attachment counts. This adds latency and billable usage. The classifier cannot run tools. Its result selects one of the three configured tiers; it cannot select an arbitrary model. The actual provider, model, tier, classifier latency, usage, and fallback reason appear on the turn. The conversation keeps its Auto selection.

A classifier failure, timeout (20 seconds), or invalid response selects the medium tier. If the selected tier cannot accept the input or tools, Wuu tries the other configured tiers; if none fits, the task fails explicitly. Unknown catalog capabilities remain subject to provider validation. Older history uses normal context compaction; the current request is not silently removed to fit a smaller model.

Tool calls and continuations keep the selected execution model. A persisted decision is reused when the same turn resumes. New tasks use the latest saved Auto configuration; changing it does not switch a task already running. Providers and models referenced by enabled Auto must be reassigned or Auto disabled before removal. Disabling Auto does not silently convert existing Auto conversations to a fixed model.

Auto is a Wuu conversation selection, including Wuu named-agent sessions. External engines manage their own model execution and cannot use this setting. It is not an upstream model name or a CLI model override. Side conversations and workers inherit execution defaults; explicitly configured worker models continue to take precedence.
