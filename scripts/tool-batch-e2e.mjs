import { execFileSync } from 'node:child_process';
import { mkdirSync,mkdtempSync,writeFileSync,appendFileSync,copyFileSync } from 'node:fs';
import { join,resolve,dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const evidence=join(root,'.tmp/e2e/tool-batch');mkdirSync(evidence,{recursive:true});
const run=mkdtempSync(join(evidence,'run-'));const log=join(run,'verification.log');
const fixture=mkdtempSync(join(tmpdir(),'wuu-independent-batch-'));
function command(program,args,cwd=root,env=process.env){
 appendFileSync(log,`${program} ${args.join(' ')}\n`);
 try{const out=execFileSync(program,args,{cwd,env,encoding:'utf8',stdio:['ignore','pipe','pipe']});appendFileSync(log,out);process.stdout.write(out);return out}
 catch(error){appendFileSync(log,`${error.stdout??''}${error.stderr??''}`);throw error}
}
const packed=JSON.parse(command('npm',['pack','--json','--pack-destination',fixture],join(root,'packages/plugin-sdk')));
writeFileSync(join(fixture,'package.json'),JSON.stringify({name:'independent-batch',private:true,type:'module',dependencies:{'@wuu/plugin-sdk':`file:${join(fixture,packed[0].filename)}`}}));
command('npm',['install','--ignore-scripts','--no-audit','--no-fund','--offline'],fixture);
copyFileSync(join(root,'internal/runtime/testdata/tool-batch.ts'),join(fixture,'tool-batch.ts'));
copyFileSync(join(root,'examples/plugins/independent-providers/batch.ts'),join(fixture,'batch-example.ts'));
command('node',['--experimental-strip-types','--input-type=module','-e',"import('./batch-example.ts').then(m=>{if(m.createReverseBatchProvider().initialize().provided_services[0].name!=='agent.tool.batch')throw Error('invalid example')})"],fixture);
command('go',['test','-race','./internal/runtime','-run','^TestSelectedToolBatchProcess','-count=1','-v'],root,{...process.env,WUU_BATCH_FIXTURE:fixture});
writeFileSync(join(run,'environment.json'),JSON.stringify({fixture,node:process.version,go:command('go',['version']).trim(),commit:command('git',['rev-parse','HEAD']).trim(),worktree:command('git',['status','--porcelain']).trim()},null,2));
console.log(`Verified external batch replacement and durable reconciliation. Evidence: ${log}`);
