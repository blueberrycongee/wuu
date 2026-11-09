# Skill Frontmatter Reference

Two tiers of frontmatter fields exist: a portable core that keeps a skill
usable by any tool reading this format, and Wuu-specific extensions. Author in
the portable core by default; reach for extensions only when the skill needs
them, and store dialect skills under `.wuu/skills/` (see "Where to Create the
Skill" in SKILL.md).

## Portable core

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Skill name. For directory-format skills the folder name is canonical and overrides this field; keep them identical. |
| `description` | string | The primary trigger. Always visible to the model; must state what the skill does and when to use it. A skill with an empty description is hidden from the model catalog. |
| `argument-hint` | string | Gray placeholder shown after `/<name>` in the input, e.g. `[pr-number]`. |
| `user-invocable` | bool | Whether typing `/<name>` invokes the skill. Default `true`. |
| `disable-model-invocation` | bool | Hide the skill from the model catalog so only the user can invoke it. Default `false`. |
| `allowed-tools` | list | Tools the skill's instructions rely on. Wuu hides the skill on tool surfaces that lack one of them, so declare only genuine requirements. |
| `shell` | string | Shell for inline `` `!cmd` `` execution in the body. Default `sh`. |

`license` and `metadata` are also accepted as inert informational fields; the
runtime ignores their values.

Body placeholders: `${ARGUMENTS}` is replaced with the text the user typed
after `/<name>`. Inline `` `!cmd` `` spans and ```` ```! ```` blocks execute at
load time and are replaced with their output — keep them fast and read-only.

## Wuu extensions (goal-engineering fields)

These fields are surfaced in Wuu's skill listings and UI to make a skill's
contract explicit. Other tools ignore them, so a skill using them belongs in
`.wuu/skills/`.

| Field | Type | Meaning |
|---|---|---|
| `when-to-use` | string | Longer usage guidance than fits in `description`. |
| `trigger-condition` | string | The user situation that should activate the skill. |
| `required-context` | list | Facts the agent must gather before acting. |
| `examples` | list | Short example invocations. |
| `verification-checklist` | list | Checks that prove the skill's job is done. |
| `progressive-disclosure` | string | Reading order: what to load first and what to defer. |
| `version` | string | Informational version string. |

## Accepted but not executed

The parser accepts these fields, and `wuu skills lint` warns on them, because
the current runtime does not act on them — a skill relying on one behaves
differently than its frontmatter promises. Do not use them until the runtime
executes them:

| Field | Declared intent | Actual behavior today |
|---|---|---|
| `model` | run the skill on a specific model | body runs on the session model |
| `context: fork` | run the body in a sub-agent | body loads inline into the current context |
| `agent` | sub-agent type for `context: fork` | no sub-agent is spawned |
| `effort` | reasoning effort override | session effort is unchanged |
| `paths` | activate only for matching paths | no conditional activation |
| `hooks` | register lifecycle hooks | hooks are not registered |
