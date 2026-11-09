import { randomUUID } from "node:crypto";
import type {
  CodexPetCommand,
  CodexPetSubmitResult,
  CodexPetSubmitTarget,
} from "../shared/protocol";

export const CODEX_PET_COMMAND_CHANNEL = "wuu:codex-pet-command";

export type CodexPetCommandTarget = {
  send(channel: string, command: CodexPetCommand): void;
};

type PendingCommand = {
  id: string;
  command: CodexPetCommand;
  timer: ReturnType<typeof setTimeout>;
  settle?: (result: CodexPetSubmitResult) => void;
  delivered: boolean;
};

// Delivers pet commands to the main window's renderer. The pet outlives that
// renderer: the window may be closed, reloading, or still initializing, so
// commands wait for the renderer to attach. Every command expires after
// `timeoutMs`; a submit that is not settled by then, or whose renderer goes
// away after delivery, fails so the pet can keep the user's text.
export class CodexPetCommandRelay {
  private target: CodexPetCommandTarget | undefined;
  private readonly pending = new Map<string, PendingCommand>();

  constructor(
    private readonly options: {
      timeoutMs: number;
      // Called when a command arrives with no attached renderer, so the host
      // can open the window that will attach.
      onMissingTarget: () => void;
    },
  ) {}

  get attached(): boolean {
    return this.target !== undefined;
  }

  jump(threadID: string): void {
    this.enqueue(randomUUID(), { kind: "jump", thread_id: threadID });
  }

  submit(text: string, target: CodexPetSubmitTarget): Promise<CodexPetSubmitResult> {
    const id = randomUUID();
    return new Promise((resolve) => this.enqueue(id, { kind: "submit", id, text, target }, resolve));
  }

  attach(target: CodexPetCommandTarget): void {
    this.target = target;
    for (const entry of this.pending.values()) {
      if (!entry.delivered) this.deliver(entry);
    }
  }

  detach(target: CodexPetCommandTarget): void {
    if (this.target !== target) return;
    this.target = undefined;
    // A delivered command belongs to the renderer that just went away; its
    // outcome is unknown, so report failure rather than wait out the timer.
    for (const [id, entry] of this.pending) {
      if (entry.delivered) this.finish(id, { ok: false });
    }
  }

  resolve(id: string, result: CodexPetSubmitResult): void {
    this.finish(id, { ok: result?.ok === true });
  }

  private enqueue(
    id: string,
    command: CodexPetCommand,
    settle?: (result: CodexPetSubmitResult) => void,
  ): void {
    const entry: PendingCommand = {
      id,
      command,
      settle,
      delivered: false,
      timer: setTimeout(() => this.finish(id, { ok: false }), this.options.timeoutMs),
    };
    this.pending.set(id, entry);
    if (this.target) {
      this.deliver(entry);
    } else {
      this.options.onMissingTarget();
    }
  }

  private deliver(entry: PendingCommand): void {
    this.target?.send(CODEX_PET_COMMAND_CHANNEL, entry.command);
    entry.delivered = true;
    // Jumps need no acknowledgement; only submits stay pending.
    if (entry.command.kind === "jump") this.finish(entry.id, { ok: true });
  }

  private finish(id: string, result: CodexPetSubmitResult): void {
    const entry = this.pending.get(id);
    if (!entry) return;
    this.pending.delete(id);
    clearTimeout(entry.timer);
    entry.settle?.(result);
  }
}
