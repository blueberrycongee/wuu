import type { RuntimeContext } from "../shared/protocol";
import type { SessionTab } from "./AppState";
import { translateCurrent } from "./i18n";

const STORAGE_KEY = "wuu.desktop.draftRecovery.v1";
const MAX_DRAFTS = 20;
const MAX_PROMPT_LENGTH = 32 * 1024;
const MAX_AGE_MS = 24 * 60 * 60 * 1000;
// Bound the complete serialized snapshot too, including JSON escaping and identifiers.
const MAX_STORAGE_LENGTH = 512 * 1024;

type StoredDraft = {
  id: string;
  kind: "draft" | "thread";
  context: RuntimeContext;
  threadID?: string;
  project?: true;
  prompt: string;
  updatedAt: number;
};
type StoredRecovery = { version: 1; activeTabID: string; drafts: StoredDraft[] };
export type ComposerRecovery = { tabs: SessionTab[]; activeTabID: string };

// Keep timestamps for unchanged live input even after its recovery copy expires.
// Ordinary app renders must not extend the retention period.
let previous: StoredRecovery | undefined;

export function sameRecoveryContext(left: RuntimeContext, right: RuntimeContext): boolean {
  return left.kind === right.kind && left.cwd === right.cwd &&
    (left.kind !== "project" || right.kind === "project" && left.project_id === right.project_id);
}

function validString(value: unknown, maximum: number): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= maximum;
}

function parseDraft(value: unknown, now: number): StoredDraft | undefined {
  if (!value || typeof value !== "object") return;
  const entry = value as Partial<StoredDraft>;
  if (!validString(entry.id, 512) || !validString(entry.prompt, MAX_PROMPT_LENGTH) || !entry.prompt.trim() ||
      typeof entry.updatedAt !== "number" || !Number.isFinite(entry.updatedAt) || entry.updatedAt > now ||
      (entry.kind !== "draft" && entry.kind !== "thread")) return;
  const context = entry.context;
  if (!context || !validString(context.cwd, 4096) ||
      (context.kind !== "no_project" && context.kind !== "project")) return;
  if (context.kind === "project" && !validString(context.project_id, 512)) return;
  if (entry.kind === "thread" && (!validString(entry.threadID, 512) || entry.id !== `thread:${entry.threadID}`)) return;
  if (entry.kind === "draft" && !entry.id.startsWith("draft:")) return;
  return {
    id: entry.id, kind: entry.kind,
    context: context.kind === "project"
      ? { kind: "project", project_id: context.project_id, cwd: context.cwd }
      : { kind: "no_project", cwd: context.cwd },
    ...(entry.kind === "thread" ? { threadID: entry.threadID } : {}),
    ...(entry.kind === "draft" && entry.project === true ? { project: true } : {}),
    prompt: entry.prompt, updatedAt: entry.updatedAt,
  };
}

function tabFromDraft(entry: StoredDraft): SessionTab {
  const draft = { prompt: entry.prompt, images: [], files: [] };
  return entry.kind === "thread"
    ? { id: entry.id, kind: "thread", threadID: entry.threadID!, context: entry.context,
        title: translateCurrent("thread.untitled"), ...draft }
    : { id: entry.id, kind: "draft", context: entry.context,
        title: translateCurrent("tabs.newConversation"), createdAt: entry.updatedAt,
        ...(entry.project ? { project: true } : {}), ...draft };
}

function writeStored(snapshot: StoredRecovery): void {
  const now = Date.now();
  const drafts = snapshot.drafts.filter((entry) => now - entry.updatedAt < MAX_AGE_MS).slice(0, MAX_DRAFTS);
  while (drafts.length) {
    const serialized = JSON.stringify({ ...snapshot, drafts });
    if (serialized.length <= MAX_STORAGE_LENGTH) {
      if (window.sessionStorage.getItem(STORAGE_KEY) !== serialized) window.sessionStorage.setItem(STORAGE_KEY, serialized);
      return;
    }
    drafts.pop();
  }
  window.sessionStorage.removeItem(STORAGE_KEY);
}

export function readComposerDraftRecovery(): ComposerRecovery {
  const empty = { tabs: [], activeTabID: "" };
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY);
    if (!raw || raw.length > MAX_STORAGE_LENGTH) {
      window.sessionStorage.removeItem(STORAGE_KEY);
      return empty;
    }
    const data = JSON.parse(raw) as Partial<StoredRecovery>;
    if (data.version !== 1 || !Array.isArray(data.drafts) || data.drafts.length > MAX_DRAFTS) {
      window.sessionStorage.removeItem(STORAGE_KEY);
      return empty;
    }
    const now = Date.now();
    const seen = new Set<string>();
    const drafts = data.drafts.flatMap((value) => {
      const entry = parseDraft(value, now);
      if (!entry || seen.has(entry.id)) return [];
      seen.add(entry.id);
      return [entry];
    });
    previous = { version: 1, activeTabID: validString(data.activeTabID, 512) ? data.activeTabID : "", drafts };
    writeStored(previous);
    const tabs = drafts.filter((entry) => now - entry.updatedAt < MAX_AGE_MS).map(tabFromDraft);
    return { tabs, activeTabID: tabs.some((tab) => tab.id === previous!.activeTabID) ? previous.activeTabID : "" };
  } catch {
    try { window.sessionStorage.removeItem(STORAGE_KEY); } catch { /* No usable recovery store. */ }
    return empty;
  }
}

// Explicit projection is the storage boundary: attachment bytes, File objects,
// selections, auth/runtime configuration and other tab fields never cross it.
export function writeComposerDraftRecovery(tabs: SessionTab[], activeTabID: string): boolean {
  try {
    const now = Date.now();
    const oldDrafts = previous?.drafts ?? [];
    const drafts = tabs.flatMap((tab) => {
      if (tab.kind === "skills") return [];
      const old = oldDrafts.find((entry) => entry.id === tab.id && sameRecoveryContext(entry.context, tab.context));
      const entry = parseDraft({ id: tab.id, kind: tab.kind, context: tab.context,
        ...(tab.kind === "thread" ? { threadID: tab.threadID } : tab.project ? { project: true } : {}), prompt: tab.prompt,
        updatedAt: old?.prompt === tab.prompt ? old.updatedAt : now }, now);
      return entry ? [entry] : [];
    }).sort((left, right) => right.updatedAt - left.updatedAt);
    previous = { version: 1, activeTabID, drafts };
    writeStored(previous);
    return true;
  } catch {
    return false;
  }
}
