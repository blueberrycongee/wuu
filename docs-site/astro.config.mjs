import { readFileSync } from "node:fs"

import { unified } from "@astrojs/markdown-remark"
import starlight from "@astrojs/starlight"
import { defineConfig } from "astro/config"
import remarkCjkFriendly from "remark-cjk-friendly/parseOnly"

import remarkMermaidBlocks from "./src/remark-mermaid.mjs"

const manifest = JSON.parse(readFileSync(new URL("../docs/site.json", import.meta.url), "utf8"))
const productionSite = process.env.DOCS_SITE_URL ?? "https://blueberrycongee.github.io"
const productionBase = process.env.DOCS_BASE ?? "/wuu"
const configuredBase = process.env.CI ? productionBase : "/"
const basePrefix = configuredBase === "/" ? "" : configuredBase.replace(/\/$/, "")

const hrefFromPage = (page, localeRoot) => {
  const route = page.replace(/\.md$/, "").replace(/\/index$/, "/")
  if (route === localeRoot || route.startsWith(`${localeRoot}/`)) {
    return route.slice(localeRoot.length) || "/"
  }
  return `${productionSite}${basePrefix}/${route}`
}
const titleFromPage = (page) => {
  const markdown = readFileSync(new URL(`../docs/${page}`, import.meta.url), "utf8")
  return markdown.match(/^#\s+(.+)$/m)?.[1] ?? page
}
const sidebar = Object.values(manifest.locales).map((config) => ({
  label: `__locale:${config.root}`,
  items: config.navigation.map((group) => ({
    label: group.title,
    collapsed: true,
    items: group.pages.map((page) => ({
      label: titleFromPage(page),
      link: hrefFromPage(page, config.root),
    })),
  })),
}))

export default defineConfig({
  site: productionSite,
  base: configuredBase,
  markdown: {
    processor: unified({ remarkPlugins: [remarkCjkFriendly, remarkMermaidBlocks] }),
  },
  integrations: [
    starlight({
      title: "wuu",
      description:
        manifest.site.descriptions?.[manifest.site.default_locale] ?? manifest.site.description,
      defaultLocale: "zh-cn",
      locales: {
        "zh-cn": { label: "简体中文", lang: "zh-CN" },
        en: { label: "English", lang: "en" },
      },
      sidebar,
      routeMiddleware: "./src/route-data.ts",
      components: {
        Head: "./src/components/Head.astro",
        SiteTitle: "./src/components/SiteTitle.astro",
        PageTitle: "./src/components/PageTitle.astro",
        LanguageSelect: "./src/components/LanguageMenu.astro",
        ThemeSelect: "./src/components/ThemeMenu.astro",
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/blueberrycongee/wuu",
        },
      ],
      customCss: ["./src/styles/custom.css"],
      markdown: {
        processedDirs: ["./generated-content/docs"],
      },
      expressiveCode: {
        themes: ["github-light", "github-dark"],
      },
      favicon: "/site-assets/brand/wuu.svg",
      editLink: {
        baseUrl: "https://github.com/blueberrycongee/wuu/edit/main/docs/",
      },
      pagination: true,
      credits: false,
    }),
  ],
})
