import type { ApplicationResponseSelectionV1 } from "./ui-composer.js";
import type { ApplicationConversationTurnV1 } from "./ui-conversation.js";

/** Ordered text and references; attachment bytes stay with the draft resource owner. */
export type ApplicationSubmissionContentPartV1 =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "pasted_text"; readonly text: string; readonly title?: string; readonly local_path?: string }
  | { readonly type: "response_selection"; readonly text: string; readonly selection: ApplicationResponseSelectionV1 }
  | { readonly type: "file_selection"; readonly text: string; readonly id: string;
      readonly intent: "comment" | "edit" | "quote"; readonly comment?: string;
      readonly source: { readonly workspace: string; readonly path: string; readonly start_line: number;
        readonly start_column: number; readonly end_line: number; readonly end_column: number;
        readonly quote: string; readonly revision: string;
        readonly pdf?: { readonly start_page: number; readonly end_page: number; readonly artifact_uri?: string;
          readonly artifact_sha256?: string; readonly artifact_thread_id?: string } } };

export interface ApplicationSubmissionInputV1 {
  readonly ownerTabId: string;
  readonly mode?: "send" | "queue" | "steer";
  readonly text?: string;
  readonly contentParts?: readonly ApplicationSubmissionContentPartV1[];
  readonly selections?: readonly ApplicationResponseSelectionV1[];
  readonly activeDocument?: { readonly path: string };
  readonly artifact?: { readonly id: string; readonly threadId: string; readonly sha: string };
  readonly consumedOfferID?: string;
}

/** Acceptance is local admission. Observe operations for delivery or recovery. */
export interface ApplicationSubmissionResultV1 {
  readonly accepted: boolean;
  readonly threadId?: string;
  readonly operationID?: string;
}
export interface ApplicationSubmissionOperationV1 {
  readonly operationID: string;
  readonly ownerTabId: string;
  readonly threadId?: string;
  readonly messageID: string;
  /** Resolved delivery route, which may differ from the requested mode.
   * Only `send` requests placement of the message; queue, steer, or an
   * unresolved route preserve the reader's current position. */
  readonly mode?: "send" | "queue" | "steer";
  readonly phase: "preparing" | "sending" | "completed" | "failed" | "cancelled";
  readonly error?: string;
}
export interface ApplicationSubmissionSnapshotV1 {
  readonly operations: readonly ApplicationSubmissionOperationV1[];
  readonly pendingCreations: readonly { readonly ownerTabId: string; readonly operationID: string; readonly startedAtMs: number; readonly turn: ApplicationConversationTurnV1 }[];
  readonly failedDraftTabIds: readonly string[];
  readonly stopRequests: readonly { readonly threadId: string; readonly phase: "pending" | "retry" }[];
}
export interface ApplicationSubmissionActionMapV1 {
  "composer.submit": { input: ApplicationSubmissionInputV1; result: ApplicationSubmissionResultV1 };
  "composer.cancel-submission": { input: { readonly operationID: string }; result: { readonly cancelled: boolean } };
}
