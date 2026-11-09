import type { SessionLocatorV1 } from "./session-transport.js";
import type { ApplicationConversationThreadItemV1 } from "./ui-conversation.js";
/** An original artifact snapshot, never a mutable file handle or callback. */
export interface ArtifactResourceV1 {
  readonly id: string;
  readonly ref?: string;
  readonly name: string;
  readonly mimeType: string;
  readonly data?: string;
  readonly text?: string;
  readonly remoteRef?: string;
  readonly uri?: string;
  readonly sha256?: string;
  readonly threadId?: string;
  readonly cwd?: string;
  readonly locator?: SessionLocatorV1;
}
export interface ArtifactCallOptionsV1 { readonly signal?: AbortSignal }
export interface ArtifactSourceV1 { readonly url: string; readonly leaseId?: string }
export interface ApplicationArtifactsV1 {
  readonly capabilities: { readonly nativeItemMenu: boolean; readonly save: boolean; readonly remoteAttachment: boolean; readonly remoteAttachmentPreview: boolean; readonly remoteItem: boolean; readonly source?: { readonly strategy: "streaming" | "bounded-memory"; readonly maxBytes?: number; readonly maxTotalBytes?: number; readonly unsupportedMimeTypes?: readonly string[] } };
  resolveSource(resource: ArtifactResourceV1): string | undefined;
  acquireSource(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<ArtifactSourceV1>;
  releaseSource(leaseId: string): void;
  readAttachment(input: { readonly remoteRef: string; readonly preview?: boolean }, options?: ArtifactCallOptionsV1): Promise<string>;
  readItem(input: { readonly remoteRef: string }, options?: ArtifactCallOptionsV1): Promise<ApplicationConversationThreadItemV1>;
  readText(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<string>;
  save(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void>;
  showItemMenu(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void>;
  openFile(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void>;
}
