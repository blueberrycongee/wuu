import "./AbortSignal";
import * as React from "react";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationController } from "../plugins/ApplicationController";
import { PluginHost } from "../plugins/PluginHost";

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
