import type { ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { linkSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough, Writable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import {
  CUA_PIP_FORCE_STOP_TIMEOUT_MS,
  CUALineDecoder,
  CUANativePiP,
  cuaFrameHelperCandidates,
  isSameExecutableFile,
} from "./cuaFrameStreams";

describe("CUALineDecoder", () => {
  it("decodes fragmented native PiP events", () => {
    const decoder = new CUALineDecoder();
    expect(decoder.push('{"event":"rea')).toEqual([]);
    expect(decoder.push('dy","width":320,"height":240}\n{"event":"user_input"}\n')).toEqual([
      { event: "ready", width: 320, height: 240 },
      { event: "user_input" },
    ]);
  });

  it("preserves UTF-8 characters split across pipe chunks", () => {
    const decoder = new CUALineDecoder();
    const data = Buffer.from('{"event":"capture_status","message":"窗口已关闭"}\n');
    const split = data.indexOf(Buffer.from("窗")) + 1;
    expect(decoder.push(data.subarray(0, split))).toEqual([]);
    expect(decoder.push(data.subarray(split))).toEqual([{ event: "capture_status", message: "窗口已关闭" }]);
  });

  it("keeps an incomplete final line for the next chunk", () => {
    const decoder = new CUALineDecoder();
    expect(decoder.push('{"event":"capture_status","status":"idle"}')).toEqual([]);
    expect(decoder.push("\n")).toEqual([{ event: "capture_status", status: "idle" }]);
  });

  it("decodes user-resized PiP geometry", () => {
    const decoder = new CUALineDecoder();
    expect(decoder.push('{"event":"geometry","x":20,"y":30,"width":520,"height":300}\n')).toEqual([
      { event: "geometry", x: 20, y: 30, width: 520, height: 300 },
    ]);
  });
});

describe("CUANativePiP", () => {
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
    const pip = new CUANativePiP(
      "/tmp/wuu-cua-mac-pip",
      "thread-id",
      "Finder",
      undefined,
      undefined,
      { x: 0, y: 0, width: 320, height: 240 },
      vi.fn(),
      onError,
      (() => child) as unknown as typeof spawn,
    );

    pip.start();
    pip.setVisible(true);
    await vi.waitFor(() => expect(onError).toHaveBeenCalledWith("write EPIPE"));

    expect(pip.isLive()).toBe(false);
    expect(child.kill).toHaveBeenCalledWith("SIGTERM");
    expect(onError).toHaveBeenCalledTimes(1);
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
