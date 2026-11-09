import type { UIContributionRegistryV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { createLocaleTranslator } from "../../../../packages/plugin-sdk/src/localization";

export function createPluginTranslator(
  host: Pick<UIContributionRegistryV1, "getLocaleEntries">,
  locale: string,
): (key: string, values?: Readonly<Record<string, string | number>>) => string {
  return createLocaleTranslator(locale, candidate => host.getLocaleEntries(candidate));
}
