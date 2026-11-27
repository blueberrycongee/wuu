import type { ExtensionApprovalState, ExtensionCommandDescriptor } from "../shared/protocol";

export type PluginCommandContribution = ExtensionCommandDescriptor;

export type PluginCommandPackage = {
  pluginId: string;
  approvalState: ExtensionApprovalState;
  enabled: boolean;
  commands: readonly PluginCommandContribution[];
};

export type RegisteredPluginPromptCommand = {
  id: string;
  pluginId: string;
  name: string;
  title: string;
  description: string;
  kind: "prompt_template" | "runtime_action";
  template?: string;
  contexts: string[];
  aliases: string[];
  keywords: string[];
};

export type PluginCommandRegistryIssueCode =
  | "collision"
  | "inactive_package"
  | "invalid"
  | "unsupported";

export type PluginCommandRegistryIssue = {
  code: PluginCommandRegistryIssueCode;
  commandId: string;
  pluginId: string;
  message: string;
};

export type PluginCommandRegistryResult = {
  commands: RegisteredPluginPromptCommand[];
  issues: PluginCommandRegistryIssue[];
};

const commandNamePattern = /^[a-z0-9](?:[a-z0-9-]{0,62})$/;
const maxTemplateLength = 16_384;
const maxDescriptionLength = 512;
const maxTitleLength = 128;
const maxAliases = 8;
const maxKeywords = 24;

export function pluginCommandPackagesFromInventory(inventory: readonly unknown[]): PluginCommandPackage[] {
  const packages: PluginCommandPackage[] = [];
  for (const value of inventory) {
    if (!isRecord(value) || value.kind !== "plugin" || !isRecord(value.provenance)) {
      continue;
    }
    const pluginId = typeof value.provenance.plugin_id === "string"
      ? value.provenance.plugin_id.trim()
      : "";
    const contributions = isRecord(value.contributions) ? value.contributions : undefined;
    const commands = Array.isArray(contributions?.commands)
      ? contributions.commands.flatMap(parseCommandContribution)
      : [];
    if (!pluginId || commands.length === 0) {
      continue;
    }
    packages.push({
      pluginId,
      approvalState: pluginApprovalState(value.approval_state),
      enabled: value.enabled === true,
      commands,
    });
  }
  return packages;
}

export function registerPluginPromptCommands(
  packages: readonly PluginCommandPackage[],
  reservedNames: ReadonlySet<string> = new Set(),
  activeContext?: string,
  availableRuntimeCommands: ReadonlySet<string> = new Set(),
): PluginCommandRegistryResult {
  const commands: RegisteredPluginPromptCommand[] = [];
  const issues: PluginCommandRegistryIssue[] = [];
  const occupiedNames = new Set(
    [...reservedNames]
      .map((name) => name.trim().toLowerCase())
      .filter(Boolean),
  );

  for (const source of [...packages].sort((left, right) => left.pluginId.localeCompare(right.pluginId))) {
    for (const contribution of [...source.commands].sort((left, right) => left.id.localeCompare(right.id))) {
      const issue = validateContribution(source.pluginId, contribution);
      if (issue) {
        issues.push(issue);
        continue;
      }
      if (!source.enabled || (source.approvalState !== "official" && source.approvalState !== "granted")) {
        issues.push(registryIssue(source.pluginId, contribution.id, "inactive_package", "Plugin package is not active"));
        continue;
      }
      if (contribution.kind === "runtime_action"
        && !availableRuntimeCommands.has(`${source.pluginId}:${contribution.id}`)) {
        issues.push(registryIssue(source.pluginId, contribution.id, "unsupported", "Runtime plugin command is not registered"));
        continue;
      }
      const contexts = uniqueStrings(contribution.contexts ?? []);
      if (activeContext && contexts.length > 0 && !contexts.includes(activeContext.toLowerCase())) {
        continue;
      }

      const aliases = uniqueStrings(contribution.aliases ?? []);
      const claimedNames = [contribution.id, ...aliases];
      const collision = claimedNames.find((name) => occupiedNames.has(name));
      if (collision) {
        issues.push(registryIssue(
          source.pluginId,
          contribution.id,
          "collision",
          `Plugin command name is already registered: ${collision}`,
        ));
        continue;
      }

      for (const name of claimedNames) {
        occupiedNames.add(name);
      }
      commands.push({
        id: `plugin:${source.pluginId}:${contribution.id}`,
        pluginId: source.pluginId,
        name: contribution.id,
        title: contribution.title.trim(),
        description: contribution.description?.trim() ?? "",
        kind: contribution.kind,
        template: contribution.template?.trim(),
        contexts,
        aliases,
        keywords: uniqueStrings(contribution.keywords ?? []),
      });
    }
  }

  return { commands, issues };
}

export function renderPluginPromptTemplate(template: string, args: string): string {
  const normalizedArgs = args.trim();
  if (template.includes("{{args}}")) {
    return template.replaceAll("{{args}}", normalizedArgs).trim();
  }
  return normalizedArgs ? `${template}\n\n${normalizedArgs}` : template;
}

function validateContribution(
  pluginId: string,
  contribution: PluginCommandContribution,
): PluginCommandRegistryIssue | undefined {
  if (!pluginId.trim()) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin identity is required");
  }
  if (!commandNamePattern.test(contribution.id)) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin command id must be lowercase kebab-case");
  }
  if (!contribution.title.trim() || contribution.title.length > maxTitleLength) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin command title is invalid");
  }
  if ((contribution.description?.length ?? 0) > maxDescriptionLength) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin command description is too long");
  }
  if (contribution.kind === "prompt_template") {
    const template = contribution.template?.trim() ?? "";
    if (!template || template.length > maxTemplateLength) {
      return registryIssue(pluginId, contribution.id, "invalid", "Plugin command prompt template is invalid");
    }
  }
  if ((contribution.aliases?.length ?? 0) > maxAliases || (contribution.keywords?.length ?? 0) > maxKeywords) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin command search metadata exceeds its limit");
  }
  if ((contribution.aliases ?? []).some((alias) => !commandNamePattern.test(alias))) {
    return registryIssue(pluginId, contribution.id, "invalid", "Plugin command aliases must be lowercase kebab-case");
  }
  return undefined;
}

function uniqueStrings(values: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const normalized = value.trim().toLowerCase();
    if (!normalized || seen.has(normalized)) {
      continue;
    }
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

function parseCommandContribution(value: unknown): PluginCommandContribution[] {
  if (!isRecord(value)
    || typeof value.id !== "string"
    || typeof value.title !== "string"
    || (value.kind !== "prompt_template" && value.kind !== "runtime_action")) {
    return [];
  }
  return [{
    id: value.id,
    title: value.title,
    description: typeof value.description === "string" ? value.description : undefined,
    kind: value.kind,
    template: typeof value.template === "string" ? value.template : undefined,
    contexts: stringArray(value.contexts),
    aliases: stringArray(value.aliases),
    keywords: stringArray(value.keywords),
  }];
}

function pluginApprovalState(value: unknown): PluginCommandPackage["approvalState"] {
  switch (value) {
    case "official":
    case "pending":
    case "granted":
    case "changed":
    case "rejected":
      return value;
    default:
      return "pending";
  }
}

function stringArray(value: unknown): string[] | undefined {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function registryIssue(
  pluginId: string,
  commandId: string,
  code: PluginCommandRegistryIssueCode,
  message: string,
): PluginCommandRegistryIssue {
  return { code, commandId, pluginId, message };
}
