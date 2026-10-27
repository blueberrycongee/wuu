import {
  LANGUAGE_PREFERENCES,
  type LanguagePreference,
} from "../shared/protocol";
import { useI18n } from "./i18n";
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
    <div className="theme-segmented settings-language-options" role="group" aria-label={t("settings.languageGroup")}>
      {options.map((option) => (
        <button key={option.value} type="button" aria-pressed={preference === option.value}
          data-testid={`settings-language-${option.value}`} onClick={() => setPreference(option.value)}>
          {option.label}
        </button>
      ))}
    </div>
  );
}
