import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { PanelRightClose } from "./WuuIcons";
import { useAutoFollowScrollContainer } from "./AutoFollowScroll";
import { ConversationTurnList } from "./ConversationTurnList";
import { sideThreadMessagesToTurns } from "./SideThreadTurns";
import {
  SIDE_THREAD_MAX_WIDTH,
  SIDE_THREAD_MIN_WIDTH,
  type SideThreadEntryState,
} from "./SideThreadState";
import { Tooltip } from "./Tooltip";
import { latestAgentMessageItemID, TurnView } from "./TurnView";
import { useI18n } from "./i18n";

export type SideThreadPanelHandle = {
  focusComposer: () => void;
};

type SideThreadPanelProps = {
  entry: SideThreadEntryState;
  mainThreadId: string;
  width: number;
  composer: ReactNode;
  cwd?: string;
  onClose: () => void;
  onResizeStart: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onChangeDraft: (draft: string) => void;
  onOpenFile?: (path: string) => void;
};

export const SideThreadPanel = forwardRef<SideThreadPanelHandle, SideThreadPanelProps>(
  function SideThreadPanel(
    {
      entry,
      mainThreadId,
      width,
      composer,
      cwd,
      onClose,
      onResizeStart,
      onChangeDraft,
      onOpenFile,
    },
    ref,
  ) {
    const { t } = useI18n();
    const titleID = useId();
    const panelRef = useRef<HTMLElement | null>(null);
    const composerHostRef = useRef<HTMLDivElement | null>(null);
    const footerRef = useRef<HTMLDivElement | null>(null);
    const bodyScroll = useAutoFollowScrollContainer({ open: true });
    const turns = useMemo(
      () => sideThreadMessagesToTurns(entry.messages),
      [entry.messages],
    );
    const latestTurn = turns.at(-1);
    const latestMessageID = useMemo(
      () => latestAgentMessageItemID(turns),
      [turns],
    );

    const focusComposer = useCallback(() => {
      const host = composerHostRef.current;
      const textarea =
        host?.querySelector<HTMLTextAreaElement>("textarea:not(:disabled)") ??
        host?.querySelector<HTMLTextAreaElement>("textarea");
      textarea?.focus();
    }, []);

    useImperativeHandle(ref, () => ({ focusComposer }), [focusComposer]);

    // Message commits can land without a stream frame (history load, peer
    // windows), so re-anchor on them like the main conversation does for
    // turn snapshots.
    useEffect(() => {
      bodyScroll.scrollToBottom();
    }, [bodyScroll, entry.messages, entry.streaming]);

    useLayoutEffect(() => {
      const panel = panelRef.current;
      const footer = footerRef.current;
      if (!panel || !footer) {
        return undefined;
      }

      const updateFooterHeight = (): void => {
        const height = Math.ceil(footer.getBoundingClientRect().height);
        panel.style.setProperty("--side-thread-footer-height", `${height}px`);
        bodyScroll.scheduleScrollToBottom();
      };

      updateFooterHeight();
      if (typeof ResizeObserver === "undefined") {
        return undefined;
      }
      const observer = new ResizeObserver(updateFooterHeight);
      observer.observe(footer);
      return () => observer.disconnect();
    }, [bodyScroll]);

    const handleStreamFrame = useCallback(() => {
      bodyScroll.scheduleScrollToBottom();
    }, [bodyScroll]);

    return (
      <aside
        ref={panelRef}
        className="side-thread-panel"
        data-main-thread-id={mainThreadId}
        data-streaming={entry.streaming ? "true" : "false"}
        data-wuu-component="side-thread"
        aria-labelledby={titleID}
      >
        <button
          type="button"
          className="side-thread-panel__resizer"
          role="separator"
          aria-label={t("sideThread.resize")}
          aria-orientation="vertical"
          aria-valuemin={SIDE_THREAD_MIN_WIDTH}
          aria-valuemax={SIDE_THREAD_MAX_WIDTH}
          aria-valuenow={width}
          onPointerDown={onResizeStart}
        />
        <header className="side-thread-panel__header">
          <h2 id={titleID} className="side-thread-panel__title">{t("sideThread.title")}</h2>
          <Tooltip content={t("sideThread.collapse")}>
            <button
              type="button"
              className="icon-button side-thread-panel__close"
              onClick={onClose}
              aria-label={t("sideThread.collapse")}
            >
              <PanelRightClose />
            </button>
          </Tooltip>
        </header>

        <div
          ref={bodyScroll.scrollRef}
          className="side-thread-panel__body"
          role="log"
          aria-live="polite"
        >
          <div className="conversation-width session-flow side-thread-panel__conversation">
            <ConversationTurnList
              threadID={entry.summary?.side_thread_id ?? `side:${mainThreadId}`}
              turns={turns}
              renderTurn={(turn) => (
                <TurnView
                  turn={turn}
                  cwd={cwd}
                  onOpenFile={onOpenFile}
                  latestAgentMessageID={latestMessageID}
                  onStreamFrame={handleStreamFrame}
                  isLatestTurn={turn.id === latestTurn?.id}
                />
              )}
            />
          </div>
        </div>

        <div ref={footerRef} className="side-thread-panel__footer">
          <div ref={composerHostRef} className="side-thread-panel__composer-host">
            {composer}
          </div>
        </div>
      </aside>
    );
  },
);
