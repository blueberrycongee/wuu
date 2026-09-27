import { afterEach, describe, expect, it, vi } from "vitest";
import type { ActivitySession } from "../shared/protocol";
import {
  boundsChanged,
  browserTabIDForActivity,
  displayedBrowserTabID,
  isForegroundControlled,
  isMeasurableRect,
  observeBrowserPanelBounds,
  pickBoundsRect,
  roundRect,
} from "./BrowserVisibility";

afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

function activity(overrides: Partial<ActivitySession> = {}): ActivitySession {
  return {
    id: "activity-1",
    kind: "browser",
    thread_id: "thread-1",
    workdir: "/repo-a",
    target: "tab-abc",
    state: "foreground_controlled",
    controller: "agent",
    created_at: "2026-07-10T10:00:00Z",
    updated_at: "2026-07-10T10:00:01Z",
    ...overrides,
  };
}

describe("isForegroundControlled", () => {
  it("is true only for a foreground-controlled browser activity", () => {
    expect(isForegroundControlled(activity())).toBe(true);
  });

  it("is false for other states, kinds, or when absent", () => {
    expect(isForegroundControlled(activity({ state: "background_controlled" }))).toBe(false);
    expect(isForegroundControlled(activity({ state: "user_controlled" }))).toBe(false);
    expect(isForegroundControlled(activity({ state: "stopped" }))).toBe(false);
    expect(isForegroundControlled(activity({ kind: "cua" }))).toBe(false);
    expect(isForegroundControlled(undefined)).toBe(false);
  });
});

describe("browserTabIDForActivity", () => {
  it("uses the activity target as the tab id", () => {
    expect(browserTabIDForActivity(activity({ target: "tab-xyz" }))).toBe("tab-xyz");
  });

  it("falls back to the activity id when target is missing or empty", () => {
    expect(browserTabIDForActivity(activity({ target: undefined }))).toBe("activity-1");
    expect(browserTabIDForActivity(activity({ target: "" }))).toBe("activity-1");
  });
});

describe("rect math", () => {
  it("rounds fractional rects to integers", () => {
    expect(roundRect({ x: 10.4, y: 20.6, width: 300.5, height: 199.49 })).toEqual({
      x: 10,
      y: 21,
      width: 301,
      height: 199,
    });
  });

  it("treats only positive-area rects as measurable", () => {
    expect(isMeasurableRect({ x: 0, y: 0, width: 100, height: 50 })).toBe(true);
    expect(isMeasurableRect({ x: 0, y: 0, width: 0, height: 50 })).toBe(false);
    expect(isMeasurableRect({ x: 0, y: 0, width: 100, height: 0 })).toBe(false);
  });

  it("reports when any dimension changed and skips identical rects", () => {
    const base = { x: 1, y: 2, width: 3, height: 4 };
    expect(boundsChanged(undefined, base)).toBe(true);
    expect(boundsChanged(base, { ...base })).toBe(false);
    expect(boundsChanged(base, { ...base, x: 2 })).toBe(true);
    expect(boundsChanged(base, { ...base, y: 3 })).toBe(true);
    expect(boundsChanged(base, { ...base, width: 5 })).toBe(true);
    expect(boundsChanged(base, { ...base, height: 5 })).toBe(true);
  });
});

describe("pickBoundsRect", () => {
  const host = { x: 4, y: 4, width: 200, height: 100 };
  const frame = { x: 0, y: 0, width: 220, height: 140 };

  it("prefers a measurable host rect", () => {
    expect(pickBoundsRect(host, frame)).toBe(host);
  });

  it("falls back to the frame when the host is hidden (zero area)", () => {
    expect(pickBoundsRect({ x: 0, y: 0, width: 0, height: 0 }, frame)).toBe(frame);
    expect(pickBoundsRect(undefined, frame)).toBe(frame);
  });

  it("returns whatever exists when neither is measurable", () => {
    const zeroHost = { x: 0, y: 0, width: 0, height: 0 };
    expect(pickBoundsRect(zeroHost, undefined)).toBe(zeroHost);
    expect(pickBoundsRect(undefined, undefined)).toBeUndefined();
  });
});

describe("observeBrowserPanelBounds", () => {
  it("stops requesting frames after a stable measurement", () => {
    const frame = document.createElement("div");
    frame.className = "workspace-browser-frame";
    const host = document.createElement("div");
    host.className = "workspace-browser-host";
    frame.append(host);
    document.body.append(frame);
    vi.spyOn(host, "getBoundingClientRect").mockReturnValue({
      x: 10,
      y: 20,
      left: 10,
      top: 20,
      right: 310,
      bottom: 220,
      width: 300,
      height: 200,
      toJSON: () => ({}),
    });

    const frames: FrameRequestCallback[] = [];
    const requestAnimationFrame = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((callback) => {
        frames.push(callback);
        return frames.length;
      });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const report = vi.fn();

    const cleanup = observeBrowserPanelBounds(report);
    expect(requestAnimationFrame).toHaveBeenCalledTimes(1);

    frames.shift()?.(0);

    expect(report).toHaveBeenCalledWith({
      x: 10,
      y: 20,
      width: 300,
      height: 200,
    });
    expect(requestAnimationFrame).toHaveBeenCalledTimes(1);
    cleanup();
  });

  it("tracks frames only while a panel geometry transition is active", () => {
    const panel = document.createElement("aside");
    panel.className = "workspace-right-panel";
    const frame = document.createElement("div");
    frame.className = "workspace-browser-frame";
    panel.append(frame);
    document.body.append(panel);
    vi.spyOn(frame, "getBoundingClientRect").mockReturnValue({
      x: 0,
      y: 0,
      left: 0,
      top: 0,
      right: 300,
      bottom: 200,
      width: 300,
      height: 200,
      toJSON: () => ({}),
    });

    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});

    const cleanup = observeBrowserPanelBounds(vi.fn());
    frames.shift()?.(0);

    const run = new Event("transitionrun", { bubbles: true });
    Object.defineProperty(run, "propertyName", { value: "transform" });
    panel.dispatchEvent(run);
    expect(frames).toHaveLength(1);
    frames.shift()?.(16);
    expect(frames).toHaveLength(1);

    const end = new Event("transitionend", { bubbles: true });
    Object.defineProperty(end, "propertyName", { value: "transform" });
    panel.dispatchEvent(end);
    frames.shift()?.(32);
    expect(frames).toHaveLength(0);
    cleanup();
  });
});

describe("displayedBrowserTabID", () => {
  it("uses the agent tab while the activity is alive, otherwise a per-thread tab", () => {
    expect(displayedBrowserTabID(activity({ state: "background_controlled", target: "tab-1" }), "thread-1")).toBe("tab-1");
    expect(displayedBrowserTabID(activity({ state: "stopped" }), "thread-1")).toBe("user:thread-1");
    expect(displayedBrowserTabID(undefined, "thread-1")).toBe("user:thread-1");
    expect(displayedBrowserTabID(undefined, undefined)).toBeUndefined();
  });
});
