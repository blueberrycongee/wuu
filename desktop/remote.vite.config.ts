import { builtinModules } from "node:module";
import { resolve } from "node:path";
import { defineConfig } from "vite";

export default defineConfig({
  build: {
    target: "node22",
    outDir: "out-remote",
    emptyOutDir: true,
    lib: { entry: resolve(__dirname, "src/remote/host.ts"), formats: ["es"], fileName: () => "host.mjs" },
    rollupOptions: { external: [...builtinModules, /^node:/, "node-pty"] },
    minify: false,
  },
});
