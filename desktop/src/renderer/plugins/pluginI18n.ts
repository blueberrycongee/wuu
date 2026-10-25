import type { PluginHost } from "./PluginHost";
import { createLocaleTranslator } from "../../../../packages/plugin-sdk/src/localization";

export function createPluginTranslator(
  host: PluginHost,
  locale: string,
): (key: string, values?: Readonly<Record<string, string | number>>) => string {
  return createLocaleTranslator(locale, candidate => host.getLocaleEntries(candidate));
}
