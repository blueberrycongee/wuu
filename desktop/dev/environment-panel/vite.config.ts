import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  cacheDir: resolve(__dirname, "../../node_modules/.vite/environment-panel"),
  plugins: [react()],
  optimizeDeps: { entries: ["dev/environment-panel/index.html"] },
  server: { host: "127.0.0.1", port: 5218, strictPort: true },
});
