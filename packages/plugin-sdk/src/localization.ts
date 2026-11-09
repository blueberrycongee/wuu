/** Resolve registered translations consistently in desktop and author previews. */
export function createLocaleTranslator(
  locale: string,
  entries: (locale: string) => Readonly<Record<string, string>>,
): (key: string, values?: Readonly<Record<string, string | number>>) => string {
  const language = locale.split("-", 1)[0] ?? locale;
  const localeChain = [...new Set([locale, language, "en-US", "en"])];
  return (key, values = {}) => {
    let template = key;
    for (const candidate of localeChain) {
      const value = entries(candidate)[key];
      if (value !== undefined) {
        template = value;
        break;
      }
    }
    return template.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
  };
}
