import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import type {
  DesktopProject,
  RuntimeContext,
  ServerEvent,
  Thread,
} from "../shared/protocol";
import {
  SCRATCH_PSEUDO_PROJECT_ID,
  initialState,
  isScratchThread,
  mergeSidebarThread,
  reduceNotification,
  sortThreads,
  threadBelongsToProject,
  threadFromRecord,
} from "./AppState";
import {
  reconcileSidebarSectionOrder,
  SIDEBAR_SECTION_COLLAB,
  SIDEBAR_SECTION_PINNED,
} from "./AppSidebar";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { isRecord, recordValue } from "./ToolActivity";
import { translateCurrent } from "./i18n";

const SIDEBAR_COLLAPSED_SECTION_IDS_KEY =
  "wuu.desktop.collapsedSidebarSectionIDs";
const SIDEBAR_EXPANDED_SECTION_IDS_KEY =
  "wuu.desktop.expandedSidebarSectionIDs";
const LEGACY_PROJECT_COLLAPSED_IDS_KEY = "wuu.desktop.collapsedProjectIDs";
const LEGACY_PROJECT_EXPANDED_IDS_KEY = "wuu.desktop.expandedProjectIDs";
const SIDEBAR_SECTION_ORDER_KEY = "wuu.desktop.sidebarSectionOrder";

export type SidebarProjectStateController = {
  collapsedSidebarSectionIDs: Set<string>;
  expandedSidebarSectionIDs: Set<string>;
  loadingProjectThreadIDs: ReadonlySet<string>;
  projectThreadsByProjectID: Record<string, Thread[]>;
  cachedScratchThreads: Thread[];
  sidebarSectionOrder: string[];
  setSidebarSectionOrder: Dispatch<SetStateAction<string[]>>;
  loadProjectThreads: (project: DesktopProject) => Promise<void>;
  cacheSidebarThreads: (threads: Thread[]) => void;
  updateCachedSidebarThread: (thread: Thread) => void;
  updateCachedSidebarThreadPinned: (threadID: string, pinned: boolean) => void;
  removeCachedSidebarThread: (threadID: string) => void;
  syncSidebarServerEvent: (event: ServerEvent) => void;
  toggleSidebarSectionCollapsed: (sectionID: string) => void;
};

const SIDEBAR_THREAD_LIFECYCLE_METHODS = new Set([
  "thread/started",
  "thread/resumed",
  "thread/updated",
  "turn/started",
  "turn/completed",
  "turn/error",
  "agent/updated",
]);

function storedSidebarSectionIDSet(
  key: string,
  legacyKey?: string,
): Set<string> {
  try {
    const stored =
      window.localStorage.getItem(key) ??
      (legacyKey ? window.localStorage.getItem(legacyKey) : null);
    const parsed: unknown = stored ? JSON.parse(stored) : [];
    if (!Array.isArray(parsed)) {
      return new Set();
    }
    return new Set(
      parsed.filter(
        (id): id is string => typeof id === "string" && id.length > 0,
      ),
    );
  } catch {
    return new Set();
  }
}

function storedSidebarSectionOrder(): string[] | undefined {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_SECTION_ORDER_KEY);
    if (!stored) return undefined;
    const parsed: unknown = JSON.parse(stored);
    if (!Array.isArray(parsed)) return undefined;
    return parsed.filter(
      (id): id is string => typeof id === "string" && id.length > 0,
    );
  } catch {
    return undefined;
  }
}

function initialCollapsedSidebarSectionIDs(): Set<string> {
  return storedSidebarSectionIDSet(
    SIDEBAR_COLLAPSED_SECTION_IDS_KEY,
    LEGACY_PROJECT_COLLAPSED_IDS_KEY,
  );
}

function initialExpandedSidebarSectionIDs(): Set<string> {
  const expanded = storedSidebarSectionIDSet(
    SIDEBAR_EXPANDED_SECTION_IDS_KEY,
    LEGACY_PROJECT_EXPANDED_IDS_KEY,
  );
  // 对话 defaults to expanded. Older builds auto-expanded it whenever the
  // no_project context was active without ever persisting that into the
  // expanded set, so a stored state without an explicit collapse marker
  // means "open". A user collapse writes the marker and wins from then on.
  if (!initialCollapsedSidebarSectionIDs().has(SCRATCH_PSEUDO_PROJECT_ID)) {
    expanded.add(SCRATCH_PSEUDO_PROJECT_ID);
  }
  return expanded;
}

/**
 * Session-tree (对话 / project) sections expand ONLY via their own header
 * toggle: expanded ⇔ the id is in the persisted expanded set. Selecting a
 * session, switching the active project/context, or opening a pinned
 * session must never change any section's expand state — the sidebar's
 * expand/collapse is purely manual (user mental model: clicking a session
 * opens its tab; clicking a header toggles that header's section).
 */
export function sessionTreeSectionExpanded(
  sectionID: string,
  expandedSidebarSectionIDs: ReadonlySet<string>,
): boolean {
  return expandedSidebarSectionIDs.has(sectionID);
}

function removeMissingIDs(
  ids: Set<string>,
  validIDs: ReadonlySet<string>,
): Set<string> {
  const next = new Set<string>();
  for (const id of ids) {
    if (validIDs.has(id)) {
      next.add(id);
    }
  }
  return next.size === ids.size ? ids : next;
}

export function threadListsEquivalent(
  left: Thread[] | undefined,
  right: Thread[],
): boolean {
  if (!left || left.length !== right.length) {
    return false;
  }
  return left.every((thread, index) => {
    const candidate = right[index];
    return (
      candidate?.id === thread.id &&
      candidate.updated_at === thread.updated_at &&
      candidate.latest_completed_turn_id === thread.latest_completed_turn_id &&
      candidate.status === thread.status &&
      candidate.pinned === thread.pinned &&
      candidate.archived === thread.archived
    );
  });
}

function threadSnapshotsShallowEqual(left: Thread, right: Thread): boolean {
  const leftKeys = Object.keys(left) as Array<keyof Thread>;
  const rightKeys = Object.keys(right);
  return (
    leftKeys.length === rightKeys.length &&
    leftKeys.every((key) => Object.is(left[key], right[key]))
  );
}

function sameWorkdirPath(left: string, right: string): boolean {
  const trim = (path: string): string => path.replace(/\/+$/, "");
  return trim(left) === trim(right);
}

export function mergeSidebarThreadSnapshots(
  cached: Thread[] | undefined,
  incoming: Thread[],
): Thread[] {
  const previous = cached ?? [];
  const byID = new Map(previous.map((thread) => [thread.id, thread]));
  let changed = false;
  for (const thread of incoming) {
    if (byID.get(thread.id) === thread) {
      continue;
    }
    const cachedThread = byID.get(thread.id);
    const mergedThread = cachedThread
      ? mergeSidebarThread(cachedThread, thread)
      : thread;
    if (
      cachedThread &&
      threadSnapshotsShallowEqual(cachedThread, mergedThread)
    ) {
      continue;
    }
    byID.set(thread.id, mergedThread);
    changed = true;
  }
  if (!changed) {
    return previous;
  }
  return sortThreads([...byID.values()]);
}

export function threadsForDesktopProject(
  threads: Thread[],
  project: DesktopProject,
): Thread[] {
  // Archived threads intentionally stay in `state.threads` so Settings → Archive
  // can list them; this helper feeds the sidebar surfaces and must hide them.
  return sortThreads(
    threads.filter(
      (thread) =>
        !thread.ephemeral &&
        !thread.archived &&
        threadBelongsToProject(thread, project),
    ),
  );
}

export function useSidebarProjectState({
  projects,
  threads,
  activeContext,
  activeProjectID,
  backgroundLoadingEnabled = true,
  setStatus,
}: {
  projects: DesktopProject[];
  threads: Thread[];
  activeContext?: RuntimeContext;
  activeProjectID?: string;
  backgroundLoadingEnabled?: boolean;
  setStatus: (status: string) => void;
}): SidebarProjectStateController {
  const [collapsedSidebarSectionIDs, setCollapsedSidebarSectionIDs] =
    useState<Set<string>>(initialCollapsedSidebarSectionIDs);
  const [expandedSidebarSectionIDs, setExpandedSidebarSectionIDs] =
    useState<Set<string>>(initialExpandedSidebarSectionIDs);
  const [projectThreadsByProjectID, setProjectThreadsByProjectID] = useState<
    Record<string, Thread[]>
  >({});
  const [cachedScratchThreads, setCachedScratchThreads] = useState<Thread[]>(
    [],
  );
  const [sidebarSectionOrder, setSidebarSectionOrder] = useState<string[]>(
    () =>
      reconcileSidebarSectionOrder(
        storedSidebarSectionOrder(),
        [],
      ),
  );
  const loadingProjectThreadIDsRef = useRef(new Set<string>());
  const projectIDs = projects.map((project) => project.id);
  const projectIdentityRevision = JSON.stringify(projectIDs);
  const projectsByID = useMemo(
    () => new Map(projects.map((project) => [project.id, project])),
    [projects],
  );
  const activeProject =
    activeContext?.kind === "project" && activeProjectID
      ? projectsByID.get(activeProjectID)
      : undefined;
  const activeProjectThreads = useMemo(
    () =>
      activeProject
        ? threadsForDesktopProject(threads, activeProject)
        : undefined,
    [activeProject, threads],
  );
  const cachedActiveProjectThreads = activeProjectID
    ? projectThreadsByProjectID[activeProjectID]
    : undefined;
  const activeProjectThreadSnapshot = useMemo(
    () =>
      activeProjectID && activeProjectThreads
        ? mergeSidebarThreadSnapshots(
            cachedActiveProjectThreads,
            activeProjectThreads,
          )
        : undefined,
    [activeProjectID, activeProjectThreads, cachedActiveProjectThreads],
  );
  const visibleProjectThreadsByProjectID = useMemo(() => {
    if (
      !activeProjectID ||
      !activeProjectThreadSnapshot ||
      activeProjectThreadSnapshot === cachedActiveProjectThreads
    ) {
      return projectThreadsByProjectID;
    }
    return {
      ...projectThreadsByProjectID,
      [activeProjectID]: activeProjectThreadSnapshot,
    };
  }, [
    activeProjectID,
    activeProjectThreadSnapshot,
    cachedActiveProjectThreads,
    projectThreadsByProjectID,
  ]);
  const loadingProjectThreadIDs = useMemo(() => {
    const loading = new Set(loadingProjectThreadIDsRef.current);
    for (const project of projects) {
      if (
        project.id !== activeProjectID &&
        sessionTreeSectionExpanded(project.id, expandedSidebarSectionIDs) &&
        !Object.prototype.hasOwnProperty.call(projectThreadsByProjectID, project.id)
      ) {
        loading.add(project.id);
      }
    }
    return loading;
  }, [activeProjectID, expandedSidebarSectionIDs, projectThreadsByProjectID, projects]);

  useEffect(() => {
    window.localStorage.setItem(
      SIDEBAR_COLLAPSED_SECTION_IDS_KEY,
      JSON.stringify([...collapsedSidebarSectionIDs]),
    );
  }, [collapsedSidebarSectionIDs]);

  useEffect(() => {
    window.localStorage.setItem(
      SIDEBAR_EXPANDED_SECTION_IDS_KEY,
      JSON.stringify([...expandedSidebarSectionIDs]),
    );
  }, [expandedSidebarSectionIDs]);

  useEffect(() => {
    window.localStorage.setItem(
      SIDEBAR_SECTION_ORDER_KEY,
      JSON.stringify(sidebarSectionOrder),
    );
  }, [sidebarSectionOrder]);

  useEffect(() => {
    setSidebarSectionOrder((current) =>
      reconcileSidebarSectionOrder(
        current,
        projectIDs,
      ),
    );
  }, [projectIdentityRevision]);

  useEffect(() => {
    const validProjectIDs = new Set(projectIDs);
    const validSectionIDs = new Set([
      ...validProjectIDs,
      SIDEBAR_SECTION_PINNED,
      SIDEBAR_SECTION_COLLAB,
      SCRATCH_PSEUDO_PROJECT_ID,
    ]);
    setCollapsedSidebarSectionIDs((current) =>
      removeMissingIDs(current, validSectionIDs),
    );
    setExpandedSidebarSectionIDs((current) =>
      removeMissingIDs(current, validSectionIDs),
    );
    setProjectThreadsByProjectID((current) => {
      const next: Record<string, Thread[]> = {};
      let changed = false;
      for (const [projectID, projectThreads] of Object.entries(current)) {
        if (validProjectIDs.has(projectID)) {
          next[projectID] = projectThreads;
        } else {
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }, [projectIdentityRevision]);

  useEffect(() => {
    if (
      !activeProjectID ||
      !activeProjectThreads ||
      !activeProjectThreadSnapshot ||
      activeProjectThreadSnapshot === cachedActiveProjectThreads
    ) {
      return;
    }
    setProjectThreadsByProjectID((current) => {
      if (current[activeProjectID] === cachedActiveProjectThreads) {
        return {
          ...current,
          [activeProjectID]: activeProjectThreadSnapshot,
        };
      }
      const merged = mergeSidebarThreadSnapshots(
        current[activeProjectID],
        activeProjectThreads,
      );
      if (threadListsEquivalent(current[activeProjectID], merged)) {
        return current;
      }
      return { ...current, [activeProjectID]: merged };
    });
  }, [
    activeProjectID,
    activeProjectThreads,
    activeProjectThreadSnapshot,
    cachedActiveProjectThreads,
  ]);

  useEffect(() => {
    if (activeContext?.kind !== "no_project") {
      return;
    }
    const activeScratchThreads = sortThreads(
      threads.filter(
        (thread) => !thread.ephemeral && isScratchThread(thread, projects),
      ),
    );
    const activeCwd = activeContext.cwd;
    setCachedScratchThreads((current) => {
      // The active workspace's list is authoritative for its own threads, but
      // this cache also feeds the cross-workspace session tab strip and the
      // global sidebar groups. Replacing it wholesale would drop every other
      // workspace's sessions the moment the user switches workspaces, which
      // regresses background tab labels to their stale snapshots (they fall
      // back to "未命名对话" until clicked). Preserve other workspaces'
      // entries and only reconcile the active workspace's slice.
      const otherWorkspaceThreads = current.filter(
        (thread) => !sameWorkdirPath(thread.cwd, activeCwd),
      );
      const next = sortThreads([
        ...otherWorkspaceThreads,
        ...activeScratchThreads,
      ]);
      return threadListsEquivalent(current, next) ? current : next;
    });
  }, [activeContext?.kind, projects, threads]);

  useEffect(() => {
    if (!backgroundLoadingEnabled || !window.wuu?.listAllThreads) return;
    let cancelled = false;
    void window.wuu.listAllThreads().then((listed) => {
      if (!cancelled) cacheSidebarThreads(listed.threads);
    }).catch((error) => {
      if (!cancelled) {
        setStatus(desktopApiErrorMessage(error, translateCurrent("project.threadsLoadFailed")));
      }
    });
    return () => { cancelled = true; };
  }, [projects, backgroundLoadingEnabled]);

  useEffect(() => {
    if (!backgroundLoadingEnabled) return;
    for (const project of projects) {
      if (!sessionTreeSectionExpanded(project.id, expandedSidebarSectionIDs)) {
        continue;
      }
      if (project.id === activeProjectID) {
        continue;
      }
      if (Object.prototype.hasOwnProperty.call(projectThreadsByProjectID, project.id)) {
        continue;
      }
      void loadProjectThreads(project);
    }
  }, [
    backgroundLoadingEnabled,
    activeProjectID,
    expandedSidebarSectionIDs,
    projectThreadsByProjectID,
    projects,
  ]);

  async function loadProjectThreads(project: DesktopProject): Promise<void> {
    if (loadingProjectThreadIDsRef.current.has(project.id)) {
      return;
    }
    loadingProjectThreadIDsRef.current.add(project.id);
    try {
      const listed = await window.wuu.listThreads(project.path);
      setProjectThreadsByProjectID((current) => ({
        ...current,
        [project.id]: threadsForDesktopProject(listed.threads, project),
      }));
    } catch (error) {
      setStatus(desktopApiErrorMessage(error, translateCurrent("project.threadsLoadFailed")));
    } finally {
      loadingProjectThreadIDsRef.current.delete(project.id);
    }
  }

  function cacheSidebarThreads(incoming: Thread[]): void {
    const scratchThreads = incoming.filter((thread) => isScratchThread(thread, projects));
    if (scratchThreads.length > 0) {
      setCachedScratchThreads((current) =>
        mergeSidebarThreadSnapshots(current, scratchThreads),
      );
    }
    setProjectThreadsByProjectID((current) => {
      let next = current;
      for (const project of projects) {
        const projectThreads = threadsForDesktopProject(incoming, project);
        if (projectThreads.length === 0) {
          continue;
        }
        if (next === current) {
          next = { ...current };
        }
        next[project.id] = mergeSidebarThreadSnapshots(
          current[project.id],
          projectThreads,
        );
      }
      return next;
    });
  }

  function updateCachedSidebarThread(thread: Thread): void {
    cacheSidebarThreads([thread]);
  }

  function updateCachedSidebarThreadPinned(threadID: string, pinned: boolean): void {
    const patch = (threads: Thread[]): Thread[] => {
      let changed = false;
      const next = threads.map((thread) => {
        if (thread.id !== threadID || thread.pinned === pinned) {
          return thread;
        }
        changed = true;
        return { ...thread, pinned };
      });
      return changed ? sortThreads(next) : threads;
    };
    setCachedScratchThreads(patch);
    setProjectThreadsByProjectID((current) => {
      let changed = false;
      const next: Record<string, Thread[]> = {};
      for (const [projectID, projectThreads] of Object.entries(current)) {
        const patched = patch(projectThreads);
        if (patched !== projectThreads) {
          changed = true;
        }
        next[projectID] = patched;
      }
      return changed ? next : current;
    });
  }

  function removeCachedSidebarThread(threadID: string): void {
    setCachedScratchThreads((current) =>
      current.filter((thread) => thread.id !== threadID),
    );
    setProjectThreadsByProjectID((current) => {
      let changed = false;
      const next: Record<string, Thread[]> = {};
      for (const [projectID, projectThreads] of Object.entries(current)) {
        const filtered = projectThreads.filter((thread) => thread.id !== threadID);
        if (filtered.length !== projectThreads.length) {
          changed = true;
        }
        next[projectID] = filtered;
      }
      return changed ? next : current;
    });
  }

  function syncSidebarServerEvent(event: ServerEvent): void {
    if (
      event.kind !== "notification" ||
      !SIDEBAR_THREAD_LIFECYCLE_METHODS.has(event.message.method)
    ) {
      return;
    }
    const params = event.message.params;
    const record = isRecord(params) ? params : undefined;
    const incomingThread = threadFromRecord(recordValue(record, "thread"));
    if (incomingThread) {
      updateCachedSidebarThread(incomingThread);
      return;
    }
    const threadID = typeof record?.thread_id === "string" ? record.thread_id : undefined;
    if (!threadID) {
      return;
    }
    const applyEvent = (current: Thread[]): Thread[] => {
      if (!current.some((thread) => thread.id === threadID)) {
        return current;
      }
      const next = reduceNotification(
        {
          ...initialState,
          activeContext: { kind: "no_project", cwd: event.workdir },
          threads: current,
        },
        event.message,
      ).threads;
      return next === current ? current : next;
    };
    setCachedScratchThreads(applyEvent);
    setProjectThreadsByProjectID((current) => {
      let changed = false;
      const next: Record<string, Thread[]> = {};
      for (const [projectID, projectThreads] of Object.entries(current)) {
        const synced = applyEvent(projectThreads);
        changed ||= synced !== projectThreads;
        next[projectID] = synced;
      }
      return changed ? next : current;
    });
  }

  function toggleSidebarSectionCollapsed(sectionID: string): void {
    // Fixed sections are pure manual sidebar sections:
    // expanded ⇔ !collapsedSidebarSectionIDs.has(id).
    if (
      sectionID === SIDEBAR_SECTION_PINNED ||
      sectionID === SIDEBAR_SECTION_COLLAB
    ) {
      setCollapsedSidebarSectionIDs((current) => {
        if (!current.has(sectionID)) {
          return new Set(current).add(sectionID);
        }
        const next = new Set(current);
        next.delete(sectionID);
        return next;
      });
      return;
    }
    // 对话 / project tree sections: expanded ⇔ the id is in the expanded
    // set. The collapse motion lives inside SidebarSection (it keeps the
    // body mounted while animating), so the state flip is immediate here.
    const expanded = sessionTreeSectionExpanded(
      sectionID,
      expandedSidebarSectionIDs,
    );
    if (!expanded) {
      setCollapsedSidebarSectionIDs((current) => {
        if (!current.has(sectionID)) {
          return current;
        }
        const next = new Set(current);
        next.delete(sectionID);
        return next;
      });
      setExpandedSidebarSectionIDs((current) =>
        current.has(sectionID) ? current : new Set(current).add(sectionID),
      );
      const project = projectsByID.get(sectionID);
      if (
        project &&
        !Object.prototype.hasOwnProperty.call(
          projectThreadsByProjectID,
          sectionID,
        )
      ) {
        void loadProjectThreads(project);
      }
      return;
    }
    setCollapsedSidebarSectionIDs((current) =>
      current.has(sectionID) ? current : new Set(current).add(sectionID),
    );
    setExpandedSidebarSectionIDs((current) => {
      if (!current.has(sectionID)) {
        return current;
      }
      const next = new Set(current);
      next.delete(sectionID);
      return next;
    });
  }

  return {
    collapsedSidebarSectionIDs,
    expandedSidebarSectionIDs,
    loadingProjectThreadIDs,
    projectThreadsByProjectID: visibleProjectThreadsByProjectID,
    cachedScratchThreads,
    sidebarSectionOrder,
    setSidebarSectionOrder,
    loadProjectThreads,
    cacheSidebarThreads,
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    syncSidebarServerEvent,
    toggleSidebarSectionCollapsed,
  };
}
