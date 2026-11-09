import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { SessionOrganizationGroup } from "../shared/protocol";
import type { ThreadSummary } from "./AppState";
import { showErrorToast } from "./Toast";
import { translateCurrent } from "./i18n";

export type SessionGroup = Pick<SessionOrganizationGroup, "id" | "name">;
export const SESSION_FOLDER_DRAG_MIME = "application/x-wuu-session-folder";
export type SessionOrganization = {
  folders: SessionGroup[];
  folderByThreadID: Record<string, string>;
};

export const emptySessionOrganization: SessionOrganization = {
  folders: [], folderByThreadID: {},
};

export function parseSessionOrganization(value: unknown): SessionOrganization {
  if (!value || typeof value !== "object" || Array.isArray(value)) return emptySessionOrganization;
  const record = value as Record<string, unknown>;
  const folders = Array.isArray(record.folders)
    ? record.folders.flatMap((item) => {
        if (!item || typeof item !== "object" || Array.isArray(item)) return [];
        const entry = item as Record<string, unknown>;
        return typeof entry.id === "string" && typeof entry.name === "string"
          ? [{ id: entry.id, name: entry.name }]
          : [];
      })
    : [];
  return { folders, folderByThreadID: {} };
}

export type SessionOrganizationController = SessionOrganization & {
  createFolder: (name: string, threadID?: string) => Promise<void>;
  renameFolder: (id: string, name: string) => Promise<void>;
  reorderFolders: (orderedIDs: string[]) => Promise<void>;
  deleteFolder: (id: string) => Promise<void>;
  moveThreadToFolder: (threadID: string, folderID?: string) => Promise<void>;
};

export function useSessionOrganization(threads: ThreadSummary[], supplied?: SessionOrganizationController): SessionOrganizationController {
  const [folders, setFolders] = useState<SessionGroup[]>([]);
  const [optimisticFolders, setOptimisticFolders] = useState<Record<string, string>>({});
  const folderReorderRevisionRef = useRef(0);
  const folderReorderQueueRef = useRef<Promise<void>>(Promise.resolve());
  const folderAssignmentRevision = JSON.stringify(
    threads.map((thread) => [thread.id, thread.folder_id ?? ""]),
  );

  useEffect(() => {
    if (supplied || !window.wuu?.getSessionOrganization) return;
    let cancelled = false;
    void window.wuu.getSessionOrganization().then((result) => {
      if (cancelled) return;
      const parsed = parseSessionOrganization(result.organization);
      setFolders(parsed.folders);
    }).catch((error) => {
      if (!cancelled) showErrorToast(error, translateCurrent("sessionOrganization.loadFailed"), "session-organization-load");
    });
    return () => { cancelled = true; };
  }, [Boolean(supplied)]);

  useEffect(() => {
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    setOptimisticFolders((current) => clearReconciledAssignments(current, byID));
  }, [folderAssignmentRevision]);

  const folderByThreadID = useMemo(() => {
    const next: Record<string, string> = {};
    for (const thread of threads) {
      const folderID = Object.hasOwn(optimisticFolders, thread.id) ? optimisticFolders[thread.id] : thread.folder_id;
      if (folderID) next[thread.id] = folderID;
    }
    return next;
  }, [optimisticFolders, threads]);

  async function moveThreadToFolder(threadID: string, folderID?: string): Promise<void> {
    const assignment = folderID ?? "";
    setOptimisticFolders((current) => ({ ...current, [threadID]: assignment }));
    try {
      await window.wuu.updateThreadOrganization(threadID, assignment);
    } catch (error) {
      setOptimisticFolders((current) => clearOptimisticAssignment(current, threadID, assignment));
      showErrorToast(error, translateCurrent("sessionOrganization.moveToFolderFailed"));
    }
  }

  if (supplied) return supplied;
  return {
    folders,
    folderByThreadID,
    createFolder: async (name, threadID) => {
      try {
        const { group } = await window.wuu.createSessionFolder(name);
        setFolders((current) => [...current, group]);
        if (threadID) void moveThreadToFolder(threadID, group.id);
      } catch (error) {
        showErrorToast(error, translateCurrent("sessionOrganization.createFolderFailed"));
        throw error;
      }
    },
    renameFolder: async (id, name) => {
      try {
        const { group } = await window.wuu.renameSessionFolder(id, name);
        setFolders((current) => current.map((item) => (item.id === id ? group : item)));
      } catch (error) {
        showErrorToast(error, translateCurrent("sessionOrganization.renameFolderFailed"));
        throw error;
      }
    },
    reorderFolders: async (orderedIDs) => {
      const reordered = reorderOrganizationGroups(folders, orderedIDs);
      if (reordered === folders) return;
      const previous = folders;
      const revision = folderReorderRevisionRef.current + 1;
      folderReorderRevisionRef.current = revision;
      setFolders(reordered);

      const request = folderReorderQueueRef.current
        .catch(() => undefined)
        .then(async () => {
          const { organization } = await window.wuu.reorderSessionFolders(orderedIDs);
          if (folderReorderRevisionRef.current !== revision) return;
          const parsed = parseSessionOrganization(organization);
          setFolders(parsed.folders);
        });
      folderReorderQueueRef.current = request;
      try {
        await request;
      } catch (error) {
        if (folderReorderRevisionRef.current === revision) {
          setFolders(previous);
        }
        showErrorToast(error, translateCurrent("sessionOrganization.reorderFolderFailed"));
      }
    },
    deleteFolder: async (id) => {
      try {
        await window.wuu.deleteSessionFolder(id);
        setFolders((current) => current.filter((item) => item.id !== id));
        setOptimisticFolders((current) => {
          const next = { ...current };
          for (const item of threads) if (item.folder_id === id) next[item.id] = "";
          return next;
        });
      } catch (error) {
        showErrorToast(error, translateCurrent("sessionOrganization.deleteFolderFailed"));
      }
    },
    moveThreadToFolder,
  };
}

export function reorderOrganizationGroups(groups: SessionGroup[], orderedIDs: string[]): SessionGroup[] {
  if (orderedIDs.length !== groups.length || new Set(orderedIDs).size !== groups.length) return groups;
  const byID = new Map(groups.map((group) => [group.id, group]));
  const reordered = orderedIDs.flatMap((id) => {
    const group = byID.get(id);
    return group ? [group] : [];
  });
  if (reordered.length !== groups.length) return groups;
  return reordered.every((group, index) => group.id === groups[index]?.id) ? groups : reordered;
}

export function clearOptimisticAssignment(
  optimistic: Record<string, string>,
  threadID: string,
  expected: string,
): Record<string, string> {
  if (optimistic[threadID] !== expected) return optimistic;
  const next = { ...optimistic };
  delete next[threadID];
  return next;
}

export function clearReconciledAssignments(
  optimistic: Record<string, string>,
  threads: Map<string, ThreadSummary>,
): Record<string, string> {
  let next = optimistic;
  for (const [threadID, value] of Object.entries(optimistic)) {
    const thread = threads.get(threadID);
    if (!thread || (thread.folder_id ?? "") !== value) continue;
    if (next === optimistic) next = { ...optimistic };
    delete next[threadID];
  }
  return next;
}

export type SessionOrganizationActions = SessionOrganization & {
  revealThread?: (threadId: string) => Promise<void>;
  togglePinned: (thread: ThreadSummary, fallback: (thread: ThreadSummary) => void) => void;
  moveToFolder: (thread: ThreadSummary, folderID?: string) => void;
  createFolderForThread: (thread: ThreadSummary) => void;
  folderDragThreadID?: string;
  startFolderDrag: (threadID: string) => void;
  endFolderDrag: () => void;
};
const SessionOrganizationContext = createContext<SessionOrganizationActions | null>(null);
export function SessionOrganizationProvider({ value, children }: { value: SessionOrganizationActions; children: ReactNode }): JSX.Element {
  return <SessionOrganizationContext.Provider value={value}>{children}</SessionOrganizationContext.Provider>;
}
export function useSessionOrganizationActions(): SessionOrganizationActions | null {
  return useContext(SessionOrganizationContext);
}
