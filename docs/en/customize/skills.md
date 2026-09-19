# Skills

A skill is a reusable workflow written in Markdown. Use one for recurring work such as reviewing a change, diagnosing a desktop issue, or checking a release. It gives the agent a task-specific procedure; it does not guarantee a correct result or grant extra permissions.

## Find and use a skill

In the desktop composer, enter `/skills` to open the catalog. Search the discovered skills, inspect their source, and use **Try now** for a user-invocable skill. Skills also appear in the slash menu, where you can pass arguments after the name:

```text
/release-check 2026.9.1
```

The Wuu engine gives the model a catalog of skill names and descriptions. When a task matches, it can use `load_skill` to read the full workflow. A missing description or `disable-model-invocation: true` keeps a skill out of that automatic-selection catalog. `user-invocable` controls whether it is offered for direct user invocation.

Availability depends on the workspace, discovered sources, and current tool surface. A skill that requires unavailable tools may be filtered out. Refresh the catalog after installing or editing one, and inspect the source path if the wrong version appears.

## Trust the workflow before using it

Read a third-party skill and its supporting scripts before enabling its workflow. The normal `load_skill` path substitutes arguments and loads instructions; it does not run inline shell expressions or code fences at load time. The instructions can still ask the agent to run commands or access the network afterward, subject to the session's tool and permission boundaries.

Project instructions such as `AGENTS.md` apply continuously within their scope. A skill is loaded for a particular workflow, while [memory](memory.md) stores information worth keeping across tasks.

## Create or install one

Place a skill in a project to share it with contributors, or in a user skills directory to reuse it across projects. The [authoring guide](skill-authoring.md) covers discovery order, supported metadata, arguments, and validation.

```bash
wuu skills lint path/to/skill
wuu skills lint --json path/to/skills-root
```

Lint checks whether the structure and metadata can be loaded. Review the workflow's behavior with a low-risk task as well.
