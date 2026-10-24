import type { ApplicationResponseSelectionV1 } from "./ui-composer.js";

export type ConversationReadonly<T> = T extends readonly (infer V)[] ? readonly ConversationReadonly<V>[] : T extends object ? { readonly [K in keyof T]: ConversationReadonly<T[K]> } : T;
export type ConversationContentPartV1 =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "pasted_text"; readonly text: string; readonly title?: string; readonly local_path?: string }
  | { readonly type: "response_selection"; readonly text: string; readonly selection: ApplicationResponseSelectionV1 }
  | { readonly type: "file_selection"; readonly text: string; readonly id: string; readonly intent: "comment" | "edit" | "quote"; readonly comment?: string; readonly source: {
    readonly workspace: string; readonly path: string; readonly start_line: number; readonly start_column: number; readonly end_line: number; readonly end_column: number; readonly quote: string; readonly revision: string;
    readonly pdf?: { readonly start_page: number; readonly end_page: number; readonly artifact_uri?: string; readonly artifact_sha256?: string; readonly artifact_thread_id?: string };
  } };
export interface ConversationHistoryTargetV1 { readonly threadId: string; readonly turnId: string; readonly itemId: string; readonly pane?: "primary" | "secondary" }
export interface ConversationEditDraftV1 {
  readonly text: string; readonly imageIds: readonly string[]; readonly fileIds: readonly string[];
  readonly contentParts?: readonly ConversationContentPartV1[];
}
export interface ConversationQuestionV1 {
  readonly request_id: string; readonly plugin_id: string; readonly execution_id: string; readonly session_id?: string;
  readonly thread_id: string; readonly turn_id: string; readonly actor_id?: string; readonly call_id?: string;
  readonly mode?: "ask" | "offer"; readonly reply_to_tool?: boolean; readonly created_at: string; readonly expires_at?: string;
  readonly questions: readonly { readonly id: string; readonly question: string; readonly header?: string; readonly detail?: string;
    readonly options?: readonly { readonly label: string; readonly description?: string }[]; readonly multi_select?: boolean; readonly allow_custom?: boolean }[];
}
export interface ConversationQuestionAnswerV1 { readonly answers: readonly { readonly id: string; readonly selected: readonly string[]; readonly custom?: string }[] }
export interface ApplicationConversationActionsViewV1 {
  readonly editing?: ConversationHistoryTargetV1 & { readonly draft: ConversationEditDraftV1; readonly submitting: boolean };
  readonly pendingFork?: ConversationHistoryTargetV1 & { readonly busyMode?: "local" | "worktree"; readonly worktreeDisabledReason?: string };
  readonly questions: readonly ConversationQuestionV1[];
  readonly pendingQuestionIds: readonly string[];
  readonly questionError?: string;
  readonly compactingThreadIds: readonly string[];
  readonly handoffThreadIds: readonly string[];
}
export interface ApplicationConversationActionMapV1 {
  "history.request-fork": { input: ConversationHistoryTargetV1; result: { readonly accepted: boolean } };
  "history.choose-fork": { input: { readonly mode: "local" | "worktree" }; result: void };
  "history.cancel-fork": { input: Record<string, never>; result: void };
  "history.begin-edit": { input: ConversationHistoryTargetV1; result: { readonly accepted: boolean } };
  "history.update-edit": { input: ConversationHistoryTargetV1 & { readonly draft: ConversationEditDraftV1 }; result: void };
  "history.cancel-edit": { input: Record<string, never>; result: void };
  /** Uses the current immutable edit draft and rejects a stale selected item. */
  "history.submit-edit": { input: ConversationHistoryTargetV1; result: void };
  "question.refresh": { input: Record<string, never>; result: void };
  "question.answer": { input: { readonly requestId: string; readonly answer: ConversationQuestionAnswerV1 }; result: void };
  "question.cancel": { input: { readonly requestId: string }; result: void };
  "question.hold": { input: { readonly requestId: string }; result: void };
  "question.send-offer": { input: { readonly requestId: string; readonly text: string }; result: { readonly accepted: boolean } };
  "conversation.compact": { input: { readonly threadId: string }; result: void };
  "conversation.handoff": { input: { readonly threadId: string; readonly provider: string; readonly model: string; readonly effort?: string; readonly intent: string }; result: { readonly threadId: string } };
  /** Accepted thread stops share the submission owner's admission and retry lanes. */
  "conversation.interrupt": { input: { readonly threadId: string }; result: void };
}
