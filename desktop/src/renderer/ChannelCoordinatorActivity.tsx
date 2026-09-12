import { useState } from "react";
import type { ChannelCoordinatorStatus, NamedAgent } from "../shared/protocol";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import "./styles/channel-coordinator.css";

export function ChannelCoordinatorActivity({ status, agents, onRetry }: {
  status?: ChannelCoordinatorStatus;
  agents: readonly NamedAgent[];
  onRetry: (sessionRef: string) => Promise<void>;
}): JSX.Element | null {
  const { t } = useI18n();
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState("");
  if (!status || status.state === "idle") return null;
  const names = (status.agent_ids ?? []).flatMap((id) => {
    const agent = agents.find((entry) => entry.id === id);
    return agent ? [agent.name] : [];
  }).join(" · ");
  const label = status.state === "needs_members" ? t("channels.coordinator.needsMembers")
    : status.state === "waiting" && names ? t("channels.coordinator.assigned", { names })
      : t(`channels.coordinator.${status.state}`);
  async function retry(): Promise<void> {
    if (!status?.session_ref || retrying) return;
    setRetrying(true);
    setRetryError("");
    try { await onRetry(status.session_ref); }
    catch (error) { setRetryError(toastErrorMessage(error)); }
    finally { setRetrying(false); }
  }
  return <div className="channel-coordinator-activity" role="status">
    <span>{label}</span>
    {status.state === "failed" && status.error ? <span className="channel-coordinator-error">{status.error}</span> : null}
    {status.state === "failed" && status.session_ref ? <button type="button" disabled={retrying} onClick={() => void retry()}>{t("channels.coordinator.retry")}</button> : null}
    {retryError && status.state === "failed" ? <span role="alert" className="channel-coordinator-error">{retryError}</span> : null}
  </div>;
}
