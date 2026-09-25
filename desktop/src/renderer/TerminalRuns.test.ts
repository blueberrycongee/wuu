import { describe, expect, it } from "vitest";
import type { Thread, ThreadItem, Turn } from "../shared/protocol";
import {
  agentRunGroupsForThread,
  agentRunsForTurn,
  isCommandToolCall,
  preferredAgentRun,
  selectAgentRun,
} from "./TerminalRuns";

function turn(items: ThreadItem[], status: Turn["status"] = "completed"): Turn {
  return { id: "turn-1", items, items_view: "full", status };
}

function commandItem(overrides: Partial<ThreadItem> = {}): ThreadItem {
  return {
    id: "call-1",
    type: "tool_call",
    status: "completed",
    name: "bash",
    arguments: JSON.stringify({ command: "npm test" }),
    display: { kind: "command", capability: "command.bash" },
    result: JSON.stringify({
      exit_code: 0,
      duration_ms: 1234,
      stdout_tail: "ok\n",
      stderr_tail: "",
      full_log_ref: "/tmp/run.log",
    }),
    ...overrides,
  };
}

describe("terminal run records", () => {
  it("recognizes command capabilities and legacy command tools only", () => {
    expect(isCommandToolCall(commandItem())).toBe(true);
    expect(isCommandToolCall(commandItem({ name: "custom", display: { capability: "command.background" } }))).toBe(true);
    expect(isCommandToolCall(commandItem({ name: "run_shell", display: undefined }))).toBe(true);
    expect(isCommandToolCall(commandItem({ name: "grep", display: { kind: "search" } }))).toBe(false);
    expect(isCommandToolCall(commandItem({ type: "agent_message" }))).toBe(false);
  });

  it.each([
    undefined,
    "ok\n",
    '{"ok":true}',
    '{"exit_code":7,"stdout_tail":"not real output"}',
  ])("extracts retained output and command metadata with model text %j", (modelText) => {
    const item = commandItem();
    const stdout = modelText ?? "ok\n";
    if (modelText !== undefined) {
      item.result_detail = {
        content: [{
          type: "text",
          text: JSON.stringify({ ...JSON.parse(item.result!), stdout_tail: stdout }),
        }],
      };
      item.result = modelText;
    }
    const [run] = agentRunsForTurn("thread-1", turn([item]));

    expect(run).toMatchObject({
      kind: "agent_run",
      execution: "snapshot",
      threadID: "thread-1",
      turnID: "turn-1",
      toolCallID: "call-1",
      command: "npm test",
      capability: "command.bash",
      status: "completed",
      stdout,
      exitCode: 0,
      durationMs: 1234,
      timedOut: false,
      truncated: false,
      fullLogRef: "/tmp/run.log",
      tty: false,
    });
  });

  it("binds only start_background results with a managed process id", () => {
    const [live, missingID] = agentRunsForTurn("thread-1", turn([
      commandItem({
        id: "call-live",
        arguments: JSON.stringify({ action: "start_background", command: "npm run dev", tty: true }),
        result: JSON.stringify({
          action: "start_background",
          id: "proc-123",
          status: "running",
          tty: true,
        }),
      }),
      commandItem({
        id: "call-no-id",
        arguments: JSON.stringify({ action: "start_background", command: "npm run dev" }),
        result: JSON.stringify({ action: "start_background", status: "running" }),
      }),
    ]));

    expect(live).toMatchObject({
      execution: "managed",
      processID: "proc-123",
      tty: true,
    });
    expect(missingID).toMatchObject({ execution: "snapshot", tty: false });
    expect(missingID.processID).toBeUndefined();
  });

  it("binds bash background starts with a managed process id", () => {
    const [live] = agentRunsForTurn("thread-1", turn([
      commandItem({
        id: "call-background",
        arguments: JSON.stringify({ command: "npm run dev", run_in_background: true }),
        display: { kind: "command", capability: "command.background" },
        result: JSON.stringify({ action: "start", id: "proc-456", status: "running", command: "npm run dev", tty: true }),
      }),
    ]));

    expect(live).toMatchObject({ execution: "managed", processID: "proc-456", command: "npm run dev", tty: true });
  });

  it("does not model background process management actions as terminal sessions", () => {
    const runs = agentRunsForTurn("thread-1", turn([
      commandItem({ arguments: JSON.stringify({ action: "read_background", process_id: "proc-123" }) }),
      commandItem({ id: "call-2", arguments: JSON.stringify({ action: "stop_background", process_id: "proc-123" }) }),
      commandItem({
        id: "call-3",
        name: "process",
        display: { kind: "command", capability: "command.background" },
        arguments: JSON.stringify({ action: "read", process_id: "proc-456" }),
      }),
    ]));

    expect(runs).toEqual([]);
  });

  it("marks non-zero exits and timeouts as failed even when the item completed", () => {
    const failed = commandItem({
      result: JSON.stringify({ exit_code: 2, stderr_tail: "failed\n" }),
    });
    const timedOut = commandItem({
      id: "call-2",
      result_detail: {
        structured_content: {
          exit_code: 0,
          timed_out: true,
          output: "timeout",
          stdout_tail_truncated: true,
        },
      },
      result: undefined,
    });
    const runs = agentRunsForTurn("thread-1", turn([failed, timedOut]));

    expect(runs[0]).toMatchObject({ status: "failed", exitCode: 2, stderr: "failed\n" });
    expect(runs[1]).toMatchObject({ status: "failed", timedOut: true, truncated: true });
    expect(runs[1].stdout).toBeUndefined();
    expect(runs[1].stderr).toBeUndefined();
  });

  it("does not expose plain legacy output and preserves settled status", () => {
    const [run] = agentRunsForTurn(
      "thread-1",
      turn([
        commandItem({
          status: "in_progress",
          result: "partial output",
        }),
      ], "interrupted"),
    );

    expect(run.status).toBe("interrupted");
    expect(run.stdout).toBeUndefined();
    expect(run.stderr).toBeUndefined();

    const [incomplete] = agentRunsForTurn(
      "thread-1",
      turn([commandItem({ status: "in_progress" })]),
    );
    expect(incomplete.status).toBe("incomplete");
  });

  it("includes completed command items from an active turn and prefers the last failed run", () => {
    const completed = turn([
      commandItem(),
      commandItem({
        id: "call-2",
        arguments: JSON.stringify({ command: "npm run lint" }),
        result: JSON.stringify({ exit_code: 1, stderr_tail: "lint failed" }),
      }),
      commandItem({
        id: "call-3",
        arguments: JSON.stringify({ command: "npm run build" }),
      }),
    ]);
    const running = {
      ...turn([commandItem({
        id: "call-live",
        arguments: JSON.stringify({ action: "start_background", command: "npm run dev" }),
        result: JSON.stringify({ action: "start_background", id: "proc-live", status: "running" }),
      })], "in_progress"),
      id: "turn-live",
    };
    const thread: Pick<Thread, "id" | "turns"> = {
      id: "thread-1",
      turns: [turn([{ id: "message-1", type: "agent_message", text: "done" }]), completed, running],
    };
    const groups = agentRunGroupsForThread(thread);

    expect(groups).toHaveLength(2);
    expect(groups[0].turnNumber).toBe(2);
    expect(groups[1].runs[0]).toMatchObject({ processID: "proc-live", execution: "managed" });
    expect(preferredAgentRun(groups[0].runs)?.toolCallID).toBe("call-2");
    expect(selectAgentRun(groups, { threadID: "thread-1", turnID: "turn-1" })?.toolCallID).toBe("call-2");
    expect(selectAgentRun(groups, {
      threadID: "thread-1",
      turnID: "turn-1",
      toolCallID: "call-3",
    })?.toolCallID).toBe("call-3");
    expect(selectAgentRun(groups, {
      threadID: "another-thread",
      turnID: "turn-1",
    })).toBeUndefined();
  });
});
