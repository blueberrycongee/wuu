import type { Turn } from "../shared/protocol";
import { useI18n } from "./i18n";

export function FusionStatus({ turn }: { turn: Turn }): JSX.Element | null {
  const { locale } = useI18n();
  const zh = locale === "zh-CN";
  const pair = turn.fusion;
  if (!pair) return null;
  const delegations = turn.items.filter((item) => item.name === "fusion_delegate" && item.type === "tool_call");
  const last = delegations.at(-1);
  let sidekickTokens = 0;
  let outcome = "";
  for (const item of delegations) {
    if (!item.result) continue;
    try {
      const report = JSON.parse(item.result);
      for (const key of ["input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens"]) {
        if (typeof report[key] === "number" && Number.isFinite(report[key]) && report[key] > 0) sidekickTokens += report[key];
      }
      if (item === last && typeof report.outcome === "string") outcome = report.outcome;
    } catch {
      // Interrupted or paged tool results may not contain a complete report.
    }
  }
  const status = last?.status === "in_progress"
    ? zh ? "Sidekick 执行中" : "Sidekick working"
    : outcome === "completed"
      ? zh ? "Sidekick 已返回完成报告" : "Sidekick reported completion"
      : outcome === "blocked" || outcome === "needs_decision"
        ? zh ? "Sidekick 已交回 Lead 处理" : "Sidekick returned control to Lead"
        : last?.status === "failed" || outcome === "failed"
          ? zh ? "Sidekick 执行失败" : "Sidekick failed"
          : "";
  return (
    <div className="fusion-status">
      <span>Fusion · Lead: {pair.lead.model} · {pair.lead.provider}</span>
      <span>Sidekick: {pair.sidekick.model} · {pair.sidekick.provider}</span>
      {status ? <span role="status">{status}</span> : null}
      {sidekickTokens > 0 ? <span>Sidekick · {sidekickTokens.toLocaleString()} tokens</span> : null}
    </div>
  );
}
