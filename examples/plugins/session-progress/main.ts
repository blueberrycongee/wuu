import {runJSONLRuntime} from '@wuu/plugin-sdk';
import {createProgressPlugin} from './runtime.js';
await runJSONLRuntime(createProgressPlugin(), {input: process.stdin, output: process.stdout});
