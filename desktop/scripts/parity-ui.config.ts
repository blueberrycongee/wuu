import { defineConfig } from 'vitest/config';
import { resolve } from 'node:path';

export default defineConfig({
  test: {
    environment: 'jsdom',
    include: ['scripts/parity-ui.test.tsx'],
    setupFiles: ['src/renderer/test/setup.ts'],
  },
  resolve: {alias: {
    '@': resolve(__dirname, '../src'),
    '@parity': resolve(process.env.WUU_PARITY_RENDERER_ROOT ?? resolve(__dirname, '..'), 'src/renderer/plugins'),
  }},
});
