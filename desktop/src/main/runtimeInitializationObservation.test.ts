import { expect, it } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { RuntimeInitializationObservation, type InitializationObservation } from "./runtimeInitializationObservation";
const result = (workspace_root: string, graph: string) => ({ workspace_root, extension_inventory: [{ graph_generation: graph }] }) as InitializeResult;
it("publishes recovered authoritative inventory through the existing observation stream", () => {
  const observer = new RuntimeInitializationObservation(), events: InitializationObservation[] = [];
  observer.subscribe(event => events.push(event));
  const first = observer.begin(); observer.complete(first, result("/a", "old"));
  observer.recover({ phase: "started", afterRequestId: first, recoveryId: "repair" });
  observer.recover({ phase: "completed", recoveryId: "repair", initialized: result("/a", "new") });
  expect(events.at(-1)).toMatchObject({ phase: "completed", initialized: { extension_inventory: [{ graph_generation: "new" }] } });
});
it.each(["/b", "/a"])("never lets late recovery override newer workspace/process initialization at %s", root => {
  const observer = new RuntimeInitializationObservation(), events: InitializationObservation[] = [];
  observer.subscribe(event => events.push(event));
  const first = observer.begin();
  observer.recover({ phase: "started", afterRequestId: first, recoveryId: "old" });
  const newer = observer.begin(); observer.complete(newer, result(root, "new-process"));
  observer.recover({ phase: "completed", recoveryId: "old", initialized: result("/a", "old-process") });
  expect(events.at(-1)).toMatchObject({ phase: "completed", requestId: newer, initialized: { workspace_root: root } });
});
it("rejects a delayed recovery-start event when renderer initialization IPC is already newer", () => {
  const observer = new RuntimeInitializationObservation(), events: InitializationObservation[] = [];
  observer.subscribe(event => events.push(event));
  const old = observer.begin(), newer = observer.begin();
  observer.recover({ phase: "started", afterRequestId: old, recoveryId: "late" });
  observer.recover({ phase: "completed", recoveryId: "late", initialized: result("/a", "old") });
  observer.complete(newer, result("/c", "current"));
  expect(events).toHaveLength(3);
  expect(events.at(-1)).toMatchObject({ requestId: newer, initialized: { workspace_root: "/c" } });
});
