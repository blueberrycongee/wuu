import type { LanguagePreferenceStore } from '../../../../desktop/src/renderer/i18n';
import type { LanguagePreference } from '../../../../desktop/src/shared/protocol';

const key = 'wuu.web.language';
const listeners = new Set<(preference: LanguagePreference) => void>();

// Account screens and the connected renderer share one local preference,
// including while no desktop bridge is installed.
export const languagePreferenceStore: LanguagePreferenceStore = {
  get() {
    const value = localStorage.getItem(key);
    return value === 'zh-CN' || value === 'en-US' ? value : 'system';
  },
  async set(language) {
    localStorage.setItem(key, language);
    for (const listener of listeners) listener(language);
    return { ok: true, language };
  },
  subscribe(listener) {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  },
};
