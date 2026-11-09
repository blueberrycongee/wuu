import { mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, it } from 'vitest';
import { requestRemoteProjects } from './remoteProjects';
import type { ProjectManager } from './projects';

it('creates and navigates computer folders without accepting traversal names',async()=>{
 const path=await realpath(await mkdtemp(join(tmpdir(),'wuu-folder-picker-')));
 const manager={} as ProjectManager;
 try {
  await requestRemoteProjects(manager,'desktop/projects/mkdir',{path,name:'New project'});
  await writeFile(join(path,'private-file'),'not listed');
  await writeFile(join(path,'extension.zip'),'package fixture');
  expect(await requestRemoteProjects(manager,'desktop/projects/folders',{path})).toMatchObject({path,folders:[{name:'New project',path:join(path,'New project')}]});
  expect(await requestRemoteProjects(manager,'desktop/projects/folders',{path})).toMatchObject({archives:[]});
  expect(await requestRemoteProjects(manager,'desktop/projects/folders',{path,includeArchives:true})).toMatchObject({archives:[{name:'extension.zip',path:join(path,'extension.zip')}]});
  for(const name of ['..','../escape','a/b','a\\b']) await expect(requestRemoteProjects(manager,'desktop/projects/mkdir',{path,name})).rejects.toThrow('Invalid folder name');
  await expect(requestRemoteProjects(manager,'desktop/projects/folders',{path:'relative'})).rejects.toThrow('absolute');
 }finally{await rm(path,{recursive:true,force:true});}
});
