import type { Thread } from "../shared/protocol";
import { resolveLocalizedText, translateCurrent } from "./i18n";

export type ThreadTitleSource = Pick<
  Thread,
  "id" | "preview" | "title" | "forked_from_id"
>;

export function threadShowsForkMarker(
  thread: ThreadTitleSource,
  threads: ThreadTitleSource[] = [],
): boolean {
  if (!thread.forked_from_id) {
    return false;
  }
  return !threads.some((candidate) => candidate.forked_from_id === thread.id);
}

/**
 * Base title for a thread (no fork marker). The sidebar uses this and pairs
 * the result with a separate fork icon to indicate forks, instead of
 * relying on a text suffix that gets truncated on long titles.
 */
export function baseThreadTitle(
  thread: ThreadTitleSource,
  threads: ThreadTitleSource[] = [],
  fallback?: string
): string {
  const resolvedFallback = fallback ?? translateCurrent("thread.untitled");
  if (threadShowsForkMarker(thread, threads)) {
    const source = threads.find((candidate) => candidate.id === thread.forked_from_id);
    // Prefer source.title (set by the right-click Rename menu) over
    // source.preview (auto-generated) so a renamed source shows the
    // user's title in the fork header.
    const sourceTitle = source
      ? source.title?.trim() || resolveLocalizedText(source.preview?.trim() ?? "")
      : undefined;
    if (sourceTitle) {
      return sourceTitle;
    }
  }
  // Prefer thread.title (set by Rename) over thread.preview
  // (auto-generated) so a renamed thread shows the user's title.
  const ownTitle =
    thread.title?.trim() || resolveLocalizedText(thread.preview?.trim() ?? "");
  return ownTitle || resolvedFallback;
}

export function threadDisplayTitle(
  thread: ThreadTitleSource | undefined,
  threads: ThreadTitleSource[] = [],
  fallback?: string
): string {
  const resolvedFallback = fallback ?? translateCurrent("thread.untitled");
  if (!thread) {
    return resolvedFallback;
  }
  const baseTitle = baseThreadTitle(thread, threads, resolvedFallback);
  if (!threadShowsForkMarker(thread, threads)) {
    return baseTitle;
  }
  return translateCurrent("thread.forkTitle", { title: baseTitle });
}

/** Title shown in the conversation title bar. A saved title wins over the generated preview. */
export function conversationHeadingTitle(
  thread: Pick<ThreadTitleSource, "title" | "preview"> | undefined,
  draftTitle: string | undefined,
  fallback: string,
): string {
  if (thread) {
    return thread.title?.trim() || resolveLocalizedText(thread.preview?.trim() ?? "") || fallback;
  }
  return draftTitle?.trim() || fallback;
}

/**
 * A draft stores the default "new conversation" label until the user renames it.
 * That placeholder must not be written as a session title, or a later generated
 * title would leave the user's unchanged label stuck in place.
 */
export function customDraftConversationTitle(
  draftTitle: string | undefined,
  defaultTitle: string,
): string {
  const trimmed = draftTitle?.trim() ?? "";
  if (!trimmed || trimmed === defaultTitle.trim()) {
    return "";
  }
  return trimmed;
}
