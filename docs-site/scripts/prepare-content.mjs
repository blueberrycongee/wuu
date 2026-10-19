import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const repositoryRoot = path.resolve(siteRoot, "..")
const docsRoot = path.join(repositoryRoot, "docs")
const outputRoot = path.join(siteRoot, "generated-content/docs")
const manifest = JSON.parse(await readFile(path.join(docsRoot, "site.json"), "utf8"))

const pages = [
  ...new Set(
    Object.values(manifest.locales).flatMap((locale) =>
      locale.navigation.flatMap((group) => group.pages),
    ),
  ),
]
const publishedPages = new Set(pages)
const locales = Object.values(manifest.locales)
const localeRoots = locales.map((locale) => locale.root)

const localeKey = (root) =>
  Object.entries(manifest.locales).find(([, config]) => config.root === root)?.[0]
const fallbackDescription = (root) =>
  manifest.site.descriptions?.[localeKey(root)] ?? manifest.site.description
const homeDescription = (root) => manifest.site.home_descriptions?.[localeKey(root)]

function summarize(markdown, fallback) {
  const body = markdown.replace(/^#\s+.+(?:\r?\n)+/, "")
  for (const block of body.split(/\r?\n\s*\r?\n/)) {
    const trimmed = block.trim()
    if (!trimmed || /^(?:#|>|```|<|\||[-*+]\s|\d+\.\s)/.test(trimmed)) continue

    const text = trimmed
      .replace(/!\[([^\]]*)\]\([^)]+\)/g, "$1")
      .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
      .replace(/<[^>]+>/g, "")
      .replace(/`([^`]+)`/g, "$1")
      .replace(/\*\*([^*]+)\*\*/g, "$1")
      .replace(/\*([^*]+)\*/g, "$1")
      .replace(/\s+/g, " ")
      .trim()

    if (!text) continue
    if (text.length <= 170) return text

    const head = text.slice(0, 170)
    const sentenceEnd = Math.max(
      head.lastIndexOf("。"),
      head.lastIndexOf("！"),
      head.lastIndexOf("？"),
      head.lastIndexOf(". "),
      head.lastIndexOf("! "),
      head.lastIndexOf("? "),
    )
    if (sentenceEnd > 90) return head.slice(0, sentenceEnd + 1).trim()

    const wordEnd = Math.max(head.lastIndexOf("，"), head.lastIndexOf(", "), head.lastIndexOf(" "))
    return `${(wordEnd > 90 ? head.slice(0, wordEnd) : head).trimEnd()}…`
  }
  return fallback
}

const customizePages = locales.map((locale) => ({
  root: locale.root,
  pages: locale.navigation
    .flatMap((group) => group.pages)
    .filter((page) => page.startsWith(`${locale.root}/customize/`))
    .map((page) => page.slice(locale.root.length + 1)),
}))
const customizeBaseline = customizePages[0]
for (const locale of customizePages.slice(1)) {
  if (JSON.stringify(locale.pages) !== JSON.stringify(customizeBaseline.pages)) {
    throw new Error(
      `Customize documentation navigation is not bilingual: ${customizeBaseline.root} and ${locale.root} must publish the same relative pages in the same order`,
    )
  }
}

const routeFromPage = (page) => page.replace(/\.md$/, "").replace(/\/index$/, "")

function rewriteMarkdownLinks(markdown, page) {
  return markdown.replace(/(!?\[[^\]\n]*\])\(([^)\s]+)(?:\s+"[^"]*")?\)/g, (match, label, rawHref) => {
    const hashIndex = rawHref.indexOf("#")
    const href = hashIndex === -1 ? rawHref : rawHref.slice(0, hashIndex)
    const fragment = hashIndex === -1 ? "" : rawHref.slice(hashIndex)
    if (/^[a-z][a-z\d+.-]*:/i.test(href)) return match

    const target = path.posix.normalize(path.posix.join(path.posix.dirname(page), href))
    const sourceLocale = localeRoots.find((root) => page.startsWith(`${root}/`))
    const targetLocale = localeRoots.find((root) => target.startsWith(`${root}/`))
    if (
      sourceLocale &&
      targetLocale &&
      sourceLocale !== targetLocale &&
      page.startsWith(`${sourceLocale}/customize/`)
    ) {
      throw new Error(`Customize page ${page} links across locales: ${target}`)
    }
    if (target.startsWith("../")) {
      const repositoryPath = path.posix.normalize(path.posix.join("docs", target))
      const view = href.endsWith("/") ? "tree" : "blob"
      return `${label}(https://github.com/blueberrycongee/wuu/${view}/main/${repositoryPath}${fragment})`
    }
    // Astro transforms embedded images, but ordinary download links need an
    // original asset at a public URL relative to the rendered page route.
    if (!label.startsWith("!") && localeRoots.some((root) => target.startsWith(`${root}/assets/`))) {
      const relative = path.posix.relative(routeFromPage(page), `docs-assets/${target}`)
      return `${label}(${relative}${fragment})`
    }
    if (!href.endsWith(".md")) return match
    if (!publishedPages.has(target)) {
      throw new Error(`Published page ${page} links to unpublished documentation: ${target}`)
    }

    const relative = path.posix.relative(routeFromPage(page), routeFromPage(target)) || "."
    return `${label}(${relative}/${fragment})`
  })
}

await rm(outputRoot, { recursive: true, force: true })

for (const page of pages) {
  const source = path.resolve(docsRoot, page)
  if (!source.startsWith(`${docsRoot}${path.sep}`)) {
    throw new Error(`Documentation path escapes docs/: ${page}`)
  }

  const markdown = await readFile(source, "utf8")
  const titleMatch = markdown.match(/^#\s+(.+)$/m)
  if (!titleMatch) {
    throw new Error(`Published page has no level-one title: ${page}`)
  }

  const destination = path.join(outputRoot, page)
  await mkdir(path.dirname(destination), { recursive: true })
  const sourceLocale = localeRoots.find((root) => page.startsWith(`${root}/`))
  const description =
    page === `${sourceLocale}/index.md`
      ? homeDescription(sourceLocale) ?? summarize(markdown, fallbackDescription(sourceLocale))
      : summarize(markdown, fallbackDescription(sourceLocale))
  const body = rewriteMarkdownLinks(markdown.replace(/^#\s+.+(?:\r?\n)+/, ""), page)
  await writeFile(
    destination,
    `---\ntitle: ${JSON.stringify(titleMatch[1])}\ndescription: ${JSON.stringify(description)}\n---\n\n${body}`,
  )
}

const assetDirectories = ["zh-cn/assets", "en/assets"]
const publicAssets = path.join(siteRoot, "public/docs-assets")
await rm(publicAssets, { recursive: true, force: true })
for (const directory of assetDirectories) {
  const source = path.join(docsRoot, directory)
  try {
    await cp(source, path.join(outputRoot, directory), { recursive: true })
    await cp(source, path.join(publicAssets, directory), { recursive: true })
  } catch (error) {
    if (error.code !== "ENOENT") throw error
  }
}

console.log(`Prepared ${pages.length} documentation pages from docs/site.json`)

// Keep the standalone landing page and the documentation host on one asset set.
const landingRoot = path.join(repositoryRoot, "landing")
const landingPublic = path.join(siteRoot, "public/site-assets")
await rm(landingPublic, { recursive: true, force: true })
await mkdir(landingPublic, { recursive: true })
for (const directory of ["brand", "logos", "blog"]) {
  await cp(path.join(landingRoot, "assets", directory), path.join(landingPublic, directory), { recursive: true })
}
// Documentation uses the app icon as its identity; the landing pages keep the
// expressive mascot mark. Copy the generated icon so both stay current.
await cp(
  path.join(repositoryRoot, "assets/app-icon-256.png"),
  path.join(landingPublic, "app-icon.png"),
)
await mkdir(path.join(landingPublic, "product"), { recursive: true })
for (const image of [
  "hero-light-en.jpg",
  "hero-dark-en.jpg",
  "hero-light-zh.jpg",
  "hero-dark-zh.jpg",
]) {
  await cp(
    path.join(landingRoot, "assets/readme", image),
    path.join(landingPublic, "product", image),
  )
}
await cp(path.join(landingRoot, "styles.css"), path.join(landingPublic, "styles.css"))

await cp(
  path.join(landingRoot, "projection-diagrams.css"),
  path.join(landingPublic, "projection-diagrams.css"),
)

await cp(path.join(landingRoot, "mascot-motion.mjs"), path.join(landingPublic, "mascot-motion.mjs"))

await cp(path.join(landingRoot, "site-nav.mjs"), path.join(landingPublic, "site-nav.mjs"))
