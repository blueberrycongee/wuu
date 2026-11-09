import { describe, expect, it } from "vitest";

import {
  pluginCommandPackagesFromInventory,
  registerPluginPromptCommands,
  renderPluginPromptTemplate,
  type PluginCommandContribution,
  type PluginCommandPackage,
} from "./PluginCommandRegistry";

function command(overrides: Partial<PluginCommandContribution> = {}): PluginCommandContribution {
  return {
    id: "ask-docs",
    title: "Ask the docs",
    description: "Answer from the project documentation",
    kind: "prompt_template",
    template: "Use the project documentation to answer:\n\n{{args}}",
    ...overrides,
  };
}

function plugin(overrides: Partial<PluginCommandPackage> = {}): PluginCommandPackage {
  return {
    pluginId: "docs",
    approvalState: "granted",
    enabled: true,
    commands: [command()],
    ...overrides,
  };
}

describe("plugin command registry", () => {
  it("reads commands only from package-root inventory records", () => {
    expect(pluginCommandPackagesFromInventory([
      {
        id: "plugin:project:docs",
        kind: "plugin",
        provenance: { plugin_id: "docs" },
        approval_state: "granted",
        enabled: true,
        contributions: { commands: [command()] },
      },
      {
        id: "hook:plugin:docs:before",
        kind: "hook",
        provenance: { plugin_id: "docs" },
        contributions: { commands: [command({ id: "not-root" })] },
      },
    ])).toEqual([plugin()]);
  });

  it("registers active package prompt commands in deterministic order", () => {
    const result = registerPluginPromptCommands([
      plugin({ pluginId: "zeta", commands: [command({ id: "later", title: "Later" })] }),
      plugin({ pluginId: "alpha", commands: [command({ id: "first", title: "First" })] }),
    ]);

    expect(result.commands.map((item) => item.name)).toEqual(["first", "later"]);
    expect(result.issues).toEqual([]);
  });

  it("rejects collisions with host commands and earlier plugin aliases", () => {
    const result = registerPluginPromptCommands([
      plugin({ pluginId: "alpha", commands: [command({ id: "one", aliases: ["shared"] })] }),
      plugin({ pluginId: "beta", commands: [command({ id: "two", aliases: ["shared"] })] }),
      plugin({ pluginId: "gamma", commands: [command({ id: "review" })] }),
    ], new Set(["review"]));

    expect(result.commands.map((item) => item.name)).toEqual(["one"]);
    expect(result.issues.map((issue) => [issue.pluginId, issue.code])).toEqual([
      ["beta", "collision"],
      ["gamma", "collision"],
    ]);
  });

  it("does not expose pending, changed, disabled, invalid, or runtime-backed commands", () => {
    const result = registerPluginPromptCommands([
      plugin({ pluginId: "pending", approvalState: "pending" }),
      plugin({ pluginId: "changed", approvalState: "changed" }),
      plugin({ pluginId: "disabled", enabled: false }),
      plugin({ pluginId: "invalid", commands: [command({ id: "Not Valid" })] }),
      plugin({ pluginId: "runtime", commands: [command({ id: "runtime", kind: "runtime_action", template: undefined })] }),
    ]);

    expect(result.commands).toEqual([]);
    expect(result.issues.map((issue) => issue.code)).toEqual([
      "inactive_package",
      "inactive_package",
      "invalid",
      "inactive_package",
      "unsupported",
    ]);
  });

  it("filters context-scoped commands", () => {
    const source = plugin({ commands: [
      command({ id: "project-only", contexts: ["project"] }),
      command({ id: "global" }),
    ] });

    expect(registerPluginPromptCommands([source], new Set(), "no_project").commands.map((item) => item.name)).toEqual(["global"]);
    expect(registerPluginPromptCommands([source], new Set(), "project").commands.map((item) => item.name)).toEqual([
      "global",
      "project-only",
    ]);
  });

  it("renders templates without losing user arguments", () => {
    expect(renderPluginPromptTemplate("Investigate:\n\n{{args}}", "  failed login  ")).toBe(
      "Investigate:\n\nfailed login",
    );
    expect(renderPluginPromptTemplate("Run the release checklist", "for desktop")).toBe(
      "Run the release checklist\n\nfor desktop",
    );
  });
});
