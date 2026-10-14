import { useEffect, useState } from "react";
import type { Thread, ThreadExecutionEnvironmentResult } from "../shared/protocol";
import { ChevronDown, Monitor, RefreshCw } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import { useI18n } from "./i18n";

/** Inspect the pinned destination only while its workspace tool picker is open. */
export function SessionEnvironmentStatus({ thread }: { thread: Thread }): JSX.Element | null {
  const { t } = useI18n();
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<{
    threadID: string;
    environment?: ThreadExecutionEnvironmentResult["environment"];
    error?: boolean;
  }>({ threadID: thread.id });
  const read = window.wuu.getThreadExecutionEnvironment;
  useEffect(() => {
    if (!read) return;
    let cancelled = false;
    setState({ threadID: thread.id });
    void read(thread.id).then(
      result => {
        if (!cancelled && result.thread_id === thread.id) {
          setState({ threadID: thread.id, environment: result.environment });
        }
      },
      () => { if (!cancelled) setState({ threadID: thread.id, error: true }); },
    );
    return () => { cancelled = true; };
  }, [read, thread.id, thread.status, thread.latest_completed_turn_id, refresh]);
  if (!read) return null;
  const current = state.threadID === thread.id ? state : undefined;
  const environment = current?.environment;
  const loading = !environment && !current?.error;
  const name = environment?.name === "local" ? t("execution.local") : environment?.name;
  return <details className="session-environment-status" data-wuu-component="session-environment">
    <summary className="workspace-tool-menu-item session-environment-summary">
      <span className="workspace-tool-menu-icon" aria-hidden="true"><Monitor className="icon" /></span>
      <TruncatedText className="session-environment-name" text={name || t("execution.environmentLabel")} />
      <ChevronDown className="icon-sm" aria-hidden="true" />
    </summary>
    <div className="session-environment-details">
      <div className="session-environment-state">
        <span role={current?.error ? "alert" : undefined}>
          {current?.error ? t("execution.inspectFailed") : environment
            ? t(environment.state === "not_initialized" ? "execution.state.notInitialized" : `execution.state.${environment.state}`)
            : t("common.loadingEllipsis")}
        </span>
        <Tooltip content={t("execution.refreshStatus")}>
          <button className="icon-button" type="button" disabled={loading}
            aria-label={t("execution.refreshStatus")} onClick={() => setRefresh(value => value + 1)}>
            <RefreshCw className="icon-sm" aria-hidden="true" />
          </button>
        </Tooltip>
      </div>
      {environment?.root ? <dl>
        {environment.backend && environment.backend !== "local" && environment.backend !== "external" ? <div>
          <dt>{t("execution.backend")}</dt><dd>{environment.backend}</dd>
        </div> : null}
        <div><dt>{t("execution.workspace")}</dt><dd>{environment.root}</dd></div>
      </dl> : null}
      {environment?.backend && !["local", "external"].includes(environment.backend) ? <p className="session-environment-local-views">{t("execution.localViews")}</p> : null}
    </div>
  </details>;
}
