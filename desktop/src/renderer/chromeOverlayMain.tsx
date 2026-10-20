import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { closestCenter, DndContext, useSensor, useSensors } from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { ChromeOverlayCommand, ChromeOverlayRendererApi, ChromeOverlayRequest, ChromeRibbonModel } from "../shared/chromeOverlay";
import { focusAfterRibbonClose, RibbonDestination } from "./NavigationRibbonDestination";
import { ChromeRibbonIcon } from "./ChromeRibbonIcon";
import { SidebarAccountPanel, SidebarAccountTrigger } from "./SidebarAccountParts";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { ContextChatCapsuleSurface } from "./ContextChatCapsuleSurface";
import { DesktopUpdateButtonSurface } from "./DesktopUpdateButtonSurface";
import { Plus } from "./WuuIcons";
import { I18nProvider } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { startFocusModality } from "./FocusModality";
import "./styles.css";
import "./styles/chrome-overlay.css";

const api = (window as unknown as Window & { wuuChromeOverlay: ChromeOverlayRendererApi }).wuuChromeOverlay;
startFocusModality();

function RibbonSurface({ model, send }: { model: ChromeRibbonModel; send: (command: ChromeOverlayCommand) => void }): JSX.Element {
  const main = useRef<HTMLDivElement>(null);
  const sensors = useSensors(useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }));
  const ids = model.items.map(item => item.id);
  const tabStop = (model.items.find(item => item.active && !item.disabled) ?? model.items.find(item => !item.disabled))?.id;
  useLayoutEffect(() => { if (main.current) main.current.scrollTop = model.scrollTop; }, []);
  const active = model.items.find(item => item.active)?.id;
  useLayoutEffect(() => {
    [...document.querySelectorAll<HTMLButtonElement>("[data-ribbon-id]")].find(button => button.dataset.ribbonId === active)?.scrollIntoView({ block: "nearest" });
  }, [active]);
  const close = (id: string) => {
    focusAfterRibbonClose(id);
    send({ type: "close-tab", id });
  };
  return <nav className="sidebar-ribbon" data-expanded="true" aria-label={model.label}
    style={{ paddingTop: model.paddingTop, paddingBottom: model.paddingBottom }}
    onKeyDownCapture={() => send({ type: "retain", active: true })}
    onPointerDownCapture={() => send({ type: "retain", active: false })}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) { send({ type: "retain", active: false }); send({ type: "pointer", inside: false }); } }}
    onPointerEnter={() => send({ type: "pointer", inside: true })} onPointerLeave={() => send({ type: "pointer", inside: false })}>
    <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis]}
      accessibility={{ screenReaderInstructions: { draggable: model.reorderHint } }}
      onDragStart={() => send({ type: "retain", active: true })}
      onDragCancel={() => { send({ type: "retain", active: false }); send({ type: "pointer", inside: false }); }}
      onDragEnd={({ active, over }) => {
        send({ type: "retain", active: false });
        if (over) send({ type: "reorder", id: String(active.id), target: String(over.id) });
        send({ type: "pointer", inside: false });
      }}>
      <div ref={main} className="sidebar-ribbon-main scrollbar-hidden" onScroll={event => send({ type: "scroll", top: event.currentTarget.scrollTop })}>
        <SortableContext items={ids} strategy={verticalListSortingStrategy}>
          {model.items.map(item => <RibbonDestination key={item.id} tabStop={item.id === tabStop} boundedNative nativeTooltip={false}
            item={{ ...item, nativeIcon: item.icon, icon: <ChromeRibbonIcon icon={item.icon} />, onSelect: () => send({ type: "select", id: item.id }), onClose: item.closable ? () => close(item.id) : undefined,
              onMute: item.canMute ? () => send({ type: "mute-tab", id: item.id, muted: !item.muted }) : undefined }}
            onMove={direction => { const target = ids[ids.indexOf(item.id) + direction]; if (target) send({ type: "reorder", id: item.id, target }); }}
            onMenu={item.website ? (x, y) => send({ type: "tab-menu", id: item.id, x, y }) : undefined} />)}
        </SortableContext>
        <button type="button" className="sidebar-ribbon-button sidebar-ribbon-add" aria-label={model.newTabLabel} title={model.newTabLabel}
          disabled={!model.canCreateTab} onClick={() => send({ type: "new-tab" })}>
          <Plus aria-hidden="true" /><span className="sidebar-ribbon-label">{model.newTabLabel}</span>
        </button>
      </div>
    </DndContext>
    {model.footer && <div className="sidebar-ribbon-footer">
      <button className="sidebar-ribbon-button" type="button" aria-label={model.footer.settings.title} aria-current={model.footer.settings.active ? "page" : undefined}
        disabled={model.footer.settings.disabled} onClick={() => send({ type: "settings" })}>
        <ChromeRibbonIcon icon={model.footer.settings.icon} /><span className="sidebar-ribbon-label">{model.footer.settings.title}</span>
      </button>
      {model.footer.account && <div className="sidebar-account"><SidebarAccountTrigger model={model.footer.account} onOpen={button => {
        const rect = button.getBoundingClientRect();
        send({ type: "account-open", anchor: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } });
      }} />{model.footer.update && <DesktopUpdateButtonSurface model={model.footer.update} boundedNative onActivate={() => send({ type: "desktop-update" })} />}</div>}
    </div>}
  </nav>;
}

function Overlay(): JSX.Element | null {
  const [request, setRequest] = useState<ChromeOverlayRequest | null>(null);
  const [revealed, setRevealed] = useState(true);
  const progress = useRef(0);
  const motionToken = useRef<string | undefined>(undefined);
  const latestRequest = useRef(request);
  latestRequest.current = request;
  useEffect(() => api.onVisibility(setRevealed), []);
  const previous = useRef<{ token: string; focus: boolean; target?: string } | undefined>(undefined);
  useEffect(() => {
    let received = false;
    const off = api.onState(state => { received = true; setRequest(state); });
    void api.ready().then(state => { if (!received) setRequest(state); });
    return off;
  }, []);
  const send = (command: ChromeOverlayCommand) => { if (request) api.command({ token: request.token, generation: request.generation, command }); };
  useLayoutEffect(() => {
    if (!request) { previous.current = undefined; return; }
    const root = document.documentElement;
    root.dataset.theme = request.appearance.theme;
    root.lang = request.appearance.locale;
    root.style.cssText = "";
    root.style.fontFamily = request.appearance.fontFamily;
    for (const [name, value] of Object.entries(request.appearance.variables)) if (/^--[a-zA-Z\d_-]+$/.test(name) && !/url\s*\(/i.test(value)) root.style.setProperty(name, value);
    root.dataset.chromeSurface = request.model.kind;
    const target = request.model.kind === "ribbon" ? request.model.focusTarget : undefined;
    const targetKey = JSON.stringify(target);
    const takeFocus = request.focus && (previous.current?.token !== request.token || !previous.current.focus || previous.current.target !== targetKey);
    previous.current = { token: request.token, focus: request.focus, target: targetKey };
    if (takeFocus) {
      const focused = target?.kind === "item" ? [...document.querySelectorAll<HTMLElement>("[data-ribbon-id]")].find(element => element.dataset.ribbonId === target.id)
        : target?.kind === "close" ? [...document.querySelectorAll<HTMLElement>("[data-ribbon-close-id]")].find(element => element.dataset.ribbonCloseId === target.id)
        : target?.kind === "desktop-update" ? document.querySelector<HTMLElement>(".desktop-update-button")
        : target?.kind === "account" ? document.querySelector<HTMLElement>(".sidebar-account-trigger")
        : target?.kind === "settings" ? document.querySelector<HTMLElement>(".sidebar-ribbon-footer > .sidebar-ribbon-button")
        : target?.kind === "new-tab" ? document.querySelector<HTMLElement>(".sidebar-ribbon-add") : undefined;
      (focused ?? document.querySelector<HTMLElement>("button:not(:disabled), input, textarea"))?.focus();
    }
    const frame = requestAnimationFrame(() => api.painted(request.token, request.generation));
    return () => cancelAnimationFrame(frame);
  }, [request]);
  useLayoutEffect(() => {
    if (!request || request.model.kind !== "ribbon") { progress.current = 0; motionToken.current = undefined; setRevealed(true); return; }
    if (motionToken.current !== request.token) { motionToken.current = request.token; progress.current = 0; }
    const duration = Number.parseFloat(getComputedStyle(document.querySelector(".sidebar-ribbon")!).getPropertyValue("--ribbon-motion-duration")) || 0;
    const from = progress.current, to = revealed ? 1 : 0, start = performance.now();
    let frame = 0;
    const tick = (now: number): void => {
      const elapsed = duration ? Math.max(0, Math.min(1, (now - start) / duration)) : 1;
      const eased = 1 - Math.pow(1 - elapsed, 3);
      progress.current = elapsed === 1 ? to : from + (to - from) * eased;
      const current = latestRequest.current;
      if (current) api.command({ token: current.token, generation: current.generation, command: { type: "reveal", fraction: progress.current } });
      if (elapsed < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [request?.token, revealed]);
  if (!request) return null;
  const language = request.appearance.locale;
  return <I18nProvider key={language} preferenceStore={{ get: () => language, set: async () => {}, subscribe: () => () => {} }}>
    <WuuUIRoot><div className="chrome-overlay-surface" onKeyDownCapture={event => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (request.model.kind === "ribbon" && event.key === "Tab") {
        const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
        if (index >= 0 && (event.shiftKey ? index > 0 : index < buttons.length - 1)) return;
      }
      if (request.model.kind !== "capsule" && (event.key === "Escape" || event.key === "Tab")) {
        event.preventDefault(); event.stopPropagation(); send({ type: "dismiss", reason: event.key === "Escape" ? "escape" : "tab" });
      }
    }}>
      {request.model.kind === "ribbon" ? <RibbonSurface model={request.model} send={send} />
        : request.model.kind === "capsule" ? <ContextChatCapsuleSurface key={request.token} model={request.model} send={send} />
        : <SidebarAccountPanel model={request.model.panel} onAction={action => send({ type: "account-action", action })} onClose={() => send({ type: "dismiss", reason: "escape" })} />}
    </div></WuuUIRoot>
  </I18nProvider>;
}

createRoot(document.getElementById("root")!).render(<Overlay />);
