import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  cacheDir: resolve(__dirname, "../../node_modules/.vite/three-pane"),
  plugins: [react()],
  optimizeDeps: {
    entries: ["dev/three-pane/index.html"],
    esbuildOptions: { jsx: "automatic" },
  },
});
