import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { BrowserControlsProvider } from "./BrowserControlsContext";
import { scopeApplicationBrowser } from "../../../packages/plugin-sdk/src/ui";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import { ApplicationBrowser } from "./plugins/ApplicationBrowser";
import { showActionErrorToast } from "./Toast";
vi.mock("./Toast", () => ({ showActionErrorToast: vi.fn(), toastErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import type { ActivitySession, BrowserDockTarget, BrowserCommandParams, BrowserSurfaceSnapshot } from "../shared/protocol";
import {
  requestWorkspaceBrowserNavigation,
  resetWorkspaceBrowserNavigationForTests,
} from "./WorkspaceBrowserNavigation";

let container: HTMLDivElement;
let root: Root | null = null;
let browser: ApplicationBrowser;
let interaction: ((generation?: number) => void | Promise<void>) | undefined;
let currentActivity: ActivitySession | undefined;
function renderElement(element: JSX.Element) {
  root!.render(<BrowserControlsProvider service={browser}>{element}</BrowserControlsProvider>);
}
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
  vi.mocked(showActionErrorToast).mockClear();
  reportBrowserBounds.mockClear();
  surfaceHandlers.length = 0;
  (window as unknown as { wuu: unknown }).wuu = {
    browserCommand,
    takeoverActivity: vi.fn(async (_thread, _activity, generation) => {
      await interaction?.(generation);
      return { activity: currentActivity! };
    }),
    browserSurface: vi.fn(async () => null),
    reportBrowserBounds,
    suppressBrowserOverlay: vi.fn(),
    onBrowserSurface: (handler: (snapshot: BrowserSurfaceSnapshot) => void) => {
      surfaceHandlers.push(handler);
      return () => undefined;
    },
    onBrowserDock: vi.fn(() => () => undefined),
    onBrowserInvalidate: vi.fn(() => () => undefined),
    onBrowserUserInput: vi.fn(() => () => undefined),
    onBrowserTabAdopted: vi.fn(() => () => undefined),
    openExternal: vi.fn(),
  };
  interaction = undefined;
  currentActivity = undefined;
  browser = new ApplicationBrowser({ api: window.wuu, onActivityChanged: vi.fn() });
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  browser.dispose();
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
  interaction = props.onUserInteraction;
  currentActivity = props.activity;
  act(() => {
    renderElement(
      <WorkspaceBrowserPanel
        visible={props.visible ?? true}
        threadID={props.threadID ?? "thread-1"}
        dockTarget={props.dockTarget}
        activeContext={{ kind: "no_project", cwd: "/repo" }}
        activity={props.activity}
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
      threadID: "thread-1",
      command: "navigate",
      url: "http://app.local:3000",
    });
  });

  it.each([
    ["localhost:3000", "http://localhost:3000/"],
    ["127.0.0.1:3000", "http://127.0.0.1:3000/"],
    ["localhost:3000?view=preview", "http://localhost:3000/?view=preview"],
    ["example.com?foo=bar", "https://example.com/?foo=bar"],
    ["example.com#section", "https://example.com/#section"],
    ["find example", "https://www.google.com/search?igu=1&q=find%20example"],
  ])("navigates submitted address %s without changing its intent", async (address, expectedURL) => {
    const { input } = render({});
    const form = container.querySelector<HTMLFormElement>(".workspace-browser-url-form");
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, address);
      input!.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      form!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(browserCommand).toHaveBeenCalledWith(expect.objectContaining({
      command: "navigate", url: expectedURL,
    }));
  });

  it("keeps page actions off before a URL is submitted", () => {
    render({});
    expect(
      container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")?.disabled,
    ).toBe(true);
  });

  it("navigates when the conversation requests a URL", async () => {
    render({ visible: true });
    browserCommand.mockClear();
  vi.mocked(showActionErrorToast).mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    await act(async () => {
      renderElement(
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
      threadID: "thread-1",
      command: "navigate",
      url: "https://docs.example.com/api",
    });
  });

  it("keeps a navigation request until the panel is visible", async () => {
    render({ visible: true });
    browserCommand.mockClear();
  vi.mocked(showActionErrorToast).mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    act(() => {
      renderElement(
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
      renderElement(
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
    expect(reportBrowserBounds).toHaveBeenCalledWith("/repo", "tab-live", null, undefined);
  });

  it("pauses for same-thread input on the displayed tab and waits before navigating", async () => {
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
      userInput()({ threadID: "thread-1", inputGeneration: 1, workdir: "/other", tabID: "tab-live" });
      userInput()({ threadID: "thread-1", inputGeneration: 1, workdir: "/repo", tabID: "another-tab" });
      userInput()({ threadID: "other-thread", inputGeneration: 1, workdir: "/repo", tabID: "tab-live" });
    });
    expect(onUserInteraction).not.toHaveBeenCalled();
    act(() => { userInput()({ threadID: "thread-1", inputGeneration: 1, workdir: "/repo", tabID: "tab-live" }); });
    expect(onUserInteraction).toHaveBeenCalledTimes(1);
    expect(onUserInteraction).toHaveBeenCalledWith(1);
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
    expect(browserCommand).toHaveBeenCalledWith({ workdir: "/repo", threadID: "thread-1", tabID: "tab-live", command: "back", url: undefined });
    onUserInteraction.mockClear();
    await act(async () => {
      rerender({ activity: { ...activity, controller: "user", state: "user_controlled" }, onUserInteraction });
    });
    act(() => { userInput()({ threadID: "thread-1", inputGeneration: 1, workdir: "/repo", tabID: "tab-live" }); });
    expect(onUserInteraction).toHaveBeenCalledTimes(1);
    onUserInteraction.mockClear();
    await act(async () => {
      rerender({
        activity,
        dockTarget: { thread_id: "thread-1", workdir: "/repo", tabID: "retained-preview" },
        onUserInteraction,
      });
    });
    act(() => { userInput()({ threadID: "other-thread", inputGeneration: 1, workdir: "/repo", tabID: "retained-preview" }); });
    expect(onUserInteraction).not.toHaveBeenCalled();
    act(() => { userInput()({ threadID: "thread-1", inputGeneration: 2, workdir: "/repo", tabID: "retained-preview" }); });
    expect(onUserInteraction).toHaveBeenCalledExactlyOnceWith(2);
  });
});

it("shows unavailable without a browser provider even when preload exists", () => {
  root = createRoot(container);
  act(() => root!.render(<WorkspaceBrowserPanel threadID="thread-1" activeContext={{ kind: "no_project", cwd: "/repo" }} />));
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(window.wuu.browserSurface).not.toHaveBeenCalled();
});

it("does not navigate the previous lease after takeover finishes on a switched thread", async () => {
  let finish!: () => void;
  const waiting = new Promise<void>(resolve => { finish = resolve; });
  const activity: ActivitySession = { id: "activity", kind: "browser", thread_id: "thread-1", workdir: "/repo", state: "active", controller: "agent", target: "tab", created_at: "", updated_at: "", execution_id: "run" };
  render({ activity, onUserInteraction: () => waiting });
  act(() => surfaceHandlers.at(-1)!({ workdir: "/repo", tabID: "tab", url: "https://example.com", title: "Page", canGoBack: true, canGoForward: false, loading: false }));
  act(() => container.querySelector<HTMLButtonElement>(".workspace-browser-nav")!.click());
  await act(async () => { rerender({ threadID: "thread-2" }); finish(); await waiting; });
  expect(browserCommand).not.toHaveBeenCalled();
});

it("does not report an old external-open failure in the next thread", async () => {
  let reject!: (error: Error) => void;
  vi.mocked(window.wuu.openExternal).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; }));
  render({});
  act(() => surfaceHandlers.at(-1)!({ workdir: "/repo", tabID: "user:thread-1", url: "https://example.com", title: "Page", canGoBack: false, canGoForward: false, loading: false }));
  act(() => container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")!.click());
  await act(async () => { rerender({ threadID: "thread-2" }); });
  await act(async () => { reject(new Error("OLD EXTERNAL ERROR")); });
  expect(container.textContent).not.toContain("OLD EXTERNAL ERROR");
  expect(showActionErrorToast).not.toHaveBeenCalled();
});


it("cleans up a retired browser lease without calling it during a presentation rebind", async () => {
  const old = new AbortController(), next = new AbortController();
  const scoped = (signal: AbortSignal) => scopeApplicationBrowser(() => browser, signal, () => signal.throwIfAborted());
  vi.mocked(window.wuu.browserSurface!).mockResolvedValue({ workdir: "/repo", tabID: "user:thread-1", url: "https://example.com",
    title: "Page", loading: false, canGoBack: false, canGoForward: false });
  root = createRoot(container);
  const tree = (signal: AbortSignal) => <BrowserControlsProvider service={scoped(signal)}><WorkspaceBrowserPanel visible
    threadID="thread-1" activeContext={{ kind: "no_project", cwd: "/repo" }} /></BrowserControlsProvider>;
  await act(async () => root!.render(tree(old.signal)));
  const input = container.querySelector("input");
  old.abort(new PluginLifecycleCancellationError());
  await act(async () => root!.render(tree(next.signal)));
  expect(container.querySelector("input")).toBe(input);
  expect(container.querySelector('[role="alert"]')).toBeNull();
  expect(container.querySelector<HTMLInputElement>("input")!.value).toBe("https://example.com");
});


it("keeps the loaded page available when opening it externally fails", async () => {
  const failure = new Error("External browser unavailable");
  vi.mocked(window.wuu.openExternal).mockRejectedValueOnce(failure);
  await act(async () => { render({}); });
  act(() => surfaceHandlers.at(-1)!({ workdir: "/repo", tabID: "user:thread-1", url: "https://example.com", title: "Page", canGoBack: false, canGoForward: false, loading: false }));
  await act(async () => container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")!.click());
  expect(showActionErrorToast).toHaveBeenCalledWith(failure);
  expect(container.querySelector('[role="alert"]')).toBeNull();
  expect(container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")!.disabled).toBe(false);
  expect(container.querySelector<HTMLInputElement>("input")!.value).toBe("https://example.com");
});

it("retries the requested address after an initial navigation rejects", async () => {
  browserCommand.mockRejectedValueOnce(new Error("Connection refused"));
  await act(async () => { render({}); });
  const input = container.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "http://localhost:4567");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => input.closest("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  await act(async () => container.querySelector<HTMLButtonElement>(".workspace-browser-retry")!.click());
  expect(browserCommand).toHaveBeenCalledTimes(2);
  expect(browserCommand.mock.calls[1][0]).toMatchObject({ command: "navigate", url: "http://localhost:4567" });
  expect(container.querySelector('[role="alert"]')).toBeNull();
});


it("retains the failed address through native loading and failed-load snapshots", async () => {
  let reject!: (error: Error) => void;
  browserCommand.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  await act(async () => { render({}); });
  const input = container.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "http://localhost:9876");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => input.closest("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
  const oldSurface = { workdir: "/repo", tabID: "user:thread-1", url: "about:blank", title: "", canGoBack: false, canGoForward: false };
  act(() => surfaceHandlers.at(-1)!({ ...oldSurface, loading: true }));
  act(() => surfaceHandlers.at(-1)!({ ...oldSurface, loading: false, error: "ERR_CONNECTION_REFUSED" }));
  await act(async () => reject(new Error("ERR_CONNECTION_REFUSED")));
  act(() => surfaceHandlers.at(-1)!({ ...oldSurface, loading: false, error: "ERR_CONNECTION_REFUSED" }));
  await act(async () => container.querySelector<HTMLButtonElement>(".workspace-browser-retry")!.click());
  expect(browserCommand).toHaveBeenCalledTimes(2);
  expect(browserCommand.mock.calls[1][0]).toMatchObject({ command: "navigate", url: "http://localhost:9876" });
  expect(container.querySelector('[role="alert"]')).toBeNull();
});
