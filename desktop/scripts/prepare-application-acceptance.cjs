// Creates only a fresh disposable fixture; never reads or changes the user's WUU_HOME.
// node desktop/scripts/prepare-application-acceptance.cjs /absolute/wuu-core [/output-parent]
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {spawnSync} = require('node:child_process');
const {createHash} = require('node:crypto');
const assert = require('node:assert/strict');
const repository = path.resolve(__dirname, '../..');
const core = path.resolve(process.argv[2] || path.join(repository, 'desktop/build/bin/wuu-core'));
assert.ok(fs.existsSync(core), `Build the integrated core first: ${core}`);
const parent = path.resolve(process.argv[3] || os.tmpdir());
fs.mkdirSync(parent, {recursive:true});
const run = fs.mkdtempSync(path.join(parent, 'wuu-application-acceptance-'));
const home = path.join(run, 'home'), workspace = path.join(run, 'workspace'), source = path.join(run, 'independent-application');
const sdk = path.join(repository, 'packages/plugin-sdk');
for (const dir of [home, workspace, source]) fs.mkdirSync(dir);
function execute(command, args, cwd=run, env=process.env) {
  const result = spawnSync(command, args, {cwd, env, encoding:'utf8', timeout:180000});
  fs.appendFileSync(path.join(run, 'setup.log'), `${command} ${args.join(' ')}\n${result.stdout || ''}\n${result.stderr || ''}\n`);
  assert.equal(result.status, 0, `Fixture setup failed; see ${run}/setup.log`);
  return result.stdout;
}
const stage = path.join(run, 'sdk'); fs.mkdirSync(stage);
fs.copyFileSync(path.join(sdk, 'package.json'), path.join(stage, 'package.json'));
execute(process.execPath, [path.join(sdk, 'node_modules/typescript/bin/tsc'), '--project', path.join(sdk, 'tsconfig.json'), '--outDir', path.join(stage, 'dist')]);
const packed = JSON.parse(execute('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', run], stage))[0].filename;
const installed = path.join(source, 'node_modules/@wuu/plugin-sdk'); fs.mkdirSync(installed, {recursive:true});
execute('tar', ['-xzf', path.join(run, packed), '--strip-components=1', '-C', installed]);
fs.copyFileSync(path.join(repository, 'examples/plugins/independent-providers/application.tsx'), path.join(source, 'application.tsx'));
execute(path.join(sdk, 'node_modules/esbuild/bin/esbuild'), ['application.tsx', '--bundle', '--platform=browser', '--format=esm', '--outfile=desktop.js'], source);
const services = ['ui.application', 'ui.shell'].map(name => ({name, version:'1.0.0', methods:[{name:'describe',input_schema:`${name}.describe.input.v1`,output_schema:`${name}.describe.output.v1`}]}));
fs.writeFileSync(path.join(source, 'plugin.json'), JSON.stringify({schema_version:1,id:'independent-application',name:'Independent session explorer',version:'1.0.0',desktop:{entry:'desktop.js',services}}));
fs.writeFileSync(path.join(source, 'package.json'), JSON.stringify({name:'independent-application-fixture',private:true,type:'module',scripts:{build:'node --check desktop.js'}}));
const config = {default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:'http://127.0.0.1:9/v1',api_key:'synthetic-not-a-secret',model:'fixture-no-inference'}},skills:{enabled:false},composition:{version:3,profile:'desktop',patches:[{modules:Object.fromEntries(['shell','application','navigation','conversation','composer','settings','preview'].map(name=>[`wuu.default-${name}`,false]).concat([['independent-application',true]]))}],services:services.map(row=>({service:row.name,major:1,provider:'independent-application'}))}};
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify(config));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({onboarding_version:100,language:'en',theme:'light'}));
const stamp = new Date().toISOString();
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({projects:[{id:'fixture',name:'Explorer acceptance',path:workspace,created_at:stamp,updated_at:stamp}],active_context:{kind:'project',project_id:'fixture',cwd:workspace}}));
const env = {...process.env,WUU_HOME:home};
execute(core, ['plugin','dev','--watch=false',source], workspace, env);
fs.writeFileSync(path.join(run, 'fixture.json'), JSON.stringify({run,home,workspace,source,core,sdkPackage:path.join(run,packed),coreSHA256:createHash('sha256').update(fs.readFileSync(core)).digest('hex')},null,2));
console.log(JSON.stringify({run,home,workspace,source,core},null,2));
