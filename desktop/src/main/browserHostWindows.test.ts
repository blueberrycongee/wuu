import type { Rectangle } from "electron";
import type { ServerEvent } from "../shared/protocol";
import { describe, expect, it, vi } from "vitest";
import type { WindowRegistry } from "./windowRegistry";
import {
  BrowserHostCoordinator,
  type BrowserDebuggerHandle,
  type BrowserHostDeps,
  type BrowserHostWindowHandle,
  type BrowserNativeImageHandle,
  type BrowserParentWindowHandle,
  type BrowserReplyPort,
  type BrowserViewHandle,
  type BrowserWebContentsHandle,
  boxModelCenter,
  browserPermissionDecision,
  configureBrowserProxy,
  interactableNodesFromSnapshot,
  pageReadableContent,
  readableBlocksFromSnapshot,
  keyChord,
  keyDispatch,
  spectatorScrollbarCSS,
  tabKey,
  valueFor,
  wheelDeltas,
} from "./browserHostWindows";

let nextWebContentsID = 1;

describe("configureBrowserProxy", () => {
  it("configures only the supplied browser session", async () => {
    const setProxy = vi.fn().mockResolvedValue(undefined);

    await expect(
      configureBrowserProxy({ setProxy }, "http://127.0.0.1:7897"),
    ).resolves.toBe(true);
    expect(setProxy).toHaveBeenCalledWith({ proxyRules: "http://127.0.0.1:7897" });
  });

  it("does not change the session when no proxy is configured", async () => {
    const setProxy = vi.fn().mockResolvedValue(undefined);

    await expect(configureBrowserProxy({ setProxy }, "  ")).resolves.toBe(false);
    expect(setProxy).not.toHaveBeenCalled();
  });
});

class FakeView implements BrowserViewHandle {
  readonly wcID = nextWebContentsID++;
  attached = false;
  url = "https://example.com/";
  title = "Example";
  readonly sentCommands: Array<{ method: string; params?: Record<string, unknown> }> = [];
  readonly responders = new Map<string, (params?: Record<string, unknown>) => Record<string, unknown>>();
  readonly loadedURLs: string[] = [];
  readonly loadedBounds: Rectangle[] = [];
  captureCount = 0;
  captureEmpty = false;
  boundsSet: Rectangle | undefined;
  visibleState: boolean | undefined;
  backgroundThrottling = true;
  readonly backgroundThrottlingChanges: boolean[] = [];
  zoomFactor = 1;
  scriptResult: unknown = undefined;
  cssSerial = 0;
  readonly insertedCSS = new Map<string, string>();
  readonly removedCSS: string[] = [];
  readonly listeners = new Map<string, Array<(...args: unknown[]) => void>>();
  readonly scripts: string[] = [];
  back = false;
  forward = false;
  loading = false;
  goBackCount = 0;
  goForwardCount = 0;
  reloadCount = 0;
  stopCount = 0;
  windowOpenHandler: ((details: { url?: string }) => { action: "deny" } | { action: "allow" }) | undefined;
  closed = false;

  readonly debuggerHandle: BrowserDebuggerHandle = {
    attach: () => {
      this.attached = true;
    },
    detach: () => {
      this.attached = false;
    },
    isAttached: () => this.attached,
    sendCommand: async (method, params) => {
      this.sentCommands.push({ method, params });
      const rawType = params && typeof params.type === "string" ? params.type : "";
      if (method === "Input.dispatchMouseEvent") {
        const type = rawType === "mousePressed" ? "mouseDown"
          : rawType === "mouseReleased" ? "mouseUp"
          : rawType;
        for (const listener of this.listeners.get("before-mouse-event") ?? []) listener({}, { type });
      }
      if (method === "Input.dispatchKeyEvent") {
        const type = rawType === "rawKeyDown" || rawType === "keyDown" ? "keyDown" : rawType;
        for (const listener of this.listeners.get("before-input-event") ?? []) listener({}, { type });
      }
      const responder = this.responders.get(method);
      return responder ? responder(params) : {};
    },
  };

  readonly webContents: BrowserWebContentsHandle = {
    id: this.wcID,
    debugger: this.debuggerHandle,
    setBackgroundThrottling: (allowed: boolean) => {
      this.backgroundThrottling = allowed;
      this.backgroundThrottlingChanges.push(allowed);
    },
    setWindowOpenHandler: (handler: (details: { url?: string }) => { action: "deny" } | { action: "allow" }) => {
      this.windowOpenHandler = handler;
    },
    canGoBack: () => this.back,
    canGoForward: () => this.forward,
    goBack: () => {
      this.goBackCount += 1;
    },
    goForward: () => {
      this.goForwardCount += 1;
    },
    reload: () => {
      this.reloadCount += 1;
    },
    stop: () => {
      this.stopCount += 1;
    },
    isLoading: () => this.loading,
    executeJavaScript: async (code: string) => {
      this.scripts.push(code);
      return this.scriptResult;
    },
    insertCSS: async (css: string) => {
      const key = `css-${++this.cssSerial}`;
      this.insertedCSS.set(key, css);
      return key;
    },
    removeInsertedCSS: async (key: string) => {
      this.removedCSS.push(key);
      this.insertedCSS.delete(key);
    },
    setZoomFactor: (factor: number) => {
      this.zoomFactor = factor;
    },
    on: (event: string, listener: (...args: unknown[]) => void) => {
      const list = this.listeners.get(event) ?? [];
      list.push(listener);
      this.listeners.set(event, list);
    },
    loadURL: async (url: string) => {
      this.loadedURLs.push(url);
      this.loadedBounds.push(this.getBounds());
      this.url = url;
    },
    getURL: () => this.url,
    getTitle: () => this.title,
    capturePage: async (): Promise<BrowserNativeImageHandle> => {
      this.captureCount += 1;
      return {
        toPNG: () => (this.captureEmpty ? Buffer.alloc(0) : Buffer.from("fake-png")),
        getSize: () => (this.captureEmpty ? { width: 0, height: 0 } : { width: 800, height: 600 }),
      };
    },
    close: () => {
      this.closed = true;
    },
    isDestroyed: () => this.closed,
  };

  emitNavigate(url: string): void {
    this.url = url;
    for (const listener of this.listeners.get("did-navigate") ?? []) listener({}, url);
  }

  getBounds(): Rectangle {
    return this.boundsSet ?? { x: 0, y: 0, width: 0, height: 0 };
  }

  setBounds(rect: Rectangle): void {
    this.boundsSet = rect;
  }

  setVisible(visible: boolean): void {
    this.visibleState = visible;
  }
}

class FakeWindow implements BrowserHostWindowHandle {
  destroyed = false;
  readonly added: BrowserViewHandle[] = [];
  readonly removed: BrowserViewHandle[] = [];
  readonly contentView = {
    addChildView: (view: BrowserViewHandle): void => {
      this.added.push(view);
    },
    removeChildView: (view: BrowserViewHandle): void => {
      this.removed.push(view);
    },
  };

  isDestroyed(): boolean {
    return this.destroyed;
  }

  destroy(): void {
    this.destroyed = true;
  }
}

type Harness = {
  coordinator: BrowserHostCoordinator;
  views: FakeView[];
  hostWindows: FakeWindow[];
  reply: BrowserReplyPort & { respond: ReturnType<typeof vi.fn>; reject: ReturnType<typeof vi.fn> };
  invalidations: string[];
  writtenPng: Map<string, Buffer>;
  writtenJson: Map<string, string>;
  mainWindow: FakeWindow;
};

function makeHarness(): Harness {
  const views: FakeView[] = [];
  const hostWindows: FakeWindow[] = [];
  const writtenPng = new Map<string, Buffer>();
  const writtenJson = new Map<string, string>();
  const invalidations: string[] = [];
  const mainWindow = new FakeWindow();

  const reply = {
    respond: vi.fn(),
    reject: vi.fn(),
  };

  const deps: BrowserHostDeps = {
    createHostWindow: () => {
      const host = new FakeWindow();
      hostWindows.push(host);
      return host;
    },
    createView: () => {
      const view = new FakeView();
      views.push(view);
      return view;
    },
    writePng: (destPath, data) => {
      writtenPng.set(destPath, data);
    },
    writeJson: (destPath, data) => {
      writtenJson.set(destPath, data);
    },
  };

  const registry = { mainWindow: () => mainWindow } as unknown as WindowRegistry;
  const coordinator = new BrowserHostCoordinator(registry, reply, deps, (workdir) => {
    invalidations.push(workdir);
  });

  return { coordinator, views, hostWindows, reply, invalidations, writtenPng, writtenJson, mainWindow };
}

function serverRequest(
  method: string,
  params: Record<string, unknown>,
  id = "server-request-1",
): Extract<ServerEvent, { kind: "server-request" }> {
  return {
    workdir: typeof params.workdir === "string" ? params.workdir : "/repo",
    kind: "server-request",
    message: { id, method, params },
  };
}

async function flushMicrotasks(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

async function openTab(harness: Harness, workdir: string, tabID: string): Promise<void> {
  await harness.coordinator.handleServerRequest(
    serverRequest("browser/open_tab", { workdir, tab_id: tabID }, `open-${workdir}-${tabID}`),
  );
}

// A minimal DOMSnapshot.captureSnapshot response with one button (backend 100)
// and one link (backend 200). The string table is index-referenced by the node
// arrays, matching the real CDP wire shape.
function twoNodeSnapshot(): Record<string, unknown> {
  return {
    strings: [
      "BUTTON", // 0 nodeName for node 0
      "A", // 1 nodeName for node 1
      "aria-label", // 2
      "Submit", // 3
      "href", // 4
      "https://x.test/", // 5
      "#text", // 6 nodeName for the link's text node
      "Release notes", // 7 text content
    ],
    documents: [
      {
        nodes: {
          nodeName: [0, 1, 6],
          parentIndex: [-1, -1, 1],
          backendNodeId: [100, 200, 201],
          attributes: [
            [2, 3], // node 0: aria-label=Submit
            [4, 5], // node 1: href=https://x.test/
            [],
          ],
        },
        layout: {
          nodeIndex: [0, 1, 2],
          text: [-1, -1, 7],
          bounds: [
            [5, 6, 50, 20],
            [7, 8, 60, 18],
            [7, 8, 60, 18],
          ],
        },
        textBoxes: { layoutIndex: [2], start: [0], length: [13] },
      },
    ],
  };
}

describe("BrowserHostCoordinator CDP routing", () => {
  it("parks hidden tabs and reactivates them only during an operation", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    expect(view.visibleState).toBe(false);
    expect(view.backgroundThrottling).toBe(true);

    let release!: () => void;
    view.webContents.loadURL = async (url: string) => {
      view.loadedURLs.push(url);
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      view.url = url;
    };
    const request = harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "navigate", params: { url: "https://active.test/" } },
        "active-1",
      ),
    );
    await Promise.resolve();
    expect(view.visibleState).toBe(true);
    expect(view.backgroundThrottling).toBe(false);

    release();
    await request;
    expect(view.visibleState).toBe(false);
    expect(view.backgroundThrottling).toBe(true);
  });

  it("translates navigate to loadURL and returns url + title", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.title = "Landing";

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "navigate", params: { url: "https://landing.test/" } },
        "nav-1",
      ),
    );

    expect(view.loadedURLs).toEqual(["https://landing.test/"]);
    expect(harness.reply.respond).toHaveBeenLastCalledWith("nav-1", {
      result: { url: "https://landing.test/", title: "Landing" },
    });
  });

  it("assigns node_ids on observe and resolves click(node_id) through the map", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.responders.set("DOMSnapshot.captureSnapshot", () => twoNodeSnapshot());

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "observe", params: { screenshot: false } },
        "obs-1",
      ),
    );

    const observeResult = harness.reply.respond.mock.calls.at(-1)?.[1] as {
      result: { nodes: Array<Record<string, unknown>>; content: Array<Record<string, unknown>> };
    };
    expect(observeResult.result.nodes).toHaveLength(2);
    expect(observeResult.result.nodes[0]).toMatchObject({ node_id: 1, role: "button", name: "Submit", bounds: [5, 6, 50, 20] });
    expect(observeResult.result.nodes[1]).toMatchObject({ node_id: 2, role: "link" });
    // The link's readable text and its click id come from the same snapshot.
    expect(observeResult.result.content).toEqual([
      { kind: "paragraph", text: "Release notes", node_id: 2 },
    ]);

    // node_id 2 must resolve to backendNodeId 200 via the per-tab map.
    let requestedBackend: unknown;
    view.responders.set("DOM.getBoxModel", (params) => {
      requestedBackend = params?.backendNodeId;
      return { model: { content: [10, 20, 30, 20, 30, 40, 10, 40] } };
    });

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "click", params: { node_id: 2 } },
        "click-1",
      ),
    );

    expect(requestedBackend).toBe(200);
    const mouseCommands = view.sentCommands.filter((c) => c.method === "Input.dispatchMouseEvent");
    expect(mouseCommands[0]?.params).toMatchObject({ type: "mousePressed", x: 20, y: 30, button: "left" });
    expect(mouseCommands[1]?.params).toMatchObject({ type: "mouseReleased", x: 20, y: 30 });
    expect(harness.reply.respond).toHaveBeenLastCalledWith("click-1", { result: { ok: true } });
  });

  it("continues readable content from content_offset", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const children = Array.from({ length: 25 }, (_, index) => ({
      tag: "p",
      text: `Paragraph ${index} stays readable.`,
    }));
    harness.views[0].responders.set("DOMSnapshot.captureSnapshot", () => buildSnapshot([
      { tag: "main", children },
    ]));

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "observe", params: { screenshot: false, content_offset: 20 } },
        "obs-page",
      ),
    );

    const observeResult = harness.reply.respond.mock.calls.at(-1)?.[1] as {
      result: { content: Array<{ text?: string }>; content_offset: number; content_total: number; content_next_offset?: number };
    };
    expect(observeResult.result.content_offset).toBe(20);
    expect(observeResult.result.content_total).toBe(25);
    expect(observeResult.result.content_next_offset).toBeUndefined();
    expect(observeResult.result.content.map((block) => block.text)).toEqual([
      "Paragraph 20 stays readable.",
      "Paragraph 21 stays readable.",
      "Paragraph 22 stays readable.",
      "Paragraph 23 stays readable.",
      "Paragraph 24 stays readable.",
    ]);
  });

  it("rejects click(node_id) when the tab has not been observed", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "click", params: { node_id: 7 } },
        "click-miss",
      ),
    );

    expect(harness.reply.reject).toHaveBeenLastCalledWith("click-miss", expect.stringContaining("node_id 7 not found"));
  });

  it("dispatches click at raw coordinates without a map lookup", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "click", params: { x: 42, y: 84 } },
        "click-xy",
      ),
    );

    const mouseCommands = view.sentCommands.filter((c) => c.method === "Input.dispatchMouseEvent");
    expect(mouseCommands[0]?.params).toMatchObject({ type: "mousePressed", x: 42, y: 84 });
    expect(view.sentCommands.some((c) => c.method === "DOM.getBoxModel")).toBe(false);
  });

  it("reports tab_not_found for an unknown tab so the tool can rebuild it", async () => {
    const harness = makeHarness();
    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "ghost", method: "navigate", params: { url: "https://x/" } },
        "ghost-1",
      ),
    );
    expect(harness.reply.reject).toHaveBeenLastCalledWith("ghost-1", "tab_not_found");
  });
});

describe("BrowserHostCoordinator large-response gate", () => {
  it("spills an over-1MB observe result to dest_path.json instead of inlining", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const hugeName = "x".repeat(1_200_000);
    view.responders.set("DOMSnapshot.captureSnapshot", () => ({
      strings: ["INPUT", "aria-label", hugeName],
      documents: [
        {
          nodes: {
            nodeName: [0],
            backendNodeId: [500],
            attributes: [[1, 2]],
          },
          layout: { nodeIndex: [0], bounds: [[1, 1, 200, 30]] },
        },
      ],
    }));

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        {
          workdir: "/repo",
          tab_id: "t1",
          method: "observe",
          params: { screenshot: false, dest_path: "/artifacts/preview.png" },
        },
        "obs-big",
      ),
    );

    const result = harness.reply.respond.mock.calls.at(-1)?.[1] as { result?: unknown; path?: string; size?: number };
    expect(result.result).toBeUndefined();
    expect(result.path).toBe("/artifacts/preview.png.json");
    expect(result.size).toBeGreaterThan(1024 * 1024);
    expect(harness.writtenJson.has("/artifacts/preview.png.json")).toBe(true);
  });

  it("inlines a small result", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    harness.views[0].responders.set("DOMSnapshot.captureSnapshot", () => twoNodeSnapshot());

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "observe", params: { screenshot: false, dest_path: "/artifacts/preview.png" } },
        "obs-small",
      ),
    );

    const result = harness.reply.respond.mock.calls.at(-1)?.[1] as { result?: unknown; path?: string };
    expect(result.path).toBeUndefined();
    expect(result.result).toBeDefined();
    expect(harness.writtenJson.size).toBe(0);
  });
});

describe("BrowserHostCoordinator screenshot", () => {
  it("captures with stayHidden and writes the PNG to dest_path", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/screenshot",
        { workdir: "/repo", tab_id: "t1", dest_path: "/artifacts/shot.png" },
        "shot-1",
      ),
    );

    expect(harness.views[0].captureCount).toBe(1);
    expect(harness.writtenPng.has("/artifacts/shot.png")).toBe(true);
    expect(harness.reply.respond).toHaveBeenLastCalledWith("shot-1", {
      width: 800,
      height: 600,
      path: "/artifacts/shot.png",
    });
  });

  it("rejects an empty capture instead of writing it", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    harness.views[0].captureEmpty = true;

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/screenshot", { workdir: "/repo", tab_id: "t1", dest_path: "/artifacts/shot.png" }, "shot-empty"),
    );

    expect(harness.writtenPng.has("/artifacts/shot.png")).toBe(false);
    expect(harness.reply.reject).toHaveBeenLastCalledWith("shot-empty", expect.stringContaining("empty frame"));
  });
});

describe("BrowserHostCoordinator lifecycle", () => {
  it("onClientTorndown only destroys the given workdir's views", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repoA", "a1");
    await openTab(harness, "/repoB", "b1");
    const [viewA, viewB] = harness.views;

    harness.coordinator.onClientTorndown("/repoA");

    expect(viewA.closed).toBe(true);
    expect(viewB.closed).toBe(false);
    expect(harness.coordinator.hasAgentTabs("/repoA")).toBe(false);
    expect(harness.coordinator.hasAgentTabs("/repoB")).toBe(true);
    expect(harness.invalidations).toEqual(["/repoA"]);
  });

  it("drops a reply when the core dies mid-request so it is not respawned", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    harness.reply.respond.mockClear();
    // Simulate the core crashing while the CDP action is in flight: the
    // server-exit lands before the reply would go out.
    view.webContents.loadURL = async (url: string) => {
      view.loadedURLs.push(url);
      harness.coordinator.markServerExit("/repo");
    };

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "navigate", params: { url: "https://x/" } },
        "mid-crash",
      ),
    );

    expect(view.loadedURLs).toEqual(["https://x/"]);
    expect(harness.reply.respond).not.toHaveBeenCalled();
  });

  it("is idempotent on close of an unknown tab", async () => {
    const harness = makeHarness();
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/close_tab", { workdir: "/repo", tab_id: "missing" }, "close-1"),
    );
    expect(harness.reply.respond).toHaveBeenLastCalledWith("close-1", { ok: true });
  });

  it("lists only the requesting workdir's tabs", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repoA", "a1");
    await openTab(harness, "/repoA", "a2");
    await openTab(harness, "/repoB", "b1");

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/list_tabs", { workdir: "/repoA" }, "list-1"),
    );
    expect(harness.reply.respond).toHaveBeenLastCalledWith("list-1", {
      tab_ids: ["a1", "a2"],
      tabs: [
        { tab_id: "a1", url: "https://example.com/", title: "Example" },
        { tab_id: "a2", url: "https://example.com/", title: "Example" },
      ],
    });
  });

  it("destroyAll tears down every view and the host window", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    harness.coordinator.destroyAll();
    expect(harness.views[0].closed).toBe(true);
    expect(harness.hostWindows[0].destroyed).toBe(true);
  });
});

describe("BrowserHostCoordinator visibility takeover", () => {
  it("reparents the view onto the reported window and applies the reported bounds", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const rect = { x: 12, y: 34, width: 640, height: 480 };
    harness.coordinator.reportBounds("/repo", "t1", harness.mainWindow as unknown as BrowserParentWindowHandle, rect, 1);

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: true }, "vis-1"),
    );

    expect(harness.mainWindow.added).toContain(view);
    expect(view.boundsSet).toEqual(rect);
    expect(view.visibleState).toBe(true);
  });

  it("overlay suppression hides then restores the view", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];

    harness.coordinator.setOverlaySuppressed("/repo", "t1", true);
    expect(view.visibleState).toBe(false);
    harness.coordinator.setOverlaySuppressed("/repo", "t1", false);
    expect(view.visibleState).toBe(false);
  });

  it("keeps cached and live native bounds aligned with a zoomed app without zooming the browser", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const window = harness.mainWindow as unknown as BrowserParentWindowHandle;
    const rect = { x: 101, y: 51, width: 801, height: 601 };
    harness.coordinator.reportBounds("/repo", "t1", window, rect, 0.8);

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: true }, "vis-zoom"),
    );
    expect(view.boundsSet).toEqual({ x: 81, y: 41, width: 641, height: 481 });

    harness.coordinator.reportBounds("/repo", "t1", window, rect, 1.25);
    expect(view.boundsSet).toEqual({ x: 126, y: 64, width: 1001, height: 751 });
    expect(view.zoomFactor).toBe(1);
  });

  it("does not paint a tab until the panel reports a rectangle", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: true }, "vis-early"),
    );
    expect(harness.mainWindow.added).toContain(view);
    expect(view.visibleState).toBe(false);
    expect(harness.coordinator.isInPanel("/repo", "t1")).toBe(false);
  });

  it("parks the tab when the panel reports an empty rectangle", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const window = harness.mainWindow as unknown as BrowserParentWindowHandle;
    harness.coordinator.reportBounds("/repo", "t1", window, { x: 8, y: 9, width: 200, height: 100 }, 1);
    expect(harness.coordinator.isInPanel("/repo", "t1")).toBe(true);

    harness.coordinator.reportBounds("/repo", "t1", window, null, 1);
    expect(harness.coordinator.isInPanel("/repo", "t1")).toBe(false);
    expect(harness.mainWindow.removed).toContain(view);
  });

  it("ignores a late rectangle after hide until the panel asks again", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const window = harness.mainWindow as unknown as BrowserParentWindowHandle;
    const rect = { x: 4, y: 6, width: 300, height: 180 };
    harness.coordinator.reportBounds("/repo", "t1", window, rect, 1);
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: false }, "vis-hide"),
    );
    const addedAfterHide = harness.mainWindow.added.length;
    harness.coordinator.reportBounds("/repo", "t1", window, rect, 1);
    expect(harness.mainWindow.added).toHaveLength(addedAfterHide);
    expect(harness.coordinator.isInPanel("/repo", "t1")).toBe(false);

    harness.coordinator.reportBounds("/repo", "t1", window, rect, 1, true);
    expect(harness.coordinator.isInPanel("/repo", "t1")).toBe(true);
    expect(view.boundsSet).toEqual(rect);
  });

  it("reports a press in the panel and ignores hovering, scrolling, and agent input", async () => {
    const harness = makeHarness();
    const userInput: string[] = [];
    harness.coordinator.setRendererSink({
      surface: () => undefined,
      userInput: (payload) => userInput.push(payload.tabID),
      adopted: () => undefined,
      presented: () => undefined,
    });
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    const window = harness.mainWindow as unknown as BrowserParentWindowHandle;
    harness.coordinator.reportBounds("/repo", "t1", window, { x: 1, y: 2, width: 80, height: 60 }, 1);

    const fireMouse = (type: string) => {
      view.listeners.get("before-mouse-event")?.[0]?.({}, { type });
    };
    for (const type of ["mouseEnter", "mouseMove", "mouseLeave", "mouseUp", "mouseWheel"]) fireMouse(type);
    expect(userInput).toEqual([]);

    fireMouse("mouseDown");
    expect(userInput).toEqual(["t1"]);

    await harness.coordinator.handleServerRequest(
      serverRequest(
        "browser/cdp",
        { workdir: "/repo", tab_id: "t1", method: "click", params: { x: 3, y: 4 } },
        "click-1",
      ),
    );
    expect(userInput).toEqual(["t1"]);
  });

  it("adopts a page-opened window as another tab", async () => {
    const harness = makeHarness();
    const adopted: string[] = [];
    harness.coordinator.setRendererSink({
      surface: () => undefined,
      userInput: () => undefined,
      adopted: (payload) => adopted.push(payload.tabID),
      presented: () => undefined,
    });
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.windowOpenHandler?.({ url: "https://example.com/next" });
    await vi.waitFor(() => expect(adopted).toHaveLength(1));
    expect(harness.views).toHaveLength(2);
    expect(harness.views[1].loadedURLs).toContain("https://example.com/next");
    expect(adopted[0].startsWith("popup-t1-")).toBe(true);

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/list_tabs", { workdir: "/repo" }, "list-popup"),
    );
    const listed = harness.reply.respond.mock.calls.at(-1)?.[1] as {
      tab_ids: string[];
      tabs: Array<{ tab_id: string; url: string }>;
    };
    expect(listed.tab_ids).toContain("t1");
    expect(listed.tabs.find((tab) => tab.tab_id.startsWith("popup-t1-"))?.url).toBe("https://example.com/next");
  });

  it("navigates through the same tab the panel is showing", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.back = true;
    const snapshot = await harness.coordinator.runCommand("/repo", "t1", "navigate", "https://example.com/page");
    expect(view.loadedURLs).toContain("https://example.com/page");
    expect(snapshot?.url).toBe("https://example.com/page");
    expect(snapshot?.tabID).toBe("t1");
    await harness.coordinator.runCommand("/repo", "t1", "back");
    expect(view.goBackCount).toBe(1);
  });
});

describe("BrowserHostCoordinator permission ownership", () => {
  it("owns its agent views and sorts permissions by ownership", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];

    expect(harness.coordinator.ownsWebContents(view.webContents.id)).toBe(true);
    expect(harness.coordinator.ownsWebContents(987654)).toBe(false);

    // Agent view: sensitive denied, benign allowed.
    expect(browserPermissionDecision(true, "media")).toBe(false);
    expect(browserPermissionDecision(true, "geolocation")).toBe(false);
    // clipboard-read is denied for agent tabs: the OS clipboard often holds secrets.
    expect(browserPermissionDecision(true, "clipboard-read")).toBe(false);
    // A capability not in the denylist stays allowed for agent tabs.
    expect(browserPermissionDecision(true, "fullscreen")).toBe(true);
    // User webview (not owned): always passthrough.
    expect(browserPermissionDecision(false, "media")).toBe(true);
    expect(browserPermissionDecision(false, "clipboard-read")).toBe(true);
  });
});

describe("pure helpers", () => {
  it("keys tabs by workdir + tab_id with a NUL separator", () => {
    expect(tabKey("/a", "t1")).toBe("/a t1");
    // Different cores can mint the same tab_id; the workdir must disambiguate.
    expect(tabKey("/a", "t1")).not.toBe(tabKey("/b", "t1"));
  });

  it("never surfaces password / hidden / credential field values to the model", () => {
    expect(valueFor({ type: "password", value: "hunter2" }, "hunter2")).toBe("");
    expect(valueFor({ type: "hidden", value: "csrf-token" }, "")).toBe("");
    expect(valueFor({ autocomplete: "current-password", value: "x" }, "x")).toBe("");
    expect(valueFor({ autocomplete: "cc-number" }, "4111111111111111")).toBe("");
    // Ordinary inputs still surface their value so the model can read the page.
    expect(valueFor({ type: "text" }, "hello")).toBe("hello");
    expect(valueFor({ type: "email" }, "a@b.com")).toBe("a@b.com");
  });

  it("computes the center of a CDP box-model content quad", () => {
    expect(boxModelCenter({ model: { content: [10, 20, 30, 20, 30, 40, 10, 40] } })).toEqual([20, 30]);
    expect(boxModelCenter({ model: {} })).toBeUndefined();
    expect(boxModelCenter({})).toBeUndefined();
  });

  it("trims a DOM snapshot to visible interactable nodes", () => {
    const nodes = interactableNodesFromSnapshot(twoNodeSnapshot());
    expect(nodes).toHaveLength(2);
    expect(nodes[0]).toMatchObject({ backendNodeId: 100, role: "button", name: "Submit", bounds: [5, 6, 50, 20] });
    expect(nodes[1]).toMatchObject({ backendNodeId: 200, role: "link", name: "Release notes" });
  });

  it("reads nested link text by layout index without dropping repeated words or requiring text boxes", () => {
    const snapshot = {
      strings: ["HTML", "HEAD", "BODY", "A", "SPAN", "#text", "Model ", "Model", " release", "href", "/release"],
      documents: [{
        nodes: {
          nodeName: [0, 1, 2, 3, 5, 4, 5, 5],
          parentIndex: [-1, 0, 0, 2, 3, 3, 5, 3],
          backendNodeId: [10, 11, 12, 13, 14, 15, 16, 17],
          attributes: [[], [], [], [9, 10], [], [], [], []],
        },
        layout: {
          nodeIndex: [0, 2, 3, 4, 5, 6, 7],
          text: [-1, -1, -1, 6, -1, 7, 8],
          bounds: Array.from({ length: 7 }, () => [0, 0, 100, 20]),
        },
      }],
    };
    expect(interactableNodesFromSnapshot(snapshot)).toEqual([
      expect.objectContaining({ backendNodeId: 13, role: "link", name: "Model Model release" }),
    ]);
  });

  it("scrolls down one viewport when no wheel delta is given", () => {
    expect(wheelDeltas(undefined, undefined)).toEqual({ dx: 0, dy: 600 });
    expect(wheelDeltas(0, -120)).toEqual({ dx: 0, dy: -120 });
  });

  it("dispatches Enter as a real key, including when passed as a list", () => {
    expect(keyChord(["Enter"])).toEqual(["Enter"]);
    expect(keyDispatch("Enter")).toMatchObject({ key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" });
  });

  it("drops zero-area and non-interactable nodes", () => {
    const snapshot = {
      strings: ["DIV", "BUTTON", "SPAN"],
      documents: [
        {
          nodes: { nodeName: [0, 1, 2], backendNodeId: [1, 2, 3], attributes: [[], [], []] },
          layout: {
            nodeIndex: [0, 1, 2],
            bounds: [
              [0, 0, 100, 40], // div — not interactable
              [0, 0, 0, 0], // button — zero area, dropped
              [0, 0, 50, 50], // span — not interactable
            ],
          },
        },
      ],
    };
    expect(interactableNodesFromSnapshot(snapshot)).toHaveLength(0);
  });

  it("treats an explicit interactable role on a generic tag as interactable", () => {
    const snapshot = {
      strings: ["DIV", "role", "button"],
      documents: [
        {
          nodes: { nodeName: [0], backendNodeId: [9], attributes: [[1, 2]] },
          layout: { nodeIndex: [0], bounds: [[1, 2, 30, 30]] },
        },
      ],
    };
    const nodes = interactableNodesFromSnapshot(snapshot);
    expect(nodes).toHaveLength(1);
    expect(nodes[0]).toMatchObject({ backendNodeId: 9, role: "button" });
  });

  it("reads release-page text that is not itself a control", () => {
    const snapshot = buildSnapshot([
      { tag: "nav", children: [{ tag: "a", attrs: { href: "/home" }, text: "Home" }] },
      {
        tag: "main",
        children: [
          { tag: "h1", text: "MiMo V2.6" },
          { tag: "div", text: "Context length 256k. Parameters 7B." },
          {
            tag: "p",
            children: [
              { tag: "#text", text: "See the" },
              { tag: "a", attrs: { href: "/collection" }, text: "collection" },
              { tag: "#text", text: "for scores." },
            ],
          },
          { tag: "ol", children: [{ tag: "li", text: "First" }, { tag: "li", text: "Second" }] },
          {
            tag: "table",
            children: [
              { tag: "tr", children: [{ tag: "th", text: "Benchmark" }, { tag: "th", text: "Score" }] },
              {
                tag: "tr",
                children: [
                  { tag: "td", text: "MMLU" },
                  { tag: "td", children: [{ tag: "a", attrs: { href: "/mmlu" }, text: "85.2" }] },
                ],
              },
            ],
          },
        ],
      },
      { tag: "footer", children: [{ tag: "a", attrs: { href: "/privacy" }, text: "Privacy" }] },
    ]);
    const nodes = interactableNodesFromSnapshot(snapshot);
    const blocks = readableBlocksFromSnapshot(snapshot);
    const collection = nodes.find((node) => node.name === "collection");
    const score = nodes.find((node) => node.name === "85.2");

    expect(nodes.map((node) => node.name).sort()).toEqual(["85.2", "Home", "Privacy", "collection"]);
    expect(blocks.map((block) => block.kind)).toEqual(["heading", "paragraph", "paragraph", "list", "table"]);
    expect(blocks[0]).toMatchObject({ kind: "heading", level: 1, text: "MiMo V2.6" });
    expect(blocks[1]).toMatchObject({ text: "Context length 256k. Parameters 7B." });
    expect(blocks[2]).toMatchObject({
      text: "See the collection for scores.",
      links: [{ text: "collection", backendNodeId: collection?.backendNodeId }],
    });
    expect(blocks[3]).toMatchObject({ kind: "list", ordered: true, items: [{ text: "First" }, { text: "Second" }] });
    expect(blocks[4]).toMatchObject({
      header: [{ text: "Benchmark" }, { text: "Score" }],
      rows: [[{ text: "MMLU" }, { text: "85.2", backendNodeId: score?.backendNodeId }]],
    });
    expect(JSON.stringify(blocks)).not.toContain("Home");
    expect(JSON.stringify(blocks)).not.toContain("Privacy");
  });

  it("pages a long article on block boundaries", () => {
    const children = Array.from({ length: 25 }, (_, index) => ({
      tag: "p",
      text: `Paragraph ${index} stays readable.`,
    }));
    const blocks = readableBlocksFromSnapshot(buildSnapshot([{ tag: "main", children }]));
    expect(blocks).toHaveLength(25);

    const first = pageReadableContent(blocks, 0);
    expect(first.blocks).toHaveLength(20);
    expect(first.total).toBe(25);
    expect(first.nextOffset).toBe(20);

    const second = pageReadableContent(blocks, first.nextOffset ?? 0);
    expect(second.offset).toBe(20);
    expect(second.blocks.map((block) => block.text)).toEqual([
      "Paragraph 20 stays readable.",
      "Paragraph 21 stays readable.",
      "Paragraph 22 stays readable.",
      "Paragraph 23 stays readable.",
      "Paragraph 24 stays readable.",
    ]);
    expect(second.nextOffset).toBeUndefined();

    const past = pageReadableContent(blocks, 100);
    expect(past.blocks).toEqual([]);
    expect(past.offset).toBe(25);
    expect(past.nextOffset).toBeUndefined();
  });

  it("splits one long paragraph instead of returning it whole", () => {
    const text = Array.from({ length: 400 }, () => "word").join(" ");
    const blocks = readableBlocksFromSnapshot(buildSnapshot([{ tag: "p", text }]));
    expect(blocks.length).toBeGreaterThan(1);
    expect(blocks.every((block) => (block.text?.length ?? 0) <= 1600)).toBe(true);
    expect(blocks.map((block) => block.text).join(" ")).toBe(text);
  });
});

interface SnapshotSpec {
  tag: string;
  attrs?: Record<string, string>;
  text?: string;
  children?: SnapshotSpec[];
}

function buildSnapshot(roots: SnapshotSpec[]): Record<string, unknown> {
  const strings: string[] = [];
  const intern = (value: string): number => {
    const existing = strings.indexOf(value);
    if (existing >= 0) return existing;
    strings.push(value);
    return strings.length - 1;
  };
  const nodeName: number[] = [];
  const parentIndex: number[] = [];
  const backendNodeId: number[] = [];
  const attributes: number[][] = [];
  const layoutNodeIndex: number[] = [];
  const layoutText: number[] = [];
  const bounds: number[][] = [];
  let backend = 1;
  let y = 0;
  const add = (node: SnapshotSpec, parent: number): void => {
    const index = nodeName.length;
    nodeName.push(intern(node.tag === "#text" ? "#text" : node.tag.toUpperCase()));
    parentIndex.push(parent);
    backendNodeId.push(backend);
    backend += 1;
    const attrs: number[] = [];
    for (const [key, value] of Object.entries(node.attrs ?? {})) attrs.push(intern(key), intern(value));
    attributes.push(attrs);
    const box = [0, y, 160, 18];
    y += 18;
    layoutNodeIndex.push(index);
    if (node.tag === "#text") {
      layoutText.push(intern(node.text ?? ""));
      bounds.push(box);
      return;
    }
    layoutText.push(-1);
    bounds.push(box);
    if (node.text) add({ tag: "#text", text: node.text }, index);
    for (const child of node.children ?? []) add(child, index);
  };
  for (const root of roots) add(root, -1);
  return {
    strings,
    documents: [{
      nodes: { nodeName, parentIndex, backendNodeId, attributes },
      layout: { nodeIndex: layoutNodeIndex, text: layoutText, bounds },
    }],
  };
}

describe("BrowserHostCoordinator preview surface accessors", () => {
  it("lays out a new hidden page before navigation and preserves an existing tab's geometry", async () => {
    const harness = makeHarness();
    await harness.coordinator.handleServerRequest(serverRequest("browser/open_tab", {
      workdir: "/repo", tab_id: "t1", initial_url: "https://example.com/",
    }));
    const view = harness.views[0];
    const viewport = view.loadedBounds[0];
    expect(viewport.width).toBeGreaterThanOrEqual(1024);
    expect(viewport.height).toBeGreaterThanOrEqual(600);
    expect(harness.coordinator.tabBounds("/repo", "t1")).toEqual(viewport);

    const preview = { x: 0, y: 0, width: 320, height: 200 };
    harness.coordinator.mountTabOnWindow("/repo", "t1", new FakeWindow(), preview, 0.25);
    await openTab(harness, "/repo", "t1");
    expect(view.getBounds()).toEqual(preview);
  });

  it("reports tab bounds for a live tab and undefined once it is gone", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    harness.views[0].setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    expect(harness.coordinator.tabBounds("/repo", "t1")).toEqual({
      x: 0,
      y: 0,
      width: 1280,
      height: 800,
    });

    expect(harness.coordinator.tabBounds("/repo", "missing")).toBeUndefined();
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/close_tab", { workdir: "/repo", tab_id: "t1" }, "close-1"),
    );
    expect(harness.coordinator.tabBounds("/repo", "t1")).toBeUndefined();
  });

  it("mounts a tab onto an observation window with zoom-fit and restores it on unmount", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    const hostWindow = harness.hostWindows[0];
    const pip = new FakeWindow();

    const restore = harness.coordinator.mountTabOnWindow(
      "/repo",
      "t1",
      pip,
      { x: 0, y: 0, width: 260, height: 163 },
      0.203,
    );
    expect(restore).toEqual({ x: 0, y: 0, width: 1280, height: 800 });
    expect(pip.added).toContain(view);
    expect(hostWindow.removed).toContain(view);
    expect(view.zoomFactor).toBeCloseTo(0.203);
    expect(view.boundsSet).toEqual({ x: 0, y: 0, width: 260, height: 163 });
    expect(view.visibleState).toBe(true);

    expect(harness.coordinator.mountTabOnWindow("/repo", "missing", pip, { x: 0, y: 0, width: 1, height: 1 }, 1)).toBeUndefined();

    harness.coordinator.unmountTabIfOwner(
      "/repo",
      "t1",
      pip.contentView,
      { x: 0, y: 0, width: 1280, height: 800 },
    );
    expect(view.zoomFactor).toBe(1);
    expect(pip.removed).toContain(view);
    expect(view.boundsSet).toEqual({ x: 0, y: 0, width: 1280, height: 800 });
  });

  it("stops painting scrollbars on the watch-only card and restores them in the panel", async () => {
    expect(spectatorScrollbarCSS(true)).toContain("scrollbar-width: none");
    expect(spectatorScrollbarCSS(false)).not.toMatch(/width:\s*0/);
    expect(spectatorScrollbarCSS(false)).toContain("scrollbar-color: transparent transparent");

    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    const pip = new FakeWindow();
    harness.coordinator.mountTabOnWindow("/repo", "t1", pip, { x: 0, y: 0, width: 260, height: 163 }, 0.203);
    await flushMicrotasks();
    expect([...view.insertedCSS.values()][0]).toContain("scrollbar-width: none");

    view.scriptResult = false;
    harness.coordinator.mountTabOnWindow("/repo", "t1", pip, { x: 0, y: 0, width: 260, height: 163 }, 0.203);
    await flushMicrotasks();
    const classic = [...view.insertedCSS.values()];
    expect(classic).toHaveLength(1);
    expect(classic[0]).not.toMatch(/width:\s*0/);

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: true }, "vis-scroll"),
    );
    await flushMicrotasks();
    expect(view.insertedCSS.size).toBe(0);
    expect(view.zoomFactor).toBe(1);
  });

  it("normalizes zoom when a takeover adopts a PiP-mounted tab, and refuses to yank it back", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    const pip = new FakeWindow();
    harness.coordinator.mountTabOnWindow("/repo", "t1", pip, { x: 0, y: 0, width: 260, height: 163 }, 0.203);

    // Visibility takeover adopts the view onto the main window.
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/set_visibility", { workdir: "/repo", tab_id: "t1", visible: true }, "vis-1"),
    );
    expect(view.zoomFactor).toBe(1);
    expect(harness.mainWindow.added).toContain(view);

    // The PiP's unmount must not tear the adopted view out of the panel.
    harness.coordinator.unmountTabIfOwner(
      "/repo",
      "t1",
      pip.contentView,
      { x: 0, y: 0, width: 1280, height: 800 },
    );
    expect(harness.mainWindow.removed).not.toContain(view);
  });

  it("refits a mounted tab only while the observation window owns it", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    const pip = new FakeWindow();
    harness.coordinator.mountTabOnWindow("/repo", "t1", pip, { x: 0, y: 0, width: 260, height: 163 }, 0.203);

    harness.coordinator.relayoutMountedTab(
      "/repo",
      "t1",
      pip.contentView,
      { x: 10, y: 5, width: 300, height: 200 },
      0.234,
    );
    expect(view.zoomFactor).toBeCloseTo(0.234);
    expect(view.boundsSet).toEqual({ x: 10, y: 5, width: 300, height: 200 });

    const stale = new FakeWindow();
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    harness.coordinator.relayoutMountedTab(
      "/repo",
      "t1",
      stale.contentView,
      { x: 0, y: 0, width: 1, height: 1 },
      1,
    );
    expect(view.boundsSet).toEqual({ x: 0, y: 0, width: 1280, height: 800 });
  });

  it("emits navigation events for the observation surface chrome", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    const seen: Array<{ workdir: string; tabID: string; url: string }> = [];
    harness.coordinator.addNavigateListener((workdir, tabID, url) => {
      seen.push({ workdir, tabID, url });
    });
    harness.views[0].emitNavigate("https://next.test/path");
    expect(seen).toEqual([{ workdir: "/repo", tabID: "t1", url: "https://next.test/path" }]);
  });

  it("reads surface meta for a live tab only", async () => {
    const harness = makeHarness();
    await openTab(harness, "/repo", "t1");
    harness.views[0].url = "https://meta.test/page";
    harness.views[0].title = "Meta";
    expect(harness.coordinator.tabSurfaceMeta("/repo", "t1")).toEqual({
      url: "https://meta.test/page",
      title: "Meta",
    });
    expect(harness.coordinator.tabSurfaceMeta("/repo", "missing")).toBeUndefined();
  });

  it("emits interaction hints after click, scroll, and type dispatch", async () => {
    const harness = makeHarness();
    const hints: Array<{ workdir: string; tabID: string; hint: unknown }> = [];
    harness.coordinator.addInteractionListener((workdir, tabID, hint) => {
      hints.push({ workdir, tabID, hint });
    });
    await openTab(harness, "/repo", "t1");
    const view = harness.views[0];

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/cdp", { workdir: "/repo", tab_id: "t1", method: "click", params: { x: 40, y: 60 } }, "click-1"),
    );
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/cdp", { workdir: "/repo", tab_id: "t1", method: "scroll", params: { x: 10, y: 10, dx: 0, dy: 240 } }, "scroll-1"),
    );

    // type needs an observe-built node map, then resolves the node center.
    view.responders.set("DOMSnapshot.captureSnapshot", () => twoNodeSnapshot());
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/cdp", { workdir: "/repo", tab_id: "t1", method: "observe", params: { screenshot: false } }, "obs-1"),
    );
    view.responders.set("DOM.getBoxModel", () => ({ model: { content: [10, 20, 30, 20, 30, 40, 10, 40] } }));
    await harness.coordinator.handleServerRequest(
      serverRequest("browser/cdp", { workdir: "/repo", tab_id: "t1", method: "type", params: { node_id: 1, text: "hi" } }, "type-1"),
    );

    expect(hints).toEqual([
      { workdir: "/repo", tabID: "t1", hint: { kind: "click", x: 40, y: 60 } },
      { workdir: "/repo", tabID: "t1", hint: { kind: "scroll", x: 10, y: 10, direction: "down" } },
      { workdir: "/repo", tabID: "t1", hint: { kind: "type", x: 20, y: 30 } },
    ]);
  });

  it("notifies tab-closed listeners on close_tab and workdir teardown", async () => {
    const harness = makeHarness();
    const closed: Array<{ workdir: string; tabID: string }> = [];
    harness.coordinator.addTabClosedListener((workdir, tabID) => {
      closed.push({ workdir, tabID });
    });
    await openTab(harness, "/repo", "t1");
    await openTab(harness, "/repo", "t2");

    await harness.coordinator.handleServerRequest(
      serverRequest("browser/close_tab", { workdir: "/repo", tab_id: "t1" }, "close-1"),
    );
    expect(closed).toEqual([{ workdir: "/repo", tabID: "t1" }]);

    harness.coordinator.onClientTorndown("/repo");
    expect(closed).toEqual([
      { workdir: "/repo", tabID: "t1" },
      { workdir: "/repo", tabID: "t2" },
    ]);
  });
});
