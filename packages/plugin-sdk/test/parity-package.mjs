import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, cp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import {preparePublicSDK} from './public-package.mjs';

const exec = promisify(execFile);
const authorSDK = fileURLToPath(new URL('../', import.meta.url));
const sdk = resolve(process.env.WUU_PARITY_SDK_ROOT ?? authorSDK);
const repo = resolve(authorSDK, '../..');
const output = resolve(process.env.WUU_PARITY_OUTPUT ?? join(repo, 'artifacts/parity-author'));
await mkdir(output, {recursive: true});
const temporary = await mkdtemp(join(tmpdir(), 'wuu-parity-author-'));
try {
  const {tsc, packed, project, manifest} = await preparePublicSDK({sdk, temporary, tarball: process.env.WUU_PARITY_SDK_TARBALL});
  for (const name of ['tools', 'sessions', 'journal', 'turn-state', 'prompt', 'compaction', 'title']) await cp(join(repo, `examples/plugins/independent-providers/${name}.ts`), join(project, `${name}.ts`));
  for (const name of ['shell', 'navigation', 'application']) await cp(join(repo, `examples/plugins/independent-providers/${name}.tsx`), join(project, `${name}.tsx`));
  await exec(process.execPath, [tsc, 'tools.ts', 'sessions.ts', 'prompt.ts', 'compaction.ts', 'title.ts', 'shell.tsx', 'navigation.tsx', 'application.tsx', '--jsx', 'react', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2022', '--strict', '--skipLibCheck'], {cwd: project});
  const checked = await exec(process.execPath, ['--test', join(authorSDK, 'test/parity-tools.test.mjs'), join(authorSDK, 'test/parity-sessions.test.mjs'), join(authorSDK, 'test/parity-policies.test.mjs'), join(authorSDK, 'test/parity-application.test.mjs')], {
    env: {...process.env, WUU_PARITY_APPLICATION_ENTRY: join(project, 'application.js'), WUU_PARITY_TOOLS_ENTRY: join(project, 'tools.js'), WUU_PARITY_SESSIONS_ENTRY: join(project, 'sessions.js'), WUU_PARITY_PROMPT_ENTRY: join(project, 'prompt.js'), WUU_PARITY_COMPACTION_ENTRY: join(project, 'compaction.js'), WUU_PARITY_TITLE_ENTRY: join(project, 'title.js')},
  });
  await writeFile(join(output, 'public-sdk-check.log'), checked.stdout);
  const tarball = await readFile(packed);
  await cp(packed, join(output, 'sdk.tgz'));
  await writeFile(join(output, 'public-sdk-check.json'), JSON.stringify({passed: true, sdkRoot: sdk, sdkTarball: process.env.WUU_PARITY_SDK_TARBALL ? resolve(process.env.WUU_PARITY_SDK_TARBALL) : null, packageSHA256: createHash('sha256').update(tarball).digest('hex'), package: manifest.name, version: manifest.version, sourceFiles: ['tools.ts', 'sessions.ts', 'journal.ts', 'turn-state.ts', 'prompt.ts', 'compaction.ts', 'title.ts', 'shell.tsx', 'navigation.tsx']}, null, 2));
  console.log(checked.stdout);
  console.log('PASS: independent providers compiled against packed public SDK and passed behavior fixtures; no dependency installation.');
  if (process.env.WUU_PARITY_BINARY) {
    await writeFile(join(output, 'tool-lifecycle.jsonl'), '');
    const plugin = join(temporary, 'independent-tools');
    await mkdir(plugin);
    await writeFile(join(project, 'runtime.mjs'), `import {appendFileSync} from 'node:fs'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createToolsProvider} from './tools.js'; const plugin=createToolsProvider('production-owner'); for(const name of ['initialize','activate','invokeService','shutdown']) {const original=plugin[name]; plugin[name]=function(...args){appendFileSync(${JSON.stringify(join(output, 'tool-lifecycle.jsonl'))},JSON.stringify({event:name,method:args[0]?.method,pid:process.pid})+'\\n'); return original?.(...args);};} await runJSONLRuntime(plugin, {input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(plugin, 'runtime.mjs')}`]);
    await writeFile(join(plugin, 'plugin.json'), JSON.stringify({schema_version: 1, id: 'independent-tools', name: 'Independent Tool Registry', version: '1.0.0', runtime: {protocol: 'wuu-plugin-v1', command: process.execPath, args: ['runtime.mjs']}}));
    const ordinary = join(temporary, 'ordinary-tools');
    await mkdir(ordinary);
    await writeFile(join(project, 'ordinary.mjs'), `import {runJSONLRuntime} from '@wuu/plugin-sdk'; await runJSONLRuntime({initialize(){return {protocol_version:3,tools:[{id:'ordinary_echo',direct_only:true,description:'Independent ordinary contribution',input_schema:{type:'object',properties:{},additionalProperties:false},activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},executeTool(){return {result:{content:[{type:'text',text:'ordinary-owner:probe'}]}};}},{input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'ordinary.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(ordinary, 'runtime.mjs')}`]);
    await writeFile(join(ordinary, 'plugin.json'), JSON.stringify({schema_version:1,id:'ordinary-tools',name:'Ordinary Tool Contribution',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const sessions = join(temporary, 'independent-sessions');
    await mkdir(sessions);
    await writeFile(join(output, 'session-lifecycle.jsonl'), '');
    await writeFile(join(project, 'session-runtime.mjs'), `import {appendFileSync} from 'node:fs'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createSessionProvider} from './sessions.js'; const plugin=createSessionProvider('production-session-owner'); const activate=plugin.activate; plugin.activate=async function(...args){await activate?.(...args); await plugin.invokeService({service:'session.domain',method:'CreateInitialized',caller:'author-bootstrap',params:{args:[{id:'author-seeded',created_at:'2026-01-01T00:00:00Z',entries:2,cwd:args[1]?.cwd??'',provider:'synthetic',model:'test'},[{role:'user',content:'seed question'},{role:'assistant',content:'provider-owned seeded answer'}]]}});}; const invoke=plugin.invokeService; plugin.invokeService=async function(...args){const event={method:args[0].method,pid:process.pid}; try {const result=await invoke(...args); appendFileSync(${JSON.stringify(join(output, 'session-lifecycle.jsonl'))},JSON.stringify({...event,ok:true})+'\\n'); return result;} catch(error){appendFileSync(${JSON.stringify(join(output, 'session-lifecycle.jsonl'))},JSON.stringify({...event,error:String(error),code:error.code})+'\\n'); throw error;}}; await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'session-runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(sessions, 'runtime.mjs')}`]);
    await writeFile(join(sessions, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-sessions',name:'Independent Session Domain',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const prompt = join(temporary, 'independent-prompt');
    await mkdir(prompt);
    await writeFile(join(project, 'prompt-runtime.mjs'), `import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createPromptProvider} from './prompt.js'; await runJSONLRuntime(createPromptProvider(), {input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'prompt-runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(prompt, 'runtime.mjs')}`]);
    await writeFile(join(prompt, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-prompt',name:'Independent Complete Prompt',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const compaction = join(temporary, 'independent-compaction');
    await mkdir(compaction);
    await writeFile(join(output, 'compaction-lifecycle.jsonl'), '');
    await writeFile(join(project, 'compaction-runtime.mjs'), `import {appendFileSync} from 'node:fs'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createCompactionProvider} from './compaction.js'; const plugin=createCompactionProvider(); const invoke=plugin.invokeService; plugin.invokeService=async function(...args){const event={method:args[0].method,pid:process.pid}; try {const result=await invoke(...args); appendFileSync(${JSON.stringify(join(output, 'compaction-lifecycle.jsonl'))},JSON.stringify({...event,ok:true})+'\\n'); return result;} catch(error){appendFileSync(${JSON.stringify(join(output, 'compaction-lifecycle.jsonl'))},JSON.stringify({...event,error:String(error),code:error.code})+'\\n'); throw error;}}; await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'compaction-runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(compaction, 'runtime.mjs')}`]);
    await writeFile(join(compaction, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-compaction',name:'Independent Refusal Policy',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const updatedTools = join(temporary, 'independent-tools-v2');
    await mkdir(updatedTools);
    await writeFile(join(project, 'updated-runtime.mjs'), `import {appendFileSync} from 'node:fs'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createToolsProvider} from './tools.js'; const plugin=createToolsProvider('production-owner-v2'); for(const name of ['initialize','activate','invokeService','shutdown']) {const original=plugin[name]; plugin[name]=function(...args){appendFileSync(${JSON.stringify(join(output, 'tool-lifecycle.jsonl'))},JSON.stringify({event:name,method:args[0]?.method,pid:process.pid,version:2})+'\\n'); return original?.(...args);};} await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'updated-runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(updatedTools, 'runtime.mjs')}`]);
    await writeFile(join(updatedTools, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-tools',name:'Independent Tool Registry',version:'2.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const title = join(temporary, 'independent-title');
    await mkdir(title);
    await writeFile(join(project, 'title-runtime.mjs'), `import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createTitleProvider} from './title.js'; await runJSONLRuntime(createTitleProvider(),{input:process.stdin,output:process.stdout});`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(project, 'title-runtime.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(title, 'runtime.mjs')}`]);
    await writeFile(join(title, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-title',name:'Independent Complete Title',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
    const {independentTitle} = await import(pathToFileURL(join(project, 'title.js')));
    const production = await exec(process.execPath, [join(authorSDK, 'test/parity-production.mjs'), plugin, ordinary, sessions, prompt, compaction, updatedTools, title, independentTitle], {env: {...process.env, WUU_PARITY_OUTPUT: output}, maxBuffer: 4 * 1024 * 1024});
    console.log(production.stdout);
  }
} catch (error) {
  console.error(error.stdout ?? '', error.stderr ?? '');
  throw error;
} finally {
  await rm(temporary, {recursive: true, force: true});
}
