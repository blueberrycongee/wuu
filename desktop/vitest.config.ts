import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [react()],
  assetsInclude: ["**/*.html"],
  test: {
    projects: [
      {
        extends: true,
        test: {
          name: "main",
          environment: "node",
          globals: false,
          include: ["src/main/**/*.test.ts", "dev/**/*.test.ts"],
        },
      },
      {
        extends: true,
        test: {
          name: "renderer",
          environment: "jsdom",
          globals: false,
          include: [
            "src/renderer/**/*.test.ts",
            "src/renderer/**/*.test.tsx",
            "src/shared/**/*.test.ts",
          ],
          setupFiles: ["src/renderer/test/setup.ts"],
        },
      },
    ],
  },
  resolve: {
    alias: {
      "@": resolve(__dirname, "src"),
    },
  },
});
