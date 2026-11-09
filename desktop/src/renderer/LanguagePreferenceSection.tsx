import {
  LANGUAGE_PREFERENCES,
  type LanguagePreference,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { SegmentedControl } from "./ui/SegmentedControl";

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
    testID: `settings-language-${value}`,
  }));
  return (
    <SegmentedControl className="settings-language-options" label={t("settings.languageGroup")}
      value={preference} options={options} onChange={setPreference} />
  );
}
