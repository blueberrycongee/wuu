import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { it, expect } from 'vitest';
import { requestRemoteFiles } from './remoteFiles';
import { WorkspaceFileService } from './workspaceFiles';

it('saves only the selected workspace and preserves a concurrent desktop edit',()=>{
 const cwd=mkdtempSync(join(tmpdir(),'remote-edit-'));const context={kind:'no_project' as const,cwd};
 try {
  writeFileSync(join(cwd,'note.txt'),'initial');const files=new WorkspaceFileService(()=>context);const before=files.readFile('note.txt');
  const params={path:'note.txt',text:'phone edit',base_sha256:before.sha256,base_mtime_ms:before.mtime_ms};
  expect(()=>requestRemoteFiles(context,'desktop/file/write',{root:tmpdir(),params})).toThrow('Unknown remote workspace');
  writeFileSync(join(cwd,'note.txt'),'desktop edit');
  expect(requestRemoteFiles(context,'desktop/file/write',{root:cwd,params})).toMatchObject({status:'conflict'});
  expect(readFileSync(join(cwd,'note.txt'),'utf8')).toBe('desktop edit');
  const current=files.readFile('note.txt');
  expect(requestRemoteFiles(context,'desktop/file/write',{root:cwd,params:{...params,base_sha256:current.sha256,base_mtime_ms:current.mtime_ms}})).toMatchObject({status:'saved'});
  expect(readFileSync(join(cwd,'note.txt'),'utf8')).toBe('phone edit');
 }finally{rmSync(cwd,{recursive:true,force:true});}
});
