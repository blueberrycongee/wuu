import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ActivitySession, ServerEvent } from "../shared/protocol";
import type { CUANativePiPEvent } from "./cuaFrameStreams";
import type { WindowRegistry } from "./windowRegistry";
import {
  ObservationCoordinator,
  activityControlMethod,
  pipVisibleForActivity,
  activityVisibleForThread,
  frameStreamRetryDelay,
  nativePiPInitialBounds,
  browserPiPInitialBounds,
  observationActivityFromServerEvent,
  observationKey,
} from "./cuaActivityWindows";

function activity(overrides: Partial<ActivitySession> = {}): ActivitySession {
  return {
    id: "activity-1",
    kind: "cua",
    thread_id: "thread-1",
    workdir: "/repo",
    plugin_id: "cua-mac",
    target: "com.apple.TextEdit",
    process_id: 42,
    window_id: 99,
    state: "active",
    controller: "agent",
    created_at: "2026-07-10T10:00:00Z",
    updated_at: "2026-07-10T10:00:01Z",
    ...overrides,
  };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("CUA native picture-in-picture", () => {
  it("scopes the native PiP to the active session", () => {
    expect(activityVisibleForThread("thread-1", "thread-1")).toBe(true);
    expect(activityVisibleForThread("thread-1", "thread-2")).toBe(false);
    expect(activityVisibleForThread("thread-1", undefined)).toBe(false);
  });

  it("places the PiP inside the Wuu window corner and active work area", () => {
    expect(nativePiPInitialBounds(
      { x: 100, y: 80, width: 1200, height: 800 },
      { x: 0, y: 0, width: 1440, height: 900 },
    )).toEqual({ x: 1028, y: 92, width: 260, height: 170 });
    expect(nativePiPInitialBounds(undefined, { x: 1440, y: 0, width: 1200, height: 900 }))
      .toEqual({ x: 2356, y: 24, width: 260, height: 170 });
    expect(browserPiPInitialBounds(
      { x: 100, y: 80, width: 1200, height: 800 },
      { x: 0, y: 0, width: 1440, height: 900 },
    )).toEqual({ x: 1026, y: 606, width: 250, height: 250 });
  });

  it("backs off native capture restarts", () => {
    expect(frameStreamRetryDelay(1)).toBe(2000);
    expect(frameStreamRetryDelay(2)).toBe(4000);
    expect(frameStreamRetryDelay(3)).toBe(8000);
    expect(frameStreamRetryDelay(6)).toBe(16000);
  });

  it("accepts CUA and browser lifecycle notifications", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: { method: "activity/updated", params: activity() },
    };
    expect(observationActivityFromServerEvent(event)?.id).toBe("activity-1");
    expect(observationActivityFromServerEvent({ ...event, message: { method: "activity/updated", params: activity({ kind: "browser" }) } })?.id)
      .toBe("activity-1");
  });

  it("maps Activity controls onto RPC methods", () => {
    expect(activityControlMethod("takeover")).toBe("activity/takeover");
    expect(activityControlMethod("release")).toBe("activity/release");
    expect(activityControlMethod("stop")).toBe("activity/stop");
  });

  it("rebinds native preview when resolved process or window changes", () => {
    expect(observationKey(activity({ process_id: 42 }))).not.toBe(observationKey(activity({ process_id: 43 })));
    expect(observationKey(activity({ window_id: 99 }))).not.toBe(observationKey(activity({ window_id: 100 })));
  });

  it("routes native control to the owning workdir and waits for authoritative state", async () => {
    let emit: ((event: CUANativePiPEvent) => void) | undefined;
    const updateActivity = vi.fn();
    const control = vi.fn(async (current: ActivitySession) => ({ ...current, controller: "user" as const, state: "user_controlled" as const, updated_at: "2026-07-10T10:00:02Z" }));
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry, undefined,
      (_activity, _key, sink) => {
        emit = sink.onEvent;
        return { start: vi.fn(), setVisible: vi.fn(), setLive: vi.fn(), updateActivity, animateInteraction: vi.fn(), stop: vi.fn() };
      }, control,
    );
    coordinator.setActiveThread("thread-1");
    coordinator.update(activity({ plugin_id: "community-driver", process_id: 0 }));
    expect(emit).toBeUndefined();
    coordinator.update(activity({ plugin_id: "community-driver" }));
    emit?.({ event: "control", action: "takeover" });
    await vi.waitFor(() => expect(updateActivity).toHaveBeenLastCalledWith(expect.objectContaining({ controller: "user" })));
    expect(control).toHaveBeenCalledWith(expect.objectContaining({ workdir: "/repo", id: "activity-1" }), "takeover");
  });

  it("waits for the outgoing helper to close and coalesces replacements", () => {
    const events: string[] = [];
    const helpers: Array<{ target: string; finishStop?: () => void }> = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (next) => {
        const helper: { target: string; finishStop?: () => void } = { target: next.target ?? "" };
        helpers.push(helper);
        return {
          start: () => { events.push(`start:${helper.target}`); },
          setVisible: () => undefined,
          animateInteraction: () => undefined,
          stop: (onStopped?: () => void) => {
            events.push(`stop:${helper.target}`);
            helper.finishStop = onStopped;
          },
        };
      },
    );
    coordinator.setActiveThread("thread-1");
    coordinator.update(activity({ target: "app-a" }));
    coordinator.update(activity({ target: "app-b", updated_at: "2026-07-10T10:00:02Z" }));
    coordinator.update(activity({ target: "app-c", updated_at: "2026-07-10T10:00:03Z" }));

    expect(events).toEqual(["start:app-a", "stop:app-a"]);
    expect(helpers).toHaveLength(1);

    helpers[0].finishStop?.();
    expect(events).toEqual(["start:app-a", "stop:app-a", "start:app-c"]);
    expect(helpers.map((helper) => helper.target)).toEqual(["app-a", "app-c"]);
  });

  it("does not start an update while a user-closed helper is still stopping", () => {
    vi.useFakeTimers();
    vi.setSystemTime("2026-07-10T10:00:01.500Z");
    const events: string[] = [];
    const helpers: Array<{ target: string; finishStop?: () => void }> = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (next) => {
        const helper: { target: string; finishStop?: () => void } = { target: next.target ?? "" };
        helpers.push(helper);
        return {
          start: () => { events.push(`start:${helper.target}`); },
          setVisible: () => undefined,
          animateInteraction: () => undefined,
          stop: (onStopped?: () => void) => {
            events.push(`stop:${helper.target}`);
            helper.finishStop = onStopped;
          },
        };
      },
    );
    const initial = activity({ target: "app-a" });
    coordinator.setActiveThread("thread-1");
    coordinator.update(initial);
    const testCoordinator = coordinator as unknown as {
      handlePiPEvent: (key: string, event: CUANativePiPEvent) => void;
    };
    testCoordinator.handlePiPEvent(observationKey(initial), { event: "user_close" });
    coordinator.update(activity({ target: "app-b", updated_at: "2026-07-10T10:00:02Z" }));

    expect(events).toEqual(["start:app-a", "stop:app-a"]);
    expect(helpers).toHaveLength(1);

    helpers[0].finishStop?.();
    expect(events).toEqual(["start:app-a", "stop:app-a", "start:app-b"]);
    expect(helpers.map((helper) => helper.target)).toEqual(["app-a", "app-b"]);
  });
});

describe("browser observation surface", () => {
  function browserActivity(overrides: Partial<ActivitySession> = {}): ActivitySession {
    return {
      id: "activity-1",
      kind: "browser",
      thread_id: "thread-1",
      workdir: "/repo",
      plugin_id: "embedded-browser",
      target: "tab-1",
      state: "background_controlled",
      controller: "agent",
      created_at: "2026-07-10T10:00:00Z",
      updated_at: "2026-07-10T10:00:01Z",
      ...overrides,
    };
  }

  type FakeSurface = {
    start: ReturnType<typeof vi.fn>;
    setVisible: ReturnType<typeof vi.fn>;
    setLive: ReturnType<typeof vi.fn>;
    updateActivity: ReturnType<typeof vi.fn>;
    animateInteraction: ReturnType<typeof vi.fn>;
    stop: (onStopped?: () => void) => void;
  };

  function makeCoordinator(): {
    coordinator: ObservationCoordinator;
    surfaces: FakeSurface[];
    stops: string[];
  } {
    const surfaces: FakeSurface[] = [];
    const stops: string[] = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (next) => {
        const surface: FakeSurface = {
          start: vi.fn(),
          setVisible: vi.fn(),
          setLive: vi.fn(),
          updateActivity: vi.fn(),
          animateInteraction: vi.fn(),
          stop: (onStopped?: () => void) => {
            stops.push(next.target ?? "");
            onStopped?.();
          },
        };
        surfaces.push(surface);
        return surface;
      },
    );
    return { coordinator, surfaces, stops };
  }

  it("passes the owning browser activity when docking its preview", () => {
    const onExpand = vi.fn();
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      () => ({
        start: vi.fn(),
        setVisible: vi.fn(),
        animateInteraction: vi.fn(),
        stop: vi.fn(),
      }),
    );
    coordinator.setBrowserExpandHandler(onExpand);
    const current = browserActivity({ thread_id: "thread-2", target: "tab-2" });
    coordinator.setActiveThread("thread-2");
    coordinator.update(current);

    const internal = coordinator as unknown as {
      handlePiPEvent: (key: string, event: CUANativePiPEvent) => void;
    };
    internal.handlePiPEvent(observationKey(current), { event: "expand" });

    expect(onExpand).toHaveBeenCalledWith(current);
  });

  it("follows window moves but waits for fresh column measurements during resize", async () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    const setHostLayout = vi.fn();
    Object.assign(surfaces[0], { setHostLayout });
    let content = { x: 100, y: 100, width: 1000, height: 800 };
    const win = Object.assign(new EventEmitter(), {
      isDestroyed: () => false,
      getContentBounds: () => content,
      webContents: { getZoomFactor: () => 1 },
    });
    const client = { host: { x: 200, y: 40, width: 800, height: 700 }, obstacles: [] };
    coordinator.setBrowserPiPHostLayout(1, win, client);
    setHostLayout.mockClear();
    content = { ...content, x: 80, width: 1020, height: 820 };
    win.emit("resize");
    win.emit("move");
    expect(setHostLayout).not.toHaveBeenCalled();
    coordinator.setBrowserPiPHostLayout(1, win, {
      ...client, host: { ...client.host, width: 820, height: 720 },
    });
    expect(setHostLayout).toHaveBeenCalledTimes(1);
    expect(setHostLayout.mock.calls[0][0].host).toEqual({ x: 280, y: 140, width: 820, height: 720 });
    content = { ...content, x: 60, y: 80 };
    win.emit("move");
    expect(setHostLayout).toHaveBeenCalledTimes(2);
    expect(setHostLayout.mock.calls[1][0].host).toEqual({ x: 260, y: 120, width: 820, height: 720 });
    await coordinator.shutdown();
    expect(win.listenerCount("move")).toBe(0);
  });

  it("starts the surface for a browser activity and hides it while the user watches the real page", () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    expect(surfaces).toHaveLength(1);
    expect(surfaces[0].start).toHaveBeenCalledTimes(1);
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);

    // Asking to show the page does not dismiss the card. Docking it does.
    coordinator.update(browserActivity({ state: "foreground_controlled", controller: "agent", updated_at: "2026-07-10T10:00:02Z" }));
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    coordinator.setBrowserInPanel(() => true);
    coordinator.refreshBrowserPresentation();
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(false);
    expect(surfaces).toHaveLength(1);

    coordinator.setBrowserInPanel(() => false);
    coordinator.refreshBrowserPresentation();
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
  });

  it("hides the mirror while the same page is in the workspace panel", () => {
    expect(pipVisibleForActivity(browserActivity(), false)).toBe(true);
    expect(pipVisibleForActivity(browserActivity(), true)).toBe(false);
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.setBrowserInPanel(() => true);
    coordinator.update(browserActivity());
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(false);
    coordinator.setBrowserInPanel(() => false);
    coordinator.refreshBrowserPresentation();
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
  });

  it("keeps a stopped surface frozen across reconciliation and resumes only a new activity", () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    coordinator.update(browserActivity({ state: "stopped", controller: "none", updated_at: "2026-07-10T10:00:02Z" }));
    expect(surfaces[0].setLive).toHaveBeenLastCalledWith(false);
    expect(surfaces).toHaveLength(1); // kept, CUA observation semantics

    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:03Z" }));
    coordinator.setActiveThread(undefined);
    coordinator.setActiveThread("thread-1");
    expect(surfaces[0].setLive).toHaveBeenLastCalledWith(false);
    coordinator.update(browserActivity({ id: "next-activity", updated_at: "2026-07-10T10:00:04Z" }));
    expect(surfaces[1].setLive).toHaveBeenLastCalledWith(true);
  });

  it("reuses a retargetable browser surface across tab activity identities", () => {
    const { coordinator, surfaces, stops } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    const retarget = vi.fn();
    Object.assign(surfaces[0], { retarget });
    const next = browserActivity({ id: "activity-2", target: "tab-2", updated_at: "2026-07-10T10:00:02Z" });
    coordinator.update(next);
    expect(stops).toEqual([]);
    expect(surfaces).toHaveLength(1);
    expect(retarget).toHaveBeenCalledWith(next, expect.any(Object));
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    // Controls from the reused window must address the new observation key.
    retarget.mock.calls[0][1].onEvent({ event: "user_close" });
    expect(stops).toHaveLength(1);
  });

  it("swaps a non-retargetable surface through the serialized replacement", () => {
    const { coordinator, surfaces, stops } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity({ target: "tab-1" }));
    coordinator.update(browserActivity({ target: "tab-2", updated_at: "2026-07-10T10:00:02Z" }));
    expect(stops).toEqual(["tab-1"]);
    expect(surfaces).toHaveLength(2);
    expect(surfaces[1].start).toHaveBeenCalledTimes(1);
  });

  it("tears the surface down when the tab is gone and does not retry", () => {
    vi.useFakeTimers();
    const { coordinator, surfaces, stops } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    const testCoordinator = coordinator as unknown as {
      handlePiPGone: (key: string) => void;
    };
    testCoordinator.handlePiPGone(observationKey(browserActivity()));
    expect(stops).toEqual(["tab-1"]);
    vi.advanceTimersByTime(60_000);
    expect(surfaces).toHaveLength(1); // no retry respawn
  });

  it("drops the surface and observation on workdir teardown", () => {
    const { coordinator, surfaces, stops } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    coordinator.dropWorkdir("/repo");
    expect(stops).toEqual(["tab-1"]);
    // A later reconcile/update for the same workdir's stale activity cannot
    // resurrect it — the observation was forgotten. A genuinely new update
    // (same timestamps as a fresh event) starts a new surface as usual.
    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:05Z" }));
    expect(surfaces).toHaveLength(2);
  });
});


it("shutdown waits for preview exit and prevents a queued replacement from starting", async () => {
  let stopped: (() => void) | undefined;
  const starts = vi.fn();
  const coordinator = new ObservationCoordinator(
    { mainWindow: () => undefined } as unknown as WindowRegistry,
    undefined,
    () => ({
      start: starts, setVisible: vi.fn(), setLive: vi.fn(), animateInteraction: vi.fn(),
      stop: (callback?: () => void) => { stopped = callback; },
    }),
  );
  coordinator.setActiveThread("thread-1");
  coordinator.update(activity());
  coordinator.update(activity({ process_id: 43, updated_at: "2026-07-10T10:00:02Z" }));
  let finished = false;
  const done = coordinator.shutdown().then(() => { finished = true; });
  await Promise.resolve();
  expect(finished).toBe(false);
  stopped?.();
  await done;
  coordinator.update(activity({ updated_at: "2026-07-10T10:00:03Z" }));
  expect(starts).toHaveBeenCalledTimes(1);
});
