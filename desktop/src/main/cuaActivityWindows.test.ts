import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ActivitySession, ServerEvent } from "../shared/protocol";
import type { CUANativePiPEvent } from "./cuaFrameStreams";
import type { WindowRegistry } from "./windowRegistry";
import type { ObservationPiPEventSink } from "./cuaActivityWindows";
import {
  ObservationCoordinator,
  activityControlMethod,
  pipVisibleForActivity,
  frameStreamRetryDelay,
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
  it("places the PiP inside the Wuu window corner and active work area", () => {
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

    vi.useFakeTimers();
    let fail: (message: string) => void = () => undefined;
    const retarget = vi.fn();
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry, undefined,
      (_activity, _key, sink) => {
        fail = sink.onFailure;
        return {
          start: vi.fn(), setVisible: vi.fn(), setLive: vi.fn(), updateActivity: vi.fn(),
          animateInteraction: vi.fn(), suspendContent: vi.fn(), retarget,
          stop: (onStopped?: () => void) => onStopped?.(),
        };
      },
    );
    coordinator.setActiveThread("thread-1");
    coordinator.update(activity({ execution_id: "turn-1" }));
    for (let attempt = 1; attempt <= 4; attempt++) {
      fail("Screen Recording permission required");
      vi.advanceTimersByTime(frameStreamRetryDelay(attempt));
    }
    expect(retarget).toHaveBeenCalledTimes(3);
    coordinator.update(activity({ execution_id: "turn-1", updated_at: "2026-07-10T10:00:02Z" }));
    vi.advanceTimersByTime(60_000);
    expect(retarget).toHaveBeenCalledTimes(3);

    coordinator.showForThread("thread-1");
    expect(retarget).toHaveBeenCalledTimes(4);
    fail("Permission still required");
    coordinator.update(activity({ execution_id: "turn-2", updated_at: "2026-07-10T10:00:03Z" }));
    expect(retarget).toHaveBeenCalledTimes(5);
    vi.advanceTimersByTime(60_000);
    expect(retarget).toHaveBeenCalledTimes(5);
    for (let attempt = 1; attempt <= 4; attempt++) {
      fail("Permission still required");
      vi.advanceTimersByTime(frameStreamRetryDelay(attempt));
    }
    expect(retarget).toHaveBeenCalledTimes(8);

    coordinator.showForThread("thread-1");
    const failFirst = fail;
    failFirst("Retry while another conversation is in view");
    // Another thread's task gets a card of its own instead of the retrying one.
    coordinator.setActiveThread("thread-2");
    coordinator.update(activity({ thread_id: "thread-2", execution_id: "other-turn", target: "other-app" }));
    expect(retarget).toHaveBeenCalledTimes(9);
    vi.advanceTimersByTime(frameStreamRetryDelay(1));
    expect(retarget).toHaveBeenCalledTimes(10);
    void coordinator.shutdown();
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

  it("replaces a card that cannot retarget without waiting for the outgoing helper", () => {
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

    // One helper process streams every preview, so a replacement starts at once.
    expect(events).toEqual(["start:app-a", "stop:app-a", "start:app-b", "stop:app-b", "start:app-c"]);
    helpers[0].finishStop?.();
    helpers[1].finishStop?.();
    expect(events).toEqual(["start:app-a", "stop:app-a", "start:app-b", "stop:app-b", "start:app-c"]);
    expect(helpers.map((helper) => helper.target)).toEqual(["app-a", "app-b", "app-c"]);
  });

  it("keeps a user-closed preview hidden across activity updates until explicitly reopened", () => {
    vi.useFakeTimers();
    vi.setSystemTime("2026-07-10T10:00:01.500Z");
    const events: string[] = [];
    const helpers: Array<{ target: string; finishStop?: () => void }> = [];
    const sinks: ObservationPiPEventSink[] = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (next, _key, sink) => {
        sinks.push(sink);
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
    sinks[0].onEvent({ event: "user_close" });
    coordinator.update(activity({ target: "app-b", updated_at: "2026-07-10T10:00:02Z" }));

    expect(events).toEqual(["start:app-a"]);
    expect(helpers).toHaveLength(1);
    coordinator.setActiveThread(undefined);
    coordinator.setActiveThread("thread-1");
    expect(events).toEqual(["start:app-a"]);
    coordinator.showForThread("thread-1");
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
    setTurnCompleted: ReturnType<typeof vi.fn>;
    updateActivity: ReturnType<typeof vi.fn>;
    animateInteraction: ReturnType<typeof vi.fn>;
    stop: (onStopped?: () => void) => void;
  };

  function makeCoordinator(snapshot?: (threadID: string) => Promise<ActivitySession[]>): {
    coordinator: ObservationCoordinator;
    surfaces: FakeSurface[];
    sinks: ObservationPiPEventSink[];
    stops: string[];
  } {
    const surfaces: FakeSurface[] = [];
    const sinks: ObservationPiPEventSink[] = [];
    const stops: string[] = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      snapshot,
      (next, _key, sink) => {
        sinks.push(sink);
        const surface: FakeSurface = {
          start: vi.fn(),
          setVisible: vi.fn(),
          setLive: vi.fn(),
          setTurnCompleted: vi.fn(),
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
    return { coordinator, surfaces, sinks, stops };
  }

  it("shows completion only for a successful final turn on the visible owning browser", () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity({ state: "foreground_controlled" }));
    const completed = surfaces[0].setTurnCompleted;
    const event: ServerEvent = { workdir: "/repo", kind: "notification", message: {
      method: "turn/completed", params: { thread_id: "thread-1", turn: { id: "turn-1", status: "completed" } },
    } };
    coordinator.handleServerEvent({ ...event, workdir: "/other" });
    coordinator.handleServerEvent({ ...event, message: { ...event.message, params: { thread_id: "other" } } });
    expect(completed).not.toHaveBeenCalled();
    coordinator.handleServerEvent(event);
    expect(completed).toHaveBeenLastCalledWith(true);
    for (const params of [
      { turn: { status: "interrupted" } },
      { turn: { status: "failed" } },
      { turn: { status: "completed" }, awaiting_auto_continuation: true },
      { turn: { status: "completed" }, truncated: true },
    ]) {
      coordinator.handleServerEvent({ ...event, message: { ...event.message, params: { thread_id: "thread-1", ...params } } });
      expect(completed).toHaveBeenLastCalledWith(false);
    }
    for (const method of ["turn/started", "turn/error"]) {
      coordinator.handleServerEvent({ ...event, message: { ...event.message, method } });
      expect(completed).toHaveBeenLastCalledWith(false);
    }
    completed.mockClear();
    coordinator.update(browserActivity({ state: "background_controlled", updated_at: "2026-07-10T10:00:02Z" }));
    coordinator.handleServerEvent(event);
    expect(completed).toHaveBeenLastCalledWith(true);
    completed.mockClear();
    coordinator.setBrowserInPanel(() => true);
    coordinator.handleServerEvent(event);
    expect(completed).not.toHaveBeenCalled();
    coordinator.setBrowserInPanel(() => false);
    // A card stays up while another conversation is in view, completion included.
    coordinator.setActiveThread("other");
    coordinator.handleServerEvent(event);
    expect(completed).toHaveBeenLastCalledWith(true);
    void coordinator.shutdown();
  });

  it("passes the owning browser activity when docking its preview", () => {
    const onExpand = vi.fn();
    let sink: ObservationPiPEventSink | undefined;
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (_next, _key, next) => (sink = next, {
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

    sink?.onEvent({ event: "expand" });

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
      isVisible: () => true,
      isMinimized: () => false,
      isFocused: () => true,
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

  it("previews default browser work across control-state updates until the page is docked", () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    expect(surfaces).toHaveLength(1);
    expect(surfaces[0].start).toHaveBeenCalledTimes(1);
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);

    // A subsequent ordinary browser action returns to background control.
    coordinator.update(browserActivity({ state: "foreground_controlled", controller: "agent", updated_at: "2026-07-10T10:00:02Z" }));
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:03Z" }));
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
    expect(pipVisibleForActivity(browserActivity({ state: "foreground_controlled" }), false)).toBe(true);
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

  it.each(["thread-2", undefined])("keeps a card visible while another conversation is in view (%s)", async (threadID) => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);

    coordinator.setActiveThread(threadID);
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    coordinator.setBrowserInPanel(() => false);
    coordinator.refreshBrowserPresentation();
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);

    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:02Z" }));
    coordinator.refreshBrowserPresentation();
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    expect(surfaces).toHaveLength(1);

    coordinator.setActiveThread("thread-1");
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    expect(surfaces).toHaveLength(1);
    await coordinator.shutdown();
  });

  it("keeps a stopped surface frozen across reconciliation and resumes only a new activity", () => {
    const { coordinator, surfaces } = makeCoordinator();
    coordinator.setActiveThread("thread-1");
    coordinator.update(browserActivity());
    const preciseUpdate = browserActivity({ updated_at: "2026-07-10T10:00:01.000000002Z" });
    coordinator.update(preciseUpdate);
    expect(surfaces[0].updateActivity).toHaveBeenLastCalledWith(preciseUpdate);
    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:01.000000001Z" }));
    expect(surfaces[0].updateActivity).toHaveBeenLastCalledWith(preciseUpdate);
    coordinator.update(browserActivity({ state: "stopped", controller: "none", updated_at: "2026-07-10T10:00:02Z" }));
    expect(surfaces[0].setLive).toHaveBeenLastCalledWith(false);
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(true);
    expect(surfaces).toHaveLength(1); // kept, CUA observation semantics

    coordinator.update(browserActivity({ updated_at: "2026-07-10T10:00:03Z" }));
    expect(surfaces[0].setLive).toHaveBeenLastCalledWith(false);
    expect(surfaces[0].updateActivity).toHaveBeenLastCalledWith(expect.objectContaining({ state: "stopped", controller: "none" }));
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
    expect(stops).toHaveLength(0);
    expect(surfaces[0].setVisible).toHaveBeenLastCalledWith(false);
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

  it("does not reconcile a gone target until its source changes or the user retries", async () => {
    vi.useFakeTimers();
    let latest = browserActivity({ execution_id: "turn-1" });
    const { coordinator, surfaces, sinks, stops } = makeCoordinator(async () => [latest]);
    coordinator.setActiveThread("thread-1");
    coordinator.update(latest);
    sinks[0].onGone();
    expect(stops).toEqual(["tab-1"]);
    const unrelated: ServerEvent = { workdir: "/repo", kind: "notification", message: {
      method: "turn/started", params: { thread_id: "thread-1" },
    } };
    coordinator.handleServerEvent(unrelated);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(surfaces).toHaveLength(1);
    coordinator.setActiveThread(undefined);
    coordinator.setActiveThread("thread-1");
    await vi.advanceTimersByTimeAsync(0);
    expect(surfaces).toHaveLength(1);

    latest = browserActivity({ execution_id: "turn-1", target: "tab-2", updated_at: "2026-07-10T10:00:02Z" });
    coordinator.update(latest);
    expect(surfaces).toHaveLength(2);
    sinks[1].onGone();
    coordinator.update(browserActivity({ execution_id: "turn-1" }));
    coordinator.update({ ...latest, updated_at: "2026-07-10T10:00:02.5Z" });
    expect(surfaces).toHaveLength(2);
    latest = { ...latest, execution_id: "turn-2", updated_at: "2026-07-10T10:00:03Z" };
    coordinator.update(latest);
    expect(surfaces).toHaveLength(3);
    sinks[2].onGone();
    coordinator.showForThread("thread-1");
    expect(surfaces).toHaveLength(4);
    await coordinator.shutdown();
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


describe("observation card stack", () => {
  function makeStack() {
    const cards: Array<{
      thread: string;
      sink: ObservationPiPEventSink;
      position: ReturnType<typeof vi.fn>;
      raise: ReturnType<typeof vi.fn>;
    }> = [];
    const raised: string[] = [];
    const coordinator = new ObservationCoordinator(
      { mainWindow: () => undefined } as unknown as WindowRegistry,
      undefined,
      (next, _key, sink) => {
        const card = { thread: next.thread_id, sink, position: vi.fn(), raise: vi.fn(() => raised.push(next.thread_id)) };
        cards.push(card);
        return {
          start: vi.fn(), setVisible: vi.fn(), setLive: vi.fn(), updateActivity: vi.fn(), setTurnCompleted: vi.fn(),
          animateInteraction: vi.fn(), stop: (onStopped?: () => void) => onStopped?.(),
          setStackPosition: card.position, raise: card.raise,
          stackLead: () => ({ frame: { x: 24, y: 24, width: 320, height: 240 }, alignment: "top-left" as const, dragging: false }),
        };
      },
    );
    return { coordinator, cards, raised };
  }

  it("closes the gap when a completed card leaves", () => {
    const { coordinator, cards } = makeStack();
    coordinator.update(activity({ thread_id: "thread-a" }));
    coordinator.update(activity({ thread_id: "thread-b", target: "com.apple.Notes" }));
    const [a, b] = cards;
    expect(a.position).toHaveBeenLastCalledWith(1, expect.anything());
    b.sink.onEvent({ event: "completion_expired" });
    expect(a.position).toHaveBeenLastCalledWith(0, expect.anything());
    void coordinator.shutdown();
  });

  it("restores the stack order when cards show again", () => {
    const { coordinator, cards, raised } = makeStack();
    coordinator.update(activity({ thread_id: "thread-a" }));
    coordinator.update(activity({ thread_id: "thread-b", target: "com.apple.Notes" }));
    coordinator.setActiveThread("thread-a");
    raised.length = 0;
    // A hidden host shows its cards again one by one; the last shown lands on top.
    cards[0].sink.onEvent({ event: "shown" });
    cards[1].sink.onEvent({ event: "shown" });
    expect(raised.at(-1)).toBe("thread-a");
    void coordinator.shutdown();
  });

  it("keeps the chosen corner for a card that starts after the stack emptied", () => {
    const { coordinator, cards } = makeStack();
    coordinator.update(activity({ thread_id: "thread-a" }));
    cards[0].sink.onGone();
    coordinator.update(activity({ thread_id: "thread-b", target: "com.apple.Notes" }));
    expect(cards[1].position).toHaveBeenLastCalledWith(0, expect.objectContaining({ alignment: "top-left" }));
    void coordinator.shutdown();
  });
});

it("shutdown waits for every card to exit and starts nothing afterwards", async () => {
  const stops: Array<() => void> = [];
  const starts = vi.fn();
  const coordinator = new ObservationCoordinator(
    { mainWindow: () => undefined } as unknown as WindowRegistry,
    undefined,
    () => ({
      start: starts, setVisible: vi.fn(), setLive: vi.fn(), animateInteraction: vi.fn(),
      stop: (callback?: () => void) => { if (callback) stops.push(callback); },
    }),
  );
  coordinator.setActiveThread("thread-1");
  coordinator.update(activity());
  coordinator.update(activity({ process_id: 43, updated_at: "2026-07-10T10:00:02Z" }));
  coordinator.update(activity({ thread_id: "thread-2", updated_at: "2026-07-10T10:00:02Z" }));
  expect(starts).toHaveBeenCalledTimes(3);
  let finished = false;
  const done = coordinator.shutdown().then(() => { finished = true; });
  await Promise.resolve();
  expect(finished).toBe(false);
  for (const stop of stops) stop();
  await done;
  coordinator.update(activity({ updated_at: "2026-07-10T10:00:03Z" }));
  expect(starts).toHaveBeenCalledTimes(3);
});
