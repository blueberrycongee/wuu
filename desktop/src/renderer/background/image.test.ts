import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { processBackground } from "./image";

class WorkerStub {
  static instances: WorkerStub[] = [];
  onmessage?: (event: { data: unknown }) => void;
  onerror?: () => void;
  postMessage = vi.fn();
  terminate = vi.fn();
  constructor() { WorkerStub.instances.push(this); }
}
beforeEach(() => { WorkerStub.instances = []; vi.stubGlobal("Worker", WorkerStub); });
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

it("terminates canceled image jobs and does not let their late result complete the request", async () => {
  const controller = new AbortController();
  const result = processBackground(new Blob(["source"]), "none", true, controller.signal);
  const rejected = expect(result).rejects.toMatchObject({ name: "AbortError" });
  controller.abort();
  WorkerStub.instances[0].onmessage?.({ data: { image: new Blob(["late"]) } });
  await rejected;
  expect(WorkerStub.instances[0].terminate).toHaveBeenCalled();
});

it("releases workers on successful processing and decoding failures", async () => {
  const output = new Blob(["processed"]);
  const success = processBackground(new Blob(["source"]), "dither", false);
  WorkerStub.instances[0].onmessage?.({ data: { image: output } });
  expect(await success).toBe(output);
  expect(WorkerStub.instances[0].terminate).toHaveBeenCalledOnce();
  const failure = processBackground(new Blob(["broken"]), "none", false);
  const rejected = expect(failure).rejects.toThrow();
  WorkerStub.instances[1].onmessage?.({ data: { error: true } });
  await rejected;
  expect(WorkerStub.instances[1].terminate).toHaveBeenCalledOnce();
});

it("bounds a stuck decoder and frees its worker", async () => {
  vi.useFakeTimers();
  const result = processBackground(new Blob(["source"]), "ascii", false);
  const rejected = expect(result).rejects.toThrow("timed out");
  await vi.runAllTimersAsync();
  await rejected;
  expect(WorkerStub.instances[0].terminate).toHaveBeenCalledOnce();
});
