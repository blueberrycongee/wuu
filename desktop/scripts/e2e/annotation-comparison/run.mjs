// Opt-in full-app visual acceptance. Production launch and preferences stay unchanged.
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
process.env.WUU_ANNOTATION_COMPARISON = '1';
await require('../../document-context-e2e.cjs');
