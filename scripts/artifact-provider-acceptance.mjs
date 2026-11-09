import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {appendFileSync,cpSync,mkdirSync,mkdtempSync,readFileSync,writeFileSync} from 'node:fs';
import {dirname,join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const base=join(root,'.tmp','artifact-provider-acceptance');mkdirSync(base,{recursive:true});
const output=mkdtempSync(join(base,'run-'));const fixture=join(output,'independent-provider');mkdirSync(fixture);
const log=join(output,'verification.log');const report={status:'running',output,checks:[],limitations:['The external fixture is in-memory, not a production cloud backend. Native OS open-with acceptance requires macOS.']};
function run(command,args,cwd=root,env={}) {
 appendFileSync(log,`${command} ${args.join(' ')}\n`);
 try {const stdout=execFileSync(command,args,{cwd,env:{...process.env,...env},encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:600000});appendFileSync(log,stdout);report.checks.push({command:[command,...args],status:'passed'});return stdout;}
 catch(error){appendFileSync(log,`${error.stdout??''}${error.stderr??''}\n`);report.checks.push({command:[command,...args],status:'failed'});throw error;}
}
console.log(`Artifact provider evidence: ${output}`);
try {
 const packed=JSON.parse(run('npm',['pack','--json','--pack-destination',output],join(root,'packages/plugin-sdk')))[0];
 report.sdkSHA256=createHash('sha256').update(readFileSync(join(output,packed.filename))).digest('hex');
 cpSync(join(root,'scripts/artifact-provider-acceptance/provider.ts'),join(fixture,'provider.ts'));
 writeFileSync(join(fixture,'package.json'),JSON.stringify({name:'independent-artifact-provider',private:true,type:'module',dependencies:{'@wuu/plugin-sdk':`file:${join(output,packed.filename)}`}}));
 run('npm',['install','--no-audit','--no-fund','--bin-links=false'],fixture);
 run('go',['test','-race','-count=1','-v','./internal/appserver','-run','^TestThreadArtifactSelectedProvider$'],root,{WUU_ARTIFACT_PROVIDER_FIXTURE:fixture});
 report.fixture=fixture;
 if(process.env.WUU_ARTIFACT_ACCEPTANCE_RENDERER==='1') {
  run('npm',['run','build'],join(root,'desktop'));
  const electron=join(root,'desktop/node_modules/.bin/electron');
  // Electron's headless Ozone backend requires no system display server or
  // sandbox changes. The same visual matrix also runs on a normal desktop.
  const displayArgs=process.platform==='linux'&&!process.env.DISPLAY?['--ozone-platform=headless','--ozone-override-screen-size=1440,1000']:[];
  run(electron,[...displayArgs,'scripts/artifact-preview-e2e.cjs'],join(root,'desktop'),{WUU_ARTIFACT_PROVIDER_FIXTURE:fixture,WUU_ARTIFACT_OUTPUT:join(output,'renderer')});
 }
 report.status='passed';
} catch(error){report.status='failed';console.error(`Artifact provider acceptance failed; inspect ${log}`);process.exitCode=1;}
finally{writeFileSync(join(output,'report.json'),JSON.stringify(report,null,2)+'\n');}
