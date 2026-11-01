import type { EventEmitter } from "node:events";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import type { DictationEvent } from "../shared/protocol";

type SpeechProcess = Pick<ChildProcessWithoutNullStreams, "stdout" | "stderr" | "stdin" | "kill"> & Pick<EventEmitter, "once">;
type Transcript = { committed: string; current: string };
type Session = { emit(event: DictationEvent): void; cleanup(): void; id: string; child?: SpeechProcess; finishing: boolean; completed: boolean; transcript: Transcript; timeout?: ReturnType<typeof setTimeout> };

export class DictationService {
  private session?: Session;
  constructor(private readonly deps: {
    askForMicrophoneAccess(): Promise<boolean>;
    spawn(locale: string): SpeechProcess;
  }) {}

  async start(id: string, locale: string, emit: (event: DictationEvent) => void, cleanup = () => {}): Promise<void> {
    this.cancel();
    const session: Session = { id, finishing: false, completed: false, cleanup, emit, transcript: { committed: "", current: "" } };
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
          if (event.type === "result") {
            event = { ...event, text: acceptTranscript(session.transcript, event.text, event.is_final) };
            if (event.is_final) {
              session.completed = true;
              clearTimeout(session.timeout);
            }
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

// macOS Speech starts a new hypothesis window at its one-minute audio limit
// or after a long pause, without marking the previous result final. Keep
// completed windows and stitch them together so a long recording does not
// collapse to its last sentence.
function acceptTranscript(transcript: Transcript, text: string, final: boolean): string {
  if (!text) return joinTranscript(transcript.committed, transcript.current);
  if (!transcript.current) transcript.current = text;
  else if (text.startsWith(transcript.current)) transcript.current = text;
  else if (transcript.current.startsWith(text)) {
    if (final) transcript.current = text;
  } else if (isNewRecognitionWindow(transcript.current, text)) {
    transcript.committed = joinTranscript(transcript.committed, transcript.current);
    transcript.current = text;
  } else {
    transcript.current = text;
  }
  return joinTranscript(transcript.committed, transcript.current);
}

// A fresh window starts near the live audio position, so it is much shorter
// than the previous hypothesis and unrelated to its beginning. Revisions keep
// most of that prefix even when they replace trailing words.
function isNewRecognitionWindow(previous: string, next: string): boolean {
  const sharedPrefix = commonPrefixLength(previous, next);
  return previous.length >= 12
    && next.length <= Math.max(4, previous.length / 2)
    && sharedPrefix < Math.min(9, Math.max(3, Math.floor(previous.length / 10)));
}

function commonPrefixLength(left: string, right: string): number {
  let length = 0;
  while (length < left.length && length < right.length && left[length] === right[length]) length++;
  return length;
}

function joinTranscript(left: string, right: string): string {
  if (!left) return right;
  if (!right) return left;
  // Adjacent windows overlap by at most a phrase; bounded search keeps every
  // partial result cheap even after several minutes of dictation.
  const limit = Math.min(left.length, right.length, 200);
  for (let length = limit; length > 0; length--) {
    if (left.endsWith(right.slice(0, length))) return left + right.slice(length);
  }
  const last = left.at(-1) ?? "";
  const first = right[0] ?? "";
  return left + (/[A-Za-z0-9]/.test(last) && /[A-Za-z0-9]/.test(first) ? " " : "") + right;
}
