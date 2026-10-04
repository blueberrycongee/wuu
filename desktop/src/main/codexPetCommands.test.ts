import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CodexPetCommand } from "../shared/protocol";
import { CodexPetCommandRelay } from "./codexPetCommands";

// Failure cases the relay exists to prevent:
// - a command issued while the main window is closed or still booting is lost;
// - a submit that the renderer never settles leaves the pet "sending" forever;
// - a renderer that reloads or closes mid-submit leaves the pet waiting;
// - a late acknowledgement settles a submit twice;
// - a renderer re-subscribing receives already-delivered commands again;
// - a stale jump fires minutes later when the window finally becomes ready.

function target() {
  const sent: CodexPetCommand[] = [];
  return {
    sent,
    send: vi.fn((channel: string, command: CodexPetCommand) => {
      expect(channel).toBe("wuu:codex-pet-command");
      sent.push(command);
    }),
  };
}

describe("CodexPetCommandRelay", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("queues commands until the renderer attaches and asks the host for a target", async () => {
    const onMissingTarget = vi.fn();
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget });
    relay.jump("thread-a");
    const submit = relay.submit("hello", "thread-b");
    expect(onMissingTarget).toHaveBeenCalled();

    const renderer = target();
    relay.attach(renderer);
    expect(renderer.sent).toEqual([
      { kind: "jump", thread_id: "thread-a" },
      expect.objectContaining({ kind: "submit", thread_id: "thread-b", text: "hello" }),
    ]);

    const submitted = renderer.sent[1] as Extract<CodexPetCommand, { kind: "submit" }>;
    relay.resolve(submitted.id, { ok: true });
    await expect(submit).resolves.toEqual({ ok: true });
  });

  it("does not redeliver commands when the same renderer attaches again", () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    const renderer = target();
    relay.attach(renderer);
    relay.jump("thread-a");
    relay.attach(renderer);
    expect(renderer.sent).toHaveLength(1);
  });

  it("fails a submit the renderer never settles", async () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    relay.attach(target());
    const submit = relay.submit("hello");
    vi.advanceTimersByTime(30_000);
    await expect(submit).resolves.toEqual({ ok: false });
  });

  it("fails delivered submits when their renderer goes away and keeps queued ones", async () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    const first = target();
    relay.attach(first);
    const delivered = relay.submit("first");
    relay.detach(first);
    await expect(delivered).resolves.toEqual({ ok: false });

    const queued = relay.submit("second");
    const second = target();
    relay.attach(second);
    expect(second.sent).toEqual([expect.objectContaining({ text: "second" })]);
    relay.resolve((second.sent[0] as { id: string }).id, { ok: true });
    await expect(queued).resolves.toEqual({ ok: true });
  });

  it("ignores a detach from a renderer that is no longer the target", async () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    const stale = target();
    const current = target();
    relay.attach(stale);
    relay.attach(current);
    const submit = relay.submit("hello");
    relay.detach(stale);
    relay.resolve((current.sent[0] as { id: string }).id, { ok: true });
    await expect(submit).resolves.toEqual({ ok: true });
  });

  it("ignores late acknowledgements after a timeout", async () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    const renderer = target();
    relay.attach(renderer);
    const submit = relay.submit("hello");
    vi.advanceTimersByTime(30_000);
    relay.resolve((renderer.sent[0] as { id: string }).id, { ok: true });
    await expect(submit).resolves.toEqual({ ok: false });
  });

  it("drops queued jumps that outlive the timeout", () => {
    const relay = new CodexPetCommandRelay({ timeoutMs: 30_000, onMissingTarget: () => {} });
    relay.jump("thread-a");
    vi.advanceTimersByTime(30_000);
    const renderer = target();
    relay.attach(renderer);
    expect(renderer.sent).toEqual([]);
  });
});
