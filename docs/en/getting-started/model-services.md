# Connecting a model service

Choose a provider, then a model it offers. The provider configuration holds the
endpoint and credentials; the model name is an ID that provider accepts. One
provider can have several configured models.

Prompts, relevant files, attachments, and tool results may be sent to your chosen
provider. Its pricing and data policies apply. With a gateway, data goes to the
configured gateway endpoint. Do not put real API Keys in project files or commit them to Git.

## Configure the desktop app

1. Open **Settings → Model providers → Add provider**.
2. Choose **OpenAI-compatible** or **Anthropic-compatible** to match the provider's API.
3. Enter a provider identifier, model name, API endpoint, and API Key, then choose **Add provider**.
4. Return to the conversation, check the selected provider and model, and send a small task to test replies and tool calls.

OpenAI and OpenRouter can use the OpenAI-compatible type; Anthropic uses the
Anthropic-compatible type. For a gateway or local service, match its protocol and
include any required API prefix in the endpoint, such as `/v1`. You can add or
switch models within an existing provider without entering its credentials again.

## Use a subscription login

- **Codex subscription:** sign in with Codex CLI, then choose to reuse that login
  during Wuu's first-run setup, or enable `reuse_codex_credentials` in the
  `openai-codex` provider configuration. The desktop cannot start OpenAI OAuth login directly.
- **xAI SuperGrok:** choose **xAI SuperGrok** when adding a provider and follow the
  login prompts. In the CLI, run `wuu login xai`, then use `--provider xai-subscription`
  for tasks. This connection uses xAI subscription login, separately from Grok CLI
  login and `XAI_API_KEY`.
- **Grok Build:** run `grok login`. The desktop shows the provider when it finds a
  usable local login; select it directly. In the CLI, use `--provider grok-build`.
  Run `grok login` again when it expires; Wuu does not modify or refresh Grok CLI credentials.

These connections use Wuu's agent to run tasks. File edits and command execution
require both the model and the service to support tool calling.

## Configure the CLI

Generate a user configuration on first use:

```bash
wuu init
```

The configuration is written to `~/.wuu/config.json` by default, or to
`$WUU_HOME/config.json` when `WUU_HOME` is set. Edit an existing configuration
directly; `wuu init --force` overwrites it.

Under `providers`, check your provider's `base_url`, `model`, and `api_key_env`.
The initial default provider is `openai`. Check that your account can use the
example model, then set the environment variable named by `api_key_env`:

```bash
export OPENAI_API_KEY="your API Key"
cd /path/to/your/project
wuu exec --provider openai --permission-mode read_only "read this project and explain how to run its tests"
```

`--provider` selects a configured provider identifier; `--model` overrides the model
for this run. On normal startup, project configuration cannot replace user-owned
endpoints, credentials, or permission modes. See [configuration](../reference/configuration.md)
for the detailed rules.

## Troubleshoot connection problems

"Credentials configured" means Wuu can read a credential, not that the provider
accepts it. For a missing API Key, check the selected provider and that the variable
named by `api_key_env` has a value. The desktop must be launched from a process that
can read that variable; you can also save an API Key directly in Settings.

If the model is not found, check its ID and your account's access. If chat works but
tools do not, check that the model and gateway support tool calling and streaming.

After connecting a model service, continue to [complete your first task](first-task.md).
