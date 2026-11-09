import { describe, expect, it } from "vitest";
import { AppServerResponseError } from "./appServerErrors";
import { encodePluginRuntimeResponse } from "./pluginRuntimeIPC";
import { decodePluginRuntimeResponse } from "../shared/pluginRuntimeIPC";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";

describe("private plugin runtime IPC", () => {
  it("unwraps only the transport envelope, never a same-shaped provider result", async () => {
    const result = { result: { ok: false, error: { code: "service_optional_absent", message: "data, not a failure" } } };
    expect(decodePluginRuntimeResponse(structuredClone(await encodePluginRuntimeResponse(async () => result)))).toEqual(result);
  });
  it("preserves structured service failures across Electron serialization", async () => {
    const wire = structuredClone(await encodePluginRuntimeResponse(async () => { throw new AppServerResponseError("No configured provider", "service_optional_absent"); }));
    expect(() => decodePluginRuntimeResponse(wire)).toThrow(expect.objectContaining({ code: "service_optional_absent", message: "No configured provider" }));
  });
  it("does not turn an arbitrary error property into a host absence", async () => {
    const wire = await encodePluginRuntimeResponse(async () => { throw Object.assign(new Error("Provider failed"), { code: "service_optional_absent" }); });
    expect(() => decodePluginRuntimeResponse(wire)).toThrow(expect.objectContaining({ message: "Provider failed", code: undefined }));
  });
  it("does not upgrade a provider retirement code to host cancellation", async () => {
    const wire = await encodePluginRuntimeResponse(async () => { throw new AppServerResponseError("Provider failed", "WUU_PLUGIN_SCOPE_RETIRED"); });
    try { decodePluginRuntimeResponse(wire); throw new Error("Expected failure"); }
    catch (error) { expect(error).not.toBeInstanceOf(PluginLifecycleCancellationError); expect(error).toMatchObject({ message: "Provider failed", code: "service_unavailable" }); }
  });
  it("retains owner-proven retirement separately from ordinary failure", async () => {
    const wire = structuredClone(await encodePluginRuntimeResponse(async () => { throw new PluginLifecycleCancellationError(); }));
    expect(() => decodePluginRuntimeResponse(wire)).toThrow(PluginLifecycleCancellationError);
  });
});
