import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { buildSync } from "esbuild";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { setImmediate } from "node:timers/promises";
import { Worker } from "node:worker_threads";
import { WorkspaceDirectoryReader } from "./workspaceDirectoryReader";

let fixture: string;
let workerURL: string;
const readers: WorkspaceDirectoryReader[] = [];
const workers: Worker[] = [];
const gates: Int32Array[] = [];

beforeAll(() => {
  fixture = mkdtempSync(join(tmpdir(), "wuu-directory-worker-"));
  const bundle = join(fixture, "worker.mjs");
  buildSync({ entryPoints: [resolve("src/main/workspaceDirectoryWorker.ts")], outfile: bundle, bundle: true, platform: "node", format: "esm" });
  workerURL = pathToFileURL(bundle).href;
});
afterEach(async () => {
  for (const gate of gates.splice(0)) { Atomics.store(gate, 0, 1); Atomics.notify(gate, 0); }
  await Promise.allSettled(readers.splice(0).map(reader => reader.close()));
  vi.restoreAllMocks();
  vi.useRealTimers();
  expect(workers.splice(0).every(worker => worker.threadId === -1)).toBe(true);
});
afterAll(() => rmSync(fixture, { recursive: true, force: true }));

function workspace(name: string): string {
  const root = mkdtempSync(join(fixture, `${name}-`));
  mkdirSync(join(root, "src"));
  writeFileSync(join(root, "src", `${name}.txt`), name);
  return root;
}

function gatedReader(root: string, phase: string) {
  const gate = new Int32Array(new SharedArrayBuffer(4));
  gates.push(gate);
  let entered!: () => void;
  const blocked = new Promise<void>(done => { entered = done; });
  const spawned: Worker[] = [];
  const reader = new WorkspaceDirectoryReader(() => {
    // The real production worker runs the original synchronous algorithm. A
    // shared-memory gate simulates blocked native I/O without sleeps or a GUI.
    const worker = new Worker(`
      const { workerData, parentPort } = require('node:worker_threads');
      const fs = require('node:fs');
      const gate = new Int32Array(workerData.gate);
      const [method, path] = workerData.phase === 'root realpath'
        ? ['realpathSync', workerData.root]
        : workerData.phase === 'directory realpath'
          ? ['realpathSync', require('node:path').join(workerData.root, 'src')]
          : [workerData.phase + 'Sync', require('node:path').join(workerData.root, 'src')];
      const original = fs[method];
      let held = false;
      fs[method] = (...args) => {
        if (!held && String(args[0]) === path) {
          held = true;
          parentPort.postMessage({ blocked: true });
          Atomics.wait(gate, 0, 0);
        }
        return original(...args);
      };
      require('node:module').syncBuiltinESMExports();
      import(workerData.url);
    `, { eval: true, workerData: { gate: gate.buffer, root, phase, url: workerURL } });
    worker.on("message", message => { if (message.blocked) entered(); });
    spawned.push(worker);
    workers.push(worker);
    return worker;
  });
  readers.push(reader);
  return { reader, blocked, spawned, release: () => { Atomics.store(gate, 0, 1); Atomics.notify(gate, 0); } };
}

describe("WorkspaceDirectoryReader worker isolation", () => {
  it.each(["root realpath", "directory realpath", "stat", "readdir"])(
    "keeps the main loop and a second workspace responsive during blocked %s",
    async phase => {
      const originalRoot = workspace("original");
      const nextRoot = workspace("current");
      const { reader, blocked, spawned, release } = gatedReader(originalRoot, phase);
      const pending = reader.list(originalRoot, "src", 1);
      let completed = false;
      void pending.then(() => { completed = true; }, () => { completed = true; });
      await blocked;
      await setImmediate();
      expect(completed).toBe(false);
      await expect(reader.list(nextRoot, "src", 1)).resolves.toMatchObject({
        root: nextRoot, path: "src", entries: [{ name: "current.txt", path: "src/current.txt", kind: "file" }],
      });
      expect(completed).toBe(false);
      release();
      await expect(pending).resolves.toMatchObject({
        root: originalRoot, path: "src", entries: [{ name: "original.txt", path: "src/original.txt", kind: "file" }],
      });
      await reader.list(nextRoot, "src", 1);
      expect(spawned).toHaveLength(2);
    },
  );

  it("bounds admission and cancels queued/running results without replaying them", async () => {
    const root = workspace("blocked");
    const { reader, blocked, spawned, release } = gatedReader(root, "readdir");
    const outcomes = Array.from({ length: 66 }, () => reader.list(root, "src", 7).then(() => "resolved", error => error.name));
    await blocked;
    await expect(reader.list(root, "src", 8)).rejects.toThrow("queue is full");
    expect(spawned).toHaveLength(2);
    reader.cancelOwner(7);
    expect(await Promise.all(outcomes)).toEqual(Array(66).fill("AbortError"));
    const nextRoot = workspace("after-cancel");
    const next = reader.list(nextRoot, "src", 8);
    release();
    await expect(next).resolves.toMatchObject({ root: nextRoot });
    expect(spawned).toHaveLength(2);
  });

  it("preserves native errors and rejects worker failure without replaying the request", async () => {
    const root = workspace("errors");
    const { reader, blocked, spawned } = gatedReader(root, "readdir");
    const pending = reader.list(root, "src", 1);
    const rejected = expect(pending).rejects.toThrow("worker exited");
    await blocked;
    await spawned[0].terminate();
    await rejected;
    await expect(reader.list(root, "missing", 1)).rejects.toMatchObject({ code: "ENOENT" });
    await expect(reader.list(root, "../outside", 1)).rejects.toThrow("invalid workspace directory path");
  });

  it("reports shutdown that cannot finish without keeping workers referenced", async () => {
    const root = workspace("stuck-shutdown");
    const { reader, blocked, spawned } = gatedReader(root, "stat");
    const pending = reader.list(root, "src", 1);
    const rejected = expect(pending).rejects.toMatchObject({ name: "AbortError" });
    await blocked;
    const worker = spawned[0];
    const terminate = worker.terminate.bind(worker);
    const spy = vi.spyOn(worker, "terminate").mockReturnValue(new Promise(() => {}));
    const unref = vi.spyOn(worker, "unref");
    vi.useFakeTimers();
    let failure: Error | undefined;
    const closed = reader.close().catch(error => { failure = error; });
    try {
      await vi.runAllTimersAsync();
      expect(failure?.message).toContain("directory worker shutdown timed out");
      await closed;
      await rejected;
      expect(unref).toHaveBeenCalled();
    } finally {
      readers.splice(readers.indexOf(reader), 1);
      spy.mockRestore();
      vi.useRealTimers();
      await terminate();
    }
  });

  it("terminates a blocked worker fixture on close and rejects later admission", async () => {
    const root = workspace("shutdown");
    const { reader, blocked, spawned } = gatedReader(root, "stat");
    const pending = reader.list(root, "src", 1);
    const rejected = expect(pending).rejects.toMatchObject({ name: "AbortError" });
    await blocked;
    await reader.close();
    await rejected;
    expect(spawned.every(worker => worker.threadId === -1)).toBe(true);
    await expect(reader.list(root, "src", 1)).rejects.toThrow("closed");
  });
});
