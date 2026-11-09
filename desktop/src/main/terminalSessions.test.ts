import { describe, expect, it, vi } from "vitest";
import { isAbsolute, resolve } from "node:path";
import type {
  RuntimeContext,
  TerminalSessionEvent,
  TerminalSessionStartParams,
} from "../shared/protocol";

// Stub the native node-pty process so lifecycle and ownership behavior can be
// tested without launching a real shell.
const ptyMock = vi.hoisted(() => ({ spawn: vi.fn() }));
vi.mock("node-pty", () => ptyMock);

const { resolveTerminalCwd, TerminalSessionManager } = await import("./terminalSessions");

function createFakePty() {
  let dataHandler: ((text: string) => void) | undefined;
  let exitHandler:
    | ((event: { exitCode: number; signal?: number }) => void)
    | undefined;
  return {
    process: {
      write: vi.fn(),
      resize: vi.fn(),
      kill: vi.fn(),
      onData: vi.fn((handler: (text: string) => void) => {
        dataHandler = handler;
      }),
      onExit: vi.fn(
        (handler: (event: { exitCode: number; signal?: number }) => void) => {
          exitHandler = handler;
        },
      ),
    },
    emitData: (text: string) => dataHandler?.(text),
    emitExit: (exitCode: number) => exitHandler?.({ exitCode }),
  };
}
describe("resolveTerminalCwd", () => {
  const context: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo/project",
  };

  it("uses the runtime context's cwd when no override is given", () => {
    expect(resolveTerminalCwd(context, {})).toBe("/repo/project");
  });

  it("prefers an explicit override cwd over the runtime context", () => {
    const override = resolve("/repo/worktrees/fork-1/project");
    const params: TerminalSessionStartParams = {
      cwd: override,
    };
    expect(resolveTerminalCwd(context, params)).toBe(override);
  });

  it("normalizes a relative override to an absolute path", () => {
    const params: TerminalSessionStartParams = { cwd: "relative/worktree" };
    const resolved = resolveTerminalCwd(context, params);
    expect(resolved).toBe(resolve("relative/worktree"));
    expect(isAbsolute(resolved)).toBe(true);
  });

  it("ignores an empty-string override and falls back to the runtime context", () => {
    expect(resolveTerminalCwd(context, { cwd: "" })).toBe("/repo/project");
  });
});

describe("TerminalSessionManager window ownership", () => {
  const context: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo/project",
  };

  it("routes events and terminal actions only to the owning window", () => {
    const fakePty = createFakePty();
    ptyMock.spawn.mockReturnValueOnce(fakePty.process);
    const emitted: Array<{
      ownerWindowID: number;
      event: TerminalSessionEvent;
    }> = [];
    const manager = new TerminalSessionManager((ownerWindowID, event) => {
      emitted.push({ ownerWindowID, event });
    });
    const started = manager.startInContext(context, {}, 7);

    fakePty.emitData("hello");
    expect(emitted).toEqual([
      {
        ownerWindowID: 7,
        event: { type: "data", id: started.id, text: "hello" },
      },
    ]);

    expect(manager.write(started.id, "blocked", 8)).toEqual({ ok: false });
    expect(manager.resize(started.id, 100, 40, 8)).toEqual({ ok: false });
    expect(manager.stop(started.id, 8)).toEqual({ ok: false });
    expect(fakePty.process.write).not.toHaveBeenCalled();
    expect(fakePty.process.resize).not.toHaveBeenCalled();
    expect(fakePty.process.kill).not.toHaveBeenCalled();

    expect(manager.write(started.id, "allowed", 7)).toEqual({ ok: true });
    expect(manager.resize(started.id, 100, 40, 7)).toEqual({ ok: true });
    expect(fakePty.process.write).toHaveBeenCalledWith("allowed");
    expect(fakePty.process.resize).toHaveBeenCalledWith(100, 40);

    fakePty.emitExit(0);
    expect(emitted[1]).toMatchObject({
      ownerWindowID: 7,
      event: { type: "exit", id: started.id, exit_code: 0 },
    });
  });

  it("stops every terminal owned by a closing window", () => {
    const first = createFakePty();
    const second = createFakePty();
    const other = createFakePty();
    ptyMock.spawn
      .mockReturnValueOnce(first.process)
      .mockReturnValueOnce(second.process)
      .mockReturnValueOnce(other.process);
    const manager = new TerminalSessionManager(() => {});
    manager.startInContext(context, {}, 7);
    manager.startInContext(context, {}, 7);
    manager.startInContext(context, {}, 8);

    manager.stopForOwner(7);

    expect(first.process.kill).toHaveBeenCalledOnce();
    expect(second.process.kill).toHaveBeenCalledOnce();
    expect(other.process.kill).not.toHaveBeenCalled();
  });
});
