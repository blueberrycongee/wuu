import { createPreviewApplication } from "../../../../packages/plugin-sdk/preview/application";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";

/** Leaf Personal fixtures explicitly select the same workbench they render. */
export async function selectPersonalTestApplication(host: PluginHost): Promise<WorkbenchController> {
  const workbench = new WorkbenchController(host);
  const preview = createPreviewApplication();
  await host.activateGeneration({ pluginId: "test.application", generation: "one", graphGeneration: "test-graph", register(api) {
    api.registerApplicationProvider({ id: "ui.application", prepare: () => ({
      activate: async () => {},
      deactivate: async () => undefined,
      createScope: signal => ({ ...preview, signal, workbench }),
      dispose: async () => { workbench.dispose(); },
    }) });
  } });
  await host.selectApplicationBinding({ pluginId: "test.application", generation: "one", id: "ui.application" });
  return workbench;
}
