import type { Turn } from "../shared/protocol";
import { useI18n } from "./i18n";

export function AutoModelStatus({ turn }: { turn: Turn }): JSX.Element | null {
  const { locale } = useI18n();
  const zh = locale === "zh-CN";
  if (turn.selecting_model && turn.status === "in_progress")
    return (
      <div className="auto-model-status" role="status">
        {zh ? "Auto · 正在选择模型" : "Auto · Selecting model"}
      </div>
    );
  const decision = turn.auto_model;
  if (!decision) return null;
  const tier = zh
    ? { simple: "简单", medium: "中等", complex: "复杂" }[decision.tier]
    : decision.tier;
  const reason = decision.reason
    ?.split(";")
    .map((reason) => {
      switch (reason) {
        case "classifier_failed":
          return zh
            ? "判断服务失败，已回落"
            : "Classifier failed; fallback selected";
        case "invalid_classifier_output":
          return zh
            ? "判断结果无效，已回落"
            : "Invalid classification; fallback selected";
        case "capability_fallback":
          return zh ? "按输入能力调整模型" : "Adjusted for input capabilities";
        default:
          return reason;
      }
    })
    .join(" · ");
  const usage = decision.usage;
  const classifierTokens = usage
    ? usage.InputTokens +
      usage.OutputTokens +
      (usage.CacheReadTokens ?? 0) +
      (usage.CacheCreationTokens ?? 0)
    : 0;
  const totalTokens =
    classifierTokens +
    (turn.input_tokens ?? 0) +
    (turn.output_tokens ?? 0) +
    (turn.cache_read_tokens ?? 0) +
    (turn.cache_creation_tokens ?? 0);
  return (
    <div className="auto-model-status">
      Auto · {tier} · {decision.selection.provider} / {decision.selection.model}
      {reason ? ` · ${reason}` : ""}
      {turn.status !== "in_progress" && totalTokens > 0 ? (
        <span
          title={
            zh
              ? "含判断与执行的本轮总用量"
              : "Turn usage including classification and execution"
          }
        >
          {" "}
          · {zh ? "总计" : "Total"} {totalTokens} tokens
        </span>
      ) : null}
      <span title={zh ? "判断调用耗时和用量" : "Classifier latency and usage"}>
        {" "}
        · {(decision.duration_ms / 1000).toFixed(1)}s
        {usage ? ` · ${classifierTokens} tokens` : ""}
      </span>
    </div>
  );
}
