import { MessageCircle, Minus, MoreHorizontal } from "./WuuIcons";
import { createContext, type ReactNode, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ThreadItem, Turn } from "../shared/protocol";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { streamTextKey, streamTextStore } from "./StreamText";
import { useI18n } from "./i18n";
import "./styles/document-dock.css";

interface WorkspaceDocumentTurnDockProps {
  children: ReactNode;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenConversation?: () => void;
  title?: string;
  topAccessory?: ReactNode;
  turns: Turn[];
  waitingQuery?: string;
}

export const WorkspaceDocumentDrawerContext = createContext<{
  documentResultExpanded: boolean;
  collapseDocumentResult: () => void;
} | null>(null);

const editorSelector = '[data-wuu-component="composer-input"]';
// Composer pickers are portalled into the shared UI layer, outside this surface.
const ownedPopoverSelector = '[data-floating-menu-owner^="composer-"], [data-floating-menu-owner="codex-runtime"]';

function latestUserItem(turn: Turn): ThreadItem | undefined {
  for (let index = turn.items.length - 1; index >= 0; index -= 1) {
    const item = turn.items[index];
    if (item.type === "user_message") return item;
  }
  return undefined;
}

function finalAnswerItems(turn: Turn): ThreadItem[] {
  return turn.items.filter((item) => item.type === "agent_message" && item.terminal);
}

function latestResultTurn(turns: Turn[]): Turn | undefined {
  for (let index = turns.length - 1; index >= 0; index -= 1) {
    const turn = turns[index];
    if (latestUserItem(turn) && finalAnswerItems(turn).length > 0) return turn;
  }
  return undefined;
}

export function WorkspaceDocumentTurnDock({
  children, cwd, onOpenFile, onOpenConversation, title, topAccessory, turns, waitingQuery,
}: WorkspaceDocumentTurnDockProps): JSX.Element {
  const { t } = useI18n();
  const turn = useMemo(() => latestResultTurn(turns), [turns]);
  const [expanded, setExpanded] = useState(false);
  const [inputFocused, setInputFocused] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [minimized, setMinimized] = useState(false);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const restoreRef = useRef<HTMLButtonElement>(null);
  const restoreEditorRef = useRef(false);
  const finalAnswers = turn ? finalAnswerItems(turn) : [];
  const previousTurnIDRef = useRef(turn?.id);
  const previousFinalAnswerCountRef = useRef(finalAnswers.length);
  const headerVisible = !minimized && (inputFocused || expanded || menuOpen);
  const drawerContext = useMemo(() => ({
    documentResultExpanded: expanded && !minimized,
    collapseDocumentResult: () => setExpanded(false),
  }), [expanded, minimized]);

  useEffect(() => {
    if (previousTurnIDRef.current !== turn?.id) {
      previousTurnIDRef.current = turn?.id;
      previousFinalAnswerCountRef.current = finalAnswers.length;
      setExpanded(Boolean(turn));
      return;
    }
    if (previousFinalAnswerCountRef.current === 0 && finalAnswers.length > 0) setExpanded(true);
    previousFinalAnswerCountRef.current = finalAnswers.length;
  }, [finalAnswers.length, turn?.id]);

  function ownsFocus(target: EventTarget | null): boolean {
    return target instanceof Node && (Boolean(surfaceRef.current?.contains(target)) ||
      (target instanceof Element && Boolean(target.closest(ownedPopoverSelector))));
  }

  useEffect(() => {
    if (minimized) return;
    const dismissOutside = (event: Event) => {
      if (ownsFocus(event.target)) return;
      setInputFocused(false);
      setMenuOpen(false);
      setExpanded(false);
    };
    document.addEventListener("pointerdown", dismissOutside, true);
    document.addEventListener("focusin", dismissOutside);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside, true);
      document.removeEventListener("focusin", dismissOutside);
    };
  }, [minimized]);

  useLayoutEffect(() => {
    if (minimized) restoreRef.current?.focus({ preventScroll: true });
    else if (restoreEditorRef.current) {
      restoreEditorRef.current = false;
      surfaceRef.current?.querySelector<HTMLElement>(editorSelector)?.focus({ preventScroll: true });
    }
  }, [minimized]);

  useLayoutEffect(() => {
    if (menuOpen) menuRef.current?.querySelector<HTMLButtonElement>("button")?.focus({ preventScroll: true });
  }, [menuOpen]);

  const minimize = () => {
    setMenuOpen(false);
    setInputFocused(false);
    setMinimized(true);
  };
  const closeMenu = () => {
    setMenuOpen(false);
    menuButtonRef.current?.focus({ preventScroll: true });
  };
  const toggleLabel = expanded ? t("workspace.documentTurn.collapse") : t("workspace.documentTurn.expand");
  const detailsID = turn ? `workspace-document-turn-${turn.id}` : undefined;

  return (
    <WorkspaceDocumentDrawerContext.Provider value={drawerContext}>
      <div ref={surfaceRef} className="workspace-document-turn-dock"
        data-wuu-component="workspace-document-turn" data-minimized={minimized}
        onFocusCapture={(event) => {
          if (event.target.matches(editorSelector)) setInputFocused(true);
        }}
        onBlurCapture={(event) => {
          if (ownsFocus(event.relatedTarget)) return;
          if (event.relatedTarget) {
            setInputFocused(false);
            setMenuOpen(false);
            if (!minimized) setExpanded(false);
          } else {
            // A null relatedTarget can be a portal mounting or native window blur.
            // Let focus settle; do not discard the transcript presentation here.
            queueMicrotask(() => {
              if (surfaceRef.current && !ownsFocus(document.activeElement)) setInputFocused(false);
            });
          }
        }}
        onKeyDown={(event) => {
          if (event.key !== "Escape" || event.defaultPrevented) return;
          if (menuOpen) {
            event.preventDefault();
            event.stopPropagation();
            closeMenu();
          } else if (expanded && !minimized) {
            event.preventDefault();
            event.stopPropagation();
            setExpanded(false);
          }
        }}>
        <div className="workspace-document-turn-content" inert={minimized} aria-hidden={minimized || undefined}>
          {topAccessory ? <div className="composer-top-accessory">{topAccessory}</div> : null}
          <div className="workspace-document-turn-header" data-visible={headerVisible}
            aria-hidden={!headerVisible} inert={!headerVisible}>
            <div className="workspace-document-turn-header-row">
              <button type="button" className="workspace-document-turn-icon" data-testid="document-chat-minimize"
                aria-label={t("workspace.documentTurn.minimize")} onClick={minimize}><Minus size={18} /></button>
              <button type="button" className="workspace-document-turn-summary"
                aria-controls={detailsID} aria-expanded={expanded} aria-label={toggleLabel}
                disabled={!turn}
                onPointerDown={(event) => { if (event.button === 0) event.preventDefault(); }}
                onClick={() => setExpanded((current) => !current)}>
                {waitingQuery && !expanded ? (
                  <span className="workspace-document-turn-waiting-query wuu-live-text-wave"
                    data-text={waitingQuery} role="status" aria-live="polite">{waitingQuery}</span>
                ) : <span className="workspace-document-turn-title">{title || t("workspace.documentTurn.title")}</span>}
              </button>
              <button ref={menuButtonRef} type="button" className="workspace-document-turn-icon"
                aria-label={t("workspace.documentTurn.options")} aria-haspopup="menu" aria-expanded={menuOpen}
                onPointerDown={(event) => { if (event.button === 0) event.preventDefault(); }}
                onClick={() => setMenuOpen((current) => !current)}><MoreHorizontal size={18} /></button>
            </div>
          </div>
          {menuOpen ? <div ref={menuRef} className="workspace-document-turn-menu" role="menu"
            aria-label={t("workspace.documentTurn.options")}
            onKeyDown={(event) => {
              const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'));
              const current = items.indexOf(document.activeElement as HTMLButtonElement);
              let next: number;
              if (event.key === "ArrowDown") next = (current + 1) % items.length;
              else if (event.key === "ArrowUp") next = (current - 1 + items.length) % items.length;
              else if (event.key === "Home") next = 0;
              else if (event.key === "End") next = items.length - 1;
              else if (event.key === "Tab") { setMenuOpen(false); return; }
              else return;
              event.preventDefault();
              items[next]?.focus();
            }}>
            {onOpenConversation ? <button type="button" role="menuitem" onClick={() => {
              setMenuOpen(false);
              onOpenConversation();
            }}>{t("workspace.documentTurn.openConversation")}</button> : null}
            <button type="button" role="menuitem" onClick={minimize}>{t("workspace.documentTurn.minimize")}</button>
          </div> : null}
          {turn ? <section className="workspace-document-turn-drawer" data-testid="workspace-document-turn-drawer"
            data-wuu-state={expanded ? "expanded" : "collapsed"}>
            {expanded ? <div className="workspace-document-turn-details" id={detailsID}>
              <div className="workspace-document-turn-result">
                {finalAnswers.map((item) => {
                  const streamKey = streamTextKey(turn.id, item.id, "text");
                  const isLive = item.status === "in_progress";
                  return <StreamingMarkdown key={item.id} streamKey={streamKey}
                    initialText={isLive && streamTextStore.has(streamKey) ? streamTextStore.seedValue(streamKey) : item.text}
                    cwd={cwd} onOpenFile={onOpenFile} isLive={isLive} phase="final_answer" />;
                })}
              </div>
            </div> : null}
          </section> : null}
          {/* Keep this owner mounted across first result, dismissal and minimization. */}
          <div className="workspace-document-turn-composer">{children}</div>
        </div>
        {minimized ? <button ref={restoreRef} type="button" className="workspace-document-turn-restore"
          data-testid="document-chat-restore" aria-label={t("workspace.documentTurn.restore")}
          onClick={() => { restoreEditorRef.current = true; setMinimized(false); }}><MessageCircle size={20} /></button> : null}
      </div>
    </WorkspaceDocumentDrawerContext.Provider>
  );
}
