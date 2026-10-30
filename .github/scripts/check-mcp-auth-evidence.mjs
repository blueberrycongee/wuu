import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const [output, binary, source] = process.argv.slice(2);
assert(output && binary && /^[a-f0-9]{40}$/.test(source || ''), 'Expected evidence directory, executable and tested source commit');
assert.equal(readFileSync(join(output, 'source-commit.txt'), 'utf8').trim(), source, 'MCP reload source does not match the tested revision');
const report = JSON.parse(readFileSync(join(output, 'results.json'), 'utf8'));
assert.equal(report.status, 'passed', 'MCP reload fixture did not pass');
const hash = createHash('sha256').update(readFileSync(binary)).digest('hex');
assert.equal(report.executable_sha256, hash, 'MCP reload used a different executable');
assert(Array.isArray(report.checks) && report.checks.length >= 11, 'MCP reload boundary cases did not execute');
assert(report.checks.every(check => typeof check === 'string' && check.length > 0), 'MCP reload cases must identify executed boundaries');
assert.equal(new Set(report.checks).size, report.checks.length, 'MCP reload cases must be distinct');
assert(Array.isArray(report.wire) && report.wire.length > 0, 'MCP reload produced no protocol evidence');
assert(report.wire.every(event => event.authenticated === true), 'MCP reload observed an unauthenticated request');
assert(report.wire.some(event => event.method === 'initialize') && report.wire.some(event => event.method === 'tools/call'), 'MCP reload did not exercise initialization and retained tool calls');
console.log(`PASS: ${report.checks.length} MCP authentication reload boundaries on the tested executable`);
