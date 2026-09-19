import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { lstatSync, readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const retiredRoots = ["presentations/", "experiments/", "landing/drafts/", "landing/.private-drafts/", "docs/plans/"]

// Runtime instructions contain example links; vendored history follows upstream
// conventions. Keep both with their consumers, outside the human-doc link gate.
function humanMarkdown(file) {
  return file.endsWith(".md") && !/^(?:prompts\/|internal\/|\.agents\/|desktop\/vendor\/)/.test(file)
}

function prose(markdown) {
  let fence
  return markdown.split("\n").map((line) => {
    const marker = line.match(/^\s{0,3}(`{3,}|~{3,})/)
    if (fence) {
      if (marker && marker[1][0] === fence[0] && marker[1].length >= fence.length &&
          line.slice(marker[0].length).trim() === "") fence = undefined
      return ""
    }
    if (marker) { fence = marker[1]; return "" }
    return line
  }).join("\n").replace(/(`+)[\s\S]*?\1/g, "")
}

function localTarget(source, href) {
  if (/^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(href)) return null
  const pathname = href.split(/[?#]/, 1)[0]
  if (!pathname) return null
  const decoded = decodeURIComponent(pathname)
  return path.posix.normalize(decoded.startsWith("/")
    ? decoded.slice(1)
    : path.posix.join(path.posix.dirname(source), decoded)).replace(/\/$/, "")
}

function check(files, manifest, read) {
  const failures = []
  const available = new Set(files)
  for (const file of files) {
    for (let dir = path.posix.dirname(file); dir !== "."; dir = path.posix.dirname(dir)) available.add(dir)
  }
  available.add(".")
  const published = new Set(Object.values(manifest.locales).flatMap((locale) =>
    locale.navigation.flatMap((group) => group.pages.map((page) => `docs/${page}`))))
  for (const page of published) {
    if (!files.has(page)) failures.push(`${page}: navigation target is not a tracked file`)
  }
  for (const file of files) {
    if (retiredRoots.some((prefix) => file.startsWith(prefix))) {
      failures.push(`${file}: one-off or private material belongs outside the public repository`)
    }
    if (file.startsWith("docs/") && file.endsWith(".md") && file !== "docs/README.md" && !published.has(file)) {
      failures.push(`${file}: merge into maintained bilingual documentation and register in docs/site.json`)
    }
    if (!humanMarkdown(file)) continue
    const text = prose(read(file))
    const links = [
      ...text.matchAll(/\]\(<?([^\s)>]+)>?(?:\s+["'][^"']*["'])?\)/g),
      ...text.matchAll(/^\s{0,3}\[[^\]]+\]:\s*<?([^\s>]+)>?/gm),
      ...text.matchAll(/\b(?:href|src)=["']([^"']+)["']/g),
    ]
    for (const match of links) {
      let target
      try { target = localTarget(file, match[1]) }
      catch { failures.push(`${file}: malformed link encoding`); continue }
      if (target !== null && !available.has(target)) {
        failures.push(`${file}: local link has no tracked target: ${match[1]}`)
      }
    }
  }
  return failures
}

function selfTest() {
  const manifest = { locales: { en: { navigation: [{ pages: ["en/index.md"] }] } } }
  const fixture = new Map([
    ["docs/en/index.md", "# Start\n[guide](../../module/README.md)\n![image](../../assets/a%20b.png)\n[code][source]\n[source]: ../../src/main.go#L1\n"],
    ["module/README.md", "# Module\n[home](../docs/en/index.md)\n`[example](missing.md)`\n```md\n[example](missing.md)\n```\n~~~md\n[example](missing.md)\n~~~\n[web](https://example.org/)\n"],
    ["assets/a b.png", ""], ["src/main.go", ""],
    ["internal/skills/bundled/pptx-generator/SKILL.md", "[template](example.pptx)"],
    ["evals/cases/example/fixture.pdf", ""],
  ])
  const run = (files = new Set(fixture.keys())) => check(files, manifest, (file) => fixture.get(file))
  assert.deepEqual(run(), [])
  const deletedCode = new Set(fixture.keys()); deletedCode.delete("src/main.go")
  assert.equal(run(deletedCode).length, 1, "a removed source target must break its public link")
  const deletedImage = new Set(fixture.keys()); deletedImage.delete("assets/a b.png")
  assert.equal(run(deletedImage).length, 1, "a local but untracked image is not public evidence")
  fixture.set("docs/en/forgotten.md", "# Missing navigation")
  assert.equal(run().length, 1)
  fixture.delete("docs/en/forgotten.md")
  fixture.set("landing/drafts/article.html.txt", "")
  assert.equal(run().length, 1, "text disguises do not make drafts private")
  fixture.delete("landing/drafts/article.html.txt")
  const deletedPage = new Set(fixture.keys()); deletedPage.delete("docs/en/index.md")
  assert.equal(run(deletedPage).length, 2, "check both navigation and inbound links")
  fixture.set("module/README.md", "[outside](../../private.md)\n[bad](bad%zz.md)\n<img src='missing.png'>")
  assert.equal(run().length, 3)
  console.log("Documentation policy self-test passed")
}

if (process.argv.includes("--self-test")) {
  selfTest()
} else {
  const files = new Set(execFileSync("git", ["ls-files", "-z"], { cwd: root, encoding: "utf8" }).split("\0").filter(Boolean))
  // Explicit additions allow pre-staging validation without treating arbitrary
  // ignored or untracked workspace files as public repository content.
  for (const file of process.argv.slice(2)) {
    if (file.startsWith("-") || path.isAbsolute(file) || file.split("/").includes("..")) {
      throw new Error("Usage: node scripts/check-docs.mjs [new-repository-relative-file ...]")
    }
    files.add(file)
  }
  for (const file of files) {
    try {
      if (lstatSync(path.join(root, file)).isSymbolicLink() && humanMarkdown(file)) {
        throw new Error(`Documentation must be a regular file: ${file}`)
      }
    } catch (error) {
      if (error.code === "ENOENT") files.delete(file)
      else throw error
    }
  }
  const manifest = JSON.parse(readFileSync(path.join(root, "docs/site.json"), "utf8"))
  const failures = check(files, manifest, (file) => readFileSync(path.join(root, file), "utf8"))
  if (failures.length) {
    console.error(failures.join("\n"))
    process.exitCode = 1
  } else {
    console.log(`Checked placement and local links in ${[...files].filter(humanMarkdown).length} tracked Markdown files`)
  }
}
