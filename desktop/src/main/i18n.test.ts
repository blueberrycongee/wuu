import { afterEach, describe, expect, it } from "vitest";
import {
  MAIN_TRANSLATION_RESOURCES,
  mainTranslate,
  resolveMainLocale,
  setMainLocale,
} from "./i18n";
import { assertCatalogContract } from "../renderer/i18n/catalogContract";

afterEach(() => setMainLocale("zh-CN"));

describe("main process i18n", () => {
  it("keeps native catalogs structurally aligned", () => {
    expect(() => assertCatalogContract(MAIN_TRANSLATION_RESOURCES)).not.toThrow();
  });

  it("resolves explicit and system language preferences", () => {
    expect(resolveMainLocale("zh-CN", "en-GB")).toBe("zh-CN");
    expect(resolveMainLocale("en-US", "zh-Hans-CN")).toBe("en-US");
    expect(resolveMainLocale("system", "zh-Hans-CN")).toBe("zh-CN");
    expect(resolveMainLocale("system", "en-GB")).toBe("en-US");
  });

  it("switches native strings immediately and interpolates values", () => {
    setMainLocale("en-US");
    expect(mainTranslate("chooseExistingFolder")).toBe(
      "Use an existing folder",
    );
    expect(mainTranslate("openConversation", { title: "Review" })).toBe(
      "Open conversation · Review",
    );
  });
});
