import { defineConfig } from "vite";
import { resolve } from "node:path";

export default defineConfig({
  cacheDir: resolve(__dirname, "../../node_modules/.vite/room-coordinator"),
  esbuild: { jsx: "automatic" },
  optimizeDeps: { entries: ["dev/room-coordinator/index.html"] },
});
