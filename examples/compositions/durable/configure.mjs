import { readFileSync, realpathSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Produces explicit user-owned configuration; it does not install or launch it.
const [binary, workspace] = process.argv.slice(2);
if (!binary || !workspace || process.argv.length !== 4) {
  throw new Error('Usage: node configure.mjs /absolute/path/to/wuu /absolute/workspace');
}
const composition = JSON.parse(readFileSync(fileURLToPath(new URL('./composition.json', import.meta.url)), 'utf8'));
console.log(JSON.stringify({
  composition,
  skills: { enabled: false },
  // This reference demonstrates direct tools; journal-backed PTC is opt-in.
  ptc: { enabled: false },
  execution_environments: {
    default: 'durable-reference',
    profiles: {
      'durable-reference': {
        backend: 'command',
        command: [realpathSync(resolve(binary)), 'execution-worker'],
        workspace: realpathSync(resolve(workspace)),
        shared: true,
        shutdown_timeout_ms: 15000,
      },
    },
  },
}, null, 2));
