import { useState } from "react";
import type { ChannelCoordinatorStatus, NamedAgent } from "../shared/protocol";
import { useI18n } from "./i18n";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { RoomCoordinatorAvatar } from "./RoomCoordinatorAvatar";
import { ChannelActivityPresence } from "./ChannelActivityPresence";
import { toastErrorMessage } from "./Toast";
import "./styles/channel-coordinator.css";

export function ChannelCoordinatorActivity({ status, agents, activeAgentIDs = [], onInspectAgent, onInspectCoordinator, onRetry }: {
  status?: ChannelCoordinatorStatus;
  agents: readonly NamedAgent[];
  activeAgentIDs?: readonly string[];
  onInspectAgent?: (agentID: string) => void;
  onInspectCoordinator?: (sessionRef: string) => void;
  onRetry: (sessionRef: string) => Promise<void>;
}): JSX.Element | null {
  const { t } = useI18n();
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState("");
  const visible = !!status && status.state !== "idle";
  const state = status?.state ?? "idle";
  const label = state === "needs_members" ? t("channels.coordinator.needsMembers")
    : state === "idle" ? "" : t(`channels.coordinator.${state}`);
  async function retry(): Promise<void> {
    if (!status?.session_ref || retrying) return;
    setRetrying(true);
    setRetryError("");
    try { await onRetry(status.session_ref); }
    catch (error) { setRetryError(toastErrorMessage(error)); }
    finally { setRetrying(false); }
  }
  const members = status?.state === "waiting" ? agents.filter(agent => status.agent_ids?.includes(agent.id)) : [];
  const showCoordinator = visible;
  const needsAttention = status?.state === "failed" || status?.state === "needs_members";
  return <ChannelActivityPresence>
    {showCoordinator ? <div key="coordinator" className="channel-coordinator-activity channel-animated-activity"
      data-activity-state={status?.state === "working" ? "thinking" : status?.state} role="status" aria-label={`Room · ${label}`} title={`Room · ${label}`}>
      <button type="button" className="channel-activity-inspect" disabled={!onInspectCoordinator || !status?.session_ref}
        aria-label={`Room · ${label} · ${t("channels.executionTrace")}`} onClick={() => status?.session_ref && onInspectCoordinator?.(status.session_ref)}>
      <RoomCoordinatorAvatar activity={status?.state === "working" ? "thinking" : status?.state === "needs_members" ? "waiting" : status?.state ?? "idle"} />
      </button>
      {needsAttention ? <span>{label}</span> : null}
      {status?.state === "failed" && status.error ? <span className="channel-coordinator-error">{status.error}</span> : null}
      {status?.state === "failed" && status.session_ref ? <button type="button" disabled={retrying} onClick={() => void retry()}>{t("channels.coordinator.retry")}</button> : null}
      {retryError && status?.state === "failed" ? <span role="alert" className="channel-coordinator-error">{retryError}</span> : null}
    </div> : null}
    {members.filter(agent => !activeAgentIDs.includes(agent.id)).map(agent => <div key={agent.id}
      className="channel-coordinator-member channel-animated-activity" data-activity-state="waiting" role="status"
      title={agent.name} aria-label={t("channels.coordinator.assigned", { names: agent.name })}>
      <button type="button" className="channel-activity-inspect" disabled={!onInspectAgent}
        aria-label={`${agent.name} · ${t("channels.sessions.history")}`} onClick={() => onInspectAgent?.(agent.id)}>
      <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key ?? "abstract-1"} avatarImage={agent.avatar_image} status="waiting" />
      </button>
    </div>)}
  </ChannelActivityPresence>;
}
