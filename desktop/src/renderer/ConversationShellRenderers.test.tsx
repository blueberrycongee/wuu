import * as React from "react";
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread } from "../shared/protocol";
import type { HeaderSnapshotV1 } from "../shared/workbench";
import {
  createThreadSessionTab,
  emptyComposerDraft,
  initialState,
  threadSessionTabID,
} from "./AppState";

vi.mock("./ConversationSplitPane", () => ({
  ConversationSplitPane: ({
    onOpenFile,
    thread,
  }: {
    onOpenFile?: (path: string) => void;
    thread: Thread;
  }) => (
    <button
      type="button"
      data-testid={`open-file-${thread.id}`}
      onClick={() => onOpenFile?.("src/App.tsx")}
    >
      open file
    </button>
  ),
}));

import {
  ConversationSplitPaneRenderer,
  ConversationTitleContent,
  ConversationTitleActions,
} from "./ConversationShellRenderers";
import { PluginHost } from "./plugins/PluginHost";
import { WorkbenchController } from "./plugins/Workbench";

let container: HTMLDivElement;
let root: Root | null = null;

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container?.remove();
});

function thread(id: string, cwd: string): Thread {
  return {
    id,
    preview: id,
    model_provider: "test",
    model: "test",
    cwd,
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

describe("ConversationSplitPaneRenderer file routing", () => {
  it("reports the pane thread together with the opened file path", () => {
    const secondaryThread = thread("secondary", "/repo/worktrees/secondary");
    const onOpenFile = vi.fn();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => {
      root?.render(
        <ConversationSplitPaneRenderer
          state={{ ...initialState, threads: [secondaryThread] }}
          thread={secondaryThread}
          pane="secondary"
          splitComposerDrafts={{
            primary: emptyComposerDraft(),
            secondary: emptyComposerDraft(),
          }}
          splitPaneRefs={{ current: { primary: null, secondary: null } }}
          viewSwitchPending={false}
          onActivatePane={() => {}}
          onClosePane={() => {}}
          onConversationScroll={() => {}}
          onSetPrompt={() => {}}
          onPasteAttachmentFiles={() => {}}
          onRemoveFile={() => {}}
          onRemoveImage={() => {}}
          onSend={() => {}}
          onInterrupt={() => {}}
          onForkMessage={() => {}}
          onOpenFile={onOpenFile}
          onOpenAgent={() => {}}
          canEditThreadMessage={() => false}
          onEditMessage={() => {}}
          onCancelEditMessage={() => {}}
          onSubmitEditMessage={() => {}}
          onStreamFrame={() => {}}
          onOpenFileDiff={() => {}}
        />,
      );
    });

    act(() => {
      container.querySelector<HTMLButtonElement>('[data-testid="open-file-secondary"]')?.click();
    });

    expect(onOpenFile).toHaveBeenCalledWith(secondaryThread, "src/App.tsx");
  });
});

describe("ConversationTitleContent presentation boundary", () => {
  it("keeps the new-conversation control before the title", () => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => root?.render(
      <ConversationTitleContent
        state={{
          ...initialState,
          activeContext: { kind: "project", project_id: "project-1", cwd: "/repo/project" },
        }}
        activeTitle="A very long conversation title that should not push the new-conversation control away"
        onStartNewThread={() => {}}
      />,
    ));

    const heading = container.querySelector(".conversation-title-heading");
    const button = heading?.querySelector<HTMLButtonElement>("button.session-tab-new");
    const title = heading?.querySelector("h1");
    expect(button).not.toBeNull();
    expect(title).not.toBeNull();
    expect(button!.compareDocumentPosition(title!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(button!.disabled).toBe(false);
  });

  it("replaces its native title root with the conversation header presenter", async () => {
    const pluginHost = new PluginHost({ react: React });
    const workbenchController = new WorkbenchController(pluginHost);
    let snapshot: HeaderSnapshotV1 | undefined;
    await pluginHost.activateGeneration({ pluginId: "conversation-header", generation: "one", register(api) {
      api.registerPresenter({ id: "conversation", target: "header.conversation", render: (props) => {
        snapshot = props.snapshot as HeaderSnapshotV1;
        return <div data-conversation-header>{snapshot.title}</div>;
      } });
    } });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => root?.render(
      <ConversationTitleContent
        state={initialState}
        activeTitle="Conversation"
        onStartNewThread={() => {}}
        pluginHost={pluginHost}
        workbenchController={workbenchController}
      />,
    ));

    expect(container.firstElementChild?.hasAttribute("data-conversation-header")).toBe(true);
    expect(container.querySelector("h1")).toBeNull();
    expect(snapshot).toEqual({ contractVersion: 1, scope: "conversation", title: "Conversation" });
  });

  it("stops presenting the conversation as busy when its terminal answer is ready", async () => {
    const pluginHost = new PluginHost({ react: React });
    const workbenchController = new WorkbenchController(pluginHost);
    let snapshot: HeaderSnapshotV1 | undefined;
    await pluginHost.activateGeneration({ pluginId: "conversation-header", generation: "one", register(api) {
      api.registerPresenter({ id: "conversation", target: "header.conversation", render: (props) => {
        snapshot = props.snapshot as HeaderSnapshotV1;
        return <div data-conversation-header />;
      } });
    } });
    const context = {
      kind: "project" as const,
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const answerReadyThread: Thread = {
      ...thread("answer-ready", context.cwd),
      status: "in_progress",
      turns: [{
        id: "turn-ready",
        status: "in_progress",
        items_view: "full",
        answer_ready_at: "2026-08-31T12:00:00Z",
        items: [{
          id: "terminal-answer",
          type: "agent_message",
          status: "completed",
          terminal: true,
          text: "done",
        }],
      }],
    };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => root?.render(
      <ConversationTitleContent
        state={{
          ...initialState,
          activeContext: context,
          thread: answerReadyThread,
          threads: [answerReadyThread],
          sessionTabs: [createThreadSessionTab(answerReadyThread, context)],
          activeSessionTabID: threadSessionTabID(answerReadyThread.id),
        }}
        runningThreadIDs={new Set([answerReadyThread.id])}
        activeTitle="Answer ready"
        onStartNewThread={() => {}}
        pluginHost={pluginHost}
        workbenchController={workbenchController}
      />,
    ));

    expect(snapshot?.busy).toBeUndefined();
  });

  function renderTitle(onRenameTitle?: (title: string) => void, title = "新建对话", editKey = "draft") {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const props = {
      state: {
        ...initialState,
        activeContext: { kind: "project" as const, project_id: "project-1", cwd: "/repo/project" },
      },
      activeTitle: title,
      onStartNewThread: () => {},
      onRenameTitle,
      titleEditKey: editKey,
    };
    act(() => root?.render(<ConversationTitleContent {...props} />));
    return props;
  }

  function setInputValue(input: HTMLInputElement, value: string): void {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }

  it("renames the conversation from the title", () => {
    const onRenameTitle = vi.fn();
    renderTitle(onRenameTitle);
    act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")?.click());
    const input = container.querySelector<HTMLInputElement>(".conversation-title-edit");
    expect(input?.value).toBe("新建对话");
    act(() => {
      setInputValue(input!, "  发布说明  ");
      input!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(onRenameTitle).toHaveBeenCalledWith("发布说明");
    expect(container.querySelector(".conversation-title-edit")).toBeNull();
    expect(container.querySelector(".conversation-title-rename")?.textContent).toBe("新建对话");
  });

  it("leaves the title unchanged on Escape, a blank name, or the same text", () => {
    const onRenameTitle = vi.fn();
    renderTitle(onRenameTitle, "发布说明");
    const open = () => act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")?.click());
    open();
    let input = container.querySelector<HTMLInputElement>(".conversation-title-edit")!;
    act(() => {
      setInputValue(input, "临时标题");
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(onRenameTitle).not.toHaveBeenCalled();
    expect(container.querySelector(".conversation-title-rename")?.textContent).toBe("发布说明");

    open();
    input = container.querySelector<HTMLInputElement>(".conversation-title-edit")!;
    act(() => {
      setInputValue(input, "   ");
      input.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
    });
    expect(onRenameTitle).not.toHaveBeenCalled();

    open();
    input = container.querySelector<HTMLInputElement>(".conversation-title-edit")!;
    act(() => {
      setInputValue(input, "发布说明");
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(onRenameTitle).not.toHaveBeenCalled();
  });

  it("does not commit an in-progress edit when Enter is composing", () => {
    const onRenameTitle = vi.fn();
    renderTitle(onRenameTitle);
    act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")?.click());
    const input = container.querySelector<HTMLInputElement>(".conversation-title-edit")!;
    act(() => {
      setInputValue(input, "发布说明");
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, isComposing: true }));
    });
    expect(onRenameTitle).not.toHaveBeenCalled();
    expect(container.querySelector(".conversation-title-edit")).not.toBeNull();
  });

  it("drops an open edit when the conversation changes", () => {
    const onRenameTitle = vi.fn();
    const props = renderTitle(onRenameTitle, "第一会话题", "one");
    act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")?.click());
    act(() => {
      setInputValue(container.querySelector<HTMLInputElement>(".conversation-title-edit")!, "未保存");
    });
    act(() => root?.render(
      <ConversationTitleContent {...props} activeTitle="第二会话题" titleEditKey="two" />,
    ));
    expect(onRenameTitle).not.toHaveBeenCalled();
    expect(container.querySelector(".conversation-title-edit")).toBeNull();
    expect(container.querySelector(".conversation-title-rename")?.textContent).toBe("第二会话题");
  });
});

describe("compact conversation actions", () => {
  function setup(activeContext = initialState.activeContext) {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const props = {
      state: { ...initialState, activeContext },
      compactNavigation: true,
      onStartNewThread: vi.fn(),
      environmentToggleRef: createRef<HTMLButtonElement>(),
      environmentPanelVisible: false,
      onToggleEnvironmentPanel: vi.fn(),
      rightPanelOpen: false,
      onToggleRightPanel: vi.fn(),
    };
    act(() => root?.render(<ConversationTitleActions {...props} />));
    return props;
  }
  const menuItems = () => Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
  function key(target: Element, value: string) {
    act(() => { target.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true })); });
  }

  it("starts a conversation directly and disables creation without a context", () => {
    const props = setup({ kind: "project", project_id: "project-1", cwd: "/repo/project" });
    act(() => container.querySelector<HTMLButtonElement>("button")!.click());
    expect(props.onStartNewThread).toHaveBeenCalledOnce();
    act(() => root?.render(<ConversationTitleActions {...props} state={initialState} />));
    expect(container.querySelector<HTMLButtonElement>("button")!.disabled).toBe(true);
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });

  it("opens secondary actions with keyboard navigation and restores focus on Escape", () => {
    const props = setup();
    const trigger = props.environmentToggleRef.current!;
    key(trigger, "ArrowDown");
    expect(document.activeElement).toBe(menuItems()[0]);
    key(menuItems()[0], "ArrowUp");
    expect(document.activeElement).toBe(menuItems()[1]);
    key(menuItems()[1], "Home");
    expect(document.activeElement).toBe(menuItems()[0]);
    key(menuItems()[0], "Escape");
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    key(trigger, "ArrowUp");
    expect(document.activeElement).toBe(menuItems()[1]);
  });

  it("runs panel actions once and closes the menu", () => {
    const props = setup();
    act(() => props.environmentToggleRef.current!.click());
    act(() => menuItems()[0].click());
    expect(props.onToggleEnvironmentPanel).toHaveBeenCalledOnce();
    expect(document.querySelector('[role="menu"]')).toBeNull();
    act(() => props.environmentToggleRef.current!.click());
    act(() => menuItems()[1].click());
    expect(props.onToggleRightPanel).toHaveBeenCalledOnce();
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });

  it("dismisses on outside interaction and removes the menu when the layout widens", () => {
    const props = setup();
    act(() => props.environmentToggleRef.current!.click());
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector('[role="menu"]')).toBeNull();
    act(() => props.environmentToggleRef.current!.click());
    act(() => root?.render(<ConversationTitleActions {...props} compactNavigation={false} />));
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(container.querySelectorAll("button")).toHaveLength(2);
  });
});
