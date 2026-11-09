import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseSpec } from '../../../internal/plugin/bundled/generative-ui/desktop.js';

const input = () => ({ version: 2, title: 'Checklist', fallback: 'Edit items', state: [
  { id: 'items', value: [{ id: 'one', text: 'First' }] },
  { id: 'scratch', value: '', scope: 'temporary' },
], body: [
  { id: 'list', type: 'each', items: { op: 'state', key: 'items' }, key: { op: 'item', path: ['id'] }, children: [
    { id: 'edit', type: 'input', label: 'Item', itemField: 'text' },
    { id: 'remove', type: 'button', label: 'Remove', onClick: [{ type: 'remove', state: 'items' }] },
  ] },
  { id: 'add', type: 'button', label: 'Add', onClick: [{ type: 'append', state: 'items', value: { op: 'literal', value: { id: 'two', text: 'Second' } } }] },
  { id: 'scratchInput', type: 'input', label: 'Temporary', state: 'scratch' },
] });
const parse = spec => parseSpec(JSON.stringify(spec));

// Protocol rejection is important: invalid scopes, ambiguous bindings, unsafe
// field keys and unbound removal must never become live editable state.
test('validates editable lists and persistent versus temporary field scopes', () => {
  assert.doesNotThrow(() => parse(input()));
  const cases = [
    spec => { spec.state[1].scope = 'other'; },
    spec => { spec.body[0].children[0].itemField = '__proto__'; },
    spec => { spec.body[0].children[0].state = 'scratch'; },
    spec => { delete spec.body[0].key; },
    spec => { spec.body[0].items = { op: 'literal', value: [] }; },
    spec => { spec.body[1].onClick[0].state = 'scratch'; },
    spec => { spec.body[1].onClick[0] = { type: 'remove', state: 'items' }; },
  ];
  for (const mutate of cases) { const spec = input(); mutate(spec); assert.throws(() => parse(spec)); }
});
