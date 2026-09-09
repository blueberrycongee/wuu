import { createHash } from 'node:crypto';
import { expect, it } from 'vitest';
import { PluginAssets } from '../src/lib/pluginAssets';

it('loads only the content matching the installed module digest and releases it',async()=>{
 const assets=new PluginAssets();const source='export function activate() {}';
 const module={id:'plugin',fingerprint:'generation',entry:'index.js',media_type:'text/javascript' as const,source,digest:createHash('sha256').update(source).digest('hex')};
 const result=await assets.module(module);
 expect(await (await fetch(result.url)).text()).toBe(source);
 await expect(assets.module({...module,source:source+'altered'})).rejects.toThrow('digest mismatch');
 assets.clear();await expect(fetch(result.url)).rejects.toThrow();
});
it('does not publish an asset after its computer connection was closed',async()=>{
 const assets=new PluginAssets();const source='export {}';
 const loading=assets.module({id:'plugin',fingerprint:'generation',entry:'index.js',media_type:'text/javascript',source,digest:createHash('sha256').update(source).digest('hex')});
 assets.clear();await expect(loading).rejects.toThrow('connection was closed');
});
