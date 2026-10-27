// Optional partial customization: keep the configured store's persistence,
// revisions and write methods; add a default to the read view only.
import {
  delegateService, pluginSettingsStore, PLUGIN_SETTINGS_STORE_DESCRIPTOR,
  PLUGIN_SETTINGS_STORE_SERVICE, runJSONLRuntime,
  type PluginSettingsAddress, type RuntimePlugin,
} from '@wuu/plugin-sdk';

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [PLUGIN_SETTINGS_STORE_DESCRIPTOR],
      required_services: [{name:PLUGIN_SETTINGS_STORE_SERVICE,major_version:1,required:true}],
    };
  },
  invokeService: delegateService(PLUGIN_SETTINGS_STORE_DESCRIPTOR, {
    async read(input, host) {
      const view = await pluginSettingsStore(host).read(input.params as PluginSettingsAddress);
      return {...view, document:{...view.document, values:{appearance:'compact', ...view.document.values}}};
    },
  }),
};
runJSONLRuntime(plugin, {input:process.stdin,output:process.stdout}).catch(error => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
