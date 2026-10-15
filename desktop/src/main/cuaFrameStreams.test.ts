import type { ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { linkSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough, Writable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import {
  CUA_PIP_FORCE_STOP_TIMEOUT_MS,
  CUAStreamDecoder,
  CUANativePreview,
  cuaFrameHelperCandidates,
  isSameExecutableFile,
} from "./cuaFrameStreams";

function packet(header: object, payload = Buffer.alloc(0)) {
  const json = Buffer.from(JSON.stringify(header));
  const prefix = Buffer.alloc(12);
  prefix.write("WCU1"); prefix.writeUInt32BE(json.length, 4); prefix.writeUInt32BE(payload.length, 8);
  return Buffer.concat([prefix, json, payload]);
}

describe("CUAStreamDecoder", () => {
  it("decodes fragmented native PiP events and raw video", () => {
    const decoder = new CUAStreamDecoder();
    const frame = { event: "frame", sequence: 1, generation: 1, configuration: 1,
      codec: "avc1.42E01F", keyframe: true, timestamp: 100, description: [1, 66, 224, 31], width: 320, height: 240 };
    const payload = Buffer.from([0, 0, 0, 3, 101, 10, 255]);
    const data = Buffer.concat([packet({ event: "ready" }), packet(frame, payload)]);
    expect(decoder.push(data.subarray(0, 7))).toEqual([]);
    expect(decoder.push(data.subarray(7, data.length - 2))).toEqual([{ event: "ready" }]);
    expect(decoder.push(data.subarray(-2))).toEqual([{ ...frame, data: payload }]);
  });

  it("preserves UTF-8 characters split across pipe chunks", () => {
    const decoder = new CUAStreamDecoder();
    const data = packet({ event: "capture_status", message: "窗口已关闭" });
    const split = data.indexOf(Buffer.from("窗")) + 1;
    expect(decoder.push(data.subarray(0, split))).toEqual([]);
    expect(decoder.push(data.subarray(split))).toEqual([{ event: "capture_status", message: "窗口已关闭" }]);
  });

  it("retains incomplete records and rejects oversized declared lengths before buffering the body", () => {
    const decoder = new CUAStreamDecoder();
    const data = packet({ event: "capture_status", status: "idle" });
    expect(decoder.push(data.subarray(0, -1))).toEqual([]);
    expect(decoder.push(data.subarray(-1))).toEqual([{ event: "capture_status", status: "idle" }]);
    const oversized = Buffer.from(data.subarray(0, 12));
    oversized.writeUInt32BE(3_000_001, 8);
    expect(() => decoder.push(oversized)).toThrow("size limit");
  });

  it("decodes user-resized PiP geometry", () => {
    const decoder = new CUAStreamDecoder();
    const geometry = { event: "geometry", x: 20, y: 30, width: 520, height: 300 };
    expect(decoder.push(packet(geometry))).toEqual([geometry]);
  });
});

describe("CUANativePreview", () => {
  it("routes binary frames and acknowledgements to independent sessions on one helper", () => {
    const commands: Array<Record<string, unknown>> = [];
    const child = Object.assign(new EventEmitter(), {
      stdin: new Writable({ write: (chunk, _encoding, callback) => { commands.push(JSON.parse(chunk.toString())); callback(); } }),
      stdout: new PassThrough(), stderr: new PassThrough(), kill: vi.fn(() => true), pid: 1234,
    }) as unknown as ChildProcessWithoutNullStreams;
    const spawnHelper = vi.fn(() => child);
    const firstFrame = vi.fn(), secondFrame = vi.fn();
    const first = new CUANativePreview("/tmp/wuu-cua-mac-pip", "first", "Finder", 1, 10, vi.fn(), vi.fn(), firstFrame, spawnHelper as unknown as typeof spawn);
    const second = new CUANativePreview("/tmp/wuu-cua-mac-pip", "second", "TextEdit", 2, 20, vi.fn(), vi.fn(), secondFrame, spawnHelper as unknown as typeof spawn);
    first.start(); second.start();
    const sessions = commands.filter(command => command.type === "open").map(command => command.session);
    expect(spawnHelper).toHaveBeenCalledTimes(1);
    const header = { event: "frame", sequence: 1, generation: 1, configuration: 1,
      codec: "avc1.42E01F", keyframe: false, timestamp: 100, width: 320, height: 240 };
    const payload = Buffer.from([0, 0, 0, 1, 1]);
    child.stdout.emit("data", Buffer.concat(sessions.map(session => packet({ ...header, session }, payload))));
    expect(firstFrame).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ session: sessions[0], data: payload }));
    expect(secondFrame).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ session: sessions[1], data: payload }));
    first.acknowledgeFrame(1); second.acknowledgeFrame(1);
    expect(commands.filter(command => command.type === "ack")).toEqual(sessions.map(session => ({ type: "ack", sequence: 1, session })));
    first.stop();
    child.stdout.emit("data", packet({ event: "closed", session: sessions[0] }));
    expect(second.isLive()).toBe(true);
    expect(child.kill).not.toHaveBeenCalled();
    second.stop();
    child.stdout.emit("data", packet({ event: "closed", session: sessions[1] }));
    child.emit("exit", 0, null);
  });

  it("turns an asynchronous EPIPE into a recoverable helper failure", async () => {
    const pipeError = Object.assign(new Error("write EPIPE"), { code: "EPIPE" });
    const stdin = new Writable({
      write: (_chunk, _encoding, callback) => callback(pipeError),
    });
    const child = Object.assign(new EventEmitter(), {
      stdin,
      stdout: new PassThrough(),
      stderr: new PassThrough(),
      kill: vi.fn(() => true),
      pid: 1234,
    }) as unknown as ChildProcessWithoutNullStreams;
    const onError = vi.fn();
    const onEvent = vi.fn();
    const onFrame = vi.fn();
    const spawnHelper = vi.fn(() => child);
    const pip = new CUANativePreview(
      "/tmp/wuu-cua-mac-pip",
      "thread-id",
      "Finder",
      undefined,
      undefined,
      onEvent,
      onError,
      onFrame,
      spawnHelper as unknown as typeof spawn,
    );

    pip.start();
    pip.setVisible(true);
    await vi.waitFor(() => expect(onError).toHaveBeenCalledWith("write EPIPE"));

    expect(pip.isLive()).toBe(false);
    expect(child.kill).toHaveBeenCalledWith("SIGTERM");
    expect(onError).toHaveBeenCalledTimes(1);

    child.stdout.emit("data", Buffer.concat([
      packet({ event: "frame", sequence: 1, generation: 1, configuration: 1, codec: "avc1.42E01F",
        keyframe: false, timestamp: 100, width: 320, height: 240 }, Buffer.from([0, 0, 0, 1, 1])),
      packet({ event: "capture_status", status: "healthy" }),
    ]));
    pip.start();
    expect(onFrame).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
    expect(spawnHelper).toHaveBeenCalledTimes(1);
  });
});

describe("cuaFrameHelperCandidates", () => {
  it("allows native staging enough time to restore before a forced stop", () => {
    expect(CUA_PIP_FORCE_STOP_TIMEOUT_MS).toBeGreaterThanOrEqual(2_000);
  });

  it("keeps the PiP on a physical sibling path instead of the MCP executable", () => {
    expect(cuaFrameHelperCandidates(
      { WUU_CUA_MAC_HELPER: "/app/bin/wuu-cua-mac" },
      "/app",
      ["/repo"],
    )).toEqual([
      join("/app", "bin", "wuu-cua-mac-pip"),
      join("/repo", "desktop", "build", "bin", "wuu-cua-mac-pip"),
    ]);
  });

  it("rejects an override that points PiP back at the MCP executable", () => {
    expect(cuaFrameHelperCandidates(
      {
        WUU_CUA_MAC_HELPER: "/app/bin/wuu-cua-mac",
        WUU_CUA_MAC_PIP_HELPER: "/app/bin/wuu-cua-mac",
      },
      undefined,
      [],
    )).toEqual([join("/app", "bin", "wuu-cua-mac-pip")]);
  });

  it("detects aliases that resolve to the same executable file", () => {
    const root = mkdtempSync(join(tmpdir(), "wuu-cua-helper-"));
    try {
      const mcp = join(root, "wuu-cua-mac");
      const symlink = join(root, "wuu-cua-mac-pip-symlink");
      const hardlink = join(root, "wuu-cua-mac-pip-hardlink");
      const copy = join(root, "wuu-cua-mac-pip-copy");
      writeFileSync(mcp, "signed helper bytes");
      symlinkSync(mcp, symlink);
      linkSync(mcp, hardlink);
      writeFileSync(copy, "signed helper bytes");

      expect(isSameExecutableFile(symlink, mcp)).toBe(true);
      expect(isSameExecutableFile(hardlink, mcp)).toBe(true);
      expect(isSameExecutableFile(copy, mcp)).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
