import { defineConfig } from "vitest/config";
import react from '@vitejs/plugin-react';
import { resolve } from 'node:path';

// Bridge tests run in Node; shell lifecycle tests opt into jsdom.
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { react: resolve(__dirname, 'node_modules/react'), 'react-dom': resolve(__dirname, 'node_modules/react-dom') } },
  test: {
    include: ["test/**/*.test.{ts,tsx}"],
  },
});
