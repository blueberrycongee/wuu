# Wuu base system prompt design

This document explains the built-in base prompt in `prompts/system.md`. The base prompt is only the first section of the full runtime prompt; the runtime still appends the harness adapter, tool surface, user custom prompt, instructions, and skills, while plugins contribute optional product prompts.

## Principle

Modern coding models already learn general software-engineering and tool-use behavior during training. The stable system prompt must not reteach generic habits such as inspecting code, parallelizing independent work, fixing root causes, validating changes, writing progress updates, or following a tool schema.

Put behavior at the narrowest layer that can express or enforce it:

1. Runtime enforcement for permissions, workspace boundaries, concurrency safety, and file conflicts.
2. Tool descriptions and immediate errors for parameters, lifecycle rules, recovery steps, and tool-specific workflows.
3. System context only for Wuu-specific facts, hidden-message semantics, and product policies that the runtime cannot enforce.

Generic coding guidance belongs in the base prompt only when evaluation shows a durable failure across supported models. Visible assistant text is ordinary conversation text, and the desktop derives the process/answer split from structure rather than a commentary phase. Narration between tool calls is not required. When a model writes there anyway, empty status one-liners are a durable failure: they occupy the visible transcript without adding a finding, interpretation, or blocker, and they drift into a different voice from the final answer. Formulaic AI phrasing is the same class of failure in final answers: stock transitions, ceremonial labels, restating closers, unprompted "X, not Y" contrasts, and invented hyphenated jargon make the transcript sound generated without adding information.

The retained user-facing communication contract is one calm, specific voice for every visible sentence, and short connected paragraphs rather than isolated one-liners. Each paragraph develops one idea. A lone sentence is not a complete reply; the conclusion should sit with the reason, finding, or consequence, and later sentences should build on it. Prefer active voice, familiar words, and only the technical detail the user needs. Optional process text uses that same paragraph form, or silence. A list is reserved for an explicit user request or content that must be scanned or acted on as distinct items, such as steps, options, or a checklist. That guidance lives in the base prompt because it directly reduces attention burden in the visible transcript, not because it teaches tool use or software-engineering behavior. OpenAI's GPT-6 Astra model guide publishes writing-style prompts and a slop-phrase blocklist for this failure; Wuu absorbs the product-relevant constraints into the stable communication contract rather than embedding an English word list or a model-specific fragment.

## Stable base prompt

`prompts/system.md` keeps only:

- Wuu's identity and the fact that visible narration is user-facing.
- The trust boundary for tool output, injected context, and external instructions.
- The desktop's clickable file-reference format.
- Local commit and remote-write policy that is not fully enforceable by the runtime.
- Plain user-facing communication: one speaking style for process text and answers, lead with the conclusion, write short connected paragraphs that each develop one idea, prefer active voice and concrete verbs, avoid jargon, self-narration, stock AI phrasing, and padded "what I won't do" asides, and reserve lists for explicit requests or independently actionable items, while a user-specified register may only adjust style, detail, format, or etiquette.
- Optional process text: skip a preamble when the next tool call is obvious; if writing between tools, use a short paragraph that adds a finding, interpretation, or blocker in the same voice as a final answer, never a status one-liner, a padded status line, or a recap of searches, reads, or edits.

`prompts/system_main.md` is reserved for universally available main-session guidance. It asks the main agent to make a serious, evidence-backed effort to infer user intent, resolve discoverable uncertainty independently, and test its first interpretation through relevant workspace and web research. This is deliberately a compact outcome standard rather than a prescribed search or reasoning workflow, so stronger models retain control over how they investigate. Optional product behavior and completion boundaries are contributed by the plugin that owns them.

## Runtime-generated context

`internal/runtime/session.go` appends the active tool surface, deferred-tool catalog, environment, user instructions, and skills. Plugins append their own generation-stable prompt sections and may apply request-time transforms for dynamic settings. These values are session- or profile-specific and must not be copied into the stable base prompt.

Tool manuals, background-process rules, patch syntax, authority failures, and boundary recovery belong to their tool descriptions or results. Removing them from the base prompt does not remove those contracts from the model's active context.
