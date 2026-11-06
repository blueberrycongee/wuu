import { build } from "esbuild";
import { mkdir, copyFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
await build({
  absWorkingDir: root,
  entryPoints: ["runtime.ts"],
  bundle: true,
  platform: "node",
  format: "esm",
  target: "node22",
  outfile: "dist/runtime.js",
});
await mkdir(new URL("package/dist/", import.meta.url), { recursive: true });
await copyFile(
  new URL("plugin.json", import.meta.url),
  new URL("package/plugin.json", import.meta.url),
);
await copyFile(
  new URL("dist/runtime.js", import.meta.url),
  new URL("package/dist/runtime.js", import.meta.url),
);
await writeFile(
  new URL("package/package.json", import.meta.url),
  '{"type":"module"}\n',
);
