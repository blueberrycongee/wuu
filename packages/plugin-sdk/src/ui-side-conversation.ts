import type { ApplicationConversationThreadItemV1, ApplicationConversationTurnV1 } from "./ui-conversation.js";
import type { ConversationContentPartV1, ConversationReadonly } from "./ui-conversation-actions.js";
import type { ApplicationResponseSelectionV1 } from "./ui-composer.js";

export type SideConversationContextV1 = { readonly kind: "project"; readonly project_id: string; readonly cwd: string } | { readonly kind: "no_project"; readonly cwd: string };
export type SideConversationSelectionV1 = { readonly type: "response"; readonly response: ApplicationResponseSelectionV1 } | { readonly type: "file"; readonly file: Extract<ConversationContentPartV1, { type: "file_selection" }>["source"] };
export interface SideConversationSummaryV1 {
  readonly side_thread_id: string; readonly main_thread_id: string; readonly status: "running" | "completed" | "failed" | "interrupted";
  readonly revision: number; readonly created_at: string; readonly updated_at: string;
  readonly main_task_summary?: { readonly running: boolean; readonly last_user_message?: string };
}
export interface SideConversationMessageV1 {
  readonly id: string; readonly side_thread_id: string; readonly role: "user" | "assistant"; readonly text: string;
  readonly selection?: SideConversationSelectionV1; readonly items?: readonly ApplicationConversationThreadItemV1[];
  readonly status?: "streaming" | "completed" | "failed" | "interrupted"; readonly error_message?: string; readonly created_at: string;
}
export interface SideConversationEntryV1 {
  readonly open: boolean; readonly summary: SideConversationSummaryV1 | null; readonly messages: readonly SideConversationMessageV1[];
  readonly draft: string; readonly draftSelection?: SideConversationSelectionV1; readonly streaming: boolean; readonly lastError?: string;
}
export interface SideConversationPageV1 {
  readonly kind: "website" | "app"; readonly pageId: string; readonly title: string; readonly url?: string;
  readonly text: string; readonly selection: string; readonly capturedAt: string;
}
export interface ApplicationSideConversationViewV1 {
  readonly parentThreadId?: string;
  readonly commandId?: string;
  readonly commands: readonly { readonly id: string; readonly title: string; readonly description: string }[];
  readonly entry?: SideConversationEntryV1;
  readonly turns: readonly ApplicationConversationTurnV1[];
  readonly disabledReason?: string;
  readonly contextChatPending: readonly { readonly sourceId: string; readonly sessionId?: string }[];
}
export interface ApplicationSideConversationActionMapV1 {
  "side.open": { input: { readonly parentThreadId: string; readonly commandId?: string; readonly prompt?: string; readonly selection?: SideConversationSelectionV1 }; result: void };
  /** Closing hides the panel; it does not stop or delete the independent reply. */
  "side.close": { input: { readonly parentThreadId: string }; result: void };
  "side.set-draft": { input: { readonly parentThreadId: string; readonly text: string }; result: void };
  "side.set-selection": { input: { readonly parentThreadId: string; readonly selection?: SideConversationSelectionV1 }; result: void };
  "side.send": { input: { readonly parentThreadId: string; readonly text?: string; readonly commandId?: string }; result: { readonly accepted: boolean } };
  "side.interrupt": { input: { readonly parentThreadId: string }; result: void };
  "side.reset": { input: { readonly parentThreadId: string }; result: void };
  /** Captured source data remains inline and is sent through the ordinary shared submission owner. */
  "context-chat.submit": { input: { readonly text: string; readonly page: SideConversationPageV1; readonly context: SideConversationContextV1; readonly sessionId?: string }; result: { readonly sessionId: string; readonly sent: boolean } };
}
export type SideConversationReadonly<T> = ConversationReadonly<T>;
