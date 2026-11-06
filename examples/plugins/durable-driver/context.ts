import {
  DriverOperationError,
  type DriverMessage,
  type DriverTurnInput,
  type DriverTurnSettings,
  type ModelToolCallV1,
  type RuntimeHost,
  driverTurn,
} from "@wuu/plugin-sdk";
import { codeOf, digest, fault, mutate, newTask, readSession, upsert, type Phase, type State } from "./state.js";

export type ContextTrigger = "manual" | "threshold" | "overflow" | "reset" | "handoff";
export type ContextDisposition = "applied" | "unchanged" | "stale";
export interface ContextOperation {
  id: string;
  trigger: ContextTrigger;
  source_head: number;
  source_digest: string;
  attempt: number;
  status?: ContextDisposition;
}
type Turn = ReturnType<typeof driverTurn>;
type Save = (phase: Phase, patch?: Partial<State>) => Promise<void>;
export interface ContextControl {
  action: "compact" | "reset" | "handoff";
  call_id: string;
  note?: string;
}

/** Resolve the text from the exact archived declaration, not a copied checkpoint
 * payload or whichever user message happens to be last after input admission. */
export async function readContextControl(host: RuntimeHost, state: State): Promise<ContextControl> {
  if (!state.model_seq || !state.call_digest || !state.context_call_id)
    throw fault("context_intent_missing");
  const history = await host.call("host.session.history.read", {
    session_id: state.session_id,
    start_seq: state.model_seq,
    end_seq: state.model_seq,
    limit: 1,
  });
  const record = history.records?.find((item) => item.seq === state.model_seq);
  const calls = JSON.parse(record?.tool_calls ?? "[]") as ModelToolCallV1[];
  if (digest(calls) !== state.call_digest) throw fault("tool_intent_digest_mismatch");
  const declared = calls.find((item) => item.id === state.context_call_id);
  if (!declared) throw fault("context_intent_missing");
  const { doc } = await readSession(host, state.session_id);
  const controls = calls.flatMap((call): ContextControl[] => {
    if (call.name !== declared.name || !doc.tasks.some((task) =>
      task.id === `${state.batch_id}:${call.id}` && task.status === "completed")) return [];
    const args = JSON.parse(call.arguments ?? "{}") as { action: ContextControl["action"]; note?: string };
    return [{ action: args.action, call_id: call.id!, note: args.note }];
  });
  // Tool completion order is not declaration order. A reset ends the run;
  // otherwise the last successful context control in model call order wins.
  const selected = controls.findLast((control) => control.action === "reset") ?? controls.at(-1);
  if (!selected) throw fault("context_intent_missing");
  return selected;
}

/** Estimates are used only to schedule compaction, never as inference usage. */
export function estimateRequestTokens(request: DriverTurnInput): number {
  let tokens = 0;
  for (const message of request.messages) {
    // JSON includes text, structured results, tool arguments and image metadata.
    // Non-ASCII text is counted more conservatively than ordinary English.
    const text = JSON.stringify(message);
    let ascii = 0, other = 0;
    for (const char of text) char.codePointAt(0)! < 128 ? ascii++ : other++;
    tokens += 4 + Math.ceil(ascii / 4 + other / 2);
  }
  return tokens + Math.ceil(JSON.stringify(request.tools ?? []).length / 4);
}

function compactThreshold(settings: DriverTurnSettings): number {
  if (settings.compact_threshold_tokens > 0) return settings.compact_threshold_tokens;
  const limits = [settings.context_tokens, settings.input_tokens].filter((value) => value > 0);
  if (!limits.length) return 0;
  return Math.max(1, Math.floor((Math.min(...limits) - settings.output_reserve_tokens) *
    (settings.compact_threshold_percent > 0 ? settings.compact_threshold_percent : 1)));
}

export function shouldCompact(settings: DriverTurnSettings, request: DriverTurnInput): boolean {
  if (!settings.can_compact || settings.disable_auto_compact) return false;
  if (settings.force_initial_compact || settings.compact_only) return true;
  const threshold = compactThreshold(settings);
  return threshold > 0 && estimateRequestTokens(request) > threshold &&
    request.messages.filter((message) => message.role !== "system").length > 1;
}

/** The public host owns summarization, inference receipts and archive commits.
 * This task stores only addresses/hashes and applies its result at a boundary.
 * An interrupted or late proposal never cuts a newer active context backwards. */
export async function changeContext(
  host: RuntimeHost,
  turn: Turn,
  state: State,
  settings: DriverTurnSettings,
  trigger: ContextTrigger,
  save: Save,
  control?: ContextControl,
  background = false,
  wait = true,
): Promise<{ status: ContextDisposition | "running"; revision: number }> {
  await turn.archiveContext();
  const source = await turn.context();
  if (!source.can_replace) throw fault("context_replacement_unavailable");
  const hash = digest(source.messages);
  const field = background ? "background_compaction" : "context_operation";
  let operation = state[field];
  const existing = Boolean(operation);
  if (operation && operation.trigger !== trigger) throw fault("context_operation_conflict");
  if (!operation) {
    operation = {
      id: `${state.run_id}:context:${state.round}:${background ? "background" : trigger}`,
      trigger,
      source_head: source.archive_head,
      source_digest: hash,
      attempt: 1,
    };
    await save(state.phase, { [field]: operation });
  }
  const task = newTask(state, "compaction", operation.id);
  task.attempt = operation.attempt;
  task.compaction = { trigger, status: "running", source_head: operation.source_head };
  // The task projection can be absent after a crash immediately after the
  // checkpoint commit. Reconstruct it without discarding an existing outcome.
  await mutate(host, state.session_id, (doc) => {
    if (!doc.tasks.some((item) => item.id === task.id)) upsert(doc, task);
  });
  const settle = async (status: ContextDisposition, revision: number) => {
    await mutate(host, state.session_id, (doc) => {
      const current = doc.tasks.find((item) => item.id === task.id)!;
      current.status = "completed";
      current.phase = "complete";
      current.compaction = { trigger, status, source_head: operation!.source_head };
      current.updated_at = new Date().toISOString();
      current.duration_ms = Date.parse(current.updated_at) - Date.parse(current.created_at);
      // Summarization usage is journaled by the selected host provider. Do not
      // invent task-level amounts when the public reply does not include them.
      if (!["reset", "handoff"].includes(trigger)) current.usage_status = "unavailable";
    });
    await save(state.phase, {
      [field]: background ? undefined : { ...operation!, status },
      context_revision: revision,
    });
    return { status, revision };
  };
  if (operation.status) return settle(operation.status, source.revision);
  let resetTail: readonly DriverMessage[] = [];
  if ((trigger === "reset" || trigger === "handoff") && operation.source_digest !== hash) {
    // A new physical admission may append input while the old reset is being
    // recovered. Only an exact original prefix permits the old replacement;
    // retain every newer durable message rather than resetting the new request.
    const prefix = source.messages.filter(message => !message.seq || message.seq <= operation.source_head);
    const tail = source.messages.filter(message => message.seq && message.seq > operation.source_head);
    if (trigger === "reset" && tail.length && digest(prefix) === operation.source_digest)
      resetTail = tail;
    else return settle("stale", source.revision);
  }
  try {
    let messages: readonly DriverMessage[];
    if (trigger === "reset" || trigger === "handoff") {
      if (!control || control.action !== trigger) throw fault("context_intent_missing");
      // A committed reset/handoff no longer contains its declaring model round.
      // Recovery after a lost context reply must not reset a second time.
      if (!source.messages.some((message) => message.seq === state.model_seq && message.role === "assistant"))
        return settle("stale", source.revision);
      messages = [...(control.note ? [{
        role: "user", content: control.note, origin: "plugin",
        client_id: `${operation.id}:${control.call_id}`,
      } as DriverMessage] : []), ...resetTail];
    } else {
      const tokens = estimateRequestTokens({ ...(await turn.read()), messages: source.messages });
      if (!existing) {
        await turn.observe({ kind: "before_compact", compact: { reason: trigger } });
        await turn.observe({ kind: "compact_start", compact: { reason: trigger, tokens_before: tokens } });
      }
      const start = () => turn.startCompaction(operation!.id, source.revision, {
            reason: trigger,
            instructions: control?.note,
            last_successful_tokens: 0,
            failed_request_tokens: trigger === "overflow" ? tokens : 0,
            target_total_tokens: compactThreshold(settings),
          });
      let proposal = existing ? await turn.resumeCompaction(operation.id) : await start();
      if (proposal.status === "missing") {
        // The checkpoint may have committed before the host accepted intent.
        if (operation.source_digest !== hash) return settle("stale", source.revision);
        proposal = await start();
      }
      if (proposal.status === "running" && wait)
        proposal = await turn.readCompaction(operation.id, true);
      if (proposal.status === "running")
        return { status: "running", revision: source.revision };
      if (proposal.status === "failed") {
        if (proposal.failure) throw new DriverOperationError(proposal.failure);
        throw fault("compaction_failed");
      }
      await turn.observe({ kind: "after_compact", compact: { reason: trigger } });
      if (proposal.status === "completed") proposal = await turn.applyCompaction(operation.id);
      if (proposal.status !== "applied" && proposal.status !== "unchanged" && proposal.status !== "stale")
        throw fault("invalid_compaction_result");
      await turn.observe({ kind: "compact_result", compact: { reason: trigger, status: proposal.status } });
      return settle(proposal.status, proposal.context_revision ?? (await turn.context()).revision);
    }
    const latest = await turn.context();
    if (digest(latest.messages) !== hash) return settle("stale", latest.revision);
    const replaced = await turn.replaceContext(latest.revision, messages);
    if (trigger !== "reset" && trigger !== "handoff")
      await turn.observe({ kind: "compact_result", compact: {
        reason: trigger, messages_before: source.messages.length, messages_after: replaced.messages.length,
      } });
    return settle("applied", replaced.revision);
  } catch (error) {
    await mutate(host, state.session_id, (doc) => {
      const current = doc.tasks.find((item) => item.id === task.id)!;
      current.status = "failed";
      current.phase = "failed";
      current.error_code = codeOf(error);
      current.compaction = { trigger, status: "failed", source_head: operation!.source_head };
      current.usage_status = "unavailable";
      current.updated_at = new Date().toISOString();
      current.duration_ms = Date.parse(current.updated_at) - Date.parse(current.created_at);
    });
    if (background) await save(state.phase, { background_compaction: undefined });
    throw error;
  }
}


// Automatic background work is deliberately bounded to one proposal per run.
// Zero disables prefetch while blocking threshold/overflow recovery remain on.
export function shouldBackgroundCompact(settings: DriverTurnSettings, request: DriverTurnInput): boolean {
  if (!settings.can_compact || settings.disable_auto_compact) return false;
  const configured = process.env.WUU_DURABLE_COMPACTION_BACKGROUND_TOKENS;
  const margin = configured === undefined ? 32768 : Number(configured);
  if (!Number.isSafeInteger(margin) || margin < 0 || margin > 2000000)
    throw fault("invalid_compaction_background_tokens");
  const threshold = compactThreshold(settings);
  return margin > 0 && threshold > 0 && estimateRequestTokens(request) > Math.max(0, threshold - margin) &&
    request.messages.filter((message) => message.role !== "system").length > 1;
}

export function backgroundContext(host: RuntimeHost, turn: Turn, state: State,
  settings: DriverTurnSettings, save: Save, wait = false) {
  return changeContext(host, turn, state, settings, "threshold", save, undefined, true, wait);
}

export async function cancelBackgroundContext(host: RuntimeHost, turn: Turn, state: State, save: Save) {
  const operation = state.background_compaction;
  if (!operation) return;
  await turn.cancelCompaction(operation.id);
  await mutate(host, state.session_id, (doc) => {
    const task = doc.tasks.find((item) => item.id === operation.id);
    if (!task) return;
    task.status = "aborted";
    task.phase = "aborted";
    task.error_code = "context_canceled";
    if (task.compaction) task.compaction.status = "canceled";
    task.updated_at = new Date().toISOString();
    task.duration_ms = Date.parse(task.updated_at) - Date.parse(task.created_at);
  });
  await save(state.phase, { background_compaction: undefined });
}
