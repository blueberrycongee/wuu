import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  optimizeDeps: { entries: ["dev/extensions/index.html"] },
  server: { host: "127.0.0.1" },
});
