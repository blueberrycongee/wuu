# Writing and installing skills

A skill normally consists of a directory with `SKILL.md` and any supporting scripts, templates, or references. Its description helps the model decide when to load the full body.

## Write a minimal skill

Create `.wuu/skills/release-check/SKILL.md`:

```markdown
---
name: release-check
description: Check the version, build, and release notes before a release.
argument-hint: "[version]"
---

# Release check

Read the project's release instructions and version files.
Check that the intended version is ${ARGUMENTS}.
Run the required release checks and report their results.
Do not create tags or publish artifacts.
```

The directory name is the discovered name. For portable skills, use 1–64 lowercase letters, digits, and single hyphens, with no leading or trailing hyphen. Write a description that says when the skill applies, and a body that makes its inputs, actions, and expected evidence clear.

## Discovery and overrides

Project discovery follows the directory chain from the repository root to the current working directory. A closer directory overrides an ancestor. At each level, roots are checked in this order, with later entries winning for the same name:

1. `.claude/skills/`
2. `.agents/skills/`
3. `.opencode/skill/`
4. `.opencode/skills/`
5. `.wuu/skills/`

User roots also use later-wins order: `~/.codex/skills/`, `~/.claude/skills/`, `~/.agents/skills/`, `~/.config/opencode/skills/`, then `skills/` under Wuu's home directory. The last path is normally `~/.wuu/skills/` and follows `WUU_HOME` when set.

Project skills override user skills. Disk definitions override same-named bundled skills. Enabled plugin packages can contribute skills as well; ordinary discovered skills at the corresponding scope take precedence over those package entries.

Each root accepts `<name>/SKILL.md` and flat `<name>.md` files. Directory form is easier to share with supporting resources. Wuu also adapts `.claude/commands/*.md` and `.wuu/commands/*.md` along the project chain, plus commands under Wuu's user home. Native skills win over same-named command templates.

## Metadata

| Field | Current behavior |
|---|---|
| `name` | Declared name; the directory name wins in directory form |
| `description` | Summary for model selection; an empty value hides the skill from that catalog |
| `argument-hint` | Hint displayed with the invocation |
| `user-invocable` | Offer direct user invocation; defaults to `true` |
| `disable-model-invocation` | Hide from automatic model selection when `true` |
| `allowed-tools` | Declare required tools for compatibility filtering; not a permission grant |
| `when-to-use`, `trigger` | Additional timing metadata |
| `required-context`, `examples`, `verification-checklist` | Additional workflow metadata |
| `progressive-disclosure`, `version` | Compatibility metadata |

The parser accepts `model`, `context`, `agent`, `effort`, `paths`, and `hooks` for compatibility, but loading a skill does not switch models, fork context, spawn an agent, change effort, activate by path, or register hooks. Lint warns when these fields promise unsupported behavior. `shell` is parsed too, but does not enable inline execution.

## Arguments and resources

The body supports `${ARGUMENTS}`, `${CLAUDE_SKILL_DIR}`, and `${CLAUDE_SESSION_ID}`. Wuu substitutes the invocation arguments, skill directory, and current session ID. The load result includes the base directory and a sample of resource filenames; resolve relative resource paths from that directory.

Inline shell expressions and code fences remain text during normal loading. If a workflow needs dynamic information, ask the agent to obtain it through an available tool, so the usual permission checks apply.

## Install and validate

Inspect downloaded `SKILL.md` files and their supporting resources before copying the whole directory into a discovered root. Check especially for command execution, network access, credential handling, and paths outside the project. Wuu does not require a build or package manifest for a local skill.

```bash
wuu skills lint .wuu/skills/release-check
wuu skills lint --json .wuu/skills
```

Lint accepts a skill directory, a root containing skills, or a flat Markdown file. Errors mean discovery cannot use the file or its metadata and cause a nonzero exit. Warnings describe a skill that loads but may not behave as intended.

Refresh the desktop catalog, preview the loaded source, and try a low-risk task. If the skill is missing, check its frontmatter and required tools. If the model never chooses it, check its description and invocation flags. If the wrong version loads, inspect same-name overrides. Structural validation does not establish that the workflow is safe or effective.
