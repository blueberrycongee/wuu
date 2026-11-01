import type { EventEmitter } from "node:events";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import type { DictationEvent } from "../shared/protocol";

type SpeechProcess = Pick<ChildProcessWithoutNullStreams, "stdout" | "stderr" | "stdin" | "kill"> & Pick<EventEmitter, "once">;
type Session = { emit(event: DictationEvent): void; cleanup(): void; id: string; child?: SpeechProcess; finishing: boolean; completed: boolean; timeout?: ReturnType<typeof setTimeout> };

export class DictationService {
  private session?: Session;
  constructor(private readonly deps: {
    askForMicrophoneAccess(): Promise<boolean>;
    spawn(locale: string): SpeechProcess;
  }) {}

  async start(id: string, locale: string, emit: (event: DictationEvent) => void, cleanup = () => {}): Promise<void> {
    this.cancel();
    const session: Session = { id, finishing: false, completed: false, cleanup, emit };
    this.session = session;
    const fail = (code: string): void => {
      if (this.session !== session) return;
      emit({ sessionId: id, type: "error", code });
      this.cancel(id);
    };
    emit({ sessionId: id, type: "state", state: "requesting_microphone_permission" });
    try {
      const allowed = await this.deps.askForMicrophoneAccess();
      if (this.session !== session) return;
      if (!allowed) { fail("microphone_permission_denied"); return; }
      const child = this.deps.spawn(locale);
      session.child = child;
      let buffer = "";
      child.stdout.setEncoding("utf8");
      child.stderr.resume();
      child.stdout.on("data", (chunk: string) => {
        if (this.session !== session) return;
        buffer += chunk;
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";
        for (const line of lines) {
          let event: DictationEvent;
          try { event = JSON.parse(line); } catch { fail("invalid_speech_output"); return; }
          if (event.type === "error") { fail(event.code); return; }
          if (event.type === "result" && event.is_final) {
            session.completed = true;
            clearTimeout(session.timeout);
          }
          emit({ ...event, sessionId: id });
        }
      });
      child.stdin.on("error", () => fail("helper_unavailable"));
      child.once("error", () => fail("helper_unavailable"));
      child.once("close", () => {
        if (this.session !== session) return;
        if (!session.completed) fail("recognition_failed");
        else this.cancel(id);
      });
    } catch { fail("helper_unavailable"); }
  }

  stop(id: string): void {
    const session = this.session;
    if (!session || session.id !== id || session.finishing || session.completed) return;
    if (!session.child) { this.cancel(id); return; }
    session.finishing = true;
    session.child.stdin.write("stop\n");
    // The native helper reports a timeout after 12 seconds. Reap it even if
    // the native run loop hangs, so cancellation always releases the mic.
    session.timeout = setTimeout(() => session.child?.kill(), 15_000);
  }

  cancel(id?: string): void {
    const session = this.session;
    if (!session || (id !== undefined && session.id !== id)) return;
    this.session = undefined;
    clearTimeout(session.timeout);
    session.cleanup();
    session.child?.kill();
    if (!session.completed) session.emit({ sessionId: session.id, type: "cancelled" });
  }
}
