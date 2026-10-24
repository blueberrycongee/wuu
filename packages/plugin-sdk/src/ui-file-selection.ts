import type { ApplicationSubmissionContentPartV1 } from "./ui-submission.js";
export type ApplicationFileSelectionPartV1 = Extract<ApplicationSubmissionContentPartV1, { readonly type: "file_selection" }>;
/** Captured immutable file/PDF resource, including its source revision. */
export type ApplicationFileSelectionSourceV1 = ApplicationFileSelectionPartV1["source"];
export interface ApplicationFileSelectionTargetV1 { readonly ownerTabId: string }
export interface ApplicationFileSelectionSnapshotV1 {
  readonly ownerTabId: string;
  readonly interactionOwnerKey: string;
  readonly enabled: boolean;
  readonly canAskSide: boolean;
  readonly comments: readonly ApplicationFileSelectionPartV1[];
}
type SourceInput = { readonly target: ApplicationFileSelectionTargetV1; readonly source: ApplicationFileSelectionSourceV1 };
export interface FileSelectionCallOptionsV1 { readonly signal?: AbortSignal }
/** Structured references share the canonical composer; DOM selection stays in the UI. */
export interface ApplicationFileSelectionServiceV1 {
  getSnapshot(target: ApplicationFileSelectionTargetV1): ApplicationFileSelectionSnapshotV1;
  subscribe(listener: () => void): () => void;
  quote(input: SourceInput): boolean;
  comment(input: SourceInput & { readonly comment: string; readonly id?: string }): boolean;
  removeComment(input: { readonly target: ApplicationFileSelectionTargetV1; readonly id: string }): boolean;
  /** Independent edit: never consumes the main composer's text or attachments. */
  edit(input: SourceInput & { readonly instruction: string }, options?: FileSelectionCallOptionsV1): Promise<boolean>;
  openPdf(input: SourceInput, options?: FileSelectionCallOptionsV1): Promise<boolean>;
  openFile(input: { readonly target: ApplicationFileSelectionTargetV1; readonly path: string }, options?: FileSelectionCallOptionsV1): Promise<void>;
  askSide(input: SourceInput, options?: FileSelectionCallOptionsV1): Promise<boolean>;
}
