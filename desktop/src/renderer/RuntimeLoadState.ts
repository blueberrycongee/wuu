import type {
  PopOutInitResult,
  ProjectListResult,
  RuntimeContext,
  ThreadResumeResult,
  InitializeResult,
  Thread,
} from "../shared/protocol";
import {
  activeWorkspaceID,
  reconcileListedThreadState,
  createDraftSessionTab,
  createThreadSessionTab,
  isThreadRunning,
  reconcileResumedThreadTurns,
  requireThread,
  sortThreads,
  upsertThread,
  type AppState,
} from "./AppState";
import type { QueuedComposerMessage } from "./ComposerMessages";
import { heldComposerMessagesFromResumeResult } from "./ComposerPendingState";
import { applyDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { translateCurrent } from "./i18n";

export type LoadedRuntimeState = Partial<AppState> & {
  heldComposerMessages?: QueuedComposerMessage[];
};

function runtimeConfiguration(
  workspaceState: ProjectListResult,
  initialized: InitializeResult,
): Partial<AppState> {
  return {
    initialized,
    projects: workspaceState.projects,
    activeContext: workspaceState.active_context,
    activeProjectId: activeWorkspaceID(workspaceState.active_context),
    gitStatus: undefined,
    status:
      initialized.status === "needs_setup"
        ? (initialized.issues?.[0]?.message ?? translateCurrent("runtime.configureCredentials"))
        : "ready",
  };
}

export async function loadRuntimeConfiguration(
  workspaceState: ProjectListResult,
): Promise<Partial<AppState>> {
  if (!workspaceState.active_context) {
    return emptyRuntimeState(workspaceState);
  }
  if (workspaceState.runtime_issue?.code === "active_project_unavailable") {
    return unavailableWorkspaceRuntimeState(workspaceState);
  }
  return runtimeConfiguration(workspaceState, await window.wuu.initialize());
}

export async function loadRuntimeThreadList(cwd?: string): Promise<Thread[]> {
  const [listed, archived] = await Promise.all([
    window.wuu.listThreads(cwd),
    window.wuu.listArchivedThreads(),
  ]);
  // Settings → Archive reads this same catalog, including other workspaces.
  return sortThreads([...listed.threads, ...archived.threads]);
}

export async function loadThreadListRefresh(state: AppState): Promise<Thread[]> {
  const listed = await window.wuu.listThreads(state.activeContext?.cwd);
  const runningHistoryIDs = new Set(
    [state.thread, state.secondaryThread, ...state.threads]
      .filter((thread): thread is Thread => Boolean(thread?.turns.some(turn => turn.status === "in_progress")))
      .map(thread => thread.id),
  );
  return Promise.all(listed.threads.map(async thread => {
    if (thread.turns.length > 0 || isThreadRunning(thread) || !runningHistoryIDs.has(thread.id)) {
      return thread;
    }
    // An idle summary cannot settle cached turns or recover terminal items and
    // errors. Fetch only histories that need repair, not the whole catalog.
    try {
      return (await window.wuu.resumeThread(thread.id)).thread ?? thread;
    } catch {
      // Keep the cached history; the next refresh retries this thread without
      // blocking discovery or successful repairs for other conversations.
      return thread;
    }
  }));
}

export async function loadRuntime(
  workspaceState: ProjectListResult,
  options: {
    resumeLatestThread?: boolean;
  } = {},
): Promise<LoadedRuntimeState> {
  if (!workspaceState.active_context) {
    return emptyRuntimeState(workspaceState);
  }
  if (workspaceState.runtime_issue?.code === "active_project_unavailable") {
    return unavailableWorkspaceRuntimeState(workspaceState);
  }
  const resumeLatestThread = options.resumeLatestThread ?? true;
  const [initialized, listedThreads] = await Promise.all([
    window.wuu.initialize(),
    loadRuntimeThreadList(),
  ]);
  // Archived conversations ride along in listedThreads for the Settings →
  // Archive page, but they are put away — a context switch must never
  // resurrect one into the composer. Resume the most recent live thread:
  // unpinned first (pinning marks a thread as parked, not as the place to
  // land), falling back to a pinned one when nothing else exists.
  const liveThreads = listedThreads.filter((candidate) => !candidate.archived);
  const defaultThread = resumeLatestThread
    ? (liveThreads.find((candidate) => !candidate.pinned) ?? liveThreads[0])
    : undefined;
  const resumed: ThreadResumeResult | undefined = defaultThread
    ? await window.wuu.resumeThread(defaultThread.id)
    : undefined;
  const thread = resumed
    ? requireThread(resumed, translateCurrent("thread.resumeMissing"))
    : undefined;
  return {
    ...runtimeConfiguration(workspaceState, initialized),
    initialized: thread ? initialized : applyDraftRuntimeMemory(initialized),
    thread,
    secondaryThread: undefined,
    activePane: "primary",
    allowThreadAutoActivation: Boolean(thread),
    threads: thread ? upsertThread(listedThreads, thread) : listedThreads,
    running: isThreadRunning(thread),
    // The resume RPC result carries the authoritative held snapshot. Seeding
    // it here makes queued/steered messages survive a renderer reload: the
    // `thread/resumed` notification is emitted before the app finishes
    // booting, when the active-context gate still drops every server event.
    heldComposerMessages: heldComposerMessagesFromResumeResult(resumed),
  };
}

export async function loadPopOutRuntime(
  init: PopOutInitResult,
): Promise<LoadedRuntimeState> {
  if (!init.kind || !init.context) {
    return { status: "no-runtime" };
  }
  if (init.kind === "draft") {
    const [listedWorkspaces, initialized, listed, archived] = await Promise.all([
      window.wuu.listProjects(),
      window.wuu.initialize(),
      window.wuu.listThreads(),
      window.wuu.listArchivedThreads(),
    ]);
    const listedThreads = sortThreads([...listed.threads, ...archived.threads]);
    const tab = createDraftSessionTab("draft:pop-out", init.context);
    return {
      initialized: applyDraftRuntimeMemory(initialized),
      projects: listedWorkspaces.projects,
      activeContext: init.context,
      activeProjectId: activeWorkspaceID(init.context),
      gitStatus: undefined,
      thread: undefined,
      secondaryThread: undefined,
      activePane: "primary",
      allowThreadAutoActivation: false,
      sessionTabs: [tab],
      activeSessionTabID: tab.id,
      threads: listedThreads,
      running: false,
      status: "ready",
    };
  }
  if (!init.threadID) {
    return { status: "no-runtime" };
  }
  const [listedWorkspaces, initialized, listed, archived, resumed] =
    await Promise.all([
      window.wuu.listProjects(),
      window.wuu.initialize(),
      window.wuu.listThreads(),
      window.wuu.listArchivedThreads(),
      window.wuu.resumeThread(init.threadID),
    ]);
  const listedThreads = sortThreads([...listed.threads, ...archived.threads]);
  const thread = reconcileResumedThreadTurns(
    requireThread(resumed, translateCurrent("thread.resumeMissing")),
    listedThreads.find((item) => item.id === init.threadID),
  );
  const tab = createThreadSessionTab(thread, init.context);
  return {
    initialized,
    projects: listedWorkspaces.projects,
    activeContext: init.context,
    activeProjectId: activeWorkspaceID(init.context),
    gitStatus: undefined,
    thread,
    secondaryThread: undefined,
    activePane: "primary",
    allowThreadAutoActivation: true,
    sessionTabs: [tab],
    activeSessionTabID: tab.id,
    threads: upsertThread(listedThreads, thread),
    running: isThreadRunning(thread),
    // Same reload-survival seeding as loadRuntime: the pop-out window must
    // restore its held snapshot from the RPC result, not the notification.
    heldComposerMessages: heldComposerMessagesFromResumeResult(resumed),
    status: "ready",
  };
}

export function emptyRuntimeState(
  workspaceState: ProjectListResult,
): Partial<AppState> {
  return {
    initialized: undefined,
    projects: workspaceState.projects,
    activeContext: undefined,
    activeProjectId: undefined,
    gitStatus: undefined,
    thread: undefined,
    secondaryThread: undefined,
    activePane: "primary",
    allowThreadAutoActivation: false,
    threads: [],
    running: false,
    status: "no-runtime",
  };
}

function unavailableWorkspaceRuntimeState(
  workspaceState: ProjectListResult,
): Partial<AppState> {
  return {
    ...emptyRuntimeState(workspaceState),
    activeContext: workspaceState.active_context,
    activeProjectId: activeWorkspaceID(workspaceState.active_context),
    status:
      workspaceState.runtime_issue?.message ?? translateCurrent("runtime.workspaceUnavailable"),
  };
}

export async function selectRuntimeContext(
  context: RuntimeContext,
): Promise<ProjectListResult> {
  if (context.kind === "project") {
    return window.wuu.selectProject(context.project_id);
  }
  return window.wuu.selectNoProject(false, context.cwd);
}


export type RuntimeRestoreSnapshot = {
  projects: ProjectListResult;
  initialized: InitializeResult;
  threads: Thread[];
  resumed: ThreadResumeResult[];
};

/** Refresh the server-owned portion of an existing workbench. Drafts and
 * pane selection stay in the renderer while opened threads regain history. */
export async function loadRuntimeRestore(state: AppState): Promise<RuntimeRestoreSnapshot> {
  const projects = await window.wuu.listProjects();
  const [initialized, listed, archived] = await Promise.all([
    window.wuu.initialize(),
    window.wuu.listAllThreads(),
    window.wuu.listArchivedThreads(),
  ]);
  const threads = [...listed.threads, ...archived.threads];
  const available = new Set(threads.flatMap((thread) => [thread.id, ...(thread.child_agents ?? []).map((agent) => agent.id)]));
  const openIDs = new Set([
    state.thread?.id,
    state.secondaryThread?.id,
    // Remote tabs recover when selected; foreground history must not queue
    // behind downloads for every background tab after a mobile reconnect.
    ...(window.wuu.loadEarlierThreadHistory ? [] : state.sessionTabs.flatMap((tab) => tab.kind === "thread" ? [tab.threadID] : [])),
  ].filter((id): id is string => Boolean(id && available.has(id))));
  const resumed = await Promise.all([...openIDs].map(async (id) => {
    const result = await window.wuu.resumeThread(id);
    // Synchronize the streaming text buffer at the response boundary, before
    // subsequent deltas append to a value cached before the disconnect.
    requireThread(result, translateCurrent("thread.resumeMissing"));
    return result;
  }));
  const byID = new Map(resumed.map((result) => [result.thread.id, result.thread]));
  for (const thread of threads) if (!byID.has(thread.id)) byID.set(thread.id, thread);
  // Child sessions are listed under their parent, but their open tabs must
  // survive even when background history is deliberately not resumed yet.
  for (const thread of state.threads) if (available.has(thread.id) && !byID.has(thread.id)) byID.set(thread.id, thread);
  return { projects, initialized, threads: [...byID.values()], resumed };
}

export function applyRuntimeRestore(state: AppState, snapshot: RuntimeRestoreSnapshot): AppState {
  const available = new Set(snapshot.threads.map((thread) => thread.id));
  const sessionTabs = state.sessionTabs.filter((tab) => tab.kind !== "thread" || available.has(tab.threadID));
  const current = {
    ...state,
    // The snapshot includes archives; stale local archive copies must not
    // override an unarchive or deletion performed while disconnected.
    threads: state.threads.filter((thread) => !thread.archived),
    thread: state.thread && available.has(state.thread.id) ? state.thread : undefined,
    secondaryThread: state.secondaryThread && available.has(state.secondaryThread.id) ? state.secondaryThread : undefined,
    sessionTabs,
    activeSessionTabID: sessionTabs.some((tab) => tab.id === state.activeSessionTabID)
      ? state.activeSessionTabID : sessionTabs[0]?.id ?? "",
  };
  return {
    ...reconcileListedThreadState(current, snapshot.threads),
    initialized: snapshot.initialized,
    projects: snapshot.projects.projects,
    activeContext: snapshot.projects.active_context,
    activeProjectId: snapshot.projects.active_project_id,
  };
}
