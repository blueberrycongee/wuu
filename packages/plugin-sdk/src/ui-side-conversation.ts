import type { ApplicationConversationThreadV1 } from "./ui-conversation.js";
import type { ConversationContentPartV1, ConversationReadonly } from "./ui-conversation-actions.js";
import type { ApplicationResponseSelectionV1 } from "./ui-composer.js";

export type SideConversationContextV1 = { readonly kind: "project"; readonly project_id: string; readonly cwd: string } | { readonly kind: "no_project"; readonly cwd: string };
export type SideConversationSelectionV1 = { readonly type: "response"; readonly response: ApplicationResponseSelectionV1 } | { readonly type: "file"; readonly file: Extract<ConversationContentPartV1, { type: "file_selection" }>["source"] };
export interface SideConversationPageV1 {
  readonly kind: "website" | "app"; readonly pageId: string; readonly title: string; readonly url?: string;
  readonly text: string; readonly selection: string; readonly capturedAt: string;
}
export interface ApplicationSideConversationViewV1 {
  readonly parentThreadId?: string;
  readonly catalogRevision?: number;
  readonly contextChatPending: readonly { readonly sourceId: string; readonly sessionId?: string }[];
}
export interface ApplicationSideConversationActionMapV1 {
  "side.open": { input: { readonly parentThreadId: string; readonly threadId?: string; readonly prompt?: string; readonly selection?: SideConversationSelectionV1 }; result: { readonly tabId: string } };
  "side.list": { input: { readonly parentThreadId: string }; result: { readonly threads: readonly ApplicationConversationThreadV1[] } };
  "side.promote": { input: { readonly threadId: string }; result: void };
  "side.delete": { input: { readonly threadId: string }; result: void };
  /** Captured source data remains inline and is sent through the ordinary shared submission owner. */
  "context-chat.submit": { input: { readonly text: string; readonly page: SideConversationPageV1; readonly context: SideConversationContextV1; readonly sessionId?: string }; result: { readonly sessionId: string; readonly sent: boolean } };
}
export type SideConversationReadonly<T> = ConversationReadonly<T>;
