// Builds a downstream-only public policy provider, then drives the encrypted
// device → loopback bridge → real desktop pool → original core session chain.
import {execFile} from 'node:child_process';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile), repo=resolve(fileURLToPath(new URL('../..',import.meta.url)));
const binary=resolve(process.env.WUU_AUTH_BINARY??join(repo,'bin/wuu'));
const sdk=resolve(process.env.WUU_AUTH_SDK_ROOT??join(repo,'packages/plugin-sdk'));
const output=resolve(process.env.WUU_AUTH_OUTPUT??join(repo,'.tmp/e2e/trusted-desktop'));
await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const workspace=join(run,'workspace'),home=join(run,'home'),source=join(run,'plugin'),statePath=join(run,'policy.json'),config=join(home,'config.json');
await Promise.all([mkdir(workspace),mkdir(home),mkdir(source)]);await writeFile(statePath,JSON.stringify({grants:[]}));
// Do not inherit real model credentials, remote GUI routes, proxy settings or
// host plugin overrides into the actual core/desktop fixture.
const infrastructure=Object.fromEntries(['PATH','GOROOT','GOPATH','GOMODCACHE','GOCACHE','GOTOOLCHAIN','GOMAXPROCS','GOFLAGS','SYSTEMROOT','TMPDIR','TEMP','TMP'].filter(key=>process.env[key]!==undefined).map(key=>[key,process.env[key]]));
const env={...infrastructure,HOME:run,USERPROFILE:run,XDG_CONFIG_HOME:join(run,'xdg-config'),XDG_CACHE_HOME:join(run,'xdg-cache'),WUU_HOME:home,WUU_NODE_EXECUTABLE:process.execPath,WUU_DESKTOP_CORE:binary,WUU_TRUSTED_WORKSPACE:workspace,WUU_TRUSTED_CONFIG:config,WUU_TRUSTED_POLICY:statePath};
const result={passed:false,run,binary,checks:[]};
try {
 const {project,tsc,packed}=await preparePublicSDK({sdk,temporary:run,tarball:process.env.WUU_AUTH_SDK_TARBALL});
 await cp(join(repo,'examples/plugins/independent-access/provider.ts'),join(project,'provider.ts'));
 await cp(join(repo,'examples/compositions/minimal/engine.ts'),join(project,'engine.ts'));
 await exec(process.execPath,[tsc,'provider.ts','engine.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 const wrapper=join(project,'runtime.mjs');
 await writeFile(wrapper,`import {runJSONLRuntime} from '@wuu/plugin-sdk';import {createAccessProvider} from './provider.js';import {createEchoEngine} from './engine.js';const a=createAccessProvider(${JSON.stringify({issuer:'https://fixture.invalid',jwksPath:join(run,'unused-jwks.json'),statePath})});const e=createEchoEngine('trusted-fixture');await runJSONLRuntime({concurrentServices:['engine.wuu','identity.authentication','resource.authorization'],initialize:()=>{const x=a.initialize(),y=e.initialize();return {...y,provided_services:[...x.provided_services,...y.provided_services]};},activate:async()=>{await a.activate();await e.activate();},shutdown:async()=>{await e.shutdown();await a.shutdown();},invokeService:(r,h,x)=>r.service==='engine.wuu'?e.invokeService(r,h,x):a.invokeService(r,h,x)},{input:process.stdin,output:process.stdout});`);
 const esbuild=join(sdk,'node_modules/esbuild/bin/esbuild');
 await exec(esbuild,[wrapper,'--bundle','--platform=node','--format=esm','--outfile='+join(source,'runtime.mjs')]);
 await writeFile(join(source,'plugin.json'),JSON.stringify({schema_version:1,id:'example.external-access',name:'Public independent access fixture',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
 await writeFile(config,JSON.stringify({composition:{version:4,profile:'minimal'},skills:{enabled:false}}));
 await exec(binary,['plugin','install',source],{cwd:workspace,env});
 const installed=JSON.parse(await readFile(config,'utf8'));installed.composition={version:4,profile:'minimal',patches:[{modules:{'example.external-access':true,'wuu.resource-policy.default':false},services:[{service:'engine.wuu',major:1,provider:'example.external-access'},{service:'identity.authentication',major:1,provider:'example.external-access'},{service:'resource.authorization',major:1,provider:'example.external-access'}]}]};await writeFile(config,JSON.stringify(installed));
 const fixture=join(run,'desktop-fixture.mjs');await exec(esbuild,[join(repo,'desktop/scripts/trusted-desktop-fixture.ts'),'--bundle','--platform=node','--format=esm','--outfile='+fixture]);
 env.WUU_TRUSTED_DESKTOP_FIXTURE=fixture;env.WUU_TRUSTED_NODE=process.execPath;
 const test=await exec('go',['test','./internal/remote/host','-run','^TestEncryptedDelegatedDesktopSharedCore$','-count=1','-v','-timeout','120s'],{cwd:repo,env,maxBuffer:8*1024*1024});
 await writeFile(join(run,'go-test.log'),test.stdout+test.stderr);
 if(!test.stdout.includes('--- PASS: TestEncryptedDelegatedDesktopSharedCore')||test.stdout.includes('--- SKIP:')) throw new Error('Trusted desktop acceptance did not run to PASS');
 result.sdk=packed;result.checks.push('public-package provider compiled without private runtime imports','real desktop pool and bridge with actual Go core','encrypted delegated device principal, scoped reads, effect ceilings and live revocation','original core process/session ownership preserved');result.passed=true;
} catch(error) {result.error=String(error.stack??error);await writeFile(join(run,'failure.log'),String(error.stdout??'')+String(error.stderr??''));throw error;} finally {await writeFile(join(run,'result.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result,null,2));}
