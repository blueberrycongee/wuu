import { createRequire } from "node:module";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { createHash } from "node:crypto";
const directory = path.dirname(fileURLToPath(import.meta.url));
const desktop = path.resolve(directory, "../../../desktop");
const { build } = createRequire(path.join(desktop, "package.json"))("esbuild");
const result = await build({
  absWorkingDir: path.resolve(directory, "../../.."), entryPoints: [path.join(directory, "src/mascot.tsx")],
  bundle: true, write: false, metafile: true, minify: true, format: "iife", jsx: "automatic", target: ["chrome100", "safari17"],
  nodePaths: [path.join(desktop, "node_modules")], outfile: "mascot.js", legalComments: "inline", define: { "process.env.NODE_ENV": '"production"' },
  // Resolve the vendored renderer from this checkout, even when dependencies are shared by a worktree.
  alias: Object.fromEntries([["blobatar", "index.ts"], ["blobatar/blob", "blob.ts"], ["blobatar/expression", "expression.ts"], ["blobatar/react", "react.tsx"], ["blobatar/motion.css", "motion.css"]].map(([name, file]) => [name, path.join(desktop, "vendor/blobatar/src", file)])),
});
const script = result.outputFiles.find(file => file.path.endsWith(".js")).text.replaceAll("</script", "<\\/script");
const style = result.outputFiles.find(file => file.path.endsWith(".css")).text;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'"><style>${style}</style></head><body><div id="root"></div><script>${script}</script></body></html>\n`;
const output = path.join(directory, "NativeUI/mascot.html");
const repository = path.resolve(directory, "../../..");
const sources = [...Object.keys(result.metafile.inputs).filter(file => !file.includes("node_modules/")), "clients/native/shared-ui/build.mjs"];
const hashes = await Promise.all([...new Set(sources)].sort().map(async file => `${createHash("sha256").update(await readFile(path.join(repository, file))).digest("hex")}  ${file}`));
hashes.push(`${createHash("sha256").update(html).digest("hex")}  clients/native/shared-ui/NativeUI/mascot.html`);
const manifest = hashes.join("\n") + "\n";
const manifestPath = path.join(directory, "NativeUI/sources.sha256");
if (process.argv.includes("--check")) {
  if (await readFile(output, "utf8") !== html || await readFile(manifestPath, "utf8") !== manifest) throw new Error("Native mascot bundle is stale. Run node clients/native/shared-ui/build.mjs");
} else { await mkdir(path.dirname(output), { recursive: true }); await writeFile(output, html); await writeFile(manifestPath, manifest); }
console.log(`Native mascot bundle: ${Math.ceil(Buffer.byteLength(html) / 1024)} KiB`);
