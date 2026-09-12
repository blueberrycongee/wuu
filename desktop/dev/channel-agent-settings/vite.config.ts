import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  cacheDir: resolve(__dirname, "../../node_modules/.vite/channel-agent-settings"),
  plugins: [react()],
  optimizeDeps: { entries: ["dev/channel-agent-settings/index.html"] },
  server: { host: "127.0.0.1", port: 5201, strictPort: true },
});
