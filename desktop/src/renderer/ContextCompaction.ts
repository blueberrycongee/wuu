import type { ThreadItemStatus } from "../shared/protocol";
import { isUnchangedContextCompaction } from "./TurnEvents";
import { translateCurrent as t } from "./i18n";

export function contextCompactionProgressTitle(text?: string, reason?: string): string {
  if (isManualCompact(reason, normalizeContextCompactionText(text))) {
    return t("compaction.compacting");
  }
  return t("compaction.autoCompacting");
}

export function contextCompactionTitle(
  text?: string,
  reason?: string,
  status?: ThreadItemStatus,
): string {
  const normalized = normalizeContextCompactionText(text);
  if (status === "failed") {
    return t("compaction.failed");
  }
  if (isFailedCompactNotice(normalized)) {
    return t("compaction.failed");
  }
  if (isUnchangedContextCompaction(normalized)) {
    return t("compaction.notNeeded");
  }
  if (isManualCompact(reason, normalized)) {
    return t("compaction.manualComplete");
  }
  return t("compaction.complete");
}

export function contextCompactionDetail(
  text?: string,
  status?: ThreadItemStatus,
): string {
  const normalized = normalizeContextCompactionText(text);
  if (status === "failed") {
    return t("compaction.failedDetail");
  }
  if (!normalized) {
    return t("compaction.completeDetail");
  }
  if (isFailedCompactNotice(normalized)) {
    return t("compaction.failedDetail");
  }
  if (isUnchangedContextCompaction(normalized)) {
    return t("compaction.notNeededDetail");
  }
  if (/^Compacted history$/i.test(normalized)) {
    return t("compaction.completeDetail");
  }
  const compactNotice = parseContextCompactionNotice(normalized);
  if (compactNotice) {
    return compactNotice.detail;
  }
  // Unknown server diagnostics belong in the fold, not the status line.
  return "";
}

export function normalizeContextCompactionText(text?: string): string {
  return (text ?? "").trim().replace(/^[✦*•]\s*/, "");
}

export function isFailedCompactNotice(text: string): boolean {
  return /^(?:(?:Manual context compaction|Context compaction|Proactive compact|Context-overflow compact|Compact) failed\b|Fresh context could not be installed\b)/i.test(
    text,
  );
}

function isManualCompact(reason: string | undefined, text: string): boolean {
  return (
    reason === "manual" ||
    /^Manual(?:ly)?\s+(?:context\s+)?compact/i.test(text)
  );
}

export function parseContextCompactionNotice(text: string): { detail: string; before?: number; after?: number } | undefined {
  const match = text.match(
    /^(?:Recovered from context overflow\s+[—-]\s+compacted|Manually compacted|Compacted|Started a fresh context window with)\s+history:\s*\d+\s*(?:→|->)\s*\d+\s+messages(?:\s+\(([^)]+)\))?$/i,
  );
  if (!match) {
    return undefined;
  }
  const [, tokenDetail] = match;
  const tokenRange = tokenDetail?.match(
    /^~?(\d+(?:\.\d+)?[kM]?)\s*(?:→|->)\s*~?(\d+(?:\.\d+)?[kM]?)\s+tokens$/i,
  );
  if (tokenRange) {
    return {
      detail: `${tokenRange[1]} → ${tokenRange[2]}`,
      before: tokenCount(tokenRange[1]),
      after: tokenCount(tokenRange[2]),
    };
  }
  return { detail: t("compaction.completeDetail") };
}

function tokenCount(value: string): number {
  const suffix = value.at(-1)?.toLowerCase();
  return parseFloat(value) * (suffix === "m" ? 1_000_000 : suffix === "k" ? 1_000 : 1);
}
