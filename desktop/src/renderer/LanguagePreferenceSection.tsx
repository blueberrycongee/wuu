import {
  LANGUAGE_PREFERENCES,
  type LanguagePreference,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { SelectMenu } from "./SelectMenu";
import type { TranslationKey } from "./i18n/resources/zh-CN";

const LANGUAGE_LABEL_KEYS = {
  system: "common.system",
  "zh-CN": "common.chinese",
  "en-US": "common.english",
} as const satisfies Record<LanguagePreference, TranslationKey>;

export function LanguagePreferenceControl(): JSX.Element {
  const { preference, setPreference, t } = useI18n();
  const options = LANGUAGE_PREFERENCES.map((value) => ({
    value,
    label: t(LANGUAGE_LABEL_KEYS[value]),
  }));
  return (
    <SelectMenu
      className="settings-language-select"
      triggerClassName="settings-language-trigger"
      ariaLabel={t("settings.languageGroup")}
      dataTestid="settings-language-select"
      value={preference}
      options={options}
      align="right"
      onChange={(value) => setPreference(value as LanguagePreference)}
    />
  );
}
