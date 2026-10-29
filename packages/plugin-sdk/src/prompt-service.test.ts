import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import test from 'node:test';
import {replacePromptSection, type PromptAssemblyResult} from './prompt-service.js';

const section = (key: string, text: string, isStatic = false) => ({key, static: isStatic, bytes: Buffer.byteLength(text), hash: createHash('sha256').update(text).digest('hex').slice(0, 32)});
const first = '开头 👋';
const second = 'second\n\nwith separator';
const result = (): PromptAssemblyResult => ({layout: 'wuu.prompt.sections.v1', content: `${first}\n\n${second}`, sections: [section('first', first, true), section('second', second)]});

test('replaces a named section without changing other text or metadata', async () => {
 const original = result();
 const updated = await replacePromptSection(original, 'second', '替换 👀');
 assert.equal(updated.content, `${first}\n\n替换 👀`);
 assert.deepEqual(updated.sections, [original.sections[0], section('second', '替换 👀')]);
 assert.equal(original.content, `${first}\n\n${second}`);
 const twice = await replacePromptSection(updated, 'first', 'new');
 assert.equal(twice.content, 'new\n\n替换 👀');
});

test('rejects undeclared or inconsistent section layouts and invalid replacements', async () => {
 const good = result();
 const bad: PromptAssemblyResult[] = [
  {...good, layout: undefined}, {...good, layout: 'unknown'},
  {...good, content: good.content + '!'}, {...good, content: good.content.replace('\n\n','\n')},
  {...good, sections:[{...good.sections[0], bytes: 1},good.sections[1]]},
  {...good, sections:[{...good.sections[0], bytes: -1},good.sections[1]]},
  {...good, sections:[{...good.sections[0], hash:'00000000000000000000000000000000'},good.sections[1]]},
  {...good, sections:[good.sections[0],{...good.sections[1],key:'first'}]},
 ];
 for (const input of bad) await assert.rejects(replacePromptSection(input, 'first', 'new'));
 await assert.rejects(replacePromptSection(good, 'missing', 'new'));
 await assert.rejects(replacePromptSection(good, 'first', ''));
});

test('preserves a byte-order mark in unchanged section text', async () => {
 const marked = '\ufefftext';
 const input: PromptAssemblyResult = {layout: 'wuu.prompt.sections.v1', content: `first\n\n${marked}`, sections: [section('first', 'first'), section('second', marked)]};
 const updated = await replacePromptSection(input, 'first', 'new');
 assert.equal(updated.content, `new\n\n${marked}`);
 assert.deepEqual(updated.sections[1], input.sections[1]);
});
