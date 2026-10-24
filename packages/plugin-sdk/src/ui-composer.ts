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
  /** Present in snapshots when this pane has a writable draft owner. */
  readonly ownerTabId?: string;
  readonly text: string;
  readonly imageIds: readonly string[];
  readonly fileIds: readonly string[];
  readonly selections: readonly ApplicationResponseSelectionV1[];
}

export interface ApplicationComposerDraftSnapshotV1 extends ApplicationComposerDraftV1 {
  /** Advances on authoritative resets/restores, including an unchanged empty string; ordinary input does not advance it. */
  readonly revision: number;
}

export interface ApplicationDraftsSnapshotV1 {
  readonly recoveryNotices: readonly { readonly ownerTabId: string; readonly reason: "interrupted" }[];
  readonly activeTabId: string;
  readonly activePane: "primary" | "secondary";
  readonly split: boolean;
  readonly primary: ApplicationComposerDraftSnapshotV1;
  readonly secondary: ApplicationComposerDraftSnapshotV1;
  readonly attachments: readonly ApplicationAttachmentV1[];
}

/** Canonical content stays in the host; edit restores its original selections and attachments. */
export interface ApplicationPendingComposerMessageV1 {
  readonly id: string;
  readonly text: string;
  readonly preview: string;
  readonly fullPreview: string;
  readonly imageIds: readonly string[];
  readonly fileIds: readonly string[];
  readonly held?: boolean;
  readonly deliveryUnconfirmed?: boolean;
  readonly heldPosition?: number;
  readonly origin?: "queue" | "steer";
  readonly operationState?: "preparing" | "sending" | "switching";
}

export interface ApplicationPendingComposerThreadV1 {
  readonly threadId: string;
  readonly queued: readonly ApplicationPendingComposerMessageV1[];
  readonly guides: readonly ApplicationPendingComposerMessageV1[];
}

export interface ApplicationComposerActionMapV1 {
  "composer.remove-pending": { input: { readonly ownerTabId: string; readonly messageId: string; readonly kind: "queue" | "guide" }; result: boolean };
  "composer.edit-pending": { input: { readonly ownerTabId: string; readonly messageId: string; readonly kind: "queue" | "guide" }; result: void };
  "composer.toggle-pending-mode": { input: { readonly ownerTabId: string; readonly messageId: string }; result: void };
  "composer.import-attachments": { input: { readonly attachments: readonly ApplicationAttachmentV1[] }; result: void };
  /** Discard unbound imports or failed admitted reads that will not be restored. */
  "composer.release-attachments": { input: { readonly attachmentIds: readonly string[]; readonly ifUnreferenced?: boolean }; result: void };
  /** The host owns encoding, retries and previews from this point onward. */
  "composer.attach": { input: { readonly files: readonly File[]; readonly ownerTabId?: string }; result: { readonly attachments: readonly ApplicationAttachmentV1[] } };
  "composer.resolve-attachment": { input: { readonly attachmentId: string }; result: ApplicationAttachmentV1 };
  "composer.retry-attachment": { input: { readonly attachmentId: string }; result: ApplicationAttachmentV1 };
  /** Reject a stale source instead of assigning its draft to a new destination. */
  "composer.checkpoint": { input: {
    readonly activeTabId: string;
    /** Ready capture/history bytes admitted in the same ownership transition. */
    readonly attachments?: readonly ApplicationAttachmentV1[];
    readonly primary: ApplicationComposerDraftV1;
    readonly secondary?: ApplicationComposerDraftV1;
  }; result: void };
}
