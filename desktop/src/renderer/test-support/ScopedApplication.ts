import * as React from "react";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationController } from "../plugins/ApplicationController";
import { PluginHost } from "../plugins/PluginHost";

// jsdom 25 lacks this browser API. Remove when the test DOM supports AbortSignal.any.
if (!AbortSignal.any) {
  Object.defineProperty(AbortSignal, "any", { configurable: true, value: (signals: AbortSignal[]) => {
    const controller = new AbortController();
    const subscriptions: Array<() => void> = [];
    const abort = (source: AbortSignal) => {
      controller.abort(source.reason);
      subscriptions.splice(0).forEach(stop => stop());
    };
    for (const signal of signals) {
      if (signal.aborted) { abort(signal); break; }
      const listener = () => abort(signal);
      signal.addEventListener("abort", listener, { once: true });
      subscriptions.push(() => signal.removeEventListener("abort", listener));
    }
    return controller.signal;
  } });
}

/** Keep controller mutation access separate from the generation's public service. */
export async function createScopedTestApplication(controller: ApplicationController): Promise<{
  application: ApplicationServiceV1;
  dispose: () => void;
}> {
  const host = new PluginHost({ react: React, application: controller });
  let application: ApplicationServiceV1 | undefined;
  const pluginId = "scoped-application-fixture";
  await host.activateGeneration({ pluginId, generation: "test", register: api => { application = api.application; } });
  if (!application) throw new Error("Test generation did not register its application service");
  return { application, dispose: () => host.unload(pluginId) };
}
