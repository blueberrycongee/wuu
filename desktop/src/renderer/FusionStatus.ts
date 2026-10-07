import type { FusionDelegation, Thread, Turn } from "../shared/protocol";

export function fusionSideRunning(thread: Thread | undefined): boolean {
  return Boolean(thread?.fusion && thread.turns.some(turn => turn.fusion?.tasks?.length
    ? turn.fusion.tasks.some(task => task.state === "queued" || task.state === "running" || task.execution_state === "running")
    : turn.fusion?.delegations?.some(dispatch => dispatch.state === "queued" || dispatch.state === "in_progress")));
}

export function fusionTurnStatus(turn: Turn, zh: boolean): { summary: string; tokens: number; failure?: FusionDelegation } | undefined {
  if (!turn.fusion) return undefined;
  const tokens = (turn.fusion.delegations ?? []).reduce((sum, dispatch) => sum + (dispatch.input_tokens ?? 0) + (dispatch.output_tokens ?? 0), 0);
  const active = turn.fusion.state === "sidekick";
  const latest = turn.fusion.delegations?.at(-1);
  const task = turn.fusion.tasks?.at(-1);
  const failure = latest?.state === "failed" ? latest : undefined;
  if (task) {
    let summary: string;
    switch (task.state) {
      case "queued": summary = zh ? "Sidekick · 等待执行" : "Sidekick · Queued"; break;
      case "running": summary = zh ? "Sidekick · 执行中" : "Sidekick · Working"; break;
      case "awaiting_review": summary = zh ? "Lead · 待审查" : "Lead · Review pending"; break;
      case "completed": summary = zh ? "Fusion · 已完成" : "Fusion · Complete"; break;
      case "failed": summary = zh ? "Sidekick · 执行失败" : "Sidekick · Failed"; break;
      case "cancelled": summary = zh ? "Fusion · 已取消" : "Fusion · Cancelled"; break;
      default: { const exhaustive: never = task; return exhaustive; }
    }
    return { summary, tokens, failure: task.state === "failed" ? failure : undefined };
  }
  const stopped = turn.status === "interrupted" || (turn.status !== "in_progress" && turn.fusion.delegations?.at(-1)?.state === "interrupted");
  const summary = stopped
    ? (zh ? "Fusion · 已停止" : "Fusion · Stopped")
    : failure
      ? (zh ? "Fusion · Sidekick 执行失败" : "Fusion · Sidekick failed")
      : turn.status === "failed"
        ? (zh ? "Fusion · Lead 执行失败" : "Fusion · Lead failed")
      : active
        ? (zh ? "Sidekick · 执行中" : "Sidekick · Working")
        : turn.status === "in_progress"
          ? (zh ? "Lead · 规划与复查" : "Lead · Planning and reviewing")
          : (zh ? "Fusion · 本轮结束" : "Fusion · Turn complete");
  return { summary, tokens, failure };
}
