import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import type { ActivitySession, BrowserCommandParams, BrowserSurfaceSnapshot } from "../shared/protocol";
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

function render(props: {
  visible?: boolean;
  activity?: ActivitySession;
  onActivityTakeover?: () => void;
  onActivityRelease?: () => void;
  onActivityStop?: () => void;
}) {
  act(() => {
    root = createRoot(container);
    root!.render(
      <WorkspaceBrowserPanel
        visible={props.visible ?? true}
        threadID="thread-1"
        activeContext={{ kind: "no_project", cwd: "/repo" }}
        activity={props.activity}
        onActivityTakeover={props.onActivityTakeover}
        onActivityRelease={props.onActivityRelease}
        onActivityStop={props.onActivityStop}
      />
    );
  });
  return {
    input: container.querySelector(".workspace-browser-url-input") as HTMLInputElement | null
  };
}

function rerender(props: {
  visible?: boolean;
  activity?: ActivitySession;
  onActivityTakeover?: () => void;
  onActivityRelease?: () => void;
  onActivityStop?: () => void;
}) {
  act(() => {
    root!.render(
      <WorkspaceBrowserPanel
        visible={props.visible ?? true}
        threadID="thread-1"
        activeContext={{ kind: "no_project", cwd: "/repo" }}
        activity={props.activity}
        onActivityTakeover={props.onActivityTakeover}
        onActivityRelease={props.onActivityRelease}
        onActivityStop={props.onActivityStop}
      />
    );
  });
}

describe("WorkspaceBrowserPanel", () => {
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
    const takeover = vi.fn();
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
    const { input } = render({ visible: true, activity, onActivityTakeover: takeover });
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
    expect(container.textContent).toContain("Agent 控制");
    reportBrowserBounds.mockClear();
    rerender({ visible: false, activity, onActivityTakeover: takeover });
    expect(browserCommand).not.toHaveBeenCalled();
    expect(reportBrowserBounds).toHaveBeenCalledWith("/repo", "tab-live", null);
  });

  it("renders Activity control and takeover, release, and stop commands", () => {
    const takeover = vi.fn();
    const release = vi.fn();
    const stop = vi.fn();
    const activity: ActivitySession = {
      id: "activity-1",
      kind: "browser",
      thread_id: "thread-1",
      workdir: "/repo",
      state: "active",
      controller: "agent",
      created_at: "2026-07-10T10:00:00Z",
      updated_at: "2026-07-10T10:00:01Z",
    };
    render({ activity, onActivityTakeover: takeover, onActivityRelease: release, onActivityStop: stop });
    expect(container.textContent).toContain("Agent 控制");
    (container.querySelector('button[aria-label="接管浏览器"]') as HTMLButtonElement | null)?.click();
    (container.querySelector('button[aria-label="停止浏览器 Activity"]') as HTMLButtonElement | null)?.click();
    expect(takeover).toHaveBeenCalledTimes(1);
    expect(stop).toHaveBeenCalledTimes(1);

    rerender({
      activity: { ...activity, state: "user_controlled", controller: "user", updated_at: "2026-07-10T10:00:02Z" },
      onActivityTakeover: takeover,
      onActivityRelease: release,
      onActivityStop: stop,
    });
    expect(container.textContent).toContain("你正在控制");
    (container.querySelector('button[aria-label="交还浏览器给 Agent"]') as HTMLButtonElement | null)?.click();
    expect(release).toHaveBeenCalledTimes(1);
  });
});
