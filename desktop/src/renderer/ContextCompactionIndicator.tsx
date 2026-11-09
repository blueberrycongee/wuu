import type { ThreadItem } from "../shared/protocol";
import { ArrowRight } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import { ContextUsageRing } from "./ContextUsageRing";
import {
  contextCompactionDetail,
  contextCompactionProgressTitle,
  contextCompactionTitle,
  isFailedCompactNotice,
  normalizeContextCompactionText,
  parseContextCompactionNotice,
} from "./ContextCompaction";

export function ContextCompactionIndicator({ item, announce }: { item: ThreadItem; announce: boolean }): JSX.Element {
  const normalized = normalizeContextCompactionText(item.text);
  const failed = item.status === "failed" || isFailedCompactNotice(normalized);
  const running = item.status === "in_progress";
  const title = running
    ? contextCompactionProgressTitle(item.text, item.reason)
    : contextCompactionTitle(item.text, item.reason, item.status);
  const detail = running ? "" : contextCompactionDetail(item.text, item.status);
  const description = detail ? `${title} · ${detail}` : title;
  const range = !failed && !running ? parseContextCompactionNotice(normalized) : undefined;
  const retainedRatio = range?.before !== undefined && range.after !== undefined && range.before > 0
    ? range.after / range.before : undefined;
  return (
    <Tooltip content={description}>
      <span
        className={`context-compaction-indicator${failed ? " is-failed" : ""}`}
        tabIndex={0}
        role={announce && failed ? "alert" : announce && running ? "status" : "img"}
        aria-live={announce && running ? "polite" : undefined}
        aria-label={description}
      >
        {retainedRatio !== undefined ? (
          <>
            {/* Relative retained history, not occupancy of the model's window. */}
            <ContextUsageRing ratio={1} />
            <ArrowRight className="context-compaction-arrow icon-xs" aria-hidden />
            <ContextUsageRing ratio={retainedRatio} />
          </>
        ) : <span>{title}</span>}
      </span>
    </Tooltip>
  );
}
