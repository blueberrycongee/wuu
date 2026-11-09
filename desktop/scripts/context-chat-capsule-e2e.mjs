import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
await require('./context-chat-capsule-e2e.cjs');
