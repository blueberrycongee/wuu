import * as React from "react";
import type { ApplicationServiceV1 } from "../../packages/plugin-sdk/src/ui";
import { createDraftSessionTab, initialState } from "../src/renderer/AppState";
import { ApplicationController } from "../src/renderer/plugins/ApplicationController";
import type { HostApplicationService } from "../src/renderer/plugins/HostApplicationService";
import type { WuuDesktopApi } from "../src/shared/protocol";
import { PluginHost } from "../src/renderer/plugins/PluginHost";
import { createDefaultWorkbench } from "../src/renderer/plugins/Workbench";

/** Synthetic draft owner behind a public generation; never connects a runtime. */
export async function createFixtureApplication(): Promise<ApplicationServiceV1> {
  if (!window.wuu) {
    const unavailable = async (): Promise<never> => {throw new Error("Runtime calls are unavailable in this isolated fixture");};
    const api: Partial<WuuDesktopApi> = {listEngines: unavailable, startThread: unavailable, startTurn: unavailable};
    Object.assign(window, {wuu: api});
  }
  const context = { kind: "no_project" as const, cwd: "/tmp/wuu-ui-fixture" };
  // Contribution registries exist only inside an activated generation, and the
  // controller reads them (and the workbench) during construction. The host's
  // application binding therefore forwards to the controller lazily.
  let controller: ApplicationController | undefined;
  const host = new PluginHost({ react: React, application: new Proxy({} as HostApplicationService, {
    get: (_target, key) => {
      if (!controller) throw new Error("The fixture application is not activated yet");
      const value = Reflect.get(controller, key, controller);
      return typeof value === "function" ? value.bind(controller) : value;
    },
    has: (_target, key) => controller !== undefined && key in controller,
  })});
  const pluginId = "isolated-ui-fixture";
  let application: ApplicationServiceV1 | undefined;
  await host.activateGeneration({pluginId, generation:"preview", register: api => {
    const workbench = createDefaultWorkbench(api.contributions, () => controller!);
    controller = new ApplicationController({ ...initialState, activeContext: context,
      activeSessionTabID: "fixture", sessionTabs: [createDraftSessionTab("fixture", context)] },
      { applicationProviderId: pluginId, contributions: api.contributions, workbench });
    workbench.start();
    application = api.application;
  }});
  if (!application) throw new Error("The fixture application generation is unavailable");
  window.addEventListener("pagehide", () => host.unload(pluginId), {once:true});
  return application;
}
