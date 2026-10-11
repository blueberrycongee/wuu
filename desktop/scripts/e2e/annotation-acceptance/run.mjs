// Full-app acceptance always loads the ordinary shipping output in desktop/out.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
assert.ok(!process.env.WUU_DOCUMENT_BUILD_DIR, 'Production acceptance does not allow an alternate build directory.');
assert.ok(!process.env.WUU_ANNOTATION_VARIANT, 'Production acceptance does not allow presentation variants.');
const require = createRequire(import.meta.url);
process.env.WUU_ANNOTATION_ACCEPTANCE = '1';
await require('../../document-context-e2e.cjs');
