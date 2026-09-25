import type { JsonValue, Thread, ThreadItem, Turn } from "../shared/protocol";

export type AgentRunStatus = "incomplete" | "completed" | "failed" | "interrupted";

export type AgentRunLocator = {
  threadID: string;
  turnID: string;
  toolCallID?: string;
};

export type AgentRunRecord = {
  kind: "agent_run";
  execution: "snapshot" | "managed";
  threadID: string;
  turnID: string;
  toolCallID: string;
  command: string;
  capability?: string;
  status: AgentRunStatus;
  stdout?: string;
  stderr?: string;
  exitCode?: number;
  durationMs?: number;
  timedOut: boolean;
  truncated: boolean;
  fullLogRef?: string;
  processID?: string;
  tty: boolean;
};

export type AgentRunTurnGroup = {
  turnID: string;
  turnNumber: number;
  status: Turn["status"];
  startedAt?: string | null;
  completedAt?: string | null;
  runs: AgentRunRecord[];
};

const LEGACY_COMMAND_TOOL_NAMES = new Set(["bash", "run_shell"]);

export function isCommandToolCall(item: ThreadItem): boolean {
  if (item.type !== "tool_call") {
    return false;
  }
  const capability = item.display?.capability?.trim().toLowerCase();
  if (capability?.startsWith("command.")) {
    return true;
  }
  if (item.display?.kind?.trim().toLowerCase() === "command") {
    return true;
  }
  return LEGACY_COMMAND_TOOL_NAMES.has(item.name?.trim().toLowerCase() ?? "");
}

export function agentRunsForTurn(threadID: string, turn: Turn): AgentRunRecord[] {
  return turn.items
    .filter(isCommandToolCall)
    .flatMap((item) => {
      const run = agentRunFromToolCall(threadID, turn, item);
      return run ? [run] : [];
    });
}

export function agentRunGroupsForThread(
  thread: Pick<Thread, "id" | "turns">,
): AgentRunTurnGroup[] {
  return thread.turns.flatMap((turn, turnIndex) => {
    const runs = agentRunsForTurn(thread.id, turn);
    return runs.length > 0
      ? [{
          turnID: turn.id,
          turnNumber: turnIndex + 1,
          status: turn.status,
          startedAt: turn.started_at,
          completedAt: turn.completed_at,
          runs,
        }]
      : [];
  });
}

export function preferredAgentRun(runs: AgentRunRecord[]): AgentRunRecord | undefined {
  for (let index = runs.length - 1; index >= 0; index -= 1) {
    if (runs[index].status === "failed") {
      return runs[index];
    }
  }
  return runs[runs.length - 1];
}

export function selectAgentRun(
  groups: AgentRunTurnGroup[],
  locator: AgentRunLocator | undefined,
): AgentRunRecord | undefined {
  if (groups.length === 0) {
    return undefined;
  }
  if (locator) {
    const runThreadID = groups[0]?.runs[0]?.threadID;
    if (runThreadID && runThreadID !== locator.threadID) {
      return undefined;
    }
    const group = groups.find((candidate) => candidate.turnID === locator.turnID);
    if (group) {
      if (locator.toolCallID) {
        const exact = group.runs.find((run) => run.toolCallID === locator.toolCallID);
        if (exact) {
          return exact;
        }
      }
      return preferredAgentRun(group.runs);
    }
  }
  return preferredAgentRun(groups[groups.length - 1].runs);
}

function agentRunFromToolCall(
  threadID: string,
  turn: Turn,
  item: ThreadItem,
): AgentRunRecord | undefined {
  const args = parseRecord(item.arguments);
  const result = toolResultRecord(item);
  // bash with run_in_background reports result action "start"; sessions
  // recorded before the process tool used the bash action "start_background".
  const action = nonEmptyString(args, "action") ?? "run";
  if (action !== "run" && action !== "start_background") {
    return undefined;
  }
  const resultAction = nonEmptyString(result, "action");
  const processID =
    resultAction === "start" || resultAction === "start_background"
      ? nonEmptyString(result, "id")
      : undefined;
  const exitCode = numberValue(result, "exit_code");
  const durationMs = numberValue(result, "duration_ms");
  const timedOut = booleanValue(result, "timed_out") ?? false;
  const failed =
    item.status === "failed" ||
    Boolean(item.error) ||
    item.result_detail?.is_error === true ||
    (exitCode !== undefined && exitCode !== 0) ||
    timedOut;
  const status: AgentRunStatus = failed
    ? "failed"
    : item.status === "completed"
      ? "completed"
      : turn.status === "interrupted"
        ? "interrupted"
        : turn.status === "failed"
          ? "failed"
          : "incomplete";
  const stdout = nonEmptyString(result, "stdout_tail");
  const stderr = nonEmptyString(result, "stderr_tail");

  return {
    kind: "agent_run",
    execution: processID ? "managed" : "snapshot",
    threadID,
    turnID: turn.id,
    toolCallID: item.id,
    command:
      nonEmptyString(args, "command") ??
      nonEmptyString(result, "command") ??
      item.display?.text?.trim() ??
      item.name?.trim() ??
      "command",
    capability: item.display?.capability?.trim() || undefined,
    status,
    stdout,
    stderr,
    exitCode,
    durationMs,
    timedOut,
    truncated:
      (booleanValue(result, "truncated") ?? false) ||
      (booleanValue(result, "stdout_tail_truncated") ?? false) ||
      (booleanValue(result, "stderr_tail_truncated") ?? false),
    fullLogRef: nonEmptyString(result, "full_log_ref"),
    processID,
    tty: processID ? (booleanValue(result, "tty") ?? false) : false,
  };
}

function toolResultRecord(item: ThreadItem): Record<string, unknown> | undefined {
  const structured = item.result_detail?.structured_content;
  if (isRecord(structured)) {
    return structured;
  }
  // The model view may itself be JSON stdout, not execution metadata.
  // Prefer the retained producer envelope before legacy text-only results.
  for (const part of item.result_detail?.content ?? []) {
    const parsed = parseRecord(part.text);
    if (parsed) {
      return parsed;
    }
  }
  return parseRecord(item.result);
}

function parseRecord(value: string | undefined): Record<string, unknown> | undefined {
  if (!value?.trim()) {
    return undefined;
  }
  try {
    const parsed: unknown = JSON.parse(value);
    return isRecord(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

function isRecord(value: JsonValue | unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function nonEmptyString(
  record: Record<string, unknown> | undefined,
  key: string,
): string | undefined {
  const value = record?.[key];
  return typeof value === "string" && value.trim() ? value : undefined;
}

function numberValue(
  record: Record<string, unknown> | undefined,
  key: string,
): number | undefined {
  const value = record?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function booleanValue(
  record: Record<string, unknown> | undefined,
  key: string,
): boolean | undefined {
  const value = record?.[key];
  return typeof value === "boolean" ? value : undefined;
}
