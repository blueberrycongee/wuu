import { readdir, readFile } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const distRoot = path.join(siteRoot, "dist")
const siteBase = (process.env.DOCS_BASE ?? "/wuu").replace(/\/$/, "")
const failures = []
const siteFiles = new Set()
const htmlFiles = []

async function walk(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const fullPath = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      await walk(fullPath)
      continue
    }
    const relativePath = path.relative(distRoot, fullPath).split(path.sep).join("/")
    siteFiles.add(relativePath)
    if (entry.name.endsWith(".html")) htmlFiles.push(relativePath)
  }
}

function resolvesLocally(sourceFile, href) {
  const pathname = href.split(/[?#]/, 1)[0]
  let target
  if (pathname.startsWith(`${siteBase}/`)) {
    target = decodeURIComponent(pathname.slice(`${siteBase}/`.length))
  } else if (pathname === siteBase) {
    target = ""
  } else if (pathname.startsWith("/")) {
    return true
  } else {
    target = path.posix.normalize(path.posix.join(path.posix.dirname(sourceFile), pathname))
  }

  const normalized = target.replace(/^\.\//, "").replace(/\/$/, "")
  return (
    siteFiles.has(normalized) ||
    siteFiles.has(`${normalized}/index.html`) ||
    siteFiles.has(`${normalized}.html`) ||
    (normalized === "" && siteFiles.has("index.html"))
  )
}

await walk(distRoot)

for (const relativePath of htmlFiles) {
  const fullPath = path.join(distRoot, relativePath)
  const html = await readFile(fullPath, "utf8")
  const main = html.match(/<main\b[^>]*>([\s\S]*?)<\/main>/i)?.[1] ?? ""
  const prose = main
    .replace(/<(script|style|pre|code)\b[^>]*>[\s\S]*?<\/\1>/gi, "")
    .replace(/<[^>]+>/g, " ")
    .replace(/&(?:ast|#42);/gi, "*")

  if (prose.includes("**")) {
    failures.push(`${relativePath}: unrendered Markdown emphasis`)
  }
  if (/data-language=["']mermaid["']|class=["'][^"']*language-mermaid/.test(main)) {
    failures.push(`${relativePath}: Mermaid diagram rendered as source code`)
  }
  if (/github\.com\/blueberrycongee\/wuu\/(?:blob|tree)\/main\/(?:en|zh-cn)\//.test(html)) {
    failures.push(`${relativePath}: repository link is missing the docs/ prefix`)
  }

  for (const match of html.matchAll(/\b(?:href|src)="([^"]+)"/g)) {
    const href = match[1]
    if (href.startsWith("#") || /^(?:data|mailto|tel):/.test(href)) continue
    const parsed = new URL(href, `https://docs.example${siteBase}/`)
    if (parsed.origin !== "https://docs.example") continue
    if (!resolvesLocally(relativePath, href)) {
      failures.push(`${relativePath}: unresolved local URL ${href}`)
    }
  }
}

if (failures.length > 0) {
  console.error("Rendered documentation checks failed:")
  for (const file of failures) console.error(`- ${file}`)
  process.exit(1)
}

console.log(`Checked Markdown and local URLs in ${htmlFiles.length} HTML pages`)
