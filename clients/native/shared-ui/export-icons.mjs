import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const directory = path.dirname(fileURLToPath(import.meta.url));
const desktop = path.resolve(directory, "../../../desktop");
const { build } = createRequire(path.join(desktop, "package.json"))("esbuild");
const result = await build({
  entryPoints: [path.join(desktop, "src/shared/iconArtwork.ts")],
  bundle: true, write: false, format: "esm", platform: "node",
});
const { iconSVG } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString("base64")}`);

// Xcode compiles these shared SVGs as tintable vector images; no WebView is needed.
for (const name of ["Copy", "Split", "Check"]) {
  const filename = `Wuu${name}.svg`;
  const output = path.resolve(directory, `../ios/App/Assets.xcassets/Wuu${name}.imageset`);
  const svg = iconSVG(name, 24)
    .replace("<svg ", '<svg xmlns="http://www.w3.org/2000/svg" ')
    .replaceAll("currentColor", "#000000")
    .replace(' aria-hidden="true"', "") + "\n";
  const contents = JSON.stringify({
    images: [{ filename, idiom: "universal" }],
    info: { author: "wuu", version: 1 },
    properties: { "preserves-vector-representation": true, "template-rendering-intent": "template" },
  }, null, 2) + "\n";
  for (const [file, content] of [[filename, svg], ["Contents.json", contents]]) {
    const target = path.join(output, file);
    if (process.argv.includes("--check")) {
      if (await readFile(target, "utf8") !== content) throw new Error(`Stale icon: ${target}. Run node clients/native/shared-ui/export-icons.mjs`);
    } else {
      await mkdir(output, { recursive: true });
      await writeFile(target, content);
    }
  }
}
console.log("Native reply icons match the shared desktop artwork.");
