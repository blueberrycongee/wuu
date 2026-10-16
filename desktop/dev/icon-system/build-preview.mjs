import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {groups} from './icon-groups.mjs';
import {iconArtwork} from '../../src/shared/iconArtwork.ts';
const here=path.dirname(fileURLToPath(import.meta.url));
const root=path.resolve(here,'../../..');
const require=createRequire(path.join(root,'desktop/package.json'));
const {parse}=require('@babel/parser');
const output=path.join(here,'rendered'); fs.mkdirSync(output,{recursive:true});
const snapshot=path.join(here,'current-artwork.json');
if(!fs.existsSync(snapshot)) fs.writeFileSync(snapshot,JSON.stringify(iconArtwork,null,2)+'\n');
const current=JSON.parse(fs.readFileSync(snapshot,'utf8'));
const candidateArtwork=iconArtwork;
fs.writeFileSync(path.join(here,'production-artwork.json'),JSON.stringify(iconArtwork,null,2)+'\n');
const keys=Object.keys(current), candidateKeys=Object.keys(candidateArtwork), grouped=Object.values(groups).flat();
if(keys.length!==candidateKeys.length||keys.some(k=>!candidateArtwork[k])||new Set(grouped).size!==keys.length||grouped.length!==keys.length) throw Error('Incomplete icon coverage');
const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;');
const text=(x,y,s,size=12,color='#6a6f73',weight=400)=>`<text x="${x}" y="${y}" font-family="DejaVu Sans,sans-serif" font-size="${size}" font-weight="${weight}" fill="${color}">${esc(s)}</text>`;
const rect=(x,y,w,h,fill,rx=0,stroke='none')=>`<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}" fill="${fill}" stroke="${stroke}"/>`;
const icon=(name,x,y,size,version='candidate',color='#30343a',stroke=1.75)=>`<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" color="${color}" stroke="${color}" stroke-width="${stroke}" stroke-linecap="round" stroke-linejoin="round">${(version==='current'?current:candidateArtwork)[name].map(([t,a])=>`<${t} ${Object.entries(a).map(([k,v])=>`${k}="${esc(v)}"`).join(' ')}/>`).join('')}</svg>`;
const wrap=(w,h,s,bg='#f3f2ef')=>`<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${rect(0,0,w,h,bg)}${s.join('')}</svg>`;
const save=(name,w,h,s,bg)=>fs.writeFileSync(path.join(output,name+'.svg'),wrap(w,h,s,bg));
const metadata='Original SVG study · PR #795 · baseline 7dfe8f99 · production artwork';
const rowH=108, cols=8, cellW=164, margin=38;
let y=142; const a=[text(38,50,'Wuu / A complete icon family',30,'#242a2e',600),text(38,80,'148 controls, drawn as one system. Existing → proposed, in every cell.',16),text(38,105,metadata,11)];
for(const [group,names] of Object.entries(groups)){
 a.push(text(margin,y,group,18,'#30343a',600),text(1248,y,String(names.length).padStart(2,'0'),13)); y+=20;
 names.forEach((n,i)=>{let x=margin+(i%cols)*cellW, yy=y+Math.floor(i/cols)*rowH;
 a.push(rect(x,yy,154,98,'#ffffff',9));
 a.push(icon(n,x+25,yy+15,30,'current'),icon(n,x+96,yy+15,30));
 a.push(text(x+69,yy+35,'→',12,'#9a9da0'));
 a.push(text(x+12,yy+64,n,10.3,'#30343a',500));
 a.push(icon(n,x+15,yy+76,14,'current'),icon(n,x+43,yy+76,14));
 a.push(rect(x+89,yy+72,54,22,'#25282e',5),icon(n,x+96,yy+76,14,'current','#e6e8eb'),icon(n,x+122,yy+76,14,'candidate','#e6e8eb'));
 });y+=Math.ceil(names.length/cols)*rowH+30;
}
a.push(text(margin,y,'30px shape comparison · 14px light / dark samples · Identical 24-unit grid and 1.75 stroke for a fair comparison.',11));
save('01-complete-before-after',1388,y+35,a);
// Full-size matrices are vector renderings, never edited screenshots.
for(const theme of ['light','dark']){
 const dark=theme==='dark',bg=dark?'#1e2025':'#f3f2ef',ink=dark?'#e7e8eb':'#30343a',secondary=dark?'#a7acb4':'#6a6f73',surface=dark?'#292c32':'#ffffff';
 let y=122; const s=[text(38,48,`Wuu / 148 production icons · ${theme}`,28,ink,600),text(38,78,'Native sizes: 14 / 15 / 16 / 20px. View at 100% to judge real-size detail.',15,secondary),text(38,101,'The same artwork in each size. No enlarged stroke or per-icon scaling tricks.',11,secondary)];
 for(const [group,names] of Object.entries(groups)){
 s.push(text(38,y+20,group,17,ink,600));y+=38;
 names.forEach((n,i)=>{const x=38+(i%6)*219, yy=y+Math.floor(i/6)*80;
 s.push(rect(x,yy,208,70,surface,8),text(x+12,yy+19,n,10.5,secondary));
 [14,15,16,20].forEach((sz,j)=>{const xx=x+12+j*48,by=yy+30,bs=28;s.push(rect(xx,by,bs,bs,dark?'#343840':'#eeefed',6),icon(n,xx+(bs-sz)/2,by+(bs-sz)/2,sz,'candidate',ink));});
 });y+=Math.ceil(names.length/6)*80+15;
 }s.push(text(38,y+9,'SVG specimen, not an app screenshot. Control boxes are 28px comparison cells; production hit areas are unchanged.',11,secondary));save(`02-native-sizes-${theme}`,1388,y+34,s,bg);
}
// Isolated real-size button specimen plus enlarged, clearly labeled details.
const focus=['Project','FolderOpen','PanelLeft','Search','Copy','PencilLine','Split','RotateCw','RotateCcw','RefreshCw','Pin','Trash2','SquarePen','Send','Settings','Terminal','Bot','FileText','GitBranch','CircleCheck','Info','Zap','Sparkles','Wrench'];
let by=140;const b=[text(38,46,'Wuu / Buttons at their working sizes',28,'#242a2e',600),text(38,76,'Existing → proposed at 14, 15, 16, 20px · light and dark · 1× SVG rendering',15),text(38,101,'28px inline comparison controls; 16px toolbar and 20px enlarged-font controls use glyph + 12px.',11)];
focus.forEach((n,i)=>{const x=38+(i%3)*444, y=by+Math.floor(i/3)*228;
 b.push(rect(x,y,430,214,'#fff',10),text(x+16,y+27,n,16,'#30343a',600));
 [14,15,16,20].forEach((sz,j)=>{let xx=x+16+j*103, bs=j<2?28:sz+12;b.push(text(xx,y+49,`${sz}px / ${bs}px`,10));
 ['light','dark'].forEach((theme,k)=>{let yy=y+58+k*45;let d=theme==='dark';b.push(rect(xx-3,yy-4,88,bs+8,d?'#25282e':'#f1f2ef',6));
 b.push(icon(n,xx+(bs-sz)/2,yy+(bs-sz)/2,sz,'current',d?'#e6e8eb':'#30343a'),text(xx+bs+4,yy+bs/2+4,'→',11,d?'#818791':'#93989c'),icon(n,xx+bs+19+(bs-sz)/2,yy+(bs-sz)/2,sz,'candidate',d?'#e6e8eb':'#30343a'));
 });
 });
 b.push(text(x+16,y+175,'3× detail from 16px geometry',10));b.push(icon(n,x+260,y+151,48,'current'),icon(n,x+344,y+151,48));
});let bh=by+Math.ceil(focus.length/3)*228+38;b.push(text(38,bh-18,'SVG study; actual-size comparison, not live-product acceptance. No button handlers, labels, or layout changed.',11));save('03-button-comparison',1388,bh,b);
// Read-only inventory of all local UI SVG sources and WuuIcons import consumers.
const walk=dir=>fs.readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()&&!['node_modules','vendor','out','build'].includes(e.name)?walk(path.join(dir,e.name)):e.isFile()?[path.join(dir,e.name)]:[]);
const entries=Object.fromEntries(keys.map(n=>[n,{name:n,group:Object.entries(groups).find(([,v])=>v.includes(n))[0],calls:[],references:[]}]))
const inline=[];const imports=[];
for(const base of ['desktop/src','clients','plugins'])for(const f of walk(path.join(root,base))){
 if(!/\.(tsx?|jsx?|mjs)$/.test(f)||/\.test\.|\.spec\.|\/vendor\//.test(f))continue;
 const source=fs.readFileSync(f,'utf8'),rel=path.relative(root,f);let ast;
 try{ast=parse(source,{sourceType:'unambiguous',plugins:['typescript','jsx']});}catch{continue;}
 const local=new Map();
 for(const node of ast.program.body)if(node.type==='ImportDeclaration'&&String(node.source.value).match(/WuuIcons$/)){
 for(const spec of node.specifiers)if(spec.type==='ImportSpecifier'){const n=spec.imported.name;local.set(spec.local.name,n);imports.push({file:rel,line:node.loc.start.line,name:n,local:spec.local.name});}}
 const visit=node=>{if(!node||typeof node!=='object')return;
 if(node.type==='JSXOpeningElement'){
 const name=node.name.name;const attrs=Object.fromEntries(node.attributes.filter(a=>a.type==='JSXAttribute').map(a=>[a.name.name, a.value?source.slice(a.value.start,a.value.end):true]));
 if(name==='svg')inline.push({file:rel,line:node.loc.start.line,attributes:attrs});
 const n=local.get(name);if(entries[n])entries[n].calls.push({file:rel,line:node.loc.start.line,attributes:attrs});
 }
 if(node.type==='CallExpression'&&node.callee.type==='Identifier'&&node.callee.name==='iconSVG'&&node.arguments[0]?.type==='StringLiteral'&&entries[node.arguments[0].value]){const n=node.arguments[0].value;entries[n].calls.push({file:rel,line:node.loc.start.line,attributes:{size:source.slice(node.arguments[1].start,node.arguments[1].end)},renderer:'iconSVG'});entries[n].references.push({file:rel,line:node.loc.start.line});}
 if(node.type==='Identifier'&&entries[local.get(node.name)]) entries[local.get(node.name)].references.push({file:rel,line:node.loc.start.line});
 for(const [k,v]of Object.entries(node))if(!['loc','tokens','comments'].includes(k)){if(Array.isArray(v))v.forEach(visit);else if(v&&typeof v==='object')visit(v);}
 };visit(ast.program);
}
for(const e of Object.values(entries)){e.references=[...new Map(e.references.map(r=>[r.file+':'+r.line,r])).values()];}

const petMap={compose:'SquarePen',open:'ExternalLink',reply:'CornerUpLeft',plus:'Plus',send:'ArrowUp',retry:'RotateCcw',idle:'MessageCircle',running:'Clock',done:'Check',failed:'CircleAlert',needs_review:'CircleHelp',close:'X'};
const inventory={baseline:'7dfe8f99fc4592324aa4015ba2224652765dc00b',baselineTree:'4ed22c2b0a74c1b0adb6004949ffbc7bfb89b710',iconCount:keys.length,groups,icons:entries,inlineSVG: inline,imports,petMap,notes:['Literal JSX attributes are source values, not resolved runtime CSS measurements.','References include imports and dynamic component-map use. No direct JSX call does not mean unused.','PublicIcon resolves 29 extension-facing names; SidePanelToggleIcon and PluginBlocksIcon share the core library.','Native iOS has three generated SVG snapshots: Check, Copy, Split. Update through export-icons.mjs only after approval.','Dynamic gauges, status progress rings, mascot art, provider marks, and arbitrary user/plugin SVG have distinct contracts. See README.']};
fs.writeFileSync(path.join(here,'inventory.json'),JSON.stringify(inventory,null,2)+'\n');
const sourceRows=Object.values(entries).map(e=>`| ${e.name} | ${e.group} | ${e.calls.length} | ${[...new Set(e.calls.map(c=>c.attributes.size).filter(Boolean))].join(', ')||'CSS / dynamic'} | ${[...new Set(e.references.map(c=>c.file.replace('desktop/src/renderer/','')))].join(', ')} |`);
fs.writeFileSync(path.join(here,'inventory.md'),'# Product icon inventory\n\nBaseline: `7dfe8f99`. 148 shared names, including aliases. Source sizes below are JSX inputs; CSS can override them. Dynamic component references are included in the source column.\n\n| Icon | Group | Direct JSX calls | Explicit size inputs | Sources |\n|---|---|---:|---|---|\n'+sourceRows.join('\n')+'\n');
console.log(JSON.stringify({count:keys.length,directJSXCalls:Object.values(entries).reduce((a,e)=>a+e.calls.filter(c=>!c.renderer).length,0),mainSVGCalls:Object.values(entries).reduce((a,e)=>a+e.calls.filter(c=>c.renderer==='iconSVG').length,0),importFiles:new Set(imports.map(i=>i.file)).size,inlineSVG:inline.length,output},null,2));
