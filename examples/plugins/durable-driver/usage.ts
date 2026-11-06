import type { DriverModelReceipts } from "@wuu/plugin-sdk";
import type { SessionDocument, State, Task } from "./state.js";
type Counts = {
    input_tokens: number;
    output_tokens: number;
    cache_creation_tokens: number;
    cache_read_tokens: number;
};
type Bucket = Counts & {
    provider: string;
    model: string;
};
type UsageTask = Task & {
    usage_by_model?: Bucket[];
    usage_receipts?: DriverModelReceipts["receipts"];
    usage_receipt_status?: DriverModelReceipts["status"];
};
const keys = ["input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens"] as const;
function validateAggregate(counts: Counts) {
    for (const key of keys)
        if (!Number.isSafeInteger(counts[key]) || counts[key] < 0)
            throw new Error("invalid model usage aggregate");
}
const zero = (): Counts => ({ input_tokens: 0, output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0 });
export function readModelUsage(turn: {
    modelReceipts(requestID: string): Promise<DriverModelReceipts>;
}, state: State) {
    return turn.modelReceipts(`${state.run_id}:model:${state.round}`);
}
/** Apply a metadata projection atomically with the caller's task transition.
 * Delta accounting preserves root totals when old terminal tasks are pruned.
 * A receipt read is evidence only and never authorizes another model call. */
export function applyModelUsage(doc: SessionDocument, state: State, result: DriverModelReceipts): void {
    const task = doc.tasks.find(t => t.id === `${state.run_id}:model:${state.round}`) as UsageTask | undefined;
    const root = doc.tasks.find(t => t.id === state.run_id) as UsageTask | undefined;
    if (!task || !root)
        throw new Error("model usage requires persisted model and root tasks");
    // Retain prior authoritative entries if retention or partial reads omit them.
    const receipts = new Map((task.usage_receipts ?? []).map(r => [r.submission_id, r]));
    for (const r of result.receipts) {
        const prior = receipts.get(r.submission_id);
        if (prior && (prior.operation_id !== r.operation_id || prior.provider !== r.provider || prior.model !== r.model))
            throw new Error("model receipt identity changed");
        receipts.set(r.submission_id, {
            operation_id: r.operation_id, submission_id: r.submission_id, provider: r.provider, model: r.model,
            outcome: r.outcome, cost_state: r.cost_state,
            usage: r.usage ? { input_tokens: r.usage.input_tokens, output_tokens: r.usage.output_tokens, cache_creation_tokens: r.usage.cache_creation_tokens, cache_read_tokens: r.usage.cache_read_tokens } : prior?.usage,
        });
    }
    if (receipts.size > 128) {
        task.usage_status = task.usage ? "incomplete" : "unavailable";
        root.usage_status = root.usage ? "incomplete" : "unavailable";
        return;
    }
    const next = zero(), buckets = new Map<string, Bucket>();
    let known = 0;
    let complete = result.status === "complete";
    for (const r of receipts.values()) {
        if (!r.usage) {
            complete = false;
            continue;
        }
        known++;
        const key = JSON.stringify([r.provider, r.model]);
        const bucket = buckets.get(key) ?? { provider: r.provider, model: r.model, ...zero() };
        for (const key of keys) {
            const count = r.usage[key] ?? 0;
            if (!Number.isSafeInteger(count) || count < 0)
                throw new Error("invalid authoritative usage count");
            next[key] += count;
            bucket[key] += count;
        }
        validateAggregate(next);
        validateAggregate(bucket);
        buckets.set(key, bucket);
    }
    // Unavailable does not turn previously recorded counts into zero.
    if (known) {
        const total = { ...zero(), ...root.usage };
        const old = { ...zero(), ...task.usage };
        for (const key of keys)
            total[key] += next[key] - old[key];
        validateAggregate(total);
        root.usage = total;
        task.usage = next;
        const rootBuckets = new Map((root.usage_by_model ?? []).map(b => [JSON.stringify([b.provider, b.model]), { ...b }]));
        for (const [sign, rows] of [[-1, task.usage_by_model ?? []], [1, [...buckets.values()]]] as const) {
            for (const row of rows) {
                const key = JSON.stringify([row.provider, row.model]);
                const bucket = rootBuckets.get(key) ?? { provider: row.provider, model: row.model, ...zero() };
                for (const field of keys)
                    bucket[field] += sign * row[field];
                validateAggregate(bucket);
                rootBuckets.set(key, bucket);
            }
        }
        if (rootBuckets.size <= 128) {
            root.usage_by_model = [...rootBuckets.values()];
            task.usage_by_model = [...buckets.values()];
        }
        else
            complete = false;
    }
    task.usage_receipts = [...receipts.values()];
    task.usage_receipt_status = result.status;
    task.usage_status = complete ? "available" : task.usage ? "incomplete" : "unavailable";
    root.usage_status = complete && root.usage_status !== "incomplete" && root.usage_status !== "unavailable" ? "available" : root.usage ? "incomplete" : "unavailable";
}
