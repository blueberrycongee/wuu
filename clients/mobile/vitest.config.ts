import { defineConfig } from "vitest/config";

// Pure-logic tests only (src/lib): no react-native imports on these paths,
// so no RN preset or web aliasing is needed. Screens/components are covered
// by typecheck + `expo export --platform web`.
export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
  },
});
