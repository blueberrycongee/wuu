import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import type { InitializeResult, RuntimeContext, SkillSummary } from "../shared/protocol";
import { translateCurrent as t } from "./i18n";
import {
  pluginCommandPackagesFromInventory,
  registerPluginPromptCommands,
  renderPluginPromptTemplate,
} from "./PluginCommandRegistry";

export type ComposerSlashCommandAction =
  | "new-thread"
  | "open-side-thread"
  | "reset-side-thread"
  | "open-review"
  | "open-skills"
  | "open-files"
  | "open-terminal"
  | "open-project"
  | "no-project"
  | "context"
  | "instructions"
  | "plugin"
  | "compact"
  | "handoff"
  | "model"
  | "fast"
  | "effort"
  | "settings";
export type ComposerSlashCommandKind = "prompt" | "action" | "skill";

export type ComposerSlashCommand = {
  id: string;
  name: string;
  title: string;
  description: string;
  tag: string;
  kind: ComposerSlashCommandKind;
  action?: ComposerSlashCommandAction;
  aliases?: string[];
  keywords?: string[];
  argumentHint?: string;
  disabledReason?: string;
  pluginId?: string;
  pluginCommandId?: string;
  promptTemplate?: string;
};

export type ComposerSlashDraft = {
  query: string;
  args: string;
};

export type ComposerFastModelTarget = {
  provider: string;
  model: string;
  current: boolean;
};

const COMPOSER_SLASH_COMMAND_LIMIT = 8;
// Skills are appended after the built-ins, so the shared limit alone would drop
// every skill from the unsearched view. Reserve slots for them instead, keeping
// installed workflows discoverable without typing a query first.
const COMPOSER_SLASH_DEFAULT_SKILL_LIMIT = 3;

export function parseComposerSlashDraft(value: string): ComposerSlashDraft | undefined {
  if (!value.startsWith("/") || value.startsWith("//") || (value.includes("\n") && !/^\/handoff(?:\s|$)/i.test(value))) {
    return undefined;
  }
  const body = value.slice(1);
  if (/^\s/.test(body)) {
    return undefined;
  }
  const match = body.match(/^(\S*)(?:\s+([\s\S]*))?$/);
  if (!match) {
    return undefined;
  }
  return {
    query: (match[1] ?? "").toLowerCase(),
    args: match[2] ?? ""
  };
}

export function isComposerTextComposing<T extends Element>(event: ReactKeyboardEvent<T>): boolean {
  return event.nativeEvent.isComposing;
}

export function buildComposerSlashCommands({
  activeContext,
  initialized,
  running,
  compactDisabledReason,
  sideThreadDisabledReason,
  handoffDisabledReason,
  skills = [],
  availablePluginRuntimeCommands = new Set<string>(),
}: {
  activeContext?: RuntimeContext;
  initialized?: InitializeResult;
  running: boolean;
  compactDisabledReason?: string;
  sideThreadDisabledReason?: string;
  handoffDisabledReason?: string;
  skills?: SkillSummary[];
  availablePluginRuntimeCommands?: ReadonlySet<string>;
}): ComposerSlashCommand[] {
  const needsRuntime = activeContext && initialized ? undefined : t("slash.selectWorkspaceFirst");
  const needsWorkspace = activeContext ? undefined : t("slash.selectWorkspaceFirst");
  const needsIdleThread = running ? t("slash.taskRunning") : undefined;
  const fastTarget = runtimeFastModelTarget(initialized);
  const commands: ComposerSlashCommand[] = [
    {
      id: "review",
      name: "review",
      title: t("slash.review.title"),
      description: t("slash.review.description"),
      tag: "Agent",
      kind: "prompt",
      aliases: ["audit"],
      keywords: ["diff", "changes", "code review", "审查", "检查"],
      disabledReason: needsRuntime
    },
    {
      id: "debug",
      name: "debug",
      title: t("slash.debug.title"),
      description: t("slash.debug.description"),
      tag: "Agent",
      kind: "prompt",
      aliases: ["investigate", "diagnose"],
      keywords: ["bug", "error", "failure", "失败", "报错", "排查", "根因"],
      disabledReason: needsRuntime
    },
    {
      id: "fix",
      name: "fix",
      title: t("slash.fix.title"),
      description: t("slash.fix.description"),
      tag: "Agent",
      kind: "prompt",
      aliases: ["repair"],
      keywords: ["bug", "issue", "修复", "改掉", "问题"],
      disabledReason: needsRuntime
    },
    {
      id: "test",
      name: "test",
      title: t("slash.test.title"),
      description: t("slash.test.description"),
      tag: "Agent",
      kind: "prompt",
      aliases: ["tests"],
      keywords: ["unit", "e2e", "coverage", "测试", "验证"],
      disabledReason: needsRuntime
    },
    {
      id: "explain",
      name: "explain",
      title: t("slash.explain.title"),
      description: t("slash.explain.description"),
      tag: "Agent",
      kind: "prompt",
      aliases: ["why"],
      keywords: ["explain", "understand", "why", "解释", "说明", "为什么"],
      disabledReason: needsRuntime
    },
    {
      id: "skills",
      name: "skills",
      title: t("slash.skills.title"),
      description: t("slash.skills.description"),
      tag: "Skill",
      kind: "action",
      action: "open-skills",
      aliases: ["skill"],
      keywords: ["skills", "skill", "技能", "能力"],
      disabledReason: needsRuntime
    },
    {
      id: "context",
      name: "context",
      title: t("slash.context.title"),
      description: t("slash.context.description"),
      tag: t("slash.tag.view"),
      kind: "action",
      action: "context",
      aliases: ["ctx"],
      keywords: ["context", "tokens", "cache", "上下文", "缓存"],
      disabledReason: needsRuntime
    },
    {
      id: "instructions",
      name: "instructions",
      title: t("slash.instructions.title"),
      description: t("slash.instructions.description"),
      tag: t("slash.tag.view"),
      kind: "action",
      action: "instructions",
      aliases: ["agents"],
      keywords: ["instructions", "agents", "claude", "指令"],
      disabledReason: needsRuntime
    },
    {
      id: "commit",
      name: "commit",
      title: t("slash.commit.title"),
      description: t("slash.commit.description"),
      tag: t("slash.tag.workflow"),
      kind: "prompt",
      aliases: ["save"],
      keywords: ["git", "commit", "提交", "保存"],
      disabledReason: needsRuntime ?? needsIdleThread
    },
    {
      id: "pr",
      name: "pr",
      title: t("slash.pr.title"),
      description: t("slash.pr.description"),
      tag: t("slash.tag.workflow"),
      kind: "prompt",
      aliases: ["pull-request", "pullrequest"],
      keywords: ["github", "pull request", "merge request", "pr", "合并请求"],
      disabledReason: needsRuntime ?? needsIdleThread
    },
    {
      id: "diff",
      name: "diff",
      title: t("slash.diff.title"),
      description: t("slash.diff.description"),
      tag: t("slash.tag.workspace"),
      kind: "action",
      action: "open-review",
      aliases: ["changes"],
      keywords: ["review", "git", "diff", "变更"],
      disabledReason: needsWorkspace
    },
    {
      id: "new",
      name: "new",
      title: t("slash.new.title"),
      description: t("slash.new.description"),
      tag: t("slash.tag.conversation"),
      kind: "action",
      action: "new-thread",
      aliases: ["clear"],
      keywords: ["conversation", "thread", "新对话"],
      disabledReason: needsWorkspace ?? needsIdleThread
    },
    {
      // Side-thread messages remain outside the main conversation history.
      // Optional args after `/side ` are sent as the side-thread prompt.
      id: "side",
      name: "side",
      title: t("slash.side.title"),
      description: t("slash.side.description"),
      tag: t("slash.tag.conversation"),
      kind: "action",
      action: "open-side-thread",
      argumentHint: t("slash.side.argumentHint"),
      keywords: ["side", "侧聊", "旁路", "进度"],
      // Side chat remains available while the main turn is running.
      disabledReason: needsWorkspace ?? sideThreadDisabledReason
    },
    {
      id: "compact",
      name: "compact",
      title: t("slash.compact.title"),
      description: t("slash.compact.description"),
      tag: t("slash.tag.conversation"),
      kind: "action",
      action: "compact",
      aliases: ["compress"],
      keywords: ["compact", "context", "summary", "压缩", "上下文", "摘要", "瘦身"],
      disabledReason: needsRuntime ?? needsIdleThread ?? compactDisabledReason
    },
    {
      id: "terminal",
      name: "terminal",
      title: t("slash.terminal.title"),
      description: t("slash.terminal.description"),
      tag: t("slash.tag.workspace"),
      kind: "action",
      action: "open-terminal",
      aliases: ["shell"],
      keywords: ["命令行", "terminal", "shell"],
      disabledReason: needsWorkspace
    },
    {
      id: "files",
      name: "files",
      title: t("slash.files.title"),
      description: t("slash.files.description"),
      tag: t("slash.tag.workspace"),
      kind: "action",
      action: "open-files",
      aliases: ["file", "tree"],
      keywords: ["文件", "浏览", "explorer"],
      disabledReason: needsWorkspace
    },
    {
      id: "project",
      name: "project",
      title: t("slash.project.title"),
      description: t("slash.project.description"),
      tag: t("slash.tag.project"),
      kind: "action",
      action: "open-project",
      aliases: ["open"],
      keywords: ["folder", "workspace", "项目"]
    },
    {
      id: "no-project",
      name: "no-project",
      title: t("slash.noProject.title"),
      description: t("slash.noProject.description"),
      tag: t("slash.tag.project"),
      kind: "action",
      action: "no-project",
      aliases: ["scratch", "none"],
      keywords: ["temporary", "临时", "无项目"],
      disabledReason: needsIdleThread
    },
    {
      id: "model",
      name: "model",
      title: t("slash.model.title"),
      description: t("slash.model.description"),
      tag: t("slash.tag.configuration"),
      kind: "action",
      action: "model",
      aliases: ["models"],
      keywords: ["provider", "effort", "variant", "模型", "参数档位"],
      disabledReason: needsRuntime ?? needsIdleThread
    },
    ...(fastTarget
      ? [
          {
            id: "fast",
            name: "fast",
            title: t("slash.fast.title"),
            description: fastTarget.current ? t("slash.fast.currentDescription") : t("slash.fast.description"),
            tag: t("slash.tag.configuration"),
            kind: "action",
            action: "fast",
            aliases: ["quick"],
            keywords: ["fast", "priority", "快速", "高速"],
            disabledReason: needsRuntime ?? needsIdleThread ?? (fastTarget.current ? t("slash.fast.alreadyEnabled") : undefined)
          } satisfies ComposerSlashCommand
        ]
      : []),
    {
      id: "effort",
      name: "effort",
      title: t("slash.effort.title"),
      description: t("slash.effort.description"),
      tag: t("slash.tag.configuration"),
      kind: "action",
      action: "effort",
      aliases: ["variant", "variants"],
      keywords: ["reasoning", "effort", "variant", "思考", "强度"],
      disabledReason: needsRuntime ?? needsIdleThread
    },
    {
      id: "settings",
      name: "settings",
      title: t("slash.settings.title"),
      description: t("slash.settings.description"),
      tag: t("slash.tag.configuration"),
      kind: "action",
      action: "settings",
      aliases: ["config"],
      keywords: ["provider", "模型", "配置"]
    }
  ];
  const skillCommands = buildSkillSlashCommands(skills, needsRuntime);
  const reservedNames = new Set(
    [...commands, ...skillCommands].flatMap((command) => [command.name, ...(command.aliases ?? [])]),
  );
  const pluginCommands = registerPluginPromptCommands(
    pluginCommandPackagesFromInventory(initialized?.extension_inventory ?? []),
    reservedNames,
    activeContext?.kind,
    availablePluginRuntimeCommands,
  ).commands.map((command): ComposerSlashCommand => ({
    id: command.id,
    name: command.name,
    title: command.title,
    description: command.description,
    tag: `Plugin · ${command.pluginId}`,
    kind: command.kind === "runtime_action" ? "action" : "prompt",
    action: command.kind === "runtime_action" ? "plugin" : undefined,
    aliases: command.aliases,
    keywords: [command.pluginId, "plugin", ...command.keywords],
    disabledReason: command.kind === "runtime_action"
      ? needsRuntime ?? sideThreadDisabledReason
      : needsRuntime,
    pluginId: command.pluginId,
    pluginCommandId: command.name,
    promptTemplate: command.template,
  }));
  return [...commands, ...pluginCommands, ...skillCommands];
}

// The side-chat composer exposes its own command surface instead of the main
// list: side commands act on the side thread only.
export function buildSideThreadSlashCommands(): ComposerSlashCommand[] {
  return [
    {
      id: "side-reset",
      name: "reset",
      title: t("slash.sideReset.title"),
      description: t("slash.sideReset.description"),
      tag: t("slash.tag.sideChat"),
      kind: "action",
      action: "reset-side-thread",
      aliases: ["rebase"],
      keywords: ["reset", "rebase", "clear", "清空", "重置", "变基"]
    }
  ];
}

export function runtimeFastModelTarget(initialized?: InitializeResult): ComposerFastModelTarget | undefined {
  if (!initialized) {
    return undefined;
  }
  const provider = initialized.providers?.find((item) => item.name === initialized.provider);
  if (!provider) {
    return undefined;
  }
  const currentModel = initialized.model.trim();
  if (!currentModel) {
    return undefined;
  }
  const currentFast = currentModel.toLowerCase().endsWith("-fast");
  const baseModel = currentFast ? currentModel.slice(0, -"-fast".length) : currentModel;
  const fastModel = `${baseModel}-fast`;
  const hasFastModel = provider.models?.some((model) => model.id === fastModel);
  if (!hasFastModel && !currentFast) {
    return undefined;
  }
  return {
    provider: provider.name,
    model: currentFast ? currentModel : fastModel,
    current: currentFast
  };
}

export function filterComposerSlashCommands(commands: ComposerSlashCommand[], query: string): ComposerSlashCommand[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) {
    return defaultComposerSlashCommands(commands);
  }
  return commands
    .filter((command) => composerSlashCommandSearchText(command).includes(normalized))
    .slice(0, COMPOSER_SLASH_COMMAND_LIMIT);
}

// Skills keep their source ordering (project, then user, then bundled), so the
// reserved slots surface the most project-specific workflows first.
function defaultComposerSlashCommands(commands: ComposerSlashCommand[]): ComposerSlashCommand[] {
  const builtIns = commands
    .filter((command) => command.kind !== "skill" && !command.pluginId)
    .slice(0, COMPOSER_SLASH_COMMAND_LIMIT);
  const plugins = commands.filter((command) => command.pluginId).slice(0, 2);
  const skills = commands.filter((command) => command.kind === "skill").slice(0, COMPOSER_SLASH_DEFAULT_SKILL_LIMIT);
  return [...builtIns, ...plugins, ...skills];
}

export function firstEnabledSlashCommandIndex(commands: ComposerSlashCommand[]): number {
  const index = commands.findIndex((command) => !command.disabledReason);
  return index >= 0 ? index : 0;
}

export function nextEnabledSlashCommandIndex(commands: ComposerSlashCommand[], current: number, direction: 1 | -1): number {
  if (commands.length === 0) {
    return 0;
  }
  for (let step = 1; step <= commands.length; step++) {
    const index = (current + direction * step + commands.length) % commands.length;
    if (!commands[index]?.disabledReason) {
      return index;
    }
  }
  return current;
}

function composerSlashCommandSearchText(command: ComposerSlashCommand): string {
  return [command.name, command.title, command.description, command.tag, ...(command.aliases ?? []), ...(command.keywords ?? [])]
    .join(" ")
    .toLowerCase();
}

export function composerSlashPrompt(command: ComposerSlashCommand, args: string): string {
  if (command.promptTemplate) {
    return renderPluginPromptTemplate(command.promptTemplate, args);
  }
  const instructions = args.trim();
  return `/${command.name}${instructions ? ` ${instructions}` : " "}`;
}

function buildSkillSlashCommands(skills: SkillSummary[], disabledReason?: string): ComposerSlashCommand[] {
  const seen = new Set<string>();
  const out: ComposerSlashCommand[] = [];
  for (const skill of [...skills].sort(compareSkillSummaries)) {
    const name = skill.name.trim();
    if (!skill.user_invocable || !name || /\s/.test(name)) {
      continue;
    }
    const key = name.toLowerCase();
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    // A skill has one human line of its own, so it serves as both the summary
    // shown beside `/name` and the longer text a built-in command puts in its
    // description.
    const summary = skill.description || skill.when_to_use || skill.trigger_condition || t("slash.useSkill");
    out.push({
      id: `skill:${key}`,
      name,
      title: summary,
      description: summary,
      tag: "Skill",
      kind: "skill",
      aliases: skill.examples?.slice(0, 3),
      keywords: [
        "skill",
        "skills",
        "技能",
        skill.source,
        skill.when_to_use,
        skill.trigger_condition,
        skill.argument_hint,
        skill.model,
        skill.context,
        skill.agent,
        ...(skill.paths ?? [])
      ].filter((value): value is string => Boolean(value)),
      argumentHint: skill.argument_hint,
      disabledReason
    });
  }
  return out;
}

function compareSkillSummaries(left: SkillSummary, right: SkillSummary): number {
  const sourceDelta = sourceRank(left.source) - sourceRank(right.source);
  if (sourceDelta !== 0) {
    return sourceDelta;
  }
  return left.name.localeCompare(right.name);
}

function sourceRank(source: string): number {
  switch (source) {
    case "project":
      return 0;
    case "user":
      return 1;
    case "bundled":
      return 2;
    default:
      return 3;
  }
}
