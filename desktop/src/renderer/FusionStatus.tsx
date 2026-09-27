import type { Turn } from "../shared/protocol";

export type FusionTurnStatus = { summary: string; tokens: number };

export function fusionTurnStatus(turn: Turn, zh: boolean, answerReady: boolean): FusionTurnStatus | null {
  if (!turn.fusion) return null;
  const delegations = turn.items.filter((item) => item.name === "fusion_delegate" && item.type === "tool_call");
  const last = delegations.at(-1);
  let tokens = 0;
  let lastOutcome = "";
  let completedCount = 0;
  for (const item of delegations) {
    if (!item.result) continue;
    try {
      const report = JSON.parse(item.result);
      for (const key of ["input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens"]) {
        if (typeof report[key] === "number" && Number.isFinite(report[key]) && report[key] > 0) tokens += report[key];
      }
      if (report.outcome === "completed") completedCount++;
      if (item === last && typeof report.outcome === "string") lastOutcome = report.outcome;
    } catch {
      // Interrupted tool results may not contain a complete report.
    }
  }

  const count = delegations.length;
  const finished = turn.status === "completed" || answerReady;
  let summary: string;
  if (count === 0) {
    summary = turn.status === "failed"
      ? zh ? "Fusion · Lead 执行失败" : "Fusion · Lead failed"
      : turn.status === "interrupted"
        ? zh ? "Fusion · Lead 已中断" : "Fusion · Lead interrupted"
        : finished
          ? zh ? "Fusion · Lead 独立完成" : "Fusion · Lead completed solo"
          : zh ? "Fusion · Lead 处理中" : "Fusion · Lead working";
  } else {
    summary = last?.status === "in_progress"
      ? zh ? "Fusion · Sidekick 执行中" : "Fusion · Sidekick working"
      : last?.status === "failed" || lastOutcome === "failed"
        ? zh ? "Fusion · Sidekick 执行失败" : "Fusion · Sidekick failed"
        : lastOutcome === "blocked" || lastOutcome === "needs_decision"
          ? zh ? "Fusion · Sidekick 已交回 Lead" : "Fusion · Sidekick returned to Lead"
          : finished && completedCount === count
            ? zh ? `Fusion · Sidekick 完成 ${count} 次委派` : `Fusion · Sidekick completed ${count} delegation${count === 1 ? "" : "s"}`
            : zh ? `Fusion · Sidekick 参与 ${count} 次委派` : `Fusion · Sidekick joined ${count} delegation${count === 1 ? "" : "s"}`;
  }
  return { summary, tokens };
}
