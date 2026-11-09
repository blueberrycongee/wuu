import { describe, expect, it } from "vitest";
import { APP_LOCALES, type AppLocale } from "../../shared/protocol";
import { assertCatalogContract } from "./catalogContract";
import { enUS } from "./resources/en-US";
import { zhCN, type TranslationKey } from "./resources/zh-CN";

const catalogs = {
  "zh-CN": zhCN,
  "en-US": enUS,
} satisfies Record<AppLocale, Record<TranslationKey, string>>;

describe("renderer translation catalogs", () => {
  it("registers every supported app locale", () => {
    expect(Object.keys(catalogs).sort()).toEqual([...APP_LOCALES].sort());
  });

  it("keeps keys, values, and placeholders aligned", () => {
    expect(() => assertCatalogContract(catalogs)).not.toThrow();
  });

  it("accepts reordered translations while still rejecting missing keys and interpolation arguments", () => {
    const reference = { "test.title": "Hello {name}", "test.detail": "Details" };
    expect(() => assertCatalogContract({
      en: reference,
      zh: { "test.detail": "详情", "test.title": "你好 {name}" },
    })).not.toThrow();
    expect(() => assertCatalogContract({ en: reference, zh: { "test.title": "你好 {name}" } })).toThrow();
    expect(() => assertCatalogContract({
      en: reference,
      zh: { "test.title": "你好 {other}", "test.detail": "详情" },
    })).toThrow();
  });
});
