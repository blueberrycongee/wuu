import { memo, useEffect, useState } from "react";
import type { ThreadItem } from "../shared/protocol";
import { LightweightStreamingText } from "./LightweightStreamingText";
import {
  buildToolActivitySections,
  summarizeToolActivity,
  parseJSONRecord,
  stringValue,
  readableToolActivityName,
} from "./ToolActivityHelpers";
import { ChevronDown } from "./WuuIcons";
import { ToolActivityPresenter } from "./plugins/ToolActivityPresenter";
import { ToolActivityMarker } from "./ToolActivityMarker";
import { useI18n } from "./i18n";
import { RemoteItemContent } from "./RemoteItemContent";
import { RichCodeBlock } from "./RichContent";
import { collectTurnArtifacts, TurnInlineArtifactOutputs, TurnEndArtifactOutputs } from "./ArtifactOutputs";
import { ENABLE_TURN_ARTIFACT_SUMMARY } from "./FeatureFlags";
export type { JsonRecord } from "./ToolActivityHelpers";
export {
  isRecord,
  numberValue,
  readableToolActivityCommand,
  readableToolName,
  recordValue,
  stringValue,
} from "./ToolActivityHelpers";

const TOOL_ACTIVITY_REVEAL_INTERVAL_MS = 85;

export function ToolActivityTimeline({
  items,
  cwd,
  revealItems = false,
  streaming = false,
  showInspectionPreviews = false,
}: {
  items: ThreadItem[];
  cwd?: string;
  revealItems?: boolean;
  showInspectionPreviews?: boolean;
  /**
   * When true, in-progress tool rows fake-stream their summary line at
   * a deliberate cadence. Flips to false the moment an agent_message in
   * the same turn starts streaming so the user's eye is not held on a
   * still-filling title while the body text rushes past underneath.
   */
  streaming?: boolean;
}): JSX.Element {
  const [visibleCount, setVisibleCount] = useState(() =>
    revealItems ? Math.min(1, items.length) : items.length,
  );
  const itemSignature = items.map((item) => item.id).join("\u0000");

  useEffect(() => {
    setVisibleCount((current) => {
      if (!revealItems) {
        return items.length;
      }
      if (items.length === 0) {
        return 0;
      }
      return Math.min(Math.max(current, 1), items.length);
    });
  }, [itemSignature, items.length, revealItems]);

  useEffect(() => {
    if (!revealItems || visibleCount >= items.length) {
      return undefined;
    }
    const timer = window.setTimeout(() => {
      setVisibleCount((current) => Math.min(current + 1, items.length));
    }, TOOL_ACTIVITY_REVEAL_INTERVAL_MS);
    return () => window.clearTimeout(timer);
  }, [itemSignature, items.length, revealItems, visibleCount]);

  return (
    <div
      className="activity-timeline"
      data-pending-count={Math.max(0, items.length - visibleCount)}
    >
      {items.slice(0, visibleCount).map((item) => (
        <ToolActivityTimelineItem
          item={item}
          cwd={cwd}
          key={item.id}
          streaming={streaming && item.status === "in_progress"}
          showInspectionPreviews={showInspectionPreviews}
        />
      ))}
    </div>
  );
}

const ToolActivityTimelineItem = memo(function ToolActivityTimelineItem({
  item,
  cwd,
  streaming,
  showInspectionPreviews,
}: {
  item: ThreadItem;
  cwd?: string;
  streaming: boolean;
  showInspectionPreviews: boolean;
}): JSX.Element {
  const inspections = showInspectionPreviews
    ? collectTurnArtifacts({ items: [item] }).filter(artifact => artifact.foldPreview) : [];
  const fallback = (
    <div className="activity-timeline-item">
      <ToolActivityRow items={[item]} streaming={streaming} />
      {inspections.length > 0 ? <TurnInlineArtifactOutputs artifacts={inspections} cwd={cwd} inspectionExpanded /> : null}
    </div>
  );
  return (
    <ToolActivityPresenter item={item} fallback={fallback} />
  );
});

function ProgramToolRecord({ item, code }: { item: ThreadItem; code: string }): JSX.Element {
  const [open, setOpen] = useState(false);
  const description = stringValue(parseJSONRecord(item.arguments), "description");
  return (
    <details
      className="program-tool-record activity-group"
      data-status={item.status}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="activity-row activity-summary">
        <ToolActivityMarker kind="command" running={item.status === "in_progress"} />
        <span className="activity-copy program-tool-label">
          <span className="activity-summary-text">{description || readableToolActivityName(item)}</span>
        </span>
        <ChevronDown className="program-tool-chevron icon-sm" aria-hidden="true" />
      </summary>
      {open ? (
        <div className="program-tool-details">
          <RichCodeBlock code={code} displayedCode={code} language="typescript" />
          {item.error ? <p>{item.error}</p> : null}
        </div>
      ) : null}
    </details>
  );
}

// Ordinary tool activity rows are one line of plain prose. We no longer render
// a separate collapsible "details" block: in nearly every case
// (list_files, read_file, grep, run_shell with a readable label) the
// toggle summary and the detail command text were the same string, so
// the previous toggle+details pair read as the same tool call shown
// twice. Tool failures remain available in debug data, but are not promoted
// into the conversation: users care about the agent's eventual outcome, not
// whether every intermediate attempt completed. Workspace switches expose their
// destination and failure inside a disclosure because they change later tool roots.
// PTC programs show their supplied description and disclose source on demand.
export function ToolActivityRow({
  items,
  streaming = false,
}: {
  items: ThreadItem[];
  /**
   * Drives the fake-stream on the summary text. When true, the summary
   * reveals progressively at a deliberate cadence; when false it snaps
   * to the full text. The catch-up signal (turn has an in-progress
   * agent_message) flips this off so the user's eye follows the body
   * text rather than a still-filling title above it.
   */
  streaming?: boolean;
}): JSX.Element {
  // Locale changes must reach rows inside the memoized activity timeline.
  const { t } = useI18n();
  const summary = summarizeToolActivity(items);
  const sections = buildToolActivitySections(items);

  // Workspace changes retain the destination of this invocation. Reading the
  // thread's current worktree here would rewrite history after the next switch.
  if (items.length === 1 && items[0].name === "set_session_workspace") {
    const item = items[0];
    const destination = stringValue(parseJSONRecord(item.result), "root")
      ?? stringValue(parseJSONRecord(item.arguments), "root");
    return (
      <details className="workspace-tool-record activity-group" data-status={item.status}>
        <summary className="activity-row activity-summary">
          <ToolActivityMarker kind="command" running={summary.running} />
          <span className="activity-copy">{t(summary.failed
            ? "toolActivity.workspaceFailed"
            : summary.running ? "toolActivity.workspaceSwitching" : "toolActivity.workspaceSwitched")}</span>
          <ChevronDown className="workspace-tool-chevron icon-sm" aria-hidden="true" />
        </summary>
        <div className="workspace-tool-details">
          {destination ? <code>{destination}</code> : null}
          {item.error ? <p>{item.error}</p> : null}
        </div>
      </details>
    );
  }

  if (items.length === 1 && items[0].name === "run_code") {
    const code = parseJSONRecord(items[0].arguments)?.code;
    // Keep source verbatim; stringValue trims meaningful indentation and newlines.
    if (typeof code === "string" && code.trim()) {
      return <ProgramToolRecord key={items[0].id} item={items[0]} code={code} />;
    }
  }

  // Each section carries both an action verb (title) and a target (detail).
  // Concatenate them so the rendered row reads as "动词 目标" — without
  // this, taking detail alone would drop the verb and surface a bare file
  // name with no hint of what was done to it. Sections without a detail
  // (e.g. "计划") fall back to the title alone. Multiple sections in the
  // same row join with "，".
  const summaryText = sections
    .map((s) => {
      // Command details are already complete action phrases such as
      // "搜索软件包" or "运行测试". Prefixing them with the generic section
      // title produced awkward rows like "检查 运行命令".
      if (s.kind === "command" && s.detail) return s.detail;
      // A plugin label is already a complete user-facing name. Prefixing it
      // with the generic "使用工具" title only exposes the implementation
      // category and makes the aggregate row feel repetitive.
      if (s.kind === "unknown" && s.detail) return s.detail;
      if (s.detail && s.title) return `${s.title} ${s.detail}`;
      return s.detail || s.title;
    })
    .filter(Boolean)
    .join("，");

  const className = `activity-group${summary.running ? " running" : ""}`;

  return (
    <article className={className}>
      <span className="activity-row activity-summary">
        <ToolActivityMarker
          kind={summary.kind === "list" || summary.kind === "create" ? summary.kind : sections[0]?.kind}
          running={summary.running}
        />
        <span className="activity-copy">
          <LightweightStreamingText
            className="activity-summary-text"
            text={summaryText}
            live={streaming ?? false}
          />
          {summary.additions > 0 ? (
            <span className="activity-add">+{summary.additions}</span>
          ) : null}
          {summary.deletions > 0 ? (
            <span className="activity-delete">-{summary.deletions}</span>
          ) : null}
        </span>
      </span>
      {items.filter(item => item.remote_content_ref).map(item => <RemoteItemContent key={item.remote_content_ref} item={item}
        render={(content, complete) => <RemoteToolResult item={content} complete={complete} />} />)}
    </article>
  );
}

function RemoteToolResult({ item, complete }: { item: ThreadItem; complete: boolean }): JSX.Element {
  const artifacts = complete ? collectTurnArtifacts({ items: [item] }) : [];
  const text = item.result || item.text || item.result_detail?.content?.filter(part => part.type === "text").map(part => part.text ?? "").join("\n") || item.arguments || "";
  return <>
    <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", maxHeight: 480, overflowY: "auto" }}>{text}</pre>
    <TurnInlineArtifactOutputs artifacts={artifacts} />
    {ENABLE_TURN_ARTIFACT_SUMMARY ? <TurnEndArtifactOutputs artifacts={artifacts} /> : null}
  </>;
}
