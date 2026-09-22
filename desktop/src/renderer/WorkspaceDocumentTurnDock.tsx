import { ChevronDown, ChevronUp } from "./WuuIcons";
import { createContext, type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import type { ThreadItem, Turn } from "../shared/protocol";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { streamTextKey, streamTextStore } from "./StreamText";
import { useI18n } from "./i18n";

interface WorkspaceDocumentTurnDockProps {
  children: ReactNode;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  topAccessory?: ReactNode;
  turns: Turn[];
  waitingQuery?: string;
}

export const WorkspaceDocumentDrawerContext = createContext<{
  documentResultExpanded: boolean;
  collapseDocumentResult: () => void;
} | null>(null);

function latestUserItem(turn: Turn): ThreadItem | undefined {
  for (let index = turn.items.length - 1; index >= 0; index -= 1) {
    const item = turn.items[index];
    if (item.type === "user_message") {
      return item;
    }
  }
  return undefined;
}

function finalAnswerItems(turn: Turn): ThreadItem[] {
  return turn.items.filter(
    (item) => item.type === "agent_message" && item.terminal,
  );
}

function latestResultTurn(turns: Turn[]): Turn | undefined {
  for (let index = turns.length - 1; index >= 0; index -= 1) {
    const turn = turns[index];
    if (latestUserItem(turn) && finalAnswerItems(turn).length > 0) {
      return turn;
    }
  }
  return undefined;
}

export function WorkspaceDocumentTurnDock({
  children,
  cwd,
  onOpenFile,
  topAccessory,
  turns,
  waitingQuery,
}: WorkspaceDocumentTurnDockProps): JSX.Element {
  const { t } = useI18n();
  const turn = useMemo(() => latestResultTurn(turns), [turns]);
  const [expanded, setExpanded] = useState(false);
  const finalAnswers = turn ? finalAnswerItems(turn) : [];
  const previousTurnIDRef = useRef(turn?.id);
  const previousFinalAnswerCountRef = useRef(finalAnswers.length);
  const drawerContext = useMemo(
    () => ({
      documentResultExpanded: expanded,
      collapseDocumentResult: () => setExpanded(false),
    }),
    [expanded],
  );

  useEffect(() => {
    if (previousTurnIDRef.current !== turn?.id) {
      previousTurnIDRef.current = turn?.id;
      previousFinalAnswerCountRef.current = finalAnswers.length;
      setExpanded(Boolean(turn));
      return;
    }
    if (
      previousFinalAnswerCountRef.current === 0 &&
      finalAnswers.length > 0
    ) {
      setExpanded(true);
    }
    previousFinalAnswerCountRef.current = finalAnswers.length;
  }, [finalAnswers.length, turn?.id]);

  if (!turn || finalAnswers.length === 0) {
    return (
      <WorkspaceDocumentDrawerContext.Provider value={drawerContext}>
        <div className="workspace-document-turn-dock" data-wuu-component="workspace-document-turn">
          {topAccessory ? <div className="composer-top-accessory">{topAccessory}</div> : null}
          {children}
        </div>
      </WorkspaceDocumentDrawerContext.Provider>
    );
  }

  const toggleLabel = expanded
    ? t("workspace.documentTurn.collapse")
    : t("workspace.documentTurn.expand");
  const detailsID = `workspace-document-turn-${turn.id}`;

  return (
    <WorkspaceDocumentDrawerContext.Provider value={drawerContext}>
      <div className="workspace-document-turn-dock" data-wuu-component="workspace-document-turn">
        {topAccessory ? <div className="composer-top-accessory">{topAccessory}</div> : null}
        <section
          className={`workspace-document-turn-drawer${expanded ? " expanded" : ""}`}
          data-testid="workspace-document-turn-drawer"
          data-wuu-state={expanded ? "expanded" : "collapsed"}
        >
          <button
            type="button"
            className="workspace-document-turn-summary"
            aria-controls={detailsID}
            aria-expanded={expanded}
            aria-label={toggleLabel}
            onClick={() => setExpanded((current) => !current)}
          >
            {waitingQuery && !expanded ? (
              <span
                className="workspace-document-turn-waiting-query"
                role="status"
                aria-live="polite"
              >
                {waitingQuery}
              </span>
            ) : null}
            {expanded ? <ChevronDown size={15} /> : <ChevronUp size={15} />}
          </button>
          {expanded ? (
            <div className="workspace-document-turn-details" id={detailsID}>
              <div className="workspace-document-turn-result">
                {finalAnswers.map((item) => {
                  const streamKey = streamTextKey(turn.id, item.id, "text");
                  const isLive = item.status === "in_progress";
                  return (
                    <StreamingMarkdown
                      key={item.id}
                      streamKey={streamKey}
                      initialText={
                        isLive && streamTextStore.has(streamKey)
                          ? streamTextStore.seedValue(streamKey)
                          : item.text
                      }
                      cwd={cwd}
                      onOpenFile={onOpenFile}
                      isLive={isLive}
                      phase="final_answer"
                    />
                  );
                })}
              </div>
            </div>
          ) : null}
        </section>
        <div className="workspace-document-turn-composer">{children}</div>
      </div>
    </WorkspaceDocumentDrawerContext.Provider>
  );
}
