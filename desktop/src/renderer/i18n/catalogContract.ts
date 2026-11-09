const TRANSLATION_KEY_PATTERN = /^[a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)*$/;
const PLACEHOLDER_PATTERN = /\{(\w+)\}/g;

type Catalogs = Record<string, Record<string, string>>;

function placeholders(template: string): string[] {
  return [...template.matchAll(PLACEHOLDER_PATTERN)]
    .map((match) => match[1])
    .sort();
}

/**
 * Enforces the catalog invariants that an automated maintainer can otherwise
 * miss while making a locally type-correct edit. Throws a key-specific error
 * so the next agent can repair the catalog without reconstructing context.
 */
export function assertCatalogContract(catalogs: Catalogs): void {
  const entries = Object.entries(catalogs);
  if (entries.length === 0) throw new Error("[i18n] No catalogs registered");

  const [referenceLocale, referenceCatalog] = entries[0];
  const referenceKeys = Object.keys(referenceCatalog).sort();

  for (const key of referenceKeys) {
    if (!TRANSLATION_KEY_PATTERN.test(key)) {
      throw new Error(`[i18n] Invalid key format: ${referenceLocale}.${key}`);
    }
  }

  for (const [locale, catalog] of entries) {
    const keys = Object.keys(catalog).sort();
    if (keys.length !== referenceKeys.length) {
      throw new Error(
        `[i18n] Key count mismatch: ${locale} has ${keys.length}, expected ${referenceKeys.length}`,
      );
    }

    for (let index = 0; index < referenceKeys.length; index += 1) {
      const key = referenceKeys[index];
      if (keys[index] !== key) {
        throw new Error(
          `[i18n] Key mismatch: ${locale}.${keys[index] ?? "<missing>"}; expected ${key}`,
        );
      }

      const translation = catalog[key];
      if (translation.trim().length === 0) {
        throw new Error(`[i18n] Blank translation: ${locale}.${key}`);
      }

      const expected = placeholders(referenceCatalog[key]);
      const actual = placeholders(translation);
      if (actual.join("\0") !== expected.join("\0")) {
        throw new Error(
          `[i18n] Placeholder mismatch: ${locale}.${key} has {${actual.join(", ")}}; expected {${expected.join(", ")}}`,
        );
      }
    }
  }
}
