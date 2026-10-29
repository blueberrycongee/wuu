import { describe, expect, it, vi } from "vitest";
import { scopeApplicationWorkbench, type ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { retainPresentationReads } from "./PresentationApplication";

// React's development performance track compares old and new props, including
// probing $$typeof and enumerating nested objects after the old scope retires.
function inspectLikeReact(previous: Record<string, any>, next: Record<string, any>, depth = 0): void {
  for (const key in previous) void (key in next);
  for (const key in next) {
    if (!(key in previous)) continue;
    const left = previous[key], right = next[key];
    if (left === right || depth >= 3 || !left || !right || typeof left !== "object" || typeof right !== "object") continue;
    if (left.$$typeof !== right.$$typeof) continue;
    if (Object.prototype.toString.call(left) === "[object Object]" && Object.prototype.toString.call(right) === "[object Object]") {
      inspectLikeReact(left, right, depth + 1);
    }
  }
}

describe("retired presentation inspection", () => {
  it("lets React compare generations without opening or invoking a retired scope", async () => {
    const create = () => {
      const lifetime = new AbortController();
      const unused = vi.fn(() => { lifetime.signal.throwIfAborted(); return {}; });
      const invoke = vi.fn(async () => { lifetime.signal.throwIfAborted(); });
      const workbench = scopeApplicationWorkbench(() => ({ getSnapshot: () => "visible", deactivateRegion: () => {} }) as unknown as ApplicationWorkbenchV1, lifetime.signal);
      const service = { workbench, get unused() { return unused(); }, invoke } as unknown as ApplicationServiceV1;
      const presentation = retainPresentationReads(service, lifetime.signal);
      return { lifetime, presentation, unused, invoke };
    };
    const old = create(), next = create();
    const retainedWorkbench = old.presentation.workbench;
    expect(retainedWorkbench.getSnapshot()).toBe("visible");
    const invoke = old.presentation.invoke;
    void next.presentation.workbench;
    const reason = new PluginLifecycleCancellationError();
    old.lifetime.abort(reason);

    expect(() => inspectLikeReact(old.presentation as unknown as Record<string, any>, next.presentation as unknown as Record<string, any>)).not.toThrow();
    expect(old.unused).not.toHaveBeenCalled();
    expect(retainedWorkbench.getSnapshot()).toBe("visible");
    expect(() => retainedWorkbench.deactivateRegion("primary")).toThrow(reason);
    await expect(invoke("draft.start", {})).rejects.toBe(reason);
  });
});
