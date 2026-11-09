// Await setup from the ESM entry point so Electron registers production file
// schemes before app readiness. The interactive journey starts after setup.
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
await require('./document-context-e2e.cjs');
