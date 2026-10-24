import { describe, expect, it, vi } from "vitest";
import type { InitializeResult, RuntimeContext, Thread } from "../shared/protocol";
import {
  captureComposerDrafts,
  createThreadSessionTab,
  emptyComposerDraft,
  initialState,
  threadSessionTabID,
  type AppState,
} from "./AppState";
import { createConversationPaneActions } from "./ConversationPaneActions";

function projectContext(): RuntimeContext {
  return { kind: "project", project_id: "project-1", cwd: "/tmp/project-1" };
}

function initialized(): InitializeResult {
  return {
    protocol_version: "1",
    provider: "codex",
    model: "gpt-5",
    workspace_root: "/tmp/project-1",
  };
}

function thread(id: string, status: Thread["status"] = "idle"): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "codex",
    model: "gpt-5",
    cwd: "/tmp/project-1",
    status,
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

function buildActions(initial: AppState = {
  ...initialState,
  activeContext: projectContext(),
  initialized: initialized(),
  status: "ready",
}) {
  let appState = initial;
  const moveSplitDraftToGlobalComposer = vi.fn();
  const actions = createConversationPaneActions({
    getComposerDraftSnapshot: () => captureComposerDrafts(appState, emptyComposerDraft()),
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    moveSplitDraftToGlobalComposer,
  });
  return {
    actions,
    getAppState: () => appState,
    moveSplitDraftToGlobalComposer,
  };
}

describe("createConversationPaneActions", () => {
  it("activates the secondary pane when it has a thread", () => {
    const secondary = thread("secondary", "in_progress");
    const harness = buildActions({
      ...initialState,
      activeContext: projectContext(),
      initialized: initialized(),
      thread: thread("primary"),
      secondaryThread: secondary,
      activePane: "primary",
    });

    harness.actions.activateConversationPane("secondary");

    expect(harness.getAppState().activePane).toBe("secondary");
    expect(harness.getAppState().activeSessionTabID).toBe(
      threadSessionTabID("secondary"),
    );
    expect(harness.getAppState().running).toBe(true);
  });

  it("keeps primary active when trying to activate an empty secondary pane", () => {
    const harness = buildActions({
      ...initialState,
      activeContext: projectContext(),
      initialized: initialized(),
      thread: thread("primary"),
      activePane: "primary",
    });

    harness.actions.activateConversationPane("secondary");

    expect(harness.getAppState().activePane).toBe("primary");
  });

  it("closes the secondary pane and restores the primary thread tab", () => {
    const harness = buildActions({
      ...initialState,
      activeContext: projectContext(),
      initialized: initialized(),
      thread: thread("primary"),
      secondaryThread: thread("secondary"),
      activePane: "secondary",
    });

    harness.actions.closeConversationPane("secondary");

    expect(harness.moveSplitDraftToGlobalComposer).toHaveBeenCalledWith("primary");
    expect(harness.getAppState().secondaryThread).toBeUndefined();
    expect(harness.getAppState().activePane).toBe("primary");
    expect(harness.getAppState().activeSessionTabID).toBe(
      threadSessionTabID("primary"),
    );
  });

  it("promotes the secondary thread when closing the primary pane", () => {
    const harness = buildActions({
      ...initialState,
      activeContext: projectContext(),
      initialized: initialized(),
      thread: thread("primary"),
      secondaryThread: thread("secondary"),
      activePane: "primary",
    });

    harness.actions.closeConversationPane("primary");

    expect(harness.moveSplitDraftToGlobalComposer).toHaveBeenCalledWith("secondary");
    expect(harness.getAppState().thread?.id).toBe("secondary");
    expect(harness.getAppState().secondaryThread).toBeUndefined();
  });

  it("opens a fresh draft tab when closing the only primary pane", () => {
    const context = projectContext();
    const primary = thread("primary");
    const harness = buildActions({
      ...initialState,
      activeContext: context,
      initialized: initialized(),
      thread: primary,
      sessionTabs: [createThreadSessionTab(primary, context)],
      activeSessionTabID: threadSessionTabID("primary"),
      activePane: "primary",
    });

    harness.actions.closeConversationPane("primary");

    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toContain("draft:closed:");
    expect(harness.getAppState().sessionTabs.at(-1)?.kind).toBe("draft");
  });
});
