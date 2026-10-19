import { readFileSync, realpathSync, statSync } from 'node:fs';
import { dirname, extname, resolve } from 'node:path';
import { parse } from '@babel/parser';

// Only modified renderer modules enter this graph. Global inputs, configuration,
// styles, new/deleted files and runtime plugin loading retain the full CI gate.
export function selectRelatedTests({ tests, changed, aliases = [], globalRoots = [] }) {
  const cache = new Map();
  const changes = new Set(changed.map(path => realpathSync(resolve(path))));
  function dependencies(file) {
    if (cache.has(file)) return cache.get(file);
    const result = { imports: [], uncertain: false };
    cache.set(file, result);
    if (!/\.[cm]?[jt]sx?$/.test(file)) return result;
    let source;
    try {
      source = parse(readFileSync(file, 'utf8'), { sourceType: 'unambiguous', plugins: ['typescript', 'jsx'], createImportExpressions: true });
    } catch { result.uncertain = true; return result; }
    function add(specifier) {
      if (specifier?.type !== 'StringLiteral') { result.uncertain = true; return; }
      const name = specifier.value;
      // These consumers can read or execute sources outside the import graph.
      if (/^(node:)?(fs|child_process|vm|module)(\/|$)/.test(name)) result.uncertain = true;
      const matches = aliases.filter(entry => typeof entry.find === 'string'
        ? name === entry.find || name.startsWith(entry.find + '/')
        : (entry.find.lastIndex = 0, entry.find.test(name)));
      const alias = matches[0];
      if (matches.some(entry => entry.replacement !== alias.replacement || entry.customResolver)) { result.uncertain = true; return; }
      let local = name;
      if (alias) {
        local = name.replace(alias.find, alias.replacement);
      }
      if (local.startsWith('#')) { result.uncertain = true; return; }
      if (!local.startsWith('.') && !local.startsWith('/')) {
        if (alias) result.uncertain = true;
        return;
      }
      const target = resolve(dirname(file), local);
      const candidates = [target];
      if (/\.[cm]?jsx?$/.test(target)) {
        const stem = target.slice(0, -extname(target).length);
        candidates.push(stem + '.ts', stem + '.tsx', stem + '.mts', stem + '.cts');
      }
      for (const extension of ['.ts', '.tsx', '.mts', '.cts', '.js', '.jsx', '.mjs', '.cjs', '.json']) {
        candidates.push(target + extension, resolve(target, 'index' + extension));
      }
      const resolved = candidates.filter(path => statSync(path, { throwIfNoEntry: false })?.isFile());
      if (resolved.length) result.imports.push(...resolved.map(path => realpathSync(path)));
      else result.uncertain = true;
    }
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (['ImportDeclaration', 'ExportNamedDeclaration', 'ExportAllDeclaration'].includes(node.type) && node.source) add(node.source);
      if (node.type === 'ImportExpression') add(node.source);
      if (node.type === 'TSImportType') add(node.argument);
      if (node.type === 'TSExternalModuleReference') add(node.expression);
      if (node.type === 'CallExpression') {
        const callee = node.callee;
        if (callee.type === 'Identifier' && callee.name === 'require') add(node.arguments[0]);
        if (callee.type === 'Identifier' && ['eval', 'Function'].includes(callee.name)) result.uncertain = true;
        if (callee.type === 'MemberExpression') {
          if (['vi', 'vitest'].includes(callee.object.name) && ['mock', 'doMock', 'importActual', 'importMock'].includes(callee.property.name)) add(node.arguments[0]);
          if (callee.object.type === 'MetaProperty') result.uncertain = true;
        }
      }
      if (node.type === 'NewExpression' && ['Worker', 'SharedWorker', 'Function', 'URL'].includes(node.callee.name)) result.uncertain = true;
      for (const [key, value] of Object.entries(node)) {
        if (['loc', 'start', 'end', 'comments', 'tokens'].includes(key)) continue;
        if (Array.isArray(value)) value.forEach(visit);
        else if (value?.type) visit(value);
      }
    }
    visit(source);
    return result;
  }
  const selected = [], uncertain = [], affectedTests = [];
  for (const test of tests) {
    const seen = new Set();
    let affected = false, opaque = false;
    const pending = [resolve(test), ...globalRoots].map(path => realpathSync(path));
    while (pending.length) {
      const file = pending.pop();
      if (seen.has(file)) continue;
      seen.add(file);
      affected ||= changes.has(file);
      const info = dependencies(file);
      opaque ||= info.uncertain;
      pending.push(...info.imports);
    }
    if (affected) affectedTests.push(test);
    if (affected || opaque) selected.push(test);
    if (opaque) uncertain.push(test);
  }
  return { selected, uncertain, affected: affectedTests };
}
