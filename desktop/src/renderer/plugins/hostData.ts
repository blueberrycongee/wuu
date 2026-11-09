import type { Context } from "cordis";

export interface HostDataEvent {
  type: string;
  thread_id?: string;
  turn_id?: string;
  created_at?: string;
  data?: unknown;
}

export interface HostDataSubscriptionParams {
  thread_id: string;
  types?: string[];
  turn_id?: string;
  limit?: number;
}

export interface HostDataSource {
  subscribe(listener: (event: HostDataEvent) => void): () => void;
}

export interface HostDataDisposable {
  dispose(): void;
}

// subscribeHostData binds a read-only host-data subscription to the lifetime of
// ctx's Cordis scope. Disposing that scope (for example, closing a pane)
// automatically unsubscribes from the source. The first phase only applies the
// metadata filter and bounded read; sanitized content and producer granularity
// are intentionally out of scope.
export function subscribeHostData(
  ctx: Context,
  source: HostDataSource,
  params: HostDataSubscriptionParams,
  handler: (event: HostDataEvent) => void,
): HostDataDisposable {
  if (!params.thread_id || params.thread_id.trim() === "") {
    throw new Error("host.data.subscribe requires thread_id");
  }
  if (params.limit !== undefined && params.limit < 0) {
    throw new Error("host.data.subscribe limit cannot be negative");
  }

  const allowed = new Set(params.types ?? []);
  const turnID = params.turn_id ?? "";
  let remaining = params.limit ?? Number.POSITIVE_INFINITY;

  const effect = ctx.effect(() => {
    const unsubscribe = source.subscribe((event) => {
      if (event.thread_id !== params.thread_id) return;
      if (allowed.size > 0 && !allowed.has(event.type)) return;
      if (turnID !== "" && event.turn_id !== turnID) return;
      if (remaining <= 0) return;
      remaining -= 1;
      handler(event);
    });
    return () => unsubscribe();
  });

  return {
    dispose: () => {
      void effect();
    },
  };
}
