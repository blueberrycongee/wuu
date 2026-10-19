import { randomUUID } from "node:crypto";
import type { ActivitySession } from "../shared/protocol";
import type { NativePreviewFrame } from "../shared/nativePreview";
import { activityTimestamp } from "../shared/activityTimestamp";

const MAX_FRAME_BYTES = 128 * 1024;
const NETWORK_CREDITS = 3;
const VIEWER_LEASE_MS = 5_000;

export type RemotePreviewSource = {
  start(): void;
  stop(): void;
  acknowledgeFrame(sequence: number): void;
  requestKeyframe(): void;
};
export type RemotePreviewHandlers = {
  frame(frame: NativePreviewFrame): void;
  state(state: "paused" | "error" | "ended", message?: string): void;
};
type Preview = {
  id: string;
  owner: number;
  activity: ActivitySession;
  source: RemotePreviewSource;
  pending: Set<number>;
  held?: number;
  latestJPEG?: NativePreviewFrame;
  recoveryFrame?: NativePreviewFrame;
  lastSequence: number;
  state: string;
  started: boolean;
  lease?: ReturnType<typeof setTimeout>;
};

// A viewer owns only a capture lease. It never promotes the target window,
// changes task authority, or places pixels in the replayable conversation log.
export class RemoteActivityPreviews {
  private readonly streams = new Map<string, Preview>();
  private readonly pendingOpens = new Map<number, {
    workdir: string; threadID: string; activityID: string; latest?: ActivitySession;
  }>();

  constructor(private readonly deps: {
    notify(owner: number, method: string, params: unknown): boolean;
    createSource(activity: ActivitySession, handlers: RemotePreviewHandlers): RemotePreviewSource;
  }) {}

  async openCurrent(owner: number, workdir: string, params: unknown, list: (threadID: string) => Promise<ActivitySession[]>): Promise<{ stream_id: string; codec: string }> {
    const input = params as { thread_id?: unknown; activity_id?: unknown; execution_id?: unknown } | null;
    if (!input || [input.thread_id, input.activity_id, input.execution_id].some(value => typeof value !== "string" || !value || value.length > 512)) {
      throw new Error("Preview requires a thread, activity, and current execution");
    }
    this.closeOwner(owner);
    const pending = { workdir, threadID: input.thread_id as string, activityID: input.activity_id as string, latest: undefined as ActivitySession | undefined };
    this.pendingOpens.set(owner, pending);
    try {
      const activities = await list(pending.threadID);
      if (this.pendingOpens.get(owner) !== pending) throw new Error("Preview open was superseded");
      let activity = activities.find(value => value.thread_id === input.thread_id && value.id === input.activity_id);
      if (pending.latest && (!activity || activityTimestamp(pending.latest.updated_at) >= activityTimestamp(activity.updated_at))) activity = pending.latest;
      if (!activity || activity.execution_id !== input.execution_id) throw new Error("Preview activity is no longer current");
      return this.open(owner, activity);
    } finally {
      if (this.pendingOpens.get(owner) === pending) this.pendingOpens.delete(owner);
    }
  }

  open(owner: number, activity: ActivitySession): { stream_id: string; codec: string } {
    if (!activity.execution_id || !activity.target?.trim() || activity.state === "stopped"
      || activity.state === "error" || activity.controller === "none"
      || (activity.kind === "cua" && !(activity.process_id! > 0))) {
      throw new Error("Activity has no live preview");
    }
    // A phone watches one card at a time. Reopening replaces its previous
    // capture rather than permitting an unbounded set of background streams.
    this.closeOwner(owner);
    const id = randomUUID();
    const source = this.deps.createSource(activity, {
      frame: frame => this.frame(id, frame),
      state: (state, message) => {
        const current = this.streams.get(id);
        if (!current) return;
        if (state === "paused") this.state(current, state, message);
        else this.close(current, state, message);
      },
    });
    const entry: Preview = { id, owner, activity, source, pending: new Set(), lastSequence: 0, state: "", started: false };
    this.streams.set(id, entry);
    this.renew(entry);
    this.state(entry, "loading");
    return { stream_id: id, codec: activity.kind === "browser" ? "jpeg" : "avc1" };
  }

  request(owner: number, method: string, params: unknown): Record<string, never> {
    const input = params as { stream_id?: unknown; sequence?: unknown; recovery?: unknown } | null;
    const entry = typeof input?.stream_id === "string" ? this.streams.get(input.stream_id) : undefined;
    if (!entry || entry.owner !== owner) throw new Error("Preview stream owner is unavailable");
    if (method === "desktop/activity/preview/close") {
      this.close(entry, "ended");
      return {};
    }
    if (method === "desktop/activity/preview/renew") {
      this.renew(entry);
      // The phone installs stream identity and its decoder before this first
      // renewal. Otherwise the first keyframe can beat the open RPC response.
      if (!entry.started) {
        entry.started = true;
        try { entry.source.start(); }
        catch (error) {
          this.close(entry, "error", error instanceof Error ? error.message : String(error));
          throw error;
        }
      }
      return {};
    }
    if (method !== "desktop/activity/preview/ack") throw new Error("Unknown preview operation");
    if (!Number.isSafeInteger(input!.sequence) || Number(input!.sequence) <= 0) throw new Error("Invalid preview acknowledgement");
    const sequence = Number(input!.sequence);
    if (!entry.pending.delete(sequence)) return {};
    this.renew(entry);
    if (input!.recovery === true) entry.source.requestKeyframe();
    const recovery = entry.recoveryFrame;
    if (recovery && entry.pending.size < NETWORK_CREDITS) {
      entry.recoveryFrame = undefined;
      entry.held = undefined;
      this.sendFrame(entry, recovery);
    } else if (entry.held !== undefined && entry.pending.size < NETWORK_CREDITS) {
      const held = entry.held;
      entry.held = undefined;
      entry.source.acknowledgeFrame(held);
    }
    const latest = entry.latestJPEG;
    if (latest && entry.pending.size < NETWORK_CREDITS) {
      entry.latestJPEG = undefined;
      this.sendFrame(entry, latest);
    }
    return {};
  }

  updateActivity(activity: ActivitySession): void {
    const key = this.activityKey(activity);
    // Track changes only while a snapshot is pending. Thread/activity IDs name
    // the owner even when the RPC routes from another workspace context.
    // Completed activities never accumulate in a second history cache.
    for (const pending of this.pendingOpens.values()) {
      if (pending.threadID === activity.thread_id && pending.activityID === activity.id
        && (!pending.latest || activityTimestamp(pending.latest.updated_at) <= activityTimestamp(activity.updated_at))) pending.latest = activity;
    }
    for (const entry of this.streams.values()) {
      const source = entry.activity;
      if (this.activityKey(source) !== key || activityTimestamp(source.updated_at) > activityTimestamp(activity.updated_at)) continue;
      if (activity.execution_id !== source.execution_id || activity.kind !== source.kind
        || activity.target !== source.target || activity.process_id !== source.process_id
        || activity.window_id !== source.window_id || activity.state === "stopped"
        || activity.state === "error" || activity.controller === "none") this.close(entry, "ended");
      else entry.activity = activity;
    }
  }

  closeOwner(owner: number): void {
    this.pendingOpens.delete(owner);
    for (const entry of this.streams.values()) if (entry.owner === owner) this.close(entry, "ended");
  }

  dropWorkdir(workdir: string): void {
    for (const entry of this.streams.values()) if (entry.activity.workdir === workdir) this.close(entry, "ended");
    for (const [owner, pending] of this.pendingOpens) if (pending.workdir === workdir || pending.latest?.workdir === workdir) this.pendingOpens.delete(owner);
  }

  shutdown(): void {
    for (const entry of this.streams.values()) this.close(entry, "ended");
    this.pendingOpens.clear();
  }

  private frame(id: string, frame: NativePreviewFrame): void {
    const entry = this.streams.get(id);
    if (!entry || !entry.started || frame.sequence <= entry.lastSequence) return;
    entry.lastSequence = frame.sequence;
    if (!frame.data.byteLength || frame.data.byteLength > MAX_FRAME_BYTES) {
      this.close(entry, "error", "Preview frame exceeds the bandwidth limit");
      return;
    }
    if (entry.pending.size >= NETWORK_CREDITS) {
      if (frame.codec === "jpeg") entry.latestJPEG = frame;
      // A ScreenCaptureKit restart invalidates the old local ACK and may
      // produce one new IDR. Retain that independent recovery frame until a
      // network credit returns, without releasing its native ACK early.
      else if (frame.keyframe) { entry.recoveryFrame = frame; entry.held = frame.sequence; }
      else this.close(entry, "error", "Preview source exceeded its frame credit");
      return;
    }
    this.sendFrame(entry, frame);
  }

  private sendFrame(entry: Preview, frame: NativePreviewFrame): void {
    entry.pending.add(frame.sequence);
    const sent = this.deps.notify(entry.owner, "desktop/activity/preview/frame", {
      ...this.identity(entry), sequence: frame.sequence, generation: frame.generation,
      configuration: frame.configuration, codec: frame.codec, keyframe: frame.keyframe,
      timestamp: frame.timestamp, width: frame.width, height: frame.height,
      ...(frame.description ? { description: frame.description } : {}),
      data: Buffer.from(frame.data).toString("base64"),
    });
    if (!sent) { this.close(entry, "error", "Preview connection is backlogged"); return; }
    this.state(entry, "live");
    // Preserve every encoded AVC dependency. The helper retains only the newest
    // raw image while its last local ACK is held, so WAN latency cannot grow a
    // stale frame queue or stall the independent desktop PiP capture session.
    if (frame.codec !== "jpeg" && entry.pending.size >= NETWORK_CREDITS) entry.held = frame.sequence;
    else entry.source.acknowledgeFrame(frame.sequence);
  }

  private identity(entry: Preview) {
    return { stream_id: entry.id, thread_id: entry.activity.thread_id,
      activity_id: entry.activity.id, execution_id: entry.activity.execution_id };
  }

  private state(entry: Preview, state: string, message?: string): void {
    if (entry.state === state && !message) return;
    entry.state = state;
    this.deps.notify(entry.owner, "desktop/activity/preview/state", { ...this.identity(entry), state, ...(message ? { message } : {}) });
  }

  private renew(entry: Preview): void {
    clearTimeout(entry.lease);
    entry.lease = setTimeout(() => this.close(entry, "ended", "Preview viewer disconnected"), VIEWER_LEASE_MS);
    entry.lease.unref?.();
  }

  private close(entry: Preview, state: "ended" | "error", message?: string): void {
    if (!this.streams.delete(entry.id)) return;
    clearTimeout(entry.lease);
    entry.latestJPEG = undefined;
    entry.recoveryFrame = undefined;
    entry.pending.clear();
    entry.source.stop();
    this.state(entry, state, message);
  }

  private activityKey(activity: ActivitySession): string {
    return JSON.stringify([activity.workdir, activity.thread_id, activity.id]);
  }
}
