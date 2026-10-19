import { createRequire } from "node:module";
import { readFile, writeFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { createHash } from "node:crypto";

const directory = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(directory, "../../..");
const desktop = path.join(repository, "desktop");
const { build } = createRequire(path.join(desktop, "package.json"))("esbuild");
const result = await build({
  absWorkingDir: repository, entryPoints: [path.join(directory, "src/mermaid.ts")],
  bundle: true, write: false, metafile: true, minify: true, format: "iife",
  target: ["chrome69", "safari17"], nodePaths: [path.join(desktop, "node_modules")],
  legalComments: "inline", supported: { "template-literal": false },
  define: { "process.env.NODE_ENV": '"production"' },
});
const script = result.outputFiles[0].text.replaceAll("</script", "<\\/script");
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'"><style>
html,body{margin:0;background:transparent;font:16px system-ui,-apple-system,sans-serif;color:CanvasText}
#diagram{min-height:64px;max-height:600px;overflow:auto;width:100%;box-sizing:border-box}
#diagram svg{display:block;margin:auto}p{margin:12px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:.875em}
#staging{position:absolute;left:-100000px;top:0;width:1000px;visibility:hidden;pointer-events:none}
</style></head><body><div id="diagram" role="img" aria-label="Mermaid 图表" data-state="loading">正在绘制图表…</div><div id="staging" aria-hidden="true"></div><script>${script}</script></body></html>\n`;

// Keep full licenses for every package actually bundled, including transitive
// dependencies. Native builds consume the snapshot without needing Node.
const packages = new Set();
for (const input of Object.keys(result.metafile.inputs)) {
  const absolute = path.resolve(repository, input);
  const marker = `${path.sep}node_modules${path.sep}`;
  const index = absolute.lastIndexOf(marker);
  if (index < 0) continue;
  const parts = absolute.slice(index + marker.length).split(path.sep);
  packages.add(path.join(absolute.slice(0, index + marker.length), ...parts.slice(0, parts[0].startsWith("@") ? 2 : 1)));
}
const notices = [];
for (const root of [...packages].sort()) {
  const pkg = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
  const files = (await readdir(root)).filter(name => /^(licen[cs]e|copying)(\.|$)/i.test(name));
  if (!files.length) throw new Error(`Missing bundled license: ${pkg.name}`);
  notices.push(`${pkg.name} ${pkg.version}\n${(await Promise.all(files.sort().map(file => readFile(path.join(root, file), "utf8")))).join("\n")}`);
}
const outputs = new Map([
  ["clients/native/shared-ui/NativeUI/mermaid.html", html],
  ["clients/native/licenses/Mermaid-Notices.txt", `Bundled Mermaid renderer — third-party notices\n\n${notices.join("\n\n---\n\n").trimEnd()}\n`],
]);
const hashes = [];
for (const file of ["clients/native/shared-ui/build-mermaid.mjs", "clients/native/shared-ui/src/mermaid.ts", "desktop/package-lock.json"]) {
  hashes.push(`${createHash("sha256").update(await readFile(path.join(repository, file))).digest("hex")}  ${file}`);
}
for (const [file, value] of outputs) hashes.push(`${createHash("sha256").update(value).digest("hex")}  ${file}`);
outputs.set("clients/native/shared-ui/NativeUI/mermaid.sha256", hashes.join("\n") + "\n");
for (const [file, value] of outputs) {
  if (process.argv.includes("--check")) {
    if (await readFile(path.join(repository, file), "utf8") !== value) throw new Error(`Stale Mermaid snapshot: ${file}`);
  } else await writeFile(path.join(repository, file), value);
}
console.log(`Native Mermaid bundle: ${Math.ceil(Buffer.byteLength(html) / 1024)} KiB; ${packages.size} dependency licenses`);
