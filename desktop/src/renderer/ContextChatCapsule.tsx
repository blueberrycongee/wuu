import { useEffect, useRef, useState } from "react";
import type { ApplicationDesktopChromeCapsuleModelV1 as ChromeCapsuleModel, ApplicationDesktopChromeOverlayCommandV1 as ChromeOverlayCommand } from "../../../packages/plugin-sdk/src/ui-desktop";
import { captureVisiblePage, type PageContextApi, type PageContextSnapshot, type PageContextSource } from "../shared/pageContext";
import type { RuntimeContext } from "../shared/protocol";
import { useChromeOverlay } from "./ChromeOverlay";
import { useI18n } from "./i18n";
import { ContextChatCapsuleSurface } from "./ContextChatCapsuleSurface";

type CapsuleEntry = { draft: string; sessionId?: string; pending?: Promise<void>; error?: string };
type Props = {
  source: PageContextSource;
  context?: RuntimeContext;
  visible: boolean;
  onSubmit: (text: string, page: PageContextSnapshot, context: RuntimeContext, sessionId?: string) => Promise<{ sessionId: string; sent: boolean }>;
  onOpen: (sessionId: string) => void;
};

/** One normal chat per source/runtime, retaining drafts when the user switches views. */
export function ContextChatCapsule(props: Props): JSX.Element {
  const entries = useRef(new Map<string, CapsuleEntry>());
  const [nativeSource, setNativeSource] = useState<PageContextSource>();
  useEffect(() => {
    const read = () => {
      const panel = document.querySelector<HTMLElement>('.web-workspace-panel[data-page-context-active="true"]');
      const id = panel?.dataset.websiteId, tabId = panel?.dataset.pageContextTabId;
      setNativeSource(id && tabId ? { kind: "website", id, tabId, title: panel.dataset.pageContextTitle || "Website" } : undefined);
    };
    read();
    window.addEventListener("wuu:page-context-source-changed", read);
    return () => window.removeEventListener("wuu:page-context-source-changed", read);
  }, []);
  const source = props.source.kind === "app" && props.source.id === "wuu:plugin" && nativeSource ? nativeSource : props.source;
  const key = JSON.stringify([props.context, source.kind, source.id, source.kind === "website" ? source.tabId : ""]);
  let entry = entries.current.get(key);
  if (!entry) { entry = { draft: "" }; entries.current.set(key, entry); }
  return <ContextChatCapsuleController key={key} {...props} source={source} entry={entry} />;
}

function ContextChatCapsuleController({ source, context, visible, onSubmit, onOpen, entry }: Props & { entry: CapsuleEntry }): JSX.Element {
  const { locale } = useI18n();
  const zh = locale === "zh-CN";
  const [expanded, setExpanded] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState(Boolean(entry.pending));
  const [error, setError] = useState(entry.error);
  const [sessionId, setSessionId] = useState(entry.sessionId);
  const [viewport, setViewport] = useState({ width: innerWidth, height: innerHeight });
  const mounted = useRef(true);
  const busyRef = useRef(Boolean(entry.pending));
  useEffect(() => {
    if (entry.pending) void entry.pending.then(() => {
      if (mounted.current) { busyRef.current = false; setBusy(false); setSessionId(entry.sessionId); setError(entry.error); }
    });
  }, [entry]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    const resize = () => setViewport({ width: innerWidth, height: innerHeight });
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  const width = expanded ? Math.min(menuOpen ? 280 : 416, viewport.width - 32) : 64;
  const height = menuOpen ? 152 : expanded && (error || sessionId) ? 96 : 64;
  const model: ChromeCapsuleModel = { kind: "capsule", expanded, menuOpen, draft: entry.draft, busy, sessionId, error,
    sourceTitle: source.title, label: zh ? "询问 Wuu" : "Ask Wuu", placeholder: zh ? "结合当前页面问 Wuu…" : "Ask about this page…",
    sendLabel: zh ? "发送" : "Send", openLabel: zh ? "打开对话" : "Open chat",
    collapseLabel: zh ? "收起，保留草稿" : "Collapse and keep draft" };
  const submit = async (text: string): Promise<void> => {
    if (busyRef.current || !text.trim() || !context) return;
    busyRef.current = true;
    entry.draft = text;
    setBusy(true); setError(undefined); entry.error = undefined;
    try {
      // Read after Send; native capture rejects navigation and tab-switch races.
      const page = source.kind === "website"
        ? await (window.wuu as typeof window.wuu & PageContextApi).readPageContext(source.id, source.tabId)
        : { kind: "app" as const, pageId: source.id, title: source.title, ...captureVisiblePage(true), capturedAt: new Date().toISOString() };
      const result = await onSubmit(text.trim(), page, context, entry.sessionId);
      entry.sessionId = result.sessionId;
      if (mounted.current) setSessionId(result.sessionId);
      if (!result.sent) throw new Error(zh ? "尚未发送，请重试。草稿已保留。" : "Not sent. Your draft is saved; try again.");
      entry.draft = "";
    } catch (cause) {
      entry.error = cause instanceof Error ? cause.message : String(cause);
      if (mounted.current) setError(entry.error);
    } finally {
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  const command = (command: ChromeOverlayCommand): void => {
    if (command.type === "capsule-expand") { setMenuOpen(false); setExpanded(true); }
    else if (command.type === "capsule-menu") { setMenuOpen(true); setExpanded(true); }
    else if (command.type === "capsule-draft") entry.draft = command.text;
    else if (command.type === "capsule-collapse") { entry.draft = command.text; setMenuOpen(false); setExpanded(false); }
    else if (command.type === "capsule-send" && !entry.pending) {
      const pending = submit(command.text);
      entry.pending = pending;
      void pending.finally(() => { if (entry.pending === pending) entry.pending = undefined; });
    }
    else if (command.type === "capsule-open" && entry.sessionId) onOpen(entry.sessionId);
  };
  const native = useChromeOverlay(visible && source.kind === "website", { x: viewport.width - width - 16, y: viewport.height - height - 16, width, height },
    model, command, () => { setMenuOpen(false); setExpanded(false); }, expanded);
  if (!visible || source.kind === "website" && !native) return <></>;
  return native ? <></> : <div className="context-chat-capsule-anchor" style={{ width }}><ContextChatCapsuleSurface model={model} send={command} /></div>;
}
