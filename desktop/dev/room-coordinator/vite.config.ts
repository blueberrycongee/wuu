import { defineConfig } from "vite";

export default defineConfig({
  esbuild: { jsx: "automatic" },
  optimizeDeps: { entries: ["dev/room-coordinator/index.html"] },
});
