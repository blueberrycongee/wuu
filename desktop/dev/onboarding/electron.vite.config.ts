import { defineConfig } from "electron-vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

// Separate entries and output keep this tool out of the packaged application
// and let it run without building the Go core or native helpers.
export default defineConfig({
  main: {
    build: {
      outDir: "out-dev/onboarding/main",
      rollupOptions: { input: { index: resolve(__dirname, "main.ts") } },
    },
  },
  renderer: {
    cacheDir: resolve(__dirname, "../../node_modules/.vite/onboarding"),
    root: ".",
    plugins: [react()],
    resolve: { dedupe: ["react", "react-dom"] },
    server: { fs: { allow: [resolve(__dirname, "../../..")] } },
    build: {
      outDir: "out-dev/onboarding/renderer",
      rollupOptions: { input: resolve(__dirname, "index.html") },
    },
  },
});
