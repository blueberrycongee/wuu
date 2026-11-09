import type { Agent } from "../shared/protocol";
import { getActiveLocale, translateCurrent } from "./i18n";

export function sortChildAgents(agents: Agent[]): Agent[] {
  return agents.slice().sort((left, right) => {
    const leftTime = Date.parse(left.started_at ?? "");
    const rightTime = Date.parse(right.started_at ?? "");
    if (Number.isFinite(leftTime) && Number.isFinite(rightTime) && leftTime !== rightTime) {
      return leftTime - rightTime;
    }
    return agentLabel(left).localeCompare(agentLabel(right), getActiveLocale());
  });
}

export function agentLabel(agent: Agent): string {
  const participantName = agent.participant?.name.trim();
  if (participantName) {
    return participantName;
  }
  const pathParts = agent.agent_path?.split("/").filter(Boolean) ?? [];
  return agent.task_name || agent.description || pathParts[pathParts.length - 1] || agent.id;
}

export function agentTooltip(agent: Agent): string {
  const path = agent.agent_path ? ` · ${agent.agent_path}` : "";
  return `${agentLabel(agent)} · ${agentStatusLabel(agent.status)}${path}`;
}

export function agentNestedLabel(agent: Agent): string | undefined {
  const total = agent.nested_count ?? 0;
  if (total <= 0) {
    return undefined;
  }
  const running = agent.nested_running_count ?? 0;
  return running > 0 ? `${running}/${total}` : `+${total}`;
}

export function agentStatusLabel(status: string | undefined): string {
  switch (status) {
    case "pending":
      return translateCurrent("agent.status.pending");
    case "queued":
      return translateCurrent("agent.status.queued");
    case "running":
      return translateCurrent("agent.status.running");
    case "waiting_children":
      return translateCurrent("agent.status.waitingChildren");
    case "completed":
      return translateCurrent("agent.status.completed");
    case "failed":
      return translateCurrent("agent.status.failed");
    case "cancelled":
      return translateCurrent("agent.status.cancelled");
    default:
      return status?.trim() || translateCurrent("common.unknown");
  }
}

export function agentStatusTone(status: string | undefined): "running" | "completed" | "failed" | "cancelled" | "pending" {
  switch (status) {
    case "running":
      return "running";
    case "completed":
      return "completed";
    case "failed":
      return "failed";
    case "cancelled":
      return "cancelled";
    default:
      return "pending";
  }
}
