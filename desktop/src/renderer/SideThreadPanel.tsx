import { ArtifactPreviewContext } from "./ArtifactPreviewContext";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";
import { useAutoFollowScrollContainer } from "./AutoFollowScroll";
import { ConversationTurnList } from "./ConversationTurnList";
import { sideThreadMessagesToTurns } from "./SideThreadTurns";
import type { SideThreadEntryState } from "./SideThreadState";
import { latestAgentMessageItemID, TurnView } from "./TurnView";

export type SideThreadPanelHandle = {
  focusComposer: () => void;
};

type SideThreadPanelProps = {
  entry: SideThreadEntryState;
  mainThreadId: string;
  active: boolean;
  title: string;
  composer: ReactNode;
  cwd?: string;
  onOpenFile?: (path: string) => void;
};

export const SideThreadPanel = forwardRef<SideThreadPanelHandle, SideThreadPanelProps>(
  function SideThreadPanel(
    {
      entry,
      mainThreadId,
      active,
      title,
      composer,
      cwd,
      onOpenFile,
    },
    ref,
  ) {
    const titleID = useId();
    const panelRef = useRef<HTMLElement | null>(null);
    const composerHostRef = useRef<HTMLDivElement | null>(null);
    const footerRef = useRef<HTMLDivElement | null>(null);
    const bodyScroll = useAutoFollowScrollContainer({ open: active });
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
      if (!active || !panel || !footer) {
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
    }, [active, bodyScroll]);

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
        <header className="side-thread-panel__header">
          <h2 id={titleID} className="side-thread-panel__title" title={title}>
            {title}
          </h2>
        </header>

        <div
          ref={bodyScroll.scrollRef}
          className="side-thread-panel__body"
          role="log"
          aria-live={active ? "polite" : "off"}
        >
          <div className="conversation-width session-flow side-thread-panel__conversation">
            <ConversationTurnList
              threadID={entry.summary?.side_thread_id ?? `side:${mainThreadId}`}
              turns={turns}
              renderTurn={(turn) => (
                <ArtifactPreviewContext.Provider value={undefined}>
                <TurnView
                  threadID={entry.summary?.side_thread_id ?? `side:${mainThreadId}`}
                  turn={turn}
                  cwd={cwd}
                  onOpenFile={onOpenFile}
                  latestAgentMessageID={latestMessageID}
                  onStreamFrame={handleStreamFrame}
                  isLatestTurn={turn.id === latestTurn?.id}
                />
                </ArtifactPreviewContext.Provider>
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
