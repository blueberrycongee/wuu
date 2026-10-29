import {appendFileSync,existsSync,writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {createInterface} from 'node:readline';
const log=process.argv[2]!;
appendFileSync(log,`start ${process.pid}\n`);
const lines=createInterface({input:process.stdin});
lines.on('line',async line=>{
  const msg=JSON.parse(line) as {id?:number|string,method?:string};
  if(msg.id===undefined)return;
  const barrier=process.argv[3];
  if(msg.method==='initialize' && barrier && existsSync(join(barrier,'block'))) {
    writeFileSync(join(barrier,'entered'),'ready');
    while(!existsSync(join(barrier,'resume'))) await new Promise(resolve=>setTimeout(resolve,10));
  }
  const result=msg.method==='initialize'
    ? {protocolVersion:'2024-11-05',capabilities:{tools:{}},serverInfo:{name:'scope-fixture',version:'1.0.0'}}
    : msg.method==='tools/list'?{tools:[{name:'fixture_probe',description:'fixture',inputSchema:{type:'object'}}]} : {};
  process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:msg.id,result})+'\n');
});
lines.on('close',()=>{appendFileSync(log,`stop ${process.pid}\n`);});
