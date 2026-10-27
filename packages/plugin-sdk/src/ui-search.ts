import type { NavigationThreadV1 } from "./ui-navigation.js";

/** Search summaries retain absent legacy timestamps without inventing dates. */
export interface ApplicationSearchThreadV1 extends Omit<NavigationThreadV1, "turns"> {
  readonly turns: readonly { readonly id: string; readonly status: "in_progress" | "completed" | "failed" | "interrupted";
    readonly started_at?: string | null; readonly completed_at?: string | null; readonly duration_ms?: number | null }[];
}

export interface ApplicationSearchResultV1 {
  /** Scoped to one accepted query response; stale IDs cannot navigate. */
  readonly resultId: string;
  readonly thread: ApplicationSearchThreadV1;
  readonly snippet?: string;
  readonly messageSeq?: number;
}
export interface ApplicationSearchTargetV1 {
  readonly id: string;
  readonly threadId: string;
  readonly messageSeq: number;
  readonly query: string;
  readonly phase: "activating" | "paging" | "hydrating" | "ready" | "not-found" | "failed";
  /** Present only when the addressed user/assistant body actually matches. */
  readonly turnId?: string;
  readonly itemId?: string;
  readonly error?: string;
}
export interface ApplicationSearchViewV1 {
  readonly query: string;
  readonly composing: boolean;
  readonly loading: boolean;
  readonly error?: string;
  readonly results: readonly ApplicationSearchResultV1[];
  readonly target?: ApplicationSearchTargetV1;
}
export interface ApplicationSearchActionMapV1 {
  "search.query": { input: { readonly query: string; readonly composing?: boolean }; result: void };
  "search.run": { input: { readonly query?: string }; result: void };
  "search.locate": { input: { readonly resultId: string }; result: { readonly targetId?: string } };
  /** Continue or retry the selected address using the current authoritative cursor. */
  "search.page": { input: { readonly targetId: string }; result: void };
  /** With an ID, cancel only that target. Without one, also cancel pending query work. */
  "search.cancel": { input: { readonly targetId?: string }; result: void };
  /** Acknowledge only after the UI reserves its local scroll/highlight operation. */
  "search.complete-target": { input: { readonly targetId: string }; result: void };
}
