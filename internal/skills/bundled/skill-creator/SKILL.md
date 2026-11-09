---
name: skill-creator
description: Guide for creating effective skills. Use when the user wants to create a new skill or improve an existing one — a reusable instruction package that extends Wuu with specialized knowledge, workflows, or tool integrations.
user-invocable: true
trigger-condition: When the user asks to create, write, package, or improve a skill, or asks how to teach Wuu a repeatable workflow.
allowed-tools: [read_file, write_file, edit_file, bash]
examples: [create a skill for our release checklist, turn this PDF-rotation workflow into a skill, improve the description of my deploy skill so it triggers reliably]
verification-checklist: [SKILL.md exists at the agreed location, wuu skills lint reports no errors, description states both what the skill does and when to use it, bundled scripts were executed at least once]
progressive-disclosure: Read references/frontmatter.md only when the user needs fields beyond name and description, or asks for Wuu-specific behavior.
---

# Skill Creator

Guidance for creating effective skills.

## About Skills

Skills are modular, self-contained folders that extend Wuu with specialized
knowledge, workflows, and tools. Think of them as onboarding guides for a
specific domain or task: they turn a general-purpose agent into one equipped
with procedural knowledge no model fully possesses.

A skill provides one or more of:

1. Specialized workflows — multi-step procedures for a specific domain
2. Tool integrations — instructions for specific file formats or APIs
3. Domain expertise — company-specific knowledge, schemas, business logic
4. Bundled resources — scripts, references, and assets for repetitive tasks

## Core Principles

### Concise is key

The context window is a public good. A skill shares it with the system prompt,
the conversation, every other skill's metadata, and the actual user request.

Default assumption: the model is already very smart. Only add context it does
not already have. Challenge every paragraph: "does this justify its token
cost?" Prefer concise examples over verbose explanations.

### Set appropriate degrees of freedom

Match specificity to how fragile the task is:

- **High freedom (prose instructions)** — multiple approaches are valid and
  context determines the best one.
- **Medium freedom (pseudocode or parameterized scripts)** — a preferred
  pattern exists; some variation is acceptable.
- **Low freedom (exact scripts, few parameters)** — the operation is fragile,
  consistency is critical, or one exact sequence must be followed.

A narrow bridge with cliffs needs guardrails; an open field allows many routes.

### Anatomy of a skill

```
skill-name/
├── SKILL.md          (required: YAML frontmatter + markdown instructions)
├── scripts/          (optional: executable code for deterministic steps)
├── references/       (optional: docs loaded into context only when needed)
└── assets/           (optional: files used in output — templates, fonts, icons)
```

- **SKILL.md frontmatter** needs `name` and `description`. The description is
  the primary trigger: it is the only part always visible to the model, so it
  must state both what the skill does and when to use it. Do not add other
  fields by default; when the user needs invocation control, tool limits, or
  Wuu-specific behavior, read [references/frontmatter.md](references/frontmatter.md)
  first.
- **SKILL.md body** is loaded only after the skill triggers. Keep it under 500
  lines; split details into references/ when approaching that limit.
- **scripts/** — for steps that are rewritten repeatedly or must be
  deterministic. Test every script by running it before shipping the skill.
- **references/** — schemas, API docs, policies, detailed guides. Link each
  file from SKILL.md and say when to read it. Keep references one level deep.
  For files over ~100 lines, start with a table of contents.
- **assets/** — never loaded into context; copied or filled into the output.

Information lives in either SKILL.md or a reference file, never both.

### What not to include

No README, INSTALLATION_GUIDE, CHANGELOG, or other auxiliary documentation. A
skill contains only what an agent needs to do the job — not the story of how
the skill was made, setup notes for humans, or duplicated summaries.

### Progressive disclosure

Skills load in three levels, so cost scales with use:

1. **Metadata (name + description)** — always in context (~100 words)
2. **SKILL.md body** — loaded when the skill triggers (<5k words)
3. **Bundled resources** — read or executed only as needed (unbounded; scripts
   can run without ever entering context)

When a skill covers multiple domains, frameworks, or variants, keep the core
workflow and selection guidance in SKILL.md and move variant details into one
reference file per variant, so choosing one variant never loads the others.

## Where to Create the Skill

Ask where the user wants the skill. If they have no preference, decide by
scope and dialect:

| Situation | Location |
|---|---|
| Project skill, portable frontmatter only | `.agents/skills/<name>/` |
| Project skill using Wuu-specific fields | `.wuu/skills/<name>/` |
| Personal skill for all projects | `~/.wuu/skills/<name>/` |

Portable-only skills stay usable by other tools that read the same format;
that is why portable frontmatter and the neutral `.agents/skills/` location
are the default. Use the Wuu dialect and `.wuu/skills/` only when the skill
genuinely needs Wuu-specific fields (see references/frontmatter.md). Never
write Wuu-specific fields into skills stored in shared ecosystem directories.

## Skill Naming

- Lowercase letters, digits, and single hyphens only; at most 64 characters.
  Normalize titles to hyphen-case ("Plan Mode" → `plan-mode`).
- Prefer short, verb-led names describing the action (`rotate-pdf`,
  `gh-address-comments`). Namespace by tool when it helps triggering.
- Name the folder exactly after the skill; the folder name is canonical.

## Creation Process

Follow these steps in order, skipping one only with a clear reason.

### 1. Understand the skill through concrete examples

Ask for concrete usage examples: "What would a user say that should trigger
this skill?" "Can you walk me through one real run of this workflow?"
Generate candidate examples and confirm them if the user has none ready.
Avoid overwhelming the user — start with the most important question.
Conclude when the functionality the skill must support is clear.

### 2. Plan the reusable contents

For each example, work out how it would be executed from scratch, then note
what would make repeat executions cheaper or more reliable:

- code rewritten every time → a `scripts/` entry
- knowledge rediscovered every time (schemas, endpoints, policies) → a
  `references/` file
- boilerplate recreated every time (templates, fixtures) → an `assets/` entry

### 3. Create the skill

Create the folder at the agreed location with SKILL.md and only the resource
directories the plan actually needs.

### 4. Implement

Start with the reusable resources, then write SKILL.md last — by then the
real shape of the workflow is known. Write the body in imperative form.

The frontmatter needs `name` and `description`. Put all "when to use this"
information in the description, not the body: the body is only visible after
the skill has already triggered. A good description names the capability and
the trigger contexts, e.g. "Extracts and edits PDF text and forms. Use when
the user asks to read, split, fill, or rotate PDF files."

Run every script at least once. If several scripts are near-identical, test a
representative sample. Delete placeholder files the skill does not need.

### 5. Validate

```
wuu skills lint <path/to/skill-folder>
```

Lint applies the same rules discovery uses: an error means the skill will be
dropped or lose metadata at load time; warnings flag fields that will not
behave as written. Fix everything it reports and rerun until clean.

### 6. Forward-test, then iterate

For any nontrivial skill, verify it works for an agent that has none of this
conversation's context. When sub-agent spawning is available, launch a
general-purpose sub-agent with a prompt shaped like a real user request:

```
Use the <skill-name> skill at <path> to <realistic task>.
```

Rules for a honest test:

- Phrase the prompt as a task, not as a review of the skill.
- Pass raw artifacts (sample inputs, logs), never your conclusions, the
  expected answer, or the intended fix.
- Use a fresh sub-agent per pass and clean up artifacts between passes so
  later runs cannot crib from earlier ones.
- If the test only succeeds when extra context is leaked, the skill body is
  incomplete — fix the skill, not the prompt.

Ask before forward-testing when a run could take long, need approvals, or
touch live systems.

After real use, fold what was learned back in: where the agent struggled,
tighten instructions or add a script; where it ignored a reference, cut it.
