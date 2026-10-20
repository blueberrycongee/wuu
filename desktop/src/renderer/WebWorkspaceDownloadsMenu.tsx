import { useEffect, useId, useRef, useState } from "react";
import type { WebWorkspaceApi } from "../shared/webWorkspace";
import type { WebWorkspaceDownloadCommand, WebWorkspaceDownloadState } from "../shared/webWorkspaceDownloads";
import { composerMenuWidth, FloatingMenuPortal, menuOpeningKey, useFloatingMenuFocus } from "./ComposerFloatingMenu";
import { Download, X } from "./WuuIcons";
import { useI18n } from "./i18n";
import { rawErrorMessage } from "./UserFacingErrors";
import { NativeBrowserPopover } from "./NativeBrowserPopover";
import "./styles/web-workspace-downloads.css";

/** Window-owned history stays available when the initiating website is closed. */
export function WebWorkspaceDownloadsMenu({ api, active = true, destinationId, presentation = "toolbar" }: { api?: WebWorkspaceApi; active?: boolean; destinationId?: string; presentation?: "toolbar" | "menu" }): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [items, setItems] = useState<WebWorkspaceDownloadState[]>([]);
  const [open, setOpen] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  const pending = useRef(new Set<string>());
  const live = useRef(true);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const panelId = useId();
  const titleId = useId();
  const activeCount = items.filter(item => !item.done).length;
  const title = activeCount ? t("webWorkspace.downloads.active", { count: formatNumber(activeCount) }) : t("webWorkspace.downloads.title");

  useEffect(() => { live.current = true; return () => { live.current = false; }; }, []);
  useEffect(() => { if (!active) setOpen(false); }, [active]);
  useEffect(() => {
    if (!api) return;
    let disposed = false;
    let changed = false;
    const unsubscribe = api.onDownloads(next => {
      changed = true;
      setItems(next);
      setLoadError(false);
    });
    // Subscribe before requesting the snapshot. A newer progress event must
    // not be overwritten by the older reply from this initial request.
    void api.listDownloads().then(next => {
      if (!disposed && !changed) { setItems(next); setLoadError(false); }
    }).catch(() => { if (!disposed && !changed) setLoadError(true); });
    return () => { disposed = true; unsubscribe(); };
  }, [api, open]);
  useFloatingMenuFocus(panel, "downloads", open && active);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent): void => {
      if (!(event.target instanceof Node) || trigger.current?.contains(event.target) || panel.current?.contains(event.target)) return;
      setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [open]);

  const persistenceError = items.find(item => item.persistenceError)?.persistenceError;
  const close = (): void => { setOpen(false); trigger.current?.focus(); };
  const command = (id: string, action: WebWorkspaceDownloadCommand): void => {
    if (!api || pending.current.has(id)) return;
    pending.current.add(id);
    setBusy(new Set(pending.current));
    setErrors(current => { const next = { ...current }; delete next[id]; return next; });
    void api.downloadCommand(id, action).catch((cause: unknown) => {
      if (live.current) setErrors(current => ({ ...current, [id]: rawErrorMessage(cause) }));
    }).finally(() => {
      pending.current.delete(id);
      if (live.current) setBusy(new Set(pending.current));
    });
  };
  const bytes = (value: number): string => {
    const units = ["B", "KB", "MB", "GB", "TB"];
    const index = Math.min(units.length - 1, Math.max(0, Math.floor(Math.log(Math.max(1, value)) / Math.log(1024))));
    return `${formatNumber(value / 1024 ** index, { maximumFractionDigits: index ? 1 : 0 })} ${units[index]}`;
  };
  const status = (item: WebWorkspaceDownloadState): string => {
    if (item.state === "completed") return t("webWorkspace.downloads.completed");
    if (item.state === "cancelled") return t("webWorkspace.downloads.cancelled");
    if (item.state === "interrupted") return t(item.done ? "webWorkspace.downloads.failed" : "webWorkspace.downloads.interrupted");
    if (!item.savePath) return t("webWorkspace.downloads.choosing");
    if (item.paused) return t("webWorkspace.downloads.paused");
    return t("webWorkspace.downloads.downloading");
  };
  const actionButton = (item: WebWorkspaceDownloadState, action: WebWorkspaceDownloadCommand, label: string): JSX.Element => (
    <button type="button" className="settings-button settings-button-ghost" data-download-command={action}
      disabled={busy.has(item.id)} onClick={() => command(item.id, action)}>{label}</button>
  );

  return <>
    <button ref={trigger} type="button" className={presentation === "menu" ? "web-workspace-menu-item" : "icon-button workspace-browser-nav web-workspace-downloads-trigger"}
      data-web-workspace-downloads-trigger="" disabled={!api} aria-label={title} title={title}
      aria-haspopup="dialog" aria-expanded={open && active} aria-controls={open && active ? panelId : undefined}
      onClick={() => setOpen(value => !value)} onKeyDown={event => {
        if (menuOpeningKey(event)) setOpen(true);
      }}>
      <Download className="icon" aria-hidden="true" />
      {presentation === "menu" ? <span>{title}</span> : activeCount ? <span className="web-workspace-download-count" aria-hidden="true">{formatNumber(activeCount)}</span> : null}
    </button>
    {open && active ? <NativeBrowserPopover onDismiss={() => setOpen(false)}><FloatingMenuPortal anchorRef={trigger} owner="web-workspace-downloads" placement="below" align="right" width={composerMenuWidth(400)} flip>
      <div ref={panel} id={panelId} className="web-workspace-downloads" data-web-workspace-downloads="" data-wuu-layer="popover" data-web-workspace-owner={destinationId}
        role="dialog" aria-labelledby={titleId} onKeyDown={event => {
          if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
        }} onBlur={event => {
          if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget)
            && !trigger.current?.contains(event.relatedTarget)) setOpen(false);
        }}>
        <div className="web-workspace-downloads-heading">
          <span id={titleId}>{t("webWorkspace.downloads.title")}</span>
          <button type="button" className="icon-button" aria-label={t("common.close")} title={t("common.close")} onClick={close}><X className="icon" aria-hidden="true" /></button>
        </div>
        {loadError ? <p className="web-workspace-downloads-empty" role="alert">{t("webWorkspace.downloads.loadFailed")}</p>
          : !items.length ? <p className="web-workspace-downloads-empty">{t("webWorkspace.downloads.empty")}</p> : null}
        {persistenceError ? <p className="web-workspace-downloads-empty" role="alert">{t(persistenceError === "read"
          ? "webWorkspace.downloads.historyReadFailed" : "webWorkspace.downloads.historyWriteFailed")}</p> : null}
        {items.length ? <ul className="web-workspace-downloads-list">
          {items.map(item => <li key={item.id} className="web-workspace-download" data-download-id={item.id} data-download-state={item.state} data-download-paused={item.paused ? "true" : undefined}>
            <div className="web-workspace-download-filename" title={item.savePath || item.filename}>{item.filename}</div>
            <p className="web-workspace-download-status" data-failed={item.state === "interrupted" && item.done ? "true" : undefined}>{status(item)}</p>
            {item.state !== "cancelled" ? <p className="web-workspace-download-size">{item.totalBytes > 0 && !item.done
              ? t("webWorkspace.downloads.progress", { received: bytes(item.receivedBytes), total: bytes(item.totalBytes) })
              : bytes(item.receivedBytes)}</p> : null}
            {!item.done && item.savePath ? <progress className="web-workspace-download-progress" max={item.totalBytes || undefined}
              value={item.totalBytes > 0 ? item.receivedBytes : undefined} aria-label={item.filename} /> : null}
            <div className="web-workspace-download-actions">
              {item.state === "completed" ? <>
                {actionButton(item, "open", t("webWorkspace.downloads.open"))}
                {actionButton(item, "reveal", t("webWorkspace.downloads.reveal"))}
              </> : !item.done ? <>
                {item.canResume && (item.paused || item.state === "interrupted") ? actionButton(item, "resume", t("webWorkspace.downloads.resume"))
                  : item.state === "progressing" && item.savePath ? actionButton(item, "pause", t("webWorkspace.downloads.pause")) : null}
                {actionButton(item, "cancel", t("common.cancel"))}
              </> : null}
            </div>
            {errors[item.id] ? <p className="web-workspace-download-error" role="alert">{errors[item.id]}</p> : null}
          </li>)}
        </ul> : null}
      </div>
    </FloatingMenuPortal></NativeBrowserPopover> : null}
  </>;
}
