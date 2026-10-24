/** Attachment bytes are owned by the application, independently of UI mounts. */
export interface ApplicationAttachmentV1 {
  readonly id: string;
  readonly kind: "image" | "file";
  readonly name: string;
  readonly mediaType: string;
  readonly status: "pending" | "ready" | "failed";
  readonly previewUrl?: string;
  readonly error?: string;
  readonly data?: string;
  readonly remoteRef?: string;
  readonly width?: number;
  readonly height?: number;
}

export interface ApplicationResponseSelectionV1 {
  readonly id: string;
  readonly text: string;
  readonly comment?: string;
  readonly source: {
    readonly thread_id: string;
    readonly turn_id: string;
    readonly item_id: string;
    readonly range_text?: string;
    readonly start_offset: number;
    readonly end_offset: number;
  };
}

export interface ApplicationComposerDraftV1 {
  readonly text: string;
  readonly imageIds: readonly string[];
  readonly fileIds: readonly string[];
  readonly selections: readonly ApplicationResponseSelectionV1[];
}

export interface ApplicationDraftsSnapshotV1 {
  readonly activeTabId: string;
  readonly activePane: "primary" | "secondary";
  readonly split: boolean;
  readonly primary: ApplicationComposerDraftV1;
  readonly secondary: ApplicationComposerDraftV1;
  readonly attachments: readonly ApplicationAttachmentV1[];
}

export interface ApplicationComposerActionMapV1 {
  "composer.import-attachments": { input: { readonly attachments: readonly ApplicationAttachmentV1[] }; result: void };
  "composer.release-attachments": { input: { readonly attachmentIds: readonly string[] }; result: void };
  /** The host owns encoding, retries and previews from this point onward. */
  "composer.attach": { input: { readonly files: readonly File[]; readonly ownerTabId?: string }; result: { readonly attachments: readonly ApplicationAttachmentV1[] } };
  "composer.resolve-attachment": { input: { readonly attachmentId: string }; result: ApplicationAttachmentV1 };
  "composer.retry-attachment": { input: { readonly attachmentId: string }; result: ApplicationAttachmentV1 };
  /** Reject a stale source instead of assigning its draft to a new destination. */
  "composer.checkpoint": { input: {
    readonly activeTabId: string;
    readonly primary: ApplicationComposerDraftV1;
    readonly secondary?: ApplicationComposerDraftV1;
  }; result: void };
}
