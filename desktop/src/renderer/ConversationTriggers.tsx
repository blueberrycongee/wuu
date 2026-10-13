import { useEffect, useId, useRef, useState } from "react";
import type { ManagedProcessSummary } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useConversationRenderActive } from "./ConversationRenderActivity";
import { useI18n } from "./i18n";
import { AlertCircle, Bell, ChevronRight, X } from "./WuuIcons";
import "./styles/conversation-triggers.css";

export function ConversationTriggers({ threadID }: { threadID: string }): JSX.Element {
  const { t } = useI18n();
  const active = useConversationRenderActive();
  const [open, setOpen] = useState(false);
  const [processes, setProcesses] = useState<ManagedProcessSummary[]>();
  const [loadError, setLoadError] = useState(false);
  const [actionError, setActionError] = useState(false);
  const [busy, setBusy] = useState<string>();
  const [revision, setRevision] = useState(0);
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const panelID = useId();
  const mounted = useRef(true);
  const mutation = useRef(0);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  useEffect(() => {
    if (!active) return;
    let disposed = false;
    let pending = false;
    const refresh = async (): Promise<void> => {
      if (pending || document.visibilityState === "hidden") return;
      pending = true;
      const generation = mutation.current;
      try {
        const result = await window.wuu.listManagedProcesses(threadID);
        if (!disposed && generation === mutation.current) {
          setProcesses(result.processes.filter((process) => process.trigger));
          setLoadError(false);
        }
      } catch {
        if (!disposed) setLoadError(true);
      } finally {
        pending = false;
      }
    };
    void refresh();
    // Only the latest answer in a visible conversation owns this subscription.
    // Refreshing also discovers tasks started by another client or a later turn.
    const interval = window.setInterval(() => void refresh(), 5000);
    document.addEventListener("visibilitychange", refresh);
    window.addEventListener("focus", refresh);
    return () => {
      disposed = true;
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", refresh);
      window.removeEventListener("focus", refresh);
    };
  }, [threadID, active, open, revision]);

  useEffect(() => { if (!active) setOpen(false); }, [active]);
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => panel.current?.focus({ preventScroll: true }));
    const dismiss = (event: PointerEvent): void => {
      if (event.target instanceof Node && !anchor.current?.contains(event.target) && !panel.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => { cancelAnimationFrame(frame); document.removeEventListener("pointerdown", dismiss); };
  }, [open]);

  const waiting = (processes ?? []).filter((process) => process.trigger?.status === "waiting" || process.trigger?.status === "received");
  const label = waiting.length > 0 && !loadError ? `${t("triggers.title")} (${waiting.length})` : t("triggers.title");
  const ordered = [...(processes ?? [])].sort((a, b) => {
    const aActive = a.trigger?.status === "waiting" || a.trigger?.status === "received";
    const bActive = b.trigger?.status === "waiting" || b.trigger?.status === "received";
    return Number(bActive) - Number(aActive) || b.started_at.localeCompare(a.started_at) || a.id.localeCompare(b.id);
  });
  const close = (): void => { setOpen(false); anchor.current?.focus({ preventScroll: true }); };
  const cancel = async (id: string): Promise<void> => {
    if (busy) return;
    mutation.current += 1;
    setBusy(id);
    setActionError(false);
    try {
      const result = await window.wuu.cancelProcessNotifications(threadID, id);
      if (mounted.current) setProcesses((current) => current?.map((process) => process.id === id ? result.process : process));
    } catch {
      if (mounted.current) setActionError(true);
    } finally {
      mutation.current += 1;
      if (mounted.current) { setBusy(undefined); setRevision((value) => value + 1); }
    }
  };

  return <>
    <button ref={anchor} type="button" className="message-action-button conversation-trigger-button"
      aria-label={label} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? panelID : undefined}
      title={label} onClick={() => setOpen((value) => !value)}
      onKeyDown={(event) => { if (event.key === "ArrowDown") { event.preventDefault(); setOpen(true); } }}>
      {loadError ? <AlertCircle size={15} aria-hidden="true" /> : <Bell size={15} aria-hidden="true" />}
    </button>
    {open ? <FloatingMenuPortal anchorRef={anchor} owner="conversation-triggers" placement="above" align="left" width={360} flip
      mobileSheet={{ label: t("triggers.title"), onClose: close }}>
      <div ref={panel} id={panelID} role="dialog" tabIndex={-1} aria-label={t("triggers.title")}
        className="composer-context-menu conversation-trigger-panel"
        onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); } }}
        onBlur={(event) => {
          if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget) && !anchor.current?.contains(event.relatedTarget)) setOpen(false);
        }}>
        <div className="conversation-trigger-heading"><span>{t("triggers.title")}</span>
          <button type="button" className="settings-button settings-button-ghost settings-icon-button" aria-label={t("common.close")} onClick={close}><X size={16} /></button>
        </div>
        {loadError ? <p className="conversation-trigger-error" role="alert">{t("triggers.loadFailed")}</p> : null}
        {actionError ? <p className="conversation-trigger-error" role="alert">{t("triggers.cancelFailed")}</p> : null}
        {!processes && !loadError ? <p>{t("common.loadingEllipsis")}</p> : null}
        {processes?.length === 0 && !loadError ? <p>{t("triggers.empty")}</p> : null}
        <div className="conversation-trigger-list">
          {ordered.map((process) => <ProcessTriggerRow key={process.id} threadID={threadID} process={process}
            busy={busy !== undefined} cancelling={busy === process.id} onCancel={() => void cancel(process.id)} />)}
        </div>
      </div>
    </FloatingMenuPortal> : null}
  </>;
}

function ProcessTriggerRow({ threadID, process, busy, cancelling, onCancel }: {
  threadID: string; process: ManagedProcessSummary; busy: boolean; cancelling: boolean; onCancel: () => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const [output, setOutput] = useState<string>();
  const [failed, setFailed] = useState(false);
  const trigger = process.trigger!;
  const pending = trigger.status === "waiting" || trigger.status === "received";
  useEffect(() => {
    if (!expanded) return;
    let disposed = false;
    setOutput(undefined);
    setFailed(false);
    void window.wuu.readManagedProcess({ thread_id: threadID, process_id: process.id, max_bytes: 8192 }).then((result) => {
      if (!disposed) setOutput(result.output);
    }).catch(() => { if (!disposed) setFailed(true); });
    return () => { disposed = true; };
  }, [expanded, threadID, process.id]);

  const schedule = [
    trigger.on_exit ? t("triggers.onExit") : "",
    trigger.recheck_minutes ? t("triggers.interval", { minutes: trigger.recheck_minutes }) : "",
    trigger.recheck_minutes && trigger.next_recheck_at
      ? t("triggers.next", { time: formatDate(trigger.next_recheck_at, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }) }) : "",
  ].filter(Boolean);
  const metadata = pending && schedule.length > 0
    ? [...(trigger.status === "received" ? [t("triggers.received")] : []), ...schedule]
    : [t(`triggers.${trigger.status}`)];

  return <section className="conversation-trigger-row">
    <details open={expanded} onToggle={(event) => setExpanded(event.currentTarget.open)}>
      <summary title={t("triggers.source")}>
        <ChevronRight className="conversation-trigger-chevron" size={14} aria-hidden="true" />
        <span className="conversation-trigger-command" title={process.command}>{process.command}</span>
        <span className="conversation-trigger-meta">
          {metadata.map((text) => <span key={text}>{text}</span>)}
        </span>
      </summary>
      <pre>{failed ? t("triggers.outputFailed") : output === undefined ? t("common.loadingEllipsis") : output || t("triggers.noOutput")}</pre>
    </details>
    {pending ? <button type="button" className="settings-button settings-button-ghost conversation-trigger-cancel"
      disabled={busy} aria-busy={cancelling} aria-label={cancelling ? t("triggers.cancelling") : t("triggers.cancel")}
      onClick={onCancel} title={t("triggers.cancelHint")}>{t("common.cancel")}</button> : null}
  </section>;
}
