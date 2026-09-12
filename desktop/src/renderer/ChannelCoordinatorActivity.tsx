import { useState, type CSSProperties } from "react";
import type { ChannelCoordinatorStatus, NamedAgent } from "../shared/protocol";
import { useI18n } from "./i18n";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { WuuMascot } from "./WuuMascot";
import { ChannelActivityPresence } from "./ChannelActivityPresence";
import { toastErrorMessage } from "./Toast";
import "./styles/channel-coordinator.css";

export function ChannelCoordinatorActivity({ status, agents, activeAgentIDs = [], onRetry }: {
  status?: ChannelCoordinatorStatus;
  agents: readonly NamedAgent[];
  activeAgentIDs?: readonly string[];
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
  const showCoordinator = visible && (status?.state !== "waiting" || (members.length === 0 && activeAgentIDs.length === 0));
  const needsAttention = status?.state === "failed" || status?.state === "needs_members";
  return <ChannelActivityPresence>
    {showCoordinator ? <div key="coordinator" className="channel-coordinator-activity channel-animated-activity"
      data-activity-state={status?.state === "working" ? "thinking" : status?.state} role="status" aria-label={label} title={label}>
      <span className="channel-coordinator-mascot" aria-hidden="true">
        <WuuMascot size={32} brand accessory="none" showActivityProp={false}
          style={{ "--mo-head": "var(--channel-coordinator-body)", "--mo-eye": "var(--channel-coordinator-eyes)" } as CSSProperties}
          activity={status?.state === "working" ? "thinking" : status?.state === "needs_members" ? "waiting" : status?.state ?? "idle"} />
      </span>
      {needsAttention ? <span>{label}</span> : null}
      {status?.state === "failed" && status.error ? <span className="channel-coordinator-error">{status.error}</span> : null}
      {status?.state === "failed" && status.session_ref ? <button type="button" disabled={retrying} onClick={() => void retry()}>{t("channels.coordinator.retry")}</button> : null}
      {retryError && status?.state === "failed" ? <span role="alert" className="channel-coordinator-error">{retryError}</span> : null}
    </div> : null}
    {members.filter(agent => !activeAgentIDs.includes(agent.id)).map(agent => <div key={agent.id}
      className="channel-coordinator-member channel-animated-activity" data-activity-state="waiting" role="status"
      title={agent.name} aria-label={t("channels.coordinator.assigned", { names: agent.name })}>
      <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key ?? "abstract-1"} avatarImage={agent.avatar_image} status="waiting" />
    </div>)}
  </ChannelActivityPresence>;
}
