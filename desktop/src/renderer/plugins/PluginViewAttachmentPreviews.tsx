import { useCallback, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ConversationAttachmentPreview, ConversationAttachmentPreviewContext, type ConversationAttachmentRequest } from "../ConversationAttachmentPreview";
import { useConversationAttachmentTabs } from "../ConversationAttachmentTabs";
import { useI18n } from "../i18n";
import { Maximize2, Minimize2, X } from "../WuuIcons";

/** The registered view owns remote byte previews; Coding preview state is untouched. */
export function PluginViewAttachmentPreviews({ children }: { children: ReactNode }): JSX.Element {
  const tabs = useConversationAttachmentTabs();
  const contentId = useId();
  const root = useRef<HTMLDivElement>(null), panel = useRef<HTMLElement>(null);
  const [compact, setCompact] = useState(false);
  useLayoutEffect(() => {
    if (!root.current || typeof ResizeObserver === "undefined") return;
    const measure = () => setCompact(root.current!.getBoundingClientRect().width < 760);
    const observer = new ResizeObserver(measure); observer.observe(root.current); measure();
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => { if (tabs.open) panel.current?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus({preventScroll:true}); }, [tabs.open]);
  const expanded = tabs.open && (tabs.manualGlobalized || compact);
  const { t } = useI18n();
  const opener = useRef<HTMLElement | null>(null);
  const openAttachment = useCallback((request: ConversationAttachmentRequest) => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    tabs.openAttachment(request);
  }, [tabs.openAttachment]);
  const close = () => { tabs.setOpen(false); if (opener.current?.isConnected) opener.current.focus({ preventScroll: true }); };
  const active = tabs.state.tabs.find(tab => tab.id === tabs.state.activeTabID);
  return <ConversationAttachmentPreviewContext.Provider value={openAttachment}>
    <div ref={root} className="plugin-view-layout" data-preview-expanded={expanded || undefined}>
      <div className="plugin-view-content-region" inert={expanded}>{children}</div>
      {tabs.open && active ? <aside ref={panel} className="plugin-view-attachment-panel" aria-label={t("conversationAttachment.open", { name: active.name })} onKeyDown={event => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
      }}>
        <div className="workspace-panel-tabbar">
          <div className="plugin-view-attachment-tabs" role="tablist" aria-label={t("plugins.viewTabs")}>
            {tabs.state.tabs.map((tab, index) => <div key={tab.id} className="plugin-view-attachment-tab">
              <button type="button" role="tab" id={`${contentId}-tab-${index}`} aria-selected={tab.id === active.id} tabIndex={tab.id === active.id ? 0 : -1} aria-controls={contentId}
                title={tab.name} onClick={() => tabs.focusTab(tab.id)} onKeyDown={event => {
                  const next = event.key === "ArrowRight" ? (index + 1) % tabs.state.tabs.length : event.key === "ArrowLeft" ? (index + tabs.state.tabs.length - 1) % tabs.state.tabs.length : event.key === "Home" ? 0 : event.key === "End" ? tabs.state.tabs.length - 1 : undefined;
                  if (next === undefined) return;
                  event.preventDefault(); tabs.focusTab(tabs.state.tabs[next].id);
                  event.currentTarget.closest('[role="tablist"]')?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
                }}>{tab.name}</button>
              <button type="button" className="icon-button" aria-label={`${t("common.close")} ${tab.name}`} onClick={() => { tabs.closeTab(tab.id); if (tabs.state.tabs.length === 1) close(); }}><X /></button>
            </div>)}
          </div>
          <button type="button" className="icon-button" aria-label={t(tabs.manualGlobalized ? "workspace.exitFullPanel" : "workspace.expandFullPanel")} aria-pressed={tabs.manualGlobalized} disabled={compact} onClick={() => tabs.setManualGlobalized(value => !value)}>{tabs.manualGlobalized ? <Minimize2 /> : <Maximize2 />}</button>
          <button type="button" className="icon-button" aria-label={t("common.close")} onClick={close}><X /></button>
        </div>
        <div id={contentId} role="tabpanel" aria-labelledby={`${contentId}-tab-${tabs.state.tabs.indexOf(active)}`} tabIndex={0} className="plugin-view-attachment-content"><ConversationAttachmentPreview key={active.id} {...active} /></div>
      </aside> : null}
    </div>
  </ConversationAttachmentPreviewContext.Provider>;
}
