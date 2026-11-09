declare module "highlight.js/lib/languages/*" {
  import type { LanguageFn } from "highlight.js";

  const language: LanguageFn;
  export default language;
}
