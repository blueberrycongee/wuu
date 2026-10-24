import * as React from "react";
import type { ApplicationServiceV1 } from "../../packages/plugin-sdk/src/ui";
import { createDraftSessionTab, initialState } from "../src/renderer/AppState";
import { ApplicationController } from "../src/renderer/plugins/ApplicationController";
import type { WuuDesktopApi } from "../src/shared/protocol";
import { PluginHost } from "../src/renderer/plugins/PluginHost";

/** Synthetic draft owner behind a public generation; never connects a runtime. */
export async function createFixtureApplication(): Promise<ApplicationServiceV1> {
  if (!window.wuu) {
    const unavailable = async (): Promise<never> => {throw new Error("Runtime calls are unavailable in this isolated fixture");};
    const api: Partial<WuuDesktopApi> = {listEngines: unavailable, startThread: unavailable, startTurn: unavailable};
    Object.assign(window, {wuu: api});
  }
  const context = { kind: "no_project" as const, cwd: "/tmp/wuu-ui-fixture" };
  const controller = new ApplicationController({ ...initialState, activeContext: context,
    activeSessionTabID: "fixture", sessionTabs: [createDraftSessionTab("fixture", context)] });
  const host = new PluginHost({react: React, application: controller});
  let application: ApplicationServiceV1 | undefined;
  const pluginId = "isolated-ui-fixture";
  await host.activateGeneration({pluginId, generation:"preview", register: api => {application = api.application;}});
  if (!application) throw new Error("The fixture application generation is unavailable");
  window.addEventListener("pagehide", () => host.unload(pluginId), {once:true});
  return application;
}
