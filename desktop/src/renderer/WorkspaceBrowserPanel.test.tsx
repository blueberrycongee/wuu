import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import type { ActivitySession, BrowserDockTarget, BrowserCommandParams, BrowserSurfaceSnapshot } from "../shared/protocol";
import {
  requestWorkspaceBrowserNavigation,
  resetWorkspaceBrowserNavigationForTests,
} from "./WorkspaceBrowserNavigation";

let container: HTMLDivElement;
let root: Root | null = null;
const browserCommand = vi.fn(async (params: BrowserCommandParams): Promise<BrowserSurfaceSnapshot> => ({
  workdir: params.workdir,
  tabID: params.tabID,
  url: params.url ?? "https://example.com/current",
  title: "Example",
  canGoBack: false,
  canGoForward: false,
  loading: false,
}));
const reportBrowserBounds = vi.fn();
const surfaceHandlers: Array<(snapshot: BrowserSurfaceSnapshot) => void> = [];

beforeEach(() => {
  browserCommand.mockClear();
  reportBrowserBounds.mockClear();
  surfaceHandlers.length = 0;
  (window as unknown as { wuu: unknown }).wuu = {
    browserCommand,
    browserSurface: vi.fn(async () => null),
    reportBrowserBounds,
    suppressBrowserOverlay: vi.fn(),
    onBrowserSurface: (handler: (snapshot: BrowserSurfaceSnapshot) => void) => {
      surfaceHandlers.push(handler);
      return () => undefined;
    },
    onBrowserUserInput: vi.fn(() => () => undefined),
    onBrowserTabAdopted: vi.fn(() => () => undefined),
    openExternal: vi.fn(),
  };
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  if (container.parentNode) container.remove();
  resetWorkspaceBrowserNavigationForTests();
  vi.restoreAllMocks();
});

type PanelProps = {
  visible?: boolean;
  threadID?: string;
  dockTarget?: BrowserDockTarget;
  activity?: ActivitySession;
  onUserInteraction?: () => void | Promise<void>;
};

function render(props: PanelProps) {
  root = createRoot(container);
  rerender(props);
  return {
    input: container.querySelector(".workspace-browser-url-input") as HTMLInputElement | null
  };
}

function rerender(props: PanelProps) {
  act(() => {
    root!.render(
      <WorkspaceBrowserPanel
        visible={props.visible ?? true}
        threadID={props.threadID ?? "thread-1"}
        dockTarget={props.dockTarget}
        activeContext={{ kind: "no_project", cwd: "/repo" }}
        activity={props.activity}
        onUserInteraction={props.onUserInteraction}
      />
    );
  });
}

describe("WorkspaceBrowserPanel", () => {
  it("opens the exact retained preview tab even after its activity stopped", async () => {
    const target = { thread_id: "thread-1", workdir: "/repo", tabID: "retained-preview" };
    vi.mocked(window.wuu.browserSurface!).mockResolvedValue({
      workdir: "/repo", tabID: target.tabID, url: "https://example.com/dashboard",
      title: "Dashboard", loading: false, canGoBack: false, canGoForward: false,
    });
    await act(async () => { render({ dockTarget: target }); });
    expect(window.wuu.browserSurface).toHaveBeenCalledWith("/repo", "retained-preview");
    expect(container.querySelector<HTMLInputElement>(".workspace-browser-url-input")?.value)
      .toBe("https://example.com/dashboard");
    expect(browserCommand).not.toHaveBeenCalled();
  });

  it("clears an old page and reports a closed preview instead of falling back", async () => {
    const { input } = render({});
    act(() => surfaceHandlers[0]?.({
      workdir: "/repo", tabID: "user:thread-1", url: "https://example.com/old",
      title: "Old page", loading: false, canGoBack: true, canGoForward: false,
    }));
    expect(input?.value).toBe("https://example.com/old");
    await act(async () => { rerender({ dockTarget: {
      thread_id: "thread-1", workdir: "/repo", tabID: "closed-preview",
    } }); });
    expect(window.wuu.browserSurface).toHaveBeenLastCalledWith("/repo", "closed-preview");
    expect(input?.value).toBe("");
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(container.textContent).not.toContain("Old page");
    expect(browserCommand).not.toHaveBeenCalled();
  });

  it("drops an adopted popup and late snapshots when switching sessions", async () => {
    let finishOldRead!: (snapshot: BrowserSurfaceSnapshot) => void;
    vi.mocked(window.wuu.browserSurface!).mockImplementationOnce(() => new Promise((resolve) => { finishOldRead = resolve; }));
    render({});
    const adopt = vi.mocked(window.wuu.onBrowserTabAdopted!).mock.calls[0][0];
    await act(async () => { adopt({ workdir: "/repo", openerTabID: "user:thread-1", tabID: "popup-1", url: "https://example.com/popup" }); });
    expect(window.wuu.browserSurface).toHaveBeenLastCalledWith("/repo", "popup-1");
    await act(async () => { rerender({ threadID: "thread-2" }); });
    expect(window.wuu.browserSurface).toHaveBeenLastCalledWith("/repo", "user:thread-2");
    await act(async () => { finishOldRead({
      workdir: "/repo", tabID: "user:thread-1", url: "https://example.com/late",
      title: "Old page", loading: false, canGoBack: true, canGoForward: false,
    }); });
    expect(container.querySelector<HTMLInputElement>(".workspace-browser-url-input")?.value).toBe("");
    expect(container.textContent).not.toContain("Old page");
  });

  it("navigates only after the user submits an address", async () => {
    const { input } = render({});
    const form = container.querySelector<HTMLFormElement>(".workspace-browser-url-form");
    expect(input).not.toBeNull();
    expect(form).not.toBeNull();

    act(() => {
      if (input) {
        const valueSetter = Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )?.set;
        valueSetter?.call(input, "http://app.local:3000");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    await act(async () => {
      form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    expect(browserCommand).toHaveBeenCalledWith({
      workdir: "/repo",
      tabID: "user:thread-1",
      command: "navigate",
      url: "http://app.local:3000",
    });
  });

  it("shows the empty browsing surface before a URL is submitted", () => {
    render({});
    expect(container.querySelector(".workspace-browser-home")?.textContent).toContain("开始浏览");
    expect(container.querySelector(".workspace-browser-home")?.textContent).toContain("输入 URL 以打开页面");
    expect(
      container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")?.disabled,
    ).toBe(true);
  });

  it("navigates when the conversation requests a URL", async () => {
    render({ visible: true });
    browserCommand.mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    await act(async () => {
      root!.render(
        <WorkspaceBrowserPanel
          visible
          threadID="thread-1"
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(browserCommand).toHaveBeenCalledWith({
      workdir: "/repo",
      tabID: "user:thread-1",
      command: "navigate",
      url: "https://docs.example.com/api",
    });
  });

  it("keeps a navigation request until the panel is visible", async () => {
    render({ visible: true });
    browserCommand.mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    act(() => {
      root!.render(
        <WorkspaceBrowserPanel
          visible={false}
          threadID="thread-1"
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(browserCommand).not.toHaveBeenCalled();
    await act(async () => {
      root!.render(
        <WorkspaceBrowserPanel
          visible
          threadID="thread-1"
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(browserCommand).toHaveBeenCalledWith(expect.objectContaining({
      command: "navigate",
      url: "https://docs.example.com/api",
    }));
  });

  it("shows the agent tab in the address bar and parks it when the panel closes", () => {
    const activity: ActivitySession = {
      id: "activity-1",
      kind: "browser",
      thread_id: "thread-1",
      workdir: "/repo",
      target: "tab-live",
      state: "background_controlled",
      controller: "agent",
      created_at: "2026-07-10T10:00:00Z",
      updated_at: "2026-07-10T10:00:01Z",
    };
    const { input } = render({ visible: true, activity });
    act(() => {
      surfaceHandlers[0]?.({
        workdir: "/repo",
        tabID: "tab-live",
        url: "https://example.com/pelican",
        title: "Pelican",
        canGoBack: true,
        canGoForward: false,
        loading: false,
      });
    });
    expect(input?.value).toBe("https://example.com/pelican");
    expect(container.querySelector(".workspace-browser-home")).toBeNull();
    reportBrowserBounds.mockClear();
    rerender({ visible: false, activity });
    expect(browserCommand).not.toHaveBeenCalled();
    expect(reportBrowserBounds).toHaveBeenCalledWith("/repo", "tab-live", null);
  });

  it("pauses for direct input only on the displayed agent tab and waits before navigating", async () => {
    let finishPause!: () => void;
    const paused = new Promise<void>((resolve) => { finishPause = resolve; });
    const onUserInteraction = vi.fn(() => paused);
    const activity: ActivitySession = {
      id: "activity-1",
      kind: "browser",
      thread_id: "thread-1",
      workdir: "/repo",
      state: "active",
      controller: "agent",
      target: "tab-live",
      created_at: "2026-07-10T10:00:00Z",
      updated_at: "2026-07-10T10:00:01Z",
    };
    render({ activity, onUserInteraction });
    const userInput = () => vi.mocked(window.wuu.onBrowserUserInput!).mock.calls.at(-1)![0];
    act(() => {
      userInput()({ workdir: "/other", tabID: "tab-live" });
      userInput()({ workdir: "/repo", tabID: "another-tab" });
    });
    expect(onUserInteraction).not.toHaveBeenCalled();
    act(() => { userInput()({ workdir: "/repo", tabID: "tab-live" }); });
    expect(onUserInteraction).toHaveBeenCalledTimes(1);
    onUserInteraction.mockClear();
    act(() => {
      surfaceHandlers.at(-1)!({
        workdir: "/repo", tabID: "tab-live", url: "https://example.com",
        title: "Example", loading: false, canGoBack: true, canGoForward: false,
      });
    });
    act(() => { container.querySelector<HTMLButtonElement>(".workspace-browser-nav")!.click(); });
    expect(onUserInteraction).toHaveBeenCalledTimes(1);
    expect(browserCommand).not.toHaveBeenCalled();
    await act(async () => { finishPause(); await paused; });
    expect(browserCommand).toHaveBeenCalledWith({ workdir: "/repo", tabID: "tab-live", command: "back", url: undefined });
    onUserInteraction.mockClear();
    await act(async () => {
      rerender({ activity: { ...activity, controller: "user", state: "user_controlled" }, onUserInteraction });
    });
    act(() => { userInput()({ workdir: "/repo", tabID: "tab-live" }); });
    expect(onUserInteraction).toHaveBeenCalledTimes(1);
    onUserInteraction.mockClear();
    await act(async () => {
      rerender({
        activity,
        dockTarget: { thread_id: "thread-1", workdir: "/repo", tabID: "retained-preview" },
        onUserInteraction,
      });
    });
    act(() => { userInput()({ workdir: "/repo", tabID: "retained-preview" }); });
    expect(onUserInteraction).not.toHaveBeenCalled();
  });
});
