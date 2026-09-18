import { afterEach, describe, expect, it } from "vitest";

import {
  buildComposerSlashCommands,
  buildSideThreadSlashCommands,
  composerSlashPrompt,
  filterComposerSlashCommands,
  runtimeFastModelTarget
} from "./ComposerSlashCommands";
import type { InitializeResult, SkillSummary } from "../shared/protocol";
import { setActiveLocale } from "./i18n";

afterEach(() => setActiveLocale("zh-CN"));

function initialized(model: string, models: string[]): InitializeResult {
  return {
    protocol_version: "1",
    workspace_root: "/repo",
    provider: "openai",
    model,
    effort: "",
    variant: "",
    features: {},
    providers: [
      {
        name: "openai",
        type: "openai",
        model,
        models: models.map((id) => ({ id }))
      }
    ]
  };
}

function skill(overrides: Partial<SkillSummary> & Pick<SkillSummary, "name">): SkillSummary {
  return {
    name: overrides.name,
    source: overrides.source ?? "bundled",
    user_invocable: overrides.user_invocable ?? true,
    disable_model_invoke: overrides.disable_model_invoke ?? false,
    description: overrides.description,
    when_to_use: overrides.when_to_use,
    trigger_condition: overrides.trigger_condition,
    argument_hint: overrides.argument_hint,
    model: overrides.model,
    context: overrides.context,
    agent: overrides.agent,
    paths: overrides.paths,
    examples: overrides.examples
  };
}

describe("composer slash commands", () => {
  it("rebuilds built-in and side-thread commands in the active language", () => {
    setActiveLocale("en-US");

    const commands = buildComposerSlashCommands({ running: false });
    const review = commands.find((command) => command.name === "review");
    const sideReset = buildSideThreadSlashCommands()[0];

    expect(review).toMatchObject({
      title: "Review current changes",
      disabledReason: "Select a workspace first",
    });
    expect(sideReset).toMatchObject({
      title: "Reset side chat",
      tag: "Side chat",
    });
  });
  it("shows /fast only when the current provider exposes a fast model", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5", "gpt-5.5-fast"]),
      running: false
    });

    expect(filterComposerSlashCommands(commands, "fast").map((command) => command.name)).toEqual(["fast"]);
    expect(runtimeFastModelTarget(initialized("gpt-5.5", ["gpt-5.5", "gpt-5.5-fast"]))).toEqual({
      provider: "openai",
      model: "gpt-5.5-fast",
      current: false
    });
  });

  it("hides /fast when the current provider has no fast model", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("mimo-v2.5-pro", ["mimo-v2.5-pro"]),
      running: false
    });

    expect(filterComposerSlashCommands(commands, "fast")).toEqual([]);
    expect(runtimeFastModelTarget(initialized("mimo-v2.5-pro", ["mimo-v2.5-pro"]))).toBeUndefined();
  });

  it("keeps /fast visible but disabled when already in fast mode", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5-fast", ["gpt-5.5", "gpt-5.5-fast"]),
      running: false
    });
    const fast = filterComposerSlashCommands(commands, "fast")[0];

    expect(fast?.disabledReason).toBe("当前已是快速模式");
    expect(runtimeFastModelTarget(initialized("gpt-5.5-fast", ["gpt-5.5", "gpt-5.5-fast"]))).toEqual({
      provider: "openai",
      model: "gpt-5.5-fast",
      current: true
    });
  });

  it("does not reserve a built-in /memory command for the plugin-owned view", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false
    });

    const memory = commands.find((command) => command.name === "memory");
    expect(memory).toBeUndefined();

    const instructions = commands.find((command) => command.name === "instructions");
    expect(instructions?.action).toBe("instructions");
    expect(instructions?.aliases).not.toContain("memory");
  });

  it("adds user-invocable skills to slash results", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      skills: [
        skill({
          name: "slides",
          description: "Create slide decks",
          argument_hint: "topic"
        }),
        skill({
          name: "internal-only",
          user_invocable: false
        })
      ]
    });

    const visible = filterComposerSlashCommands(commands, "slides");

    expect(visible.map((command) => command.name)).toEqual(["slides"]);
    expect(visible[0]?.kind).toBe("skill");
    expect(composerSlashPrompt(visible[0]!, "")).toBe("/slides ");
    expect(composerSlashPrompt(visible[0]!, "quarterly roadmap")).toBe("/slides quarterly roadmap");
    expect(filterComposerSlashCommands(commands, "internal-only")).toEqual([]);
  });

  it("keeps skills discoverable in the unsearched default view", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      skills: [
        skill({ name: "project-skill", source: "project" }),
        skill({ name: "user-skill", source: "user" }),
        skill({ name: "bundled-skill", source: "bundled" }),
        skill({ name: "extra-skill", source: "bundled" })
      ]
    });

    const visible = filterComposerSlashCommands(commands, "");
    const visibleSkills = visible.filter((command) => command.kind === "skill");

    // Skills are appended after the built-ins, so a flat limit would always
    // truncate them away before the user types anything.
    expect(visibleSkills.map((command) => command.name)).toEqual([
      "project-skill",
      "user-skill",
      "bundled-skill"
    ]);
    expect(visible.filter((command) => command.kind !== "skill")).toHaveLength(8);
  });

  it("keeps the default view stable when no skills are installed", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false
    });

    expect(filterComposerSlashCommands(commands, "")).toEqual(commands.slice(0, 8));
  });

  it("summarizes a skill with its own description", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      skills: [skill({ name: "slides", description: "Create slide decks" })]
    });

    const slides = filterComposerSlashCommands(commands, "slides")[0];

    // The row renders `/name` from `name`, so the title carries the summary
    // rather than repeating the command.
    expect(slides?.name).toBe("slides");
    expect(slides?.title).toBe("Create slide decks");
  });

  it("falls back to when_to_use when a skill has no description", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      skills: [skill({ name: "release-check", when_to_use: "Before a release" })]
    });

    expect(commands.find((command) => command.name === "release-check")?.title).toBe(
      "Before a release"
    );
  });

  it("names /skills after the catalog it opens", () => {
    setActiveLocale("en-US");

    const skills = buildComposerSlashCommands({ running: false }).find((command) => command.name === "skills");

    expect(skills?.action).toBe("open-skills");
    expect(skills?.title).toBe("Open Skills catalog");
  });

  it("adds /compact as a local action instead of a model prompt", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false
    });

    const compact = filterComposerSlashCommands(commands, "compact")[0];

    expect(compact?.kind).toBe("action");
    expect(compact?.action).toBe("compact");
    expect(compact?.disabledReason).toBeUndefined();
  });

  it("disables /side until the draft has a persisted thread", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      sideThreadDisabledReason: "先发送一条消息"
    });

    const side = filterComposerSlashCommands(commands, "side")[0];
    expect(side?.action).toBe("open-side-thread");
    expect(side?.argumentHint).toBe("提示词");
    expect(side?.disabledReason).toBe("先发送一条消息");
  });

  it("can disable /compact for surfaces without a compactable model context", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false,
      compactDisabledReason: "群聊暂不支持上下文压缩"
    });

    const compact = filterComposerSlashCommands(commands, "compact")[0];

    expect(compact?.kind).toBe("action");
    expect(compact?.disabledReason).toBe("群聊暂不支持上下文压缩");
  });

  it("adds /context as a local action instead of a model prompt", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false
    });

    const context = filterComposerSlashCommands(commands, "context")[0];

    expect(context?.kind).toBe("action");
    expect(context?.action).toBe("context");
  });

  it("keeps task slash commands as command text", () => {
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: initialized("gpt-5.5", ["gpt-5.5"]),
      running: false
    });

    const debug = filterComposerSlashCommands(commands, "debug")[0];
    const fix = filterComposerSlashCommands(commands, "fix")[0];
    const commit = filterComposerSlashCommands(commands, "commit")[0];
    const pr = filterComposerSlashCommands(commands, "pr")[0];

    expect(debug?.kind).toBe("prompt");
    expect(composerSlashPrompt(debug!, "login failure")).toBe("/debug login failure");
    expect(composerSlashPrompt(fix!, "")).toBe("/fix ");
    expect(composerSlashPrompt(commit!, "")).toBe("/commit ");
    expect(composerSlashPrompt(pr!, "draft")).toBe("/pr draft");
  });

  it("registers active plugin prompt templates and renders them in the composer", () => {
    const runtime = initialized("gpt-5.5", ["gpt-5.5"]);
    runtime.extension_inventory = [
      {
        id: "plugin:project:docs",
        name: "docs",
        kind: "plugin",
        provenance: { kind: "plugin", source: "project", scope: "project", plugin_id: "docs" },
        state: "granted",
        approval_state: "granted",
        runtime_state: "inactive",
        enabled: true,
        contributions: {
          commands: [{
            id: "ask-docs",
            title: "Ask the docs",
            description: "Answer using project documentation",
            kind: "prompt_template",
            template: "Use the project documentation to answer:\n\n{{args}}",
            aliases: ["docs"],
          }],
        },
      },
    ] as unknown as NonNullable<InitializeResult["extension_inventory"]>;
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: runtime,
      running: false,
    });

    const pluginCommand = filterComposerSlashCommands(commands, "docs")[0];

    expect(pluginCommand).toMatchObject({
      id: "plugin:docs:ask-docs",
      name: "ask-docs",
      pluginId: "docs",
      tag: "Plugin · docs",
      kind: "prompt",
    });
    expect(composerSlashPrompt(pluginCommand!, "release process")).toBe(
      "Use the project documentation to answer:\n\nrelease process",
    );
  });

  it("fails closed for inactive and runtime-backed plugin commands", () => {
    const runtime = initialized("gpt-5.5", ["gpt-5.5"]);
    runtime.extension_inventory = [
      {
        id: "plugin:project:pending",
        name: "pending",
        kind: "plugin",
        provenance: { kind: "plugin", source: "project", scope: "project", plugin_id: "pending" },
        state: "pending",
        approval_state: "pending",
        runtime_state: "inactive",
        enabled: true,
        contributions: { commands: [{ id: "pending", title: "Pending", kind: "prompt_template", template: "No" }] },
      },
      {
        id: "plugin:project:runtime",
        name: "runtime",
        kind: "plugin",
        provenance: { kind: "plugin", source: "project", scope: "project", plugin_id: "runtime" },
        state: "granted",
        approval_state: "granted",
        runtime_state: "active",
        enabled: true,
        contributions: { commands: [{ id: "runtime", title: "Runtime", kind: "runtime_action" }] },
      },
    ] as unknown as NonNullable<InitializeResult["extension_inventory"]>;
    const commands = buildComposerSlashCommands({
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
      initialized: runtime,
      running: false,
    });

    expect(filterComposerSlashCommands(commands, "pending")).toEqual([]);
    expect(filterComposerSlashCommands(commands, "runtime")).toEqual([]);
  });

});
