import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";

/**
 * Settings navigation unmounts the destination that is no longer active.
 * Keep its last confirmed read for the lifetime of the settings authority so
 * a return paints known content immediately while the page revalidates it in
 * the background. A renewed authority (for example after a plugin generation
 * change) starts without another authority's data.
 */
const retainedPageData = new WeakMap<ApplicationSettingsControlsV1, Map<string, unknown>>();

export function readRetainedSettingsData<T>(
  controls: ApplicationSettingsControlsV1,
  key: string,
): T | undefined {
  return retainedPageData.get(controls)?.get(key) as T | undefined;
}

export function writeRetainedSettingsData<T>(
  controls: ApplicationSettingsControlsV1,
  key: string,
  value: T,
): void {
  let values = retainedPageData.get(controls);
  if (!values) {
    values = new Map();
    retainedPageData.set(controls, values);
  }
  values.set(key, value);
}
