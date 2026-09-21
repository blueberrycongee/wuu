import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import type { ActivitySession } from "../shared/protocol";
import { requestWorkspaceBrowserNavigation } from "./WorkspaceBrowserNavigation";

type FakeWebviewMethods = {
  loadURL: ReturnType<typeof vi.fn>;
  getURL: ReturnType<typeof vi.fn>;
  getTitle: ReturnType<typeof vi.fn>;
  canGoBack: ReturnType<typeof vi.fn>;
  canGoForward: ReturnType<typeof vi.fn>;
  goBack: ReturnType<typeof vi.fn>;
  goForward: ReturnType<typeof vi.fn>;
  reload: ReturnType<typeof vi.fn>;
  stop: ReturnType<typeof vi.fn>;
};

type FakeWebview = HTMLElement & FakeWebviewMethods;

function makeFakeWebview(): FakeWebview {
  // jsdom does not know the Electron <webview> custom element. We
  // create a plain div and bolt the webview-only API on top so the
  // component's imperative mount path keeps working under jsdom.
  const el = document.createElement("div");
  const methods: FakeWebviewMethods = {
    loadURL: vi.fn(),
    getURL: vi.fn(() => ""),
    getTitle: vi.fn(() => ""),
    canGoBack: vi.fn(() => false),
    canGoForward: vi.fn(() => false),
    goBack: vi.fn(),
    goForward: vi.fn(),
    reload: vi.fn(),
    stop: vi.fn()
  };
  return Object.assign(el, methods);
}

let container: HTMLDivElement;
let root: Root | null = null;
let fakeWebview: FakeWebview;
let originalCreateElement: typeof document.createElement;

beforeAll(() => {
  (globalThis as { Electron?: unknown }).Electron = {
    WebviewTag: class WebviewTag {}
  };
});

beforeEach(() => {
  fakeWebview = makeFakeWebview();
  originalCreateElement = document.createElement.bind(document);
  document.createElement = ((tag: string, options?: ElementCreationOptions) => {
    if (String(tag).toLowerCase() === "webview") {
      return fakeWebview as unknown as HTMLElement;
    }
    return originalCreateElement(tag, options);
  }) as typeof document.createElement;
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  if (container.parentNode) {
    container.remove();
  }
  document.createElement = originalCreateElement;
  vi.restoreAllMocks();
});

function render(props: {
  mounted?: boolean;
  activity?: ActivitySession;
  onActivityTakeover?: () => void;
  onActivityRelease?: () => void;
  onActivityStop?: () => void;
}) {
  act(() => {
    root = createRoot(container);
    root!.render(
      <WorkspaceBrowserPanel
        mounted={props.mounted}
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
  mounted?: boolean;
  activity?: ActivitySession;
  onActivityTakeover?: () => void;
  onActivityRelease?: () => void;
  onActivityStop?: () => void;
}) {
  act(() => {
    root!.render(
      <WorkspaceBrowserPanel
        mounted={props.mounted}
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
  it("navigates only after the user submits an address", () => {
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
    act(() => {
      form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    expect(fakeWebview.loadURL).toHaveBeenCalledWith("http://app.local:3000");
  });

  it("shows the empty browsing surface before a URL is submitted", () => {
    render({});
    expect(container.querySelector(".workspace-browser-home")?.textContent).toContain("开始浏览");
    expect(container.querySelector(".workspace-browser-home")?.textContent).toContain("输入 URL 以打开页面");
    expect(
      container.querySelector<HTMLButtonElement>(".workspace-browser-open-external")?.disabled,
    ).toBe(true);
  });

  it("navigates when the conversation requests a URL", () => {
    render({ mounted: true });
    fakeWebview.loadURL.mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    act(() => {
      root!.render(
        <WorkspaceBrowserPanel
          mounted
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(fakeWebview.loadURL).toHaveBeenCalledWith("https://docs.example.com/api");
  });

  it("navigates while hidden so background tabs can reuse the same page", () => {
    render({ mounted: true });
    fakeWebview.loadURL.mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    act(() => {
      root!.render(
        <WorkspaceBrowserPanel
          mounted
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(fakeWebview.loadURL).toHaveBeenCalledWith("https://docs.example.com/api");
  });

  it("does not consume a navigation request after the browser tab is discarded", () => {
    render({ mounted: true });
    fakeWebview.loadURL.mockClear();
    const request = requestWorkspaceBrowserNavigation({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    act(() => {
      root!.render(
        <WorkspaceBrowserPanel
          mounted={false}
          activeContext={{ kind: "no_project", cwd: "/repo" }}
          requestedURL={request}
        />,
      );
    });
    expect(fakeWebview.loadURL).not.toHaveBeenCalledWith("https://docs.example.com/api");
  });

  it("clears the webview when the browser panel closes", () => {
    render({ mounted: true });
    fakeWebview.loadURL.mockClear();
    rerender({ mounted: false });
    expect(fakeWebview.stop).toHaveBeenCalledTimes(1);
    expect(fakeWebview.loadURL).toHaveBeenCalledWith("about:blank");
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
