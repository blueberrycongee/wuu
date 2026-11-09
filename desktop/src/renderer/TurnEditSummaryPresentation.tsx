import type { Turn } from "../shared/protocol";
import { TurnEditSummaryCard, turnHasFileEdits } from "./TurnEditSummaryCard";
import {
  TurnOutputSummaryPresentation,
  turnOutputSummaryVisible,
} from "./TurnOutputSummaryCard";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";

/** The timeline card is a temporary review surface, not the edit record. */
export function TurnEditSummaryPresentation({
  turn,
  isLatestTurn,
  cwd,
  onOpenFile,
  onOpenFileDiff,
  onCollapseComplete,
}: {
  turn: Turn;
  isLatestTurn: boolean;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenFileDiff?: (selection: TurnFileDiffSelection) => void;
  onCollapseComplete?: () => void;
}): JSX.Element | null {
  return (
    <TurnOutputSummaryPresentation
      visible={turnOutputSummaryVisible(turn, isLatestTurn) && turnHasFileEdits(turn)}
      onCollapseComplete={onCollapseComplete}
    >
      <TurnEditSummaryCard
        turn={turn}
        cwd={cwd}
        onOpenFile={onOpenFile}
        onOpenFileDiff={onOpenFileDiff}
        compact={turn.status === "failed" || turn.status === "interrupted"}
      />
    </TurnOutputSummaryPresentation>
  );
}
