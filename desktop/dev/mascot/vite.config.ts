import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  // Preview optimization must not replace the running desktop's dependencies.
  cacheDir: resolve(__dirname, "../../node_modules/.vite/mascot"),
  plugins: [react()],
});
