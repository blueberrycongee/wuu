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
  const body = rewriteMarkdownLinks(markdown.replace(/^#\s+.+(?:\r?\n)+/, ""), page)
  await writeFile(destination, `---\ntitle: ${JSON.stringify(titleMatch[1])}\n---\n\n${body}`)
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
await cp(path.join(landingRoot, "styles.css"), path.join(landingPublic, "styles.css"))

await cp(
  path.join(landingRoot, "projection-diagrams.css"),
  path.join(landingPublic, "projection-diagrams.css"),
)

await cp(path.join(landingRoot, "mascot-motion.mjs"), path.join(landingPublic, "mascot-motion.mjs"))

await cp(path.join(landingRoot, "site-nav.mjs"), path.join(landingPublic, "site-nav.mjs"))
