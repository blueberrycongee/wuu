import { build } from "esbuild";
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const output = resolve(process.env.GENUI_OUTPUT ?? `${root}/.tmp/personal-genui`);
await mkdir(output, { recursive: true });
const bundle = await build({ absWorkingDir: root, entryPoints: ["desktop/dev/personal-genui/preview.tsx"], outfile: `${output}/preview.js`, bundle: true, write: false, minify: true, format: "iife", platform: "browser", define: { "process.env.NODE_ENV": '"production"' }, loader: { ".woff2": "dataurl", ".svg": "dataurl" } });
const js = bundle.outputFiles.find(f => f.path.endsWith(".js")).text.replaceAll("</script", "<\\/script");
const css = bundle.outputFiles.find(f => f.path.endsWith(".css")).text;
const digest = createHash("sha256").update(js).digest("base64");
await writeFile(`${output}/preview.html`, `<!doctype html><html lang="zh-CN" data-theme="light"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'sha256-${digest}'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'"><title>Twin GenUI · 私有示例</title><style>${css}</style><div id="root"></div><script>${js}</script></html>`);
await build({ absWorkingDir: root, entryPoints: ["plugins/personal/src/genui/service.ts", "plugins/personal/src/genui/fixtures.ts"], outdir: output, bundle: true, platform: "node", format: "esm", outExtension: { ".js": ".mjs" } });
console.log(output);
