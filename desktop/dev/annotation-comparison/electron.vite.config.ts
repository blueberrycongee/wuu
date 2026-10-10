import { defineConfig } from "electron-vite";
import { resolve } from "node:path";
import type { Plugin } from "vite";
import desktopConfig from "../../electron.vite.config";

const desktopRoot = resolve(__dirname, "../..");
const variant = process.env.WUU_ANNOTATION_VARIANT ?? "baseline";
if (variant !== "baseline" && variant !== "refined") {
  throw new Error("WUU_ANNOTATION_VARIANT must be baseline or refined.");
}
const outputRoot = resolve(desktopRoot, "out-dev/annotation-comparison", variant);

/** Select presentation at build time; application state and the shipping entry stay shared. */
function annotationPresentation(): Plugin {
  const replacements = new Map([
    ["PdfSelectionMenu.tsx:./SelectionActionMenu", "SelectionActionMenuRefined.tsx"],
    ["PdfSelectionMenu.tsx:./SelectionActionMenuPosition", "SelectionActionMenuPositionRefined.ts"],
    ["ComposerFileSelectionCard.tsx:./ComposerResponseSelectionCard", "ComposerQuoteCardRefined.tsx"],
    ["ThreadItemView.tsx:./FileSelectionCards", "SentFileSelectionCardsRefined.tsx"],
  ]);
  return {
    name: "wuu-annotation-comparison-presentation",
    enforce: "pre",
    resolveId(source, importer) {
      if (variant !== "refined" || !importer) return null;
      const owner = importer.replaceAll("\\", "/").split("/").pop();
      const replacement = replacements.get(`${owner}:${source}`);
      return replacement ? resolve(desktopRoot, "src/renderer", replacement) : null;
    },
    transform(code, id) {
      if (variant !== "refined") return null;
      if (id === resolve(desktopRoot, "src/renderer/ComposerFileSelectionCard.tsx")) {
        const card = "<ComposerQuoteCard ";
        if (!code.includes(card)) throw new Error("The draft quote adapter changed; review the comparison mapping before rebuilding.");
        return {
          code: code.replace(card, `${card}sourceName={source.path.split(/[\\\\/]/).pop()} location={fileSelectionLocation(source)} `),
          map: null,
        };
      }
      if (id !== resolve(desktopRoot, "src/renderer/PdfSelectionMenu.tsx")) return null;
      const importNames = "selectionActionMenuMetrics, selectionActionMenuPosition";
      const rangeBounds = "current.range.getBoundingClientRect()";
      if (!code.includes(importNames) || !code.includes(rangeBounds)) {
        throw new Error("The PDF selection adapter changed; review the comparison mapping before rebuilding.");
      }
      return {
        code: code.replace(importNames, `selectionActionMenuAnchor, ${importNames}`)
          .replace(rangeBounds, "selectionActionMenuAnchor(current.range, { left, top, right, bottom })"),
        map: null,
      };
    },
  };
}

export default defineConfig({
  ...desktopConfig,
  main: {
    ...desktopConfig.main,
    build: { ...desktopConfig.main?.build, outDir: resolve(outputRoot, "main") },
  },
  preload: {
    ...desktopConfig.preload,
    build: { ...desktopConfig.preload?.build, outDir: resolve(outputRoot, "preload") },
  },
  renderer: {
    ...desktopConfig.renderer,
    cacheDir: resolve(desktopRoot, "node_modules/.vite/annotation-comparison", variant),
    plugins: [...(desktopConfig.renderer?.plugins ?? []), annotationPresentation()],
    build: { ...desktopConfig.renderer?.build, outDir: resolve(outputRoot, "renderer") },
  },
});
