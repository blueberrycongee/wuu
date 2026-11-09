import type {
  CodexPetHint,
  RunningThreadSnapshot,
  ServerEvent,
} from "../shared/protocol";

// The looping animation the pet settles into. `waiting` means a blocking
// question or approval needs the user and outranks running work.
export type CodexPetMood = "idle" | "running" | "waiting";
// One-shot animations for a settled top-level turn.
export type CodexPetReaction = "review" | "failed";

type HintPatch = Pick<CodexPetHint, "status" | "attention"> & { preview?: string };

// The fields of the core notifications the pet reads; see
// internal/appserver/protocol.go for the full shapes.
type EventParams = {
  thread?: { id?: string; parent_id?: string; ephemeral?: boolean };
  thread_id?: string;
  turn?: { status?: string };
  content?: string;
  awaiting_auto_continuation?: boolean;
  request?: { request_id?: string; thread_id?: string; mode?: string };
  request_id?: string;
};

// Bubble rows ellipsize; this only bounds the payload.
const PATCH_PREVIEW_MAX = 500;

type Listener = {
  onMood: (mood: CodexPetMood) => void;
  onReaction: (reaction: CodexPetReaction) => void;
  onHints: (hints: CodexPetHint[]) => void;
};

// Derives the pet's activity from core events in the main process, so the
// pet keeps working while the main window is closed. The renderer stays the
// authority for bubble rows; while its feed is detached, events patch the
// last rows it pushed.
export class CodexPetActivity {
  private running = new Set<string>();
  // Subagent and ephemeral helper threads: their turns are not the user's.
  private readonly backgroundThreads = new Set<string>();
  private readonly questions = new Map<string, { threadID: string; workdir: string }>();
  private rendererHints: CodexPetHint[] = [];
  private readonly patches = new Map<string, HintPatch>();
  private rendererAttached = false;
  private currentMood: CodexPetMood = "idle";

  constructor(private readonly listener: Listener) {}

  get mood(): CodexPetMood {
    return this.currentMood;
  }

  hints(): CodexPetHint[] {
    if (this.rendererAttached || this.patches.size === 0) return this.rendererHints;
    return this.rendererHints.map((hint) => {
      const patch = this.patches.get(hint.thread_id);
      return patch ? { ...hint, ...patch } : hint;
    });
  }

  setRendererHints(hints: CodexPetHint[]): void {
    this.rendererHints = hints;
    this.patches.clear();
    this.listener.onHints(this.hints());
  }

  setRendererAttached(attached: boolean): void {
    if (attached === this.rendererAttached) return;
    this.rendererAttached = attached;
    this.patches.clear();
    this.listener.onHints(this.hints());
  }

  setRunningThreads(snapshot: RunningThreadSnapshot[]): void {
    this.running = new Set(snapshot.map((entry) => entry.thread_id));
    this.updateMood();
  }

  handleServerEvent(event: ServerEvent): void {
    if (event.kind === "server-exit") {
      for (const [id, question] of this.questions) {
        if (question.workdir === event.workdir) this.questions.delete(id);
      }
      this.updateMood();
      return;
    }
    if (event.kind !== "notification") return;
    const params = (event.message.params ?? {}) as EventParams;
    switch (event.message.method) {
      case "thread/started":
      case "thread/resumed":
      case "thread/updated": {
        const thread = params.thread;
        if (typeof thread?.id !== "string") return;
        if (thread.parent_id || thread.ephemeral) this.backgroundThreads.add(thread.id);
        else this.backgroundThreads.delete(thread.id);
        this.updateMood();
        return;
      }
      case "turn/started":
        this.patch(params.thread_id, { status: "running", attention: false });
        return;
      case "turn/completed":
      case "turn/error":
        this.settleTurn(event.message.method, params);
        return;
      case "user-question/requested": {
        const request = params.request;
        if (
          typeof request?.request_id !== "string" ||
          typeof request.thread_id !== "string" ||
          request.mode === "offer"
        ) {
          return;
        }
        this.questions.set(request.request_id, {
          threadID: request.thread_id,
          workdir: event.workdir,
        });
        this.patch(request.thread_id, { status: "needs_review", attention: true });
        this.updateMood();
        return;
      }
      case "user-question/resolved": {
        const question = this.questions.get(params.request_id ?? "");
        if (!question) return;
        this.questions.delete(params.request_id ?? "");
        const stillWaiting = [...this.questions.values()].some(
          (entry) => entry.threadID === question.threadID,
        );
        if (!stillWaiting) this.patch(question.threadID, { status: "running", attention: false });
        this.updateMood();
        return;
      }
    }
  }

  private settleTurn(method: string, params: EventParams): void {
    const threadID = params.thread_id;
    if (typeof threadID !== "string") return;
    const status = params.turn?.status;
    if (status === "interrupted") {
      this.patch(threadID, { status: "idle", attention: false });
      return;
    }
    const failed = method === "turn/error" || status === "failed";
    if (!failed && params.awaiting_auto_continuation) return;
    const preview = typeof params.content === "string"
      ? params.content.replace(/\s+/g, " ").trim().slice(0, PATCH_PREVIEW_MAX)
      : "";
    this.patch(threadID, {
      status: failed ? "failed" : "done",
      attention: true,
      ...(preview ? { preview } : {}),
    });
    if (!this.backgroundThreads.has(threadID)) {
      this.listener.onReaction(failed ? "failed" : "review");
    }
  }

  private patch(threadID: unknown, patch: HintPatch): void {
    if (this.rendererAttached || typeof threadID !== "string") return;
    if (!this.rendererHints.some((hint) => hint.thread_id === threadID)) return;
    this.patches.set(threadID, patch);
    this.listener.onHints(this.hints());
  }

  private updateMood(): void {
    let mood: CodexPetMood = "idle";
    if (this.questions.size > 0) {
      mood = "waiting";
    } else if ([...this.running].some((id) => !this.backgroundThreads.has(id))) {
      mood = "running";
    }
    if (mood === this.currentMood) return;
    this.currentMood = mood;
    this.listener.onMood(mood);
  }
}
